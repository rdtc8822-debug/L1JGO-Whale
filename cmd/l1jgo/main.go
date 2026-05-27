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
	"github.com/l1jgo/server/internal/dialog"
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
	fmt.Println("\033[36;1m  │\033[0m           L1JGO-Whale  v0.3.32           \033[36;1m│\033[0m")
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

	// 初始化客戶端文字編碼（Big5 或 GBK）
	packet.InitEncoding(cfg.Character.ClientLanguageCode)

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
	questRepo := persist.NewQuestRepo(db)
	houseRepo := persist.NewHouseRepo(db)
	innRepo := persist.NewInnRepo(db)
	buddyRepo := persist.NewBuddyRepo(db)
	excludeRepo := persist.NewExcludeRepo(db)
	boardRepo := persist.NewBoardRepo(db)
	mailRepo := persist.NewMailRepo(db)
	petRepo := persist.NewPetRepo(db)
	auctionRepo := persist.NewAuctionRepo(db)
	castleRepo := persist.NewCastleRepo(db)

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

	lightSpawnList, err := data.LoadLightSpawnList("data/yaml/light_spawn_list.yaml")
	if err != nil {
		return fmt.Errorf("load light spawn list: %w", err)
	}
	printStat("路燈點位", len(lightSpawnList))

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

	mobGroupTable, err := data.LoadMobGroupTable("data/yaml/mobgroup_list.yaml")
	if err != nil {
		return fmt.Errorf("load mob group: %w", err)
	}
	printStat("怪物群體", mobGroupTable.Count())

	npcCount := spawnNpcsSafe(worldState, npcTable, spawnList, mapDataTable, sprTable, mobGroupTable, log)
	printStat("NPC 生成", npcCount)

	npcActionTable, err := data.LoadNpcActionTable("data/yaml/npc_action_list.yaml")
	if err != nil {
		return fmt.Errorf("load npc actions: %w", err)
	}
	printStat("NPC 動作", npcActionTable.Count())

	// 動態對話（YAML+HTM 引擎）：掃 data/dialogs/ 子資料夾
	dialogManager := dialog.NewManager()
	if dialogRegs, err := dialog.LoadAll("data/dialogs"); err != nil {
		return fmt.Errorf("load dialogs: %w", err)
	} else {
		dialogManager.SetAll(dialogRegs)
		printStat("動態對話", dialogManager.Count())
	}

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

	randomPortalTable, err := data.LoadRandomPortalTable("data/yaml/portal_random_list.yaml")
	if err != nil {
		return fmt.Errorf("load random portal table: %w", err)
	}
	printStat("隨機傳送門", randomPortalTable.Count())

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

	itemPowerTable, err := data.LoadItemPowerTable("data/yaml/item_power.yaml")
	if err != nil {
		return fmt.Errorf("load item power table: %w", err)
	}
	printStat("物品強化加成", itemPowerTable.Count())

	itemMakingTable, err := data.LoadItemMakingTable("data/yaml/item_making_list.yaml")
	if err != nil {
		return fmt.Errorf("load item making table: %w", err)
	}
	printStat("製作配方", itemMakingTable.Count())

	fireCrystalTable, err := data.LoadFireCrystalTable("data/yaml/fire_crystal_list.yaml")
	if err != nil {
		return fmt.Errorf("load fire crystal table: %w", err)
	}
	printStat("火結晶表", fireCrystalTable.Count())

	fireSmithRecipeTable, err := data.LoadFireSmithRecipeTable("data/yaml/firesmith_recipe_list.yaml")
	if err != nil {
		return fmt.Errorf("load firesmith recipe table: %w", err)
	}
	printStat("火神合成配方", fireSmithRecipeTable.Count())

	resolventTable, err := data.LoadResolventTable("data/yaml/resolvent_list.yaml")
	if err != nil {
		return fmt.Errorf("load resolvent table: %w", err)
	}
	printStat("溶解表", resolventTable.Count())

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

	hierarchTable, err := data.LoadHierarchTable("data/yaml/hierarchs.yaml")
	if err != nil {
		return fmt.Errorf("load hierarchs: %w", err)
	}
	printStat("隨身祭司", hierarchTable.Count())

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

	itemBoxTable, err := data.LoadItemBoxTable("data/yaml/item_box.yaml")
	if err != nil {
		return fmt.Errorf("load item box: %w", err)
	}
	printStat("物品箱", itemBoxTable.Count())

	itemUpgradeTable, err := data.LoadItemUpgradeTable("data/yaml/item_upgrade.yaml")
	if err != nil {
		return fmt.Errorf("load item upgrade: %w", err)
	}
	printStat("物品升級", itemUpgradeTable.Count())

	itemVIPTable, err := data.LoadItemVIPTable("data/yaml/item_vip.yaml")
	if err != nil {
		return fmt.Errorf("load item vip: %w", err)
	}
	printStat("VIP物品", itemVIPTable.Count())

	npcChatTable, err := data.LoadNpcChatTable("data/yaml/npc_chat.yaml")
	if err != nil {
		return fmt.Errorf("load npc chat: %w", err)
	}
	printStat("NPC聊天", npcChatTable.Count())

	houseTable, err := data.LoadHouseTable("data/yaml/house_list.yaml")
	if err != nil {
		return fmt.Errorf("load house table: %w", err)
	}
	printStat("住宅", houseTable.Count())

	// 旅館房間載入
	innRooms, err := loadInnRooms(ctx, innRepo)
	if err != nil {
		return fmt.Errorf("load inn rooms: %w", err)
	}
	printStat("旅館房間", len(innRooms))

	questData, err := data.LoadQuestTable("data/yaml/quests.yaml")
	if err != nil {
		return fmt.Errorf("load quest table: %w", err)
	}
	printStat("任務範本", questData.Count())

	dungeonTable, err := data.LoadDungeonTable("data/yaml/quest_dungeons.yaml")
	if err != nil {
		return fmt.Errorf("load dungeon table: %w", err)
	}
	printStat("副本定義", dungeonTable.Count())

	trapData, err := data.LoadTrapData("data/yaml")
	if err != nil {
		return fmt.Errorf("load trap data: %w", err)
	}
	printStat("陷阱範本", len(trapData.Templates))
	printStat("陷阱生成點", len(trapData.Spawns))

	castleTable, err := data.LoadCastleTable("data/yaml/castles.yaml")
	if err != nil {
		return fmt.Errorf("load castles: %w", err)
	}
	printStat("城堡", castleTable.Count())

	warGiftTable, err := data.LoadWarGiftTable("data/yaml/castle_war_gifts.yaml")
	if err != nil {
		return fmt.Errorf("load war gifts: %w", err)
	}
	printStat("攻城禮物", warGiftTable.Count())

	// 5d-1. 建立陷阱管理器（tile-based O(1) 查詢）
	trapMgr := world.NewTrapManager(trapData, mapDataTable)
	printStat("陷阱實例", trapMgr.Count())

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
		AccountRepo:      accountRepo,
		CharRepo:         charRepo,
		ItemRepo:         itemRepo,
		Config:           cfg,
		Log:              log,
		World:            worldState,
		Scripting:        luaEngine,
		NpcActions:       npcActionTable,
		Items:            itemTable,
		Shops:            shopTable,
		Drops:            dropTable,
		Teleports:        teleportTable,
		TeleportHtml:     teleportHtmlTable,
		Portals:          portalTable,
		RandomPortals:    randomPortalTable,
		Skills:           skillTable,
		Npcs:             npcTable,
		MobSkills:        mobSkillTable,
		MapData:          mapDataTable,
		Polys:            polymorphTable,
		ArmorSets:        armorSetTable,
		ItemPowers:       itemPowerTable,
		SprTable:         sprTable,
		WarehouseRepo:    warehouseRepo,
		WALRepo:          walRepo,
		ClanRepo:         clanRepo,
		BuffRepo:         buffRepo,
		Doors:            doorTable,
		ItemMaking:       itemMakingTable,
		FireCrystals:     fireCrystalTable,
		FireSmithRecipes: fireSmithRecipeTable,
		Resolvents:       resolventTable,
		SpellbookReqs:    spellbookReqs,
		BuffIcons:        buffIconTable,
		NpcServices:      npcServiceTable,
		QuestRepo:        questRepo,
		BuddyRepo:        buddyRepo,
		ExcludeRepo:      excludeRepo,
		BoardRepo:        boardRepo,
		MailRepo:         mailRepo,
		PetRepo:          petRepo,
		PetTypes:         petTypeTable,
		PetItems:         petItemTable,
		Dolls:            dollTable,
		Hierarchs:        hierarchTable,
		TeleportPages:    teleportPageTable,
		WeaponSkills:     weaponSkillTable,
		ItemBoxes:        itemBoxTable,
		ItemUpgrades:     itemUpgradeTable,
		ItemVIPs:         itemVIPTable,
		NpcChats:         npcChatTable,
		MobGroups:        mobGroupTable,
		Houses:           houseTable,
		HouseRepo:        houseRepo,
		InnRepo:          innRepo,
		InnRooms:         innRooms,
		QuestData:        questData,
		ClanMatching:     handler.NewClanMatchingManager(),
		Alliances:        handler.NewAllianceManager(),
		TrapMgr:          trapMgr,
		Castles:          castleTable,
		WarGifts:         warGiftTable,
		CastleRepo:       castleRepo,
		Dialogs:          dialogManager,
	}
	handler.RegisterAll(pktReg, deps)
	handler.SetShowNpcID(cfg.Debug.ShowNpcID)
	// 註冊 YAML 對話的 live dialog renderer（必須在 deps 建構後）
	handler.SetDialogManagerForLive(deps)

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
	// Phase 1: 卷軸延遲傳送（特效後延遲 1 tick 執行傳送）
	runner.Register(system.NewScrollTeleportSystem(worldState, deps))
	// Phase 1: 製作交易視窗延遲物品發送（S_Trade 後 1 tick 發送 S_TradeAddItem）
	runner.Register(system.NewCraftTradeSystem(worldState, deps))
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
	// 共用給物品系統（堆疊、背包限制、封包通知）
	deps.ItemCreate = system.NewItemCreateSystem(deps)
	// 寵物生命週期系統（召喚/收回/解放/死亡/經驗/指令）
	deps.PetLife = system.NewPetSystem(deps)
	// 魔法娃娃系統（召喚/解散/屬性加成）
	deps.DollMgr = system.NewDollSystem(deps)
	// 隨身祭司系統（召喚/解散/自動增益）
	deps.HierarchMgr = system.NewHierarchSystem(deps)
	// 寵物比賽系統（報名/比賽/獎勵）
	deps.PetMatch = system.NewPetMatchSystem(deps)
	// 任務動作系統（直接呼叫，非 Phase 系統）
	deps.Quest = system.NewQuestSystem(deps)
	// 陷阱觸發系統（直接呼叫，非 Phase 系統）
	deps.Trap = system.NewTrapSystem(deps)
	// 倉庫系統（直接呼叫，非 Phase 系統）
	deps.Warehouse = system.NewWarehouseSystem(deps)
	// PvP 系統（直接呼叫，非 Phase 系統）
	deps.PvP = system.NewPvPSystem(deps)
	// 天寶幣商城系統（直接呼叫，非 Phase 系統）
	deps.ShopCnMgr = system.NewShopCnSystem(deps)
	// 強化物品購買系統（直接呼叫，非 Phase 系統）
	deps.PowerItemMgr = system.NewPowerItemSystem(deps)
	// 魔法商店系統（直接呼叫，非 Phase 系統）
	deps.SpellShopMgr = system.NewSpellShopSystem(deps)
	// GM 命令系統（直接呼叫，非 Phase 系統）
	deps.GMCmd = system.NewGMCommandSystem(deps)
	// 個人商店交易系統（直接呼叫，非 Phase 系統）
	deps.PrivShop = system.NewPrivateShopSystem(deps)
	// 城堡管理系統（直接呼叫，非 Phase 系統）
	castleSys := system.NewCastleSystem(deps)
	deps.Castle = castleSys
	// 啟動時生成所有城堡的投石車（Java: ServerWarExecutor 啟動後生成）
	for _, c := range castleTable.All() {
		if len(c.Catapults) > 0 {
			castleSys.SpawnCatapults(c.ID)
		}
	}
	// 戰爭系統（直接呼叫，非 Phase 系統）
	deps.War = system.NewWarSystem(deps)

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
	npcSvcSys := system.NewNpcServiceSystem(deps)
	deps.NpcSvc = npcSvcSys
	charResetSys := system.NewCharResetSystem(deps)
	deps.CharReset = charResetSys
	statAllocSys := system.NewStatAllocSystem(deps)
	deps.StatAlloc = statAllocSys
	marriageSys := system.NewMarriageSystem(deps)
	deps.Marriage = marriageSys
	innSys := system.NewInnSystem(deps)
	deps.Inn = innSys
	summonSys := system.NewSummonSystem(deps)
	deps.Summon = summonSys
	runner.Register(system.NewBuffTickSystem(worldState, deps))
	runner.Register(system.NewNpcRespawnSystem(worldState, mapDataTable, deps))
	runner.Register(system.NewNpcAISystem(worldState, deps))
	runner.Register(system.NewCompanionAISystem(worldState, deps))
	// Phase 3: Post-update
	runner.Register(system.NewRegenSystem(worldState, luaEngine, houseTable, cfg))
	runner.Register(system.NewWeatherSystem(worldState))
	runner.Register(system.NewLightSpawnSystem(worldState, lightSpawnList, npcTable))
	mapTimerSys := system.NewMapTimerSystem(worldState, deps)
	deps.MapTimer = mapTimerSys
	runner.Register(mapTimerSys)
	hauntedHouseSys := system.NewHauntedHouseSystem(worldState, deps)
	deps.HauntedHouse = hauntedHouseSys
	inputSys.SetHauntedHouse(hauntedHouseSys)
	runner.Register(hauntedHouseSys)
	questWorldSys := system.NewQuestWorldSystem(worldState, dungeonTable, deps)
	deps.QuestWorld = questWorldSys
	inputSys.SetQuestWorld(questWorldSys)
	runner.Register(questWorldSys)
	dragonDoorSys := system.NewDragonDoorSystem(worldState, deps)
	deps.DragonDoor = dragonDoorSys
	runner.Register(dragonDoorSys)
	runner.Register(system.NewNpcChatSystem(worldState, deps))
	runner.Register(system.NewGroundItemSystem(worldState))
	runner.Register(system.NewGroundEffectSystem(worldState, deps))
	runner.Register(system.NewPartyRefreshSystem(worldState, deps, 10)) // 10 ticks = 2 seconds
	runner.Register(system.NewLiveDialogSystem(worldState))             // 動態 HTML 對話即時更新（@dynamic 路徑）
	rankingSys := system.NewRankingSystem(worldState, deps)
	deps.Ranking = rankingSys
	runner.Register(rankingSys)
	auctionSys := system.NewAuctionSystem(worldState, deps, auctionRepo)
	deps.Auction = auctionSys
	runner.Register(auctionSys)
	deps.Fishing = system.NewFishingSystem(deps)
	runner.Register(system.NewTrapRespawnSystem(trapMgr))
	runner.Register(system.NewCastleWarTickSystem(deps.Castle))
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

// loadInnRooms 載入旅館房間資料。若 NPC 沒有房間記錄，自動建立 16 間。
// Java: InnTable — 啟動時從 房間資料數據 載入。
func loadInnRooms(ctx context.Context, innRepo *persist.InnRepo) (map[int32]map[int32]*persist.InnRoom, error) {
	// 9 個旅館 NPC
	innNpcIDs := []int32{70012, 70019, 70031, 70065, 70070, 70075, 70084, 70054, 70096}

	// 確保所有旅館 NPC 都有 16 間房間記錄
	for _, npcID := range innNpcIDs {
		if err := innRepo.EnsureRooms(ctx, npcID); err != nil {
			return nil, err
		}
	}

	// 載入所有房間
	rooms, err := innRepo.LoadAll(ctx)
	if err != nil {
		return nil, err
	}

	// 建立 npcID → roomNumber → room 對照表
	result := make(map[int32]map[int32]*persist.InnRoom)
	for _, room := range rooms {
		if result[room.NpcID] == nil {
			result[room.NpcID] = make(map[int32]*persist.InnRoom)
		}
		result[room.NpcID][room.RoomNumber] = room
	}
	return result, nil
}

// spawnNpcs creates NPC instances from spawn list and adds them to world state.
// sprTable may be nil (speeds fall back to YAML template values).
func spawnNpcs(ws *world.State, npcTable *data.NpcTable, spawns []data.SpawnEntry, maps *data.MapDataTable, sprTable *data.SprTable, mobGroups *data.MobGroupTable, log *zap.Logger) int {
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
			// spread: "point" → 精確座標，無隨機偏移（NPC、Boss）
			if spawn.Spread != "point" {
				rx := spawn.RandomX
				ry := spawn.RandomY
				// 多隻怪物同座標時，按數量比例自動套用隨機範圍避免聚堆
				if rx == 0 && ry == 0 && spawn.Count > 1 {
					rx = int32(spawn.Count)
					if rx > 25 {
						rx = 25
					}
					ry = rx
				}
				if rx > 0 {
					x += int32(rand.Intn(int(rx*2+1))) - rx
				}
				if ry > 0 {
					y += int32(rand.Intn(int(ry*2+1))) - ry
				}
			}

			leader := createNpcFromTemplate(tmpl, x, y, spawn.MapID, spawn.Heading, spawn.RespawnDelay, sprTable)
			system.ApplyNpcInitialHideLikeJava(leader)
			leader.MobGroupID = spawn.MobGroupID
			ws.AddNpc(leader)
			if maps != nil {
				maps.SetImpassable(leader.MapID, leader.X, leader.Y, true)
			}
			total++

			// 群體生成（Java: L1MobGroupSpawn.doSpawn）
			if spawn.MobGroupID > 0 && mobGroups != nil {
				group := mobGroups.Get(spawn.MobGroupID)
				if group != nil {
					total += spawnMobGroup(ws, leader, group, npcTable, maps, sprTable)
				}
			}
		}
	}
	return total
}

// createNpcFromTemplate 從模板建立 NPC 實體。
func createNpcFromTemplate(tmpl *data.NpcTemplate, x, y int32, mapID, heading int16, respawnDelay int, sprTable *data.SprTable) *world.NpcInfo {
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
	return &world.NpcInfo{
		ID:            world.NextNpcID(),
		NpcID:         tmpl.NpcID,
		Impl:          tmpl.Impl,
		GfxID:         tmpl.GfxID,
		LightSize:     byte(tmpl.LightSize),
		Name:          tmpl.Name,
		NameID:        tmpl.NameID,
		Level:         tmpl.Level,
		X:             x,
		Y:             y,
		MapID:         mapID,
		Heading:       heading,
		HP:            tmpl.HP,
		MaxHP:         tmpl.HP,
		MP:            tmpl.MP,
		MaxMP:         tmpl.MP,
		AC:            tmpl.AC,
		STR:           tmpl.STR,
		DEX:           tmpl.DEX,
		Intel:         tmpl.INT,
		Exp:           tmpl.Exp,
		Lawful:        tmpl.Lawful,
		Size:          tmpl.Size,
		MR:            tmpl.MR,
		Undead:        tmpl.Undead,
		Hard:          tmpl.Hard,
		Agro:          tmpl.Agro,
		Family:        tmpl.Family,
		AgroFamily:    tmpl.AgroFamily,
		AtkDmg:        int32(tmpl.Level) + int32(tmpl.STR)/3,
		Ranged:        tmpl.Ranged,
		AtkSpeed:      atkSpeed,
		AtkMagicSpeed: tmpl.AtkMagicSpeed,
		SubMagicSpeed: tmpl.SubMagicSpeed,
		MoveSpeed:     moveSpeed,
		PoisonAtk:     tmpl.PoisonAtk,
		FireRes:       tmpl.FireRes,
		WaterRes:      tmpl.WaterRes,
		WindRes:       tmpl.WindRes,
		EarthRes:      tmpl.EarthRes,
		SpawnX:        x,
		SpawnY:        y,
		SpawnMapID:    mapID,
		RespawnDelay:  respawnDelay,
	}
}

// spawnMobGroup 生成怪物群體的隊員。
// Java: L1MobGroupSpawn.doSpawn — 在 leader 周圍 ±2 格生成 minion。
func spawnMobGroup(ws *world.State, leader *world.NpcInfo, group *data.MobGroup, npcTable *data.NpcTable, maps *data.MapDataTable, sprTable *data.SprTable) int {
	groupInfo := &world.MobGroupInfo{
		Leader:             leader,
		Members:            []*world.NpcInfo{leader},
		RemoveGroupOnDeath: group.RemoveGroupIfLeaderDie,
	}
	leader.GroupInfo = groupInfo

	spawned := 0
	for _, minion := range group.Minions {
		if minion.NpcID == 0 || minion.Count == 0 {
			continue
		}
		mTmpl := npcTable.Get(minion.NpcID)
		if mTmpl == nil {
			continue
		}
		for j := 0; j < minion.Count; j++ {
			// Java: leader 座標 ±2 格（random.nextInt(5) - 2）
			mx := leader.X + int32(rand.Intn(5)) - 2
			my := leader.Y + int32(rand.Intn(5)) - 2

			mob := createNpcFromTemplate(mTmpl, mx, my, leader.MapID, leader.Heading, 0, sprTable)
			mob.IsMinion = true       // 隊員不獨立重生
			mob.GroupInfo = groupInfo // 回指群體資訊
			mob.SpawnX = leader.SpawnX
			mob.SpawnY = leader.SpawnY
			mob.SpawnMapID = leader.SpawnMapID

			ws.AddNpc(mob)
			if maps != nil {
				maps.SetImpassable(mob.MapID, mob.X, mob.Y, true)
			}
			groupInfo.Members = append(groupInfo.Members, mob)
			spawned++
		}
	}
	return spawned
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
