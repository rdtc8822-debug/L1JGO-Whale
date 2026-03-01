package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"math/rand"

	"github.com/l1jgo/server/internal/config"
	"github.com/l1jgo/server/internal/core/ecs"
	"github.com/l1jgo/server/internal/core/event"
	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	gonet "github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/system"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// ── Startup display helpers ────────────────────────────────────────

func printBanner(serverName string, serverID int) {
	fmt.Println()
	fmt.Println("\033[36;1m  ┌───────────────────────────────────────────┐\033[0m")
	fmt.Println("\033[36;1m  │\033[0m           L1JGO-Whale  v0.1.0             \033[36;1m│\033[0m")
	fmt.Println("\033[36;1m  │\033[0m      天堂 3.80C · Go 遊戲伺服器           \033[36;1m│\033[0m")
	fmt.Println("\033[36;1m  └───────────────────────────────────────────┘\033[0m")
	fmt.Println()
	fmt.Printf("  \033[1m伺服器:\033[0m %s \033[90m(編號: %d)\033[0m\n\n", serverName, serverID)
}

func printSection(title string) {
	// Use rune count for CJK width calculation (each CJK char = 2 columns)
	displayWidth := 0
	for _, r := range title {
		if r > 0x7F {
			displayWidth += 2
		} else {
			displayWidth++
		}
	}
	lineLen := 46 - displayWidth - 1
	if lineLen < 3 {
		lineLen = 3
	}
	fmt.Printf("  \033[33m── %s %s\033[0m\n", title, strings.Repeat("─", lineLen))
}

func printStat(label string, count int) {
	numStr := fmt.Sprintf("%d", count)
	// Use display width for CJK characters
	displayWidth := 0
	for _, r := range label {
		if r > 0x7F {
			displayWidth += 2
		} else {
			displayWidth++
		}
	}
	dotsLen := 42 - displayWidth - len(numStr)
	if dotsLen < 3 {
		dotsLen = 3
	}
	fmt.Printf("  %s \033[90m%s\033[0m \033[32m%s\033[0m\n", label, strings.Repeat("·", dotsLen), numStr)
}

func printOK(msg string) {
	fmt.Printf("  \033[32m✓\033[0m %s\n", msg)
}

func printReady(msg string) {
	fmt.Printf("  \033[32m▶\033[0m %s\n", msg)
}

// ── Main server logic ─────────────────────────────────────────────

func run() error {
	// 1. Load config
	cfgPath := "config/server.toml"
	if p := os.Getenv("L1JGO_CONFIG"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 2. Init logger
	log, err := newLogger(cfg.Logging)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer log.Sync()

	printBanner(cfg.Server.Name, cfg.Server.ID)

	// 3. Connect to PostgreSQL and run migrations
	printSection("資料庫")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := persist.NewDB(ctx, cfg.Database, log)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()
	printOK("PostgreSQL 連線成功")

	if err := persist.RunMigrations(ctx, db.Pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	printOK("資料庫遷移完成")
	fmt.Println()

	// 4. Create repositories
	accountRepo := persist.NewAccountRepo(db)
	charRepo := persist.NewCharacterRepo(db)
	itemRepo := persist.NewItemRepo(db)
	warehouseRepo := persist.NewWarehouseRepo(db)
	walRepo := persist.NewWALRepo(db)
	clanRepo := persist.NewClanRepo(db)
	buffRepo := persist.NewBuffRepo(db)
	buddyRepo := persist.NewBuddyRepo(db)
	excludeRepo := persist.NewExcludeRepo(db)
	boardRepo := persist.NewBoardRepo(db)
	mailRepo := persist.NewMailRepo(db)
	petRepo := persist.NewPetRepo(db)

	// 4a. WAL crash recovery — replay unprocessed economic transactions
	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		recovered, err := walRepo.RecoverWAL(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("WAL crash recovery: %w", err)
		}
		if recovered > 0 {
			log.Warn("WAL 崩潰恢復完成", zap.Int("重播筆數", recovered))
		}
	}

	// 5. Create ECS World and game World State
	ecsWorld := ecs.NewWorld()
	worldState := world.NewState()

	// 5a. Load NPC data and spawn NPCs
	printSection("資料載入")

	npcTable, err := data.LoadNpcTable("data/yaml/npc_list.yaml")
	if err != nil {
		return fmt.Errorf("load npc table: %w", err)
	}
	printStat("NPC 模板", npcTable.Count())

	spawnList, err := data.LoadSpawnList("data/yaml/spawn_list.yaml")
	if err != nil {
		return fmt.Errorf("load spawn list: %w", err)
	}

	mapDataTable, err := data.LoadMapData("data/yaml/map_list.yaml", "map")
	if err != nil {
		return fmt.Errorf("load map data: %w", err)
	}
	printStat("地圖資料", mapDataTable.Count())

	sprTable, err := data.LoadSprTable("data/yaml/spr_action.yaml")
	if err != nil {
		return fmt.Errorf("load spr table: %w", err)
	}
	printStat("精靈動作", sprTable.Count())

	npcCount := spawnNpcs(worldState, npcTable, spawnList, mapDataTable, sprTable, log)
	printStat("NPC 生成", npcCount)

	npcActionTable, err := data.LoadNpcActionTable("data/yaml/npc_action_list.yaml")
	if err != nil {
		return fmt.Errorf("load npc actions: %w", err)
	}
	printStat("NPC 動作", npcActionTable.Count())

	// 5c. Load item templates and shop data
	itemTable, err := data.LoadItemTable(
		"data/yaml/weapon_list.yaml",
		"data/yaml/armor_list.yaml",
		"data/yaml/etcitem_list.yaml",
	)
	if err != nil {
		return fmt.Errorf("load item table: %w", err)
	}
	printStat("道具模板", itemTable.Count())

	shopTable, err := data.LoadShopTable("data/yaml/shop_list.yaml")
	if err != nil {
		return fmt.Errorf("load shop table: %w", err)
	}
	printStat("商店", shopTable.Count())

	dropTable, err := data.LoadDropTable("data/yaml/drop_list.yaml")
	if err != nil {
		return fmt.Errorf("load drop table: %w", err)
	}
	printStat("掉寶表", dropTable.Count())

	teleportTable, err := data.LoadTeleportTable("data/yaml/teleport_list.yaml")
	if err != nil {
		return fmt.Errorf("load teleport table: %w", err)
	}
	printStat("傳送點", teleportTable.Count())

	teleportHtmlTable, err := data.LoadTeleportHtmlTable("data/yaml/teleport_html.yaml")
	if err != nil {
		return fmt.Errorf("load teleport html: %w", err)
	}
	printStat("傳送選單", teleportHtmlTable.Count())

	portalTable, err := data.LoadPortalTable("data/yaml/portal_list.yaml")
	if err != nil {
		return fmt.Errorf("load portal table: %w", err)
	}
	printStat("傳送門", portalTable.Count())

	skillTable, err := data.LoadSkillTable("data/yaml/skill_list.yaml")
	if err != nil {
		return fmt.Errorf("load skill table: %w", err)
	}
	printStat("技能", skillTable.Count())

	mobSkillTable, err := data.LoadMobSkillTable("data/yaml/mob_skill_list.yaml")
	if err != nil {
		return fmt.Errorf("load mob skill table: %w", err)
	}
	printStat("怪物技能", mobSkillTable.Count())

	polymorphTable, err := data.LoadPolymorphTable("data/yaml/polymorph_list.yaml")
	if err != nil {
		return fmt.Errorf("load polymorph table: %w", err)
	}
	printStat("變身形態", polymorphTable.Count())

	armorSetTable, err := data.LoadArmorSetTable("data/yaml/armor_set_list.yaml")
	if err != nil {
		return fmt.Errorf("load armor set table: %w", err)
	}
	printStat("套裝定義", armorSetTable.Count())

	itemMakingTable, err := data.LoadItemMakingTable("data/yaml/item_making_list.yaml")
	if err != nil {
		return fmt.Errorf("load item making table: %w", err)
	}
	printStat("製作配方", itemMakingTable.Count())

	spellbookReqs, err := data.LoadSpellbookReqTable("data/yaml/spellbook_level_req.yaml")
	if err != nil {
		return fmt.Errorf("load spellbook reqs: %w", err)
	}
	printStat("魔法書需求", spellbookReqs.Count())

	buffIconTable, err := data.LoadBuffIconTable("data/yaml/buff_icon_map.yaml")
	if err != nil {
		return fmt.Errorf("load buff icons: %w", err)
	}
	printStat("Buff圖示", buffIconTable.Count())

	npcServiceTable, err := data.LoadNpcServiceTable("data/yaml/npc_services.yaml")
	if err != nil {
		return fmt.Errorf("load npc services: %w", err)
	}
	printStat("NPC服務", npcServiceTable.Count())

	petTypeTable, err := data.LoadPetTypeTable("data/yaml/pet_types.yaml")
	if err != nil {
		return fmt.Errorf("load pet types: %w", err)
	}
	printStat("寵物種類", petTypeTable.Count())

	petItemTable, err := data.LoadPetItemTable("data/yaml/pet_items.yaml")
	if err != nil {
		return fmt.Errorf("load pet items: %w", err)
	}
	printStat("寵物裝備", petItemTable.Count())

	dollTable, err := data.LoadDollTable("data/yaml/dolls.yaml")
	if err != nil {
		return fmt.Errorf("load dolls: %w", err)
	}
	printStat("魔法娃娃", dollTable.Count())

	teleportPageTable, err := data.LoadTeleportPageTable("data/yaml/npc_teleport_page.yaml")
	if err != nil {
		return fmt.Errorf("load teleport pages: %w", err)
	}
	printStat("分頁傳送", teleportPageTable.Count())

	weaponSkillTable, err := data.LoadWeaponSkillTable("data/yaml/weapon_skill.yaml")
	if err != nil {
		return fmt.Errorf("load weapon skills: %w", err)
	}
	printStat("武器技能", weaponSkillTable.Count())

	doorTable, err := data.LoadDoorTable("data/yaml/door_gfx.yaml", "data/yaml/door_spawn.yaml")
	if err != nil {
		return fmt.Errorf("load door table: %w", err)
	}
	doorCount := spawnDoors(worldState, doorTable)
	printStat("門", doorCount)

	// 5b. Initialize Lua scripting engine
	luaEngine, err := scripting.NewEngine("scripts", log)
	if err != nil {
		return fmt.Errorf("lua engine: %w", err)
	}
	defer luaEngine.Close()
	printOK("Lua 腳本載入完成")

	// 5d. Load clans from DB
	clanCount, err := loadClans(ctx, worldState, clanRepo)
	if err != nil {
		return fmt.Errorf("load clans: %w", err)
	}
	printStat("血盟", clanCount)

	// 5e. Initialize item ObjectID counter from DB to avoid collisions
	maxObjID, err := itemRepo.MaxObjID(ctx)
	if err != nil {
		return fmt.Errorf("query max obj_id: %w", err)
	}
	if maxObjID >= 500_000_000 {
		world.SetItemObjIDStart(maxObjID)
	}

	// 5f. Initialize emblem ID counter from DB and ensure emblem directory exists
	maxEmblemID, err := clanRepo.MaxEmblemID(ctx)
	if err != nil {
		return fmt.Errorf("query max emblem_id: %w", err)
	}
	if maxEmblemID > 0 {
		world.SetEmblemIDStart(maxEmblemID)
	}
	if err := os.MkdirAll("emblem", 0755); err != nil {
		return fmt.Errorf("create emblem dir: %w", err)
	}
	fmt.Println()

	// 6. Create packet handler registry and register handlers
	pktReg := packet.NewRegistry(log)
	deps := &handler.Deps{
		AccountRepo: accountRepo,
		CharRepo:    charRepo,
		ItemRepo:    itemRepo,
		Config:      cfg,
		Log:         log,
		World:       worldState,
		Scripting:   luaEngine,
		NpcActions:  npcActionTable,
		Items:       itemTable,
		Shops:       shopTable,
		Drops:        dropTable,
		Teleports:    teleportTable,
		TeleportHtml: teleportHtmlTable,
		Portals:      portalTable,
		Skills:       skillTable,
		Npcs:         npcTable,
		MobSkills:      mobSkillTable,
		MapData:        mapDataTable,
		Polys:          polymorphTable,
		ArmorSets:      armorSetTable,
		SprTable:       sprTable,
		WarehouseRepo:  warehouseRepo,
		WALRepo:        walRepo,
		ClanRepo:       clanRepo,
		BuffRepo:       buffRepo,
		Doors:          doorTable,
		ItemMaking:     itemMakingTable,
		SpellbookReqs:  spellbookReqs,
		BuffIcons:      buffIconTable,
		NpcServices:    npcServiceTable,
		BuddyRepo:     buddyRepo,
		ExcludeRepo:   excludeRepo,
		BoardRepo:     boardRepo,
		MailRepo:      mailRepo,
		PetRepo:       petRepo,
		PetTypes:      petTypeTable,
		PetItems:      petItemTable,
		Dolls:         dollTable,
		TeleportPages: teleportPageTable,
		WeaponSkills:  weaponSkillTable,
	}
	handler.RegisterAll(pktReg, deps)

	// 7. Create network server
	pktPerSec := 0
	if cfg.RateLimit.Enabled {
		pktPerSec = cfg.RateLimit.PacketsPerSecond
	}
	netServer, err := gonet.NewServer(
		cfg.Network.BindAddress,
		cfg.Network.InQueueSize,
		cfg.Network.OutQueueSize,
		pktPerSec,
		log,
	)
	if err != nil {
		return fmt.Errorf("net server: %w", err)
	}
	go netServer.AcceptLoop()

	// 8. Create event bus, session store, and systems
	eventBus := event.NewBus()
	sessStore := gonet.NewSessionStore()
	runner := coresys.NewRunner()
	// Phase 0: Input — 註冊到 Runner，並由 inputPoll 以 2ms 頻率高頻驅動
	// （透過 Runner.TickPhase 在系統 tick 之間只跑 Phase 0，消除 0~200ms 的輸入延遲）
	inputSys := system.NewInputSystem(netServer, pktReg, sessStore, cfg.Network.MaxPacketsPerTick, accountRepo, charRepo, itemRepo, buffRepo, worldState, mapDataTable, petRepo, log)
	runner.Register(inputSys)
	// Phase 1: Event dispatch (double-buffer swap + deliver previous tick's events)
	runner.Register(system.NewEventDispatchSystem(eventBus))
	// Wire event bus into handler deps (for EntityKilled emission, etc.)
	deps.Bus = eventBus
	// Subscribe to game events (proves event bus pipeline end-to-end)
	event.Subscribe(eventBus, func(ev event.EntityKilled) {
		log.Debug("event: EntityKilled",
			zap.Uint64("killer_session", ev.KillerSessionID),
			zap.Int32("npc_template", ev.NpcTemplateID),
			zap.Int32("exp", ev.ExpGained),
		)
	})
	event.Subscribe(eventBus, func(ev event.PlayerDied) {
		log.Debug("event: PlayerDied",
			zap.Int32("char_id", ev.CharID),
			zap.Int16("map", ev.MapID),
		)
	})
	event.Subscribe(eventBus, func(ev event.PlayerKilled) {
		log.Info("event: PlayerKilled (PK)",
			zap.Int32("killer", ev.KillerCharID),
			zap.Int32("victim", ev.VictimCharID),
			zap.Int16("map", ev.MapID),
		)
	})

	// 交易系統（直接呼叫，非 Phase 系統）
	deps.Trade = system.NewTradeSystem(deps)
	// 隊伍系統（直接呼叫，非 Phase 系統）
	deps.Party = system.NewPartySystem(deps)
	// 血盟系統（直接呼叫，非 Phase 系統）
	deps.Clan = system.NewClanSystem(deps)
	// 裝備系統（直接呼叫，非 Phase 系統）
	deps.Equip = system.NewEquipSystem(deps)
	// 物品使用系統（直接呼叫，非 Phase 系統）
	deps.ItemUse = system.NewItemUseSystem(deps)
	// 信件系統（直接呼叫，非 Phase 系統）
	deps.Mail = system.NewMailSystem(deps)
	// 商店系統（直接呼叫，非 Phase 系統）
	deps.Shop = system.NewShopSystem(deps)
	// 製作系統（直接呼叫，非 Phase 系統）
	deps.Craft = system.NewCraftSystem(deps)
	// 物品地面操作系統（銷毀、掉落、撿取）
	deps.ItemGround = system.NewItemGroundSystem(deps)
	// 寵物生命週期系統（召喚/收回/解放/死亡/經驗/指令）
	deps.PetLife = system.NewPetSystem(deps)
	// 魔法娃娃系統（召喚/解散/屬性加成）
	deps.DollMgr = system.NewDollSystem(deps)
	// 倉庫系統（直接呼叫，非 Phase 系統）
	deps.Warehouse = system.NewWarehouseSystem(deps)
	// PvP 系統（直接呼叫，非 Phase 系統）
	deps.PvP = system.NewPvPSystem(deps)

	// Phase 2: Game logic
	combatSys := system.NewCombatSystem(deps)
	deps.Combat = combatSys
	runner.Register(combatSys)
	skillSys := system.NewSkillSystem(deps)
	deps.Skill = skillSys
	runner.Register(skillSys)
	deathSys := system.NewDeathSystem(deps)
	deps.Death = deathSys
	polySys := system.NewPolymorphSystem(deps)
	deps.Polymorph = polySys
	summonSys := system.NewSummonSystem(deps)
	deps.Summon = summonSys
	runner.Register(system.NewBuffTickSystem(worldState, deps))
	runner.Register(system.NewNpcRespawnSystem(worldState, mapDataTable))
	runner.Register(system.NewNpcAISystem(worldState, deps))
	runner.Register(system.NewCompanionAISystem(worldState, deps))
	// Phase 3: Post-update
	runner.Register(system.NewRegenSystem(worldState, luaEngine))
	runner.Register(system.NewWeatherSystem(worldState))
	runner.Register(system.NewGroundItemSystem(worldState))
	runner.Register(system.NewPartyRefreshSystem(worldState, deps, 10)) // 10 ticks = 2 seconds
	rankingSys := system.NewRankingSystem(worldState, deps)
	deps.Ranking = rankingSys
	runner.Register(rankingSys)
	runner.Register(system.NewVisibilitySystem(worldState, deps))
	// Phase 4: Output — flush buffered packets to TCP
	runner.Register(system.NewOutputSystem(sessStore))
	// Phase 5: Persistence (auto-save interval from config)
	persistSys := system.NewPersistenceSystem(worldState, charRepo, itemRepo, buffRepo, walRepo, log, cfg.Persistence.BatchIntervalTicks)
	runner.Register(persistSys)
	// Phase 6: Cleanup
	runner.Register(system.NewCleanupSystem(ecsWorld))

	// 9. Start game loop
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	// 雙頻率遊戲迴圈（架構合規）：
	// - systemTicker (200ms)：runner.Tick() 執行全 Phase 0-6
	// - inputPoll (2ms)：runner.TickPhase(PhaseInput) 只執行 Phase 0
	// Phase 0 高頻運行讓封包處理延遲從 0~200ms 降至 0~2ms（超越 Java 的 ~10ms）。
	// Phase 1-6 維持 200ms 頻率，所有 tick 計數邏輯（Buff、回血、AI）不受影響。
	systemTicker := time.NewTicker(cfg.Network.TickRate)
	inputPoll := time.NewTicker(2 * time.Millisecond)
	defer systemTicker.Stop()
	defer inputPoll.Stop()

	// Display server ready section
	printSection("伺服器就緒")
	printReady(fmt.Sprintf("監聽位址 %s", netServer.Addr().String()))
	printReady(fmt.Sprintf("遊戲迴圈啟動 (系統tick: %s, 輸入輪詢: 2ms)", cfg.Network.TickRate))
	fmt.Println()

	for {
		select {
		case <-systemTicker.C:
			// 完整 tick：Phase 0-6 按順序執行（Phase 0 可能是空操作，因 inputPoll 已排空）
			runner.Tick(cfg.Network.TickRate)
		case <-inputPoll.C:
			// 高頻輸入輪詢：只跑 Phase 0（透過 Runner.TickPhase 維持架構合規）
			runner.TickPhase(coresys.PhaseInput, 0)
		case sig := <-shutdownCh:
			log.Info("收到關閉信號", zap.String("signal", sig.String()))
			// Save all players before stopping
			persistSys.SaveAllPlayers()
			netServer.Shutdown()
			log.Info("伺服器已停止")
			return nil
		}
	}
}

// loadClans loads all clans and members from DB into world state.
func loadClans(ctx context.Context, ws *world.State, clanRepo *persist.ClanRepo) (int, error) {
	clans, members, err := clanRepo.LoadAll(ctx)
	if err != nil {
		return 0, err
	}

	// Build clan map
	clanMap := make(map[int32]*world.ClanInfo, len(clans))
	for _, c := range clans {
		clanMap[c.ClanID] = &world.ClanInfo{
			ClanID:       c.ClanID,
			ClanName:     c.ClanName,
			LeaderID:     c.LeaderID,
			LeaderName:   c.LeaderName,
			FoundDate:    c.FoundDate,
			HasCastle:    c.HasCastle,
			HasHouse:     c.HasHouse,
			Announcement: c.Announcement,
			EmblemID:     c.EmblemID,
			EmblemStatus: c.EmblemStatus,
			Members:      make(map[int32]*world.ClanMember),
		}
	}

	// Assign members
	for _, m := range members {
		clan, ok := clanMap[m.ClanID]
		if !ok {
			continue
		}
		clan.Members[m.CharID] = &world.ClanMember{
			CharID:   m.CharID,
			CharName: m.CharName,
			Rank:     m.Rank,
			Notes:    m.Notes,
		}
	}

	// Register all clans
	for _, clan := range clanMap {
		ws.Clans.AddClan(clan)
	}

	return len(clans), nil
}

// spawnNpcs creates NPC instances from spawn list and adds them to world state.
// sprTable may be nil (speeds fall back to YAML template values).
func spawnNpcs(ws *world.State, npcTable *data.NpcTable, spawns []data.SpawnEntry, maps *data.MapDataTable, sprTable *data.SprTable, log *zap.Logger) int {
	total := 0
	for _, spawn := range spawns {
		tmpl := npcTable.Get(spawn.NpcID)
		if tmpl == nil {
			log.Warn("生成: 未知的 NPC ID", zap.Int32("npc_id", spawn.NpcID))
			continue
		}
		for i := 0; i < spawn.Count; i++ {
			x := spawn.X
			y := spawn.Y
			if spawn.RandomX > 0 {
				x += int32(rand.Intn(int(spawn.RandomX*2+1))) - spawn.RandomX
			}
			if spawn.RandomY > 0 {
				y += int32(rand.Intn(int(spawn.RandomY*2+1))) - spawn.RandomY
			}

			// Resolve animation-based speeds from SprTable (mirrors Java L1NpcInstance.initStats).
			// Only override when the template marks the action as enabled (non-zero).
			atkSpeed := tmpl.AtkSpeed
			moveSpeed := tmpl.PassiveSpeed
			if sprTable != nil {
				gfx := int(tmpl.GfxID)
				if tmpl.AtkSpeed != 0 {
					if v := sprTable.GetAttackSpeed(gfx, data.ActAttack); v > 0 {
						atkSpeed = int16(v)
					}
				}
				if tmpl.PassiveSpeed != 0 {
					if v := sprTable.GetMoveSpeed(gfx, data.ActWalk); v > 0 {
						moveSpeed = int16(v)
					}
				}
			}

			npc := &world.NpcInfo{
				ID:           world.NextNpcID(),
				NpcID:        tmpl.NpcID,
				Impl:         tmpl.Impl,
				GfxID:        tmpl.GfxID,
				Name:         tmpl.Name,
				NameID:       tmpl.NameID,
				Level:        tmpl.Level,
				X:            x,
				Y:            y,
				MapID:        spawn.MapID,
				Heading:      spawn.Heading,
				HP:           tmpl.HP,
				MaxHP:        tmpl.HP,
				MP:           tmpl.MP,
				MaxMP:        tmpl.MP,
				AC:           tmpl.AC,
				STR:          tmpl.STR,
				DEX:          tmpl.DEX,
				Exp:          tmpl.Exp,
				Lawful:       tmpl.Lawful,
				Size:         tmpl.Size,
				MR:           tmpl.MR,
				Undead:       tmpl.Undead,
				Agro:         tmpl.Agro,
				AtkDmg:       int32(tmpl.Level) + int32(tmpl.STR)/3,
				Ranged:       tmpl.Ranged,
				AtkSpeed:     atkSpeed,
				MoveSpeed:    moveSpeed,
				PoisonAtk:    tmpl.PoisonAtk,
				SpawnX:       x,
				SpawnY:       y,
				SpawnMapID:   spawn.MapID,
				RespawnDelay: spawn.RespawnDelay,
			}
			ws.AddNpc(npc)
			if maps != nil {
				maps.SetImpassable(npc.MapID, npc.X, npc.Y, true)
			}
			total++
		}
	}
	return total
}

// spawnDoors creates door instances from door spawn data and adds them to world state.
func spawnDoors(ws *world.State, doorTable *data.DoorTable) int {
	total := 0
	for _, spawn := range doorTable.Spawns() {
		gfx := doorTable.GetGfx(spawn.GfxID)
		if gfx == nil {
			continue
		}

		// Calculate absolute edge locations from base position + offset
		var baseLoc int32
		if gfx.Direction == 0 {
			baseLoc = spawn.X
		} else {
			baseLoc = spawn.Y
		}

		door := &world.DoorInfo{
			ID:        world.NextDoorID(),
			DoorID:    spawn.ID,
			GfxID:     spawn.GfxID,
			X:         spawn.X,
			Y:         spawn.Y,
			MapID:     spawn.MapID,
			MaxHP:     spawn.HP,
			HP:        spawn.HP,
			KeeperID:  spawn.Keeper,
			Direction: gfx.Direction,
			LeftEdge:  baseLoc + int32(gfx.LeftEdgeOffset),
			RightEdge: baseLoc + int32(gfx.RightEdgeOffset),
		}

		if spawn.IsOpening {
			door.OpenStatus = world.DoorActionOpen
		} else {
			door.OpenStatus = world.DoorActionClose
		}

		ws.AddDoor(door)
		total++
	}
	return total
}

func newLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	var zapCfg zap.Config
	if cfg.Format == "json" {
		zapCfg = zap.NewProductionConfig()
	} else {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		zapCfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05")
		zapCfg.EncoderConfig.ConsoleSeparator = "  "
		zapCfg.DisableCaller = true
		zapCfg.DisableStacktrace = true
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)

	return zapCfg.Build()
}
