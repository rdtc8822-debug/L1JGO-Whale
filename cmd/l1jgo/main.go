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

	npcCount := spawnNpcs(worldState, npcTable, spawnList, mapDataTable, log)
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

	// 5b. Initialize Lua scripting engine
	luaEngine, err := scripting.NewEngine("scripts", log)
	if err != nil {
		return fmt.Errorf("lua engine: %w", err)
	}
	defer luaEngine.Close()
	printOK("Lua 腳本載入完成")
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
		WarehouseRepo:  warehouseRepo,
		WALRepo:        walRepo,
	}
	handler.RegisterAll(pktReg, deps)

	// 7. Create network server
	netServer, err := gonet.NewServer(
		cfg.Network.BindAddress,
		cfg.Network.InQueueSize,
		cfg.Network.OutQueueSize,
		log,
	)
	if err != nil {
		return fmt.Errorf("net server: %w", err)
	}
	go netServer.AcceptLoop()

	// 8. Create systems and register with runner
	runner := coresys.NewRunner()
	inputSys := system.NewInputSystem(netServer, pktReg, cfg.Network.MaxPacketsPerTick, accountRepo, charRepo, itemRepo, worldState, mapDataTable, log)
	runner.Register(inputSys)
	runner.Register(system.NewRegenSystem(worldState))
	runner.Register(system.NewCleanupSystem(ecsWorld))

	// 9. Start game loop
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(cfg.Network.TickRate)
	defer ticker.Stop()

	// Display server ready section
	printSection("伺服器就緒")
	printReady(fmt.Sprintf("監聽位址 %s", netServer.Addr().String()))
	printReady(fmt.Sprintf("遊戲迴圈啟動 (tick: %s)", cfg.Network.TickRate))
	fmt.Println()

	saveCounter := 0
	partyRefreshCounter := 0
	const saveInterval = 1500        // 1500 ticks × 200ms = 5 minutes
	const partyRefreshInterval = 10 // 10 ticks × 200ms = 2 seconds (faster than Java's 25s for responsive minimap)

	for {
		select {
		case <-ticker.C:
			runner.Tick(cfg.Network.TickRate)
			// NPC respawn tick
			tickNpcRespawn(worldState, mapDataTable)
			// NPC AI tick (aggro, chase, attack)
			tickNpcAI(worldState, deps)
			// Ground item TTL expiration
			tickGroundItems(worldState)
			// Buff expiration (spell buffs + brave potions) — every tick
			worldState.AllPlayers(func(p *world.PlayerInfo) {
				handler.TickPlayerBuffs(p, deps)
			})
			// HP/MP regen is now handled by RegenSystem (Phase 3 PostUpdate)
			// Party position refresh every 25 seconds (Java L1PartyRefresh)
			partyRefreshCounter++
			if partyRefreshCounter >= partyRefreshInterval {
				partyRefreshCounter = 0
				worldState.AllPlayers(func(p *world.PlayerInfo) {
					if p.PartyID != 0 {
						handler.RefreshPartyPositions(p, deps)
					}
				})
			}
			// Periodic auto-save every 5 minutes
			saveCounter++
			if saveCounter >= saveInterval {
				saveCounter = 0
				saveAllPlayers(worldState, charRepo, itemRepo, log)
			}
		case sig := <-shutdownCh:
			log.Info("收到關閉信號", zap.String("signal", sig.String()))
			// Save all players before stopping
			saveAllPlayers(worldState, charRepo, itemRepo, log)
			netServer.Shutdown()
			log.Info("伺服器已停止")
			return nil
		}
	}
}

// spawnNpcs creates NPC instances from spawn list and adds them to world state.
func spawnNpcs(ws *world.State, npcTable *data.NpcTable, spawns []data.SpawnEntry, maps *data.MapDataTable, log *zap.Logger) int {
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
				AtkSpeed:     tmpl.AtkSpeed,
				MoveSpeed:    tmpl.PassiveSpeed,
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

// tickNpcRespawn processes NPC respawn timers each tick.
func tickNpcRespawn(ws *world.State, maps *data.MapDataTable) {
	for _, npc := range ws.NpcList() {
		if !npc.Dead {
			continue
		}
		if npc.RespawnTimer > 0 {
			npc.RespawnTimer--
			if npc.RespawnTimer <= 0 {
				// Respawn the NPC
				npc.Dead = false
				npc.HP = npc.MaxHP
				npc.MP = npc.MaxMP
				npc.X = npc.SpawnX
				npc.Y = npc.SpawnY
				npc.MapID = npc.SpawnMapID
				npc.AggroTarget = 0
				npc.AttackTimer = 0
				npc.MoveTimer = 0

				// Set tile as blocked
				if maps != nil {
					maps.SetImpassable(npc.MapID, npc.X, npc.Y, true)
				}

				// Notify nearby players
				nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
				for _, viewer := range nearby {
					sendNpcPackFromMain(viewer.Session, npc)
				}
			}
		}
	}
}

// tickNpcAI processes NPC AI via Lua: Go handles target detection + command execution,
// Lua handles all decision logic (scripts/ai/default.lua).
func tickNpcAI(ws *world.State, deps *handler.Deps) {
	for _, npc := range ws.NpcList() {
		if npc.Dead {
			continue
		}
		if npc.Impl != "L1Monster" {
			continue
		}

		// Decrement timers
		if npc.AttackTimer > 0 {
			npc.AttackTimer--
		}
		if npc.MoveTimer > 0 {
			npc.MoveTimer--
		}

		// --- Target detection (Go engine responsibility) ---
		var target *world.PlayerInfo
		if npc.AggroTarget != 0 {
			target = ws.GetBySession(npc.AggroTarget)
			if target == nil || target.Dead || target.MapID != npc.MapID {
				npc.AggroTarget = 0
				target = nil
			}
		}

		// Agro mobs scan for new target if none
		if target == nil && npc.Agro {
			nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
			bestDist := int32(999)
			for _, p := range nearby {
				if p.Dead {
					continue
				}
				dist := chebyshev32(npc.X, npc.Y, p.X, p.Y)
				if dist <= 8 && dist < bestDist {
					bestDist = dist
					target = p
				}
			}
			if target != nil {
				npc.AggroTarget = target.SessionID
				npc.MoveTimer = 0  // snap out of wander — react immediately
				npc.WanderDist = 0
			}
		}

		// Skip Lua call if no players nearby (optimization)
		if target == nil {
			nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
			if len(nearby) == 0 {
				continue
			}
		}

		// --- Build AIContext for Lua ---
		targetDist := int32(0)
		targetID, targetAC, targetLevel := 0, 0, 0
		targetX, targetY := int32(0), int32(0)
		if target != nil {
			targetDist = chebyshev32(npc.X, npc.Y, target.X, target.Y)
			targetID = int(target.CharID)
			targetAC = int(target.AC)
			targetLevel = int(target.Level)
			targetX = target.X
			targetY = target.Y
		}

		spawnDist := chebyshev32(npc.X, npc.Y, npc.SpawnX, npc.SpawnY)

		// Convert mob skills to Lua entries
		var mobSkills []scripting.MobSkillEntry
		if skills := deps.MobSkills.Get(npc.NpcID); skills != nil {
			mobSkills = make([]scripting.MobSkillEntry, len(skills))
			for i, sk := range skills {
				mobSkills[i] = scripting.MobSkillEntry{
					SkillID:       sk.SkillID,
					MpConsume:     sk.MpConsume,
					TriggerRandom: sk.TriggerRandom,
					TriggerHP:     sk.TriggerHP,
					TriggerRange:  sk.TriggerRange,
					ActID:         sk.ActID,
					GfxID:         sk.GfxID,
				}
			}
		}

		ctx := scripting.AIContext{
			NpcID:       int(npc.NpcID),
			X:           int(npc.X),
			Y:           int(npc.Y),
			MapID:       int(npc.MapID),
			HP:          int(npc.HP),
			MaxHP:       int(npc.MaxHP),
			MP:          int(npc.MP),
			MaxMP:       int(npc.MaxMP),
			Level:       int(npc.Level),
			AtkDmg:      int(npc.AtkDmg),
			AtkSpeed:    int(npc.AtkSpeed),
			MoveSpeed:   int(npc.MoveSpeed),
			Ranged:      int(npc.Ranged),
			Agro:        npc.Agro,
			TargetID:    targetID,
			TargetX:     int(targetX),
			TargetY:     int(targetY),
			TargetDist:  int(targetDist),
			TargetAC:    targetAC,
			TargetLevel: targetLevel,
			CanAttack:   npc.AttackTimer <= 0,
			CanMove:     npc.MoveTimer <= 0,
			Skills:      mobSkills,
			WanderDist:  npc.WanderDist,
			SpawnDist:   int(spawnDist),
		}

		// --- Call Lua AI ---
		cmds := deps.Scripting.RunNpcAI(ctx)

		// --- Execute commands ---
		for _, cmd := range cmds {
			switch cmd.Type {
			case "attack":
				if target != nil {
					npcMeleeAttack(ws, npc, target, deps)
					setNpcAtkCooldown(npc)
				}
			case "ranged_attack":
				if target != nil {
					npcRangedAttack(ws, npc, target, deps)
					setNpcAtkCooldown(npc)
				}
			case "skill":
				if target != nil {
					executeNpcSkill(ws, npc, target, cmd.SkillID, cmd.ActID, cmd.GfxID, deps)
					setNpcAtkCooldown(npc)
				}
			case "move_toward":
				if target != nil {
					npcMoveToward(ws, npc, target.X, target.Y, deps.MapData)
					npc.MoveTimer = 3
				}
			case "wander":
				npcWander(ws, npc, cmd.Dir, deps.MapData)
			case "lose_aggro":
				npc.AggroTarget = 0
			}
		}
	}
}

// setNpcAtkCooldown sets the attack cooldown based on AtkSpeed.
func setNpcAtkCooldown(npc *world.NpcInfo) {
	atkCooldown := 10
	if npc.AtkSpeed > 0 {
		atkCooldown = int(npc.AtkSpeed) / 200
		if atkCooldown < 3 {
			atkCooldown = 3
		}
	}
	npc.AttackTimer = atkCooldown
}

// npcMeleeAttack handles NPC melee attack with Lua damage formula.
func npcMeleeAttack(ws *world.State, npc *world.NpcInfo, target *world.PlayerInfo, deps *handler.Deps) {
	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)

	res := deps.Scripting.CalcNpcMelee(scripting.CombatContext{
		AttackerLevel:  int(npc.Level),
		AttackerSTR:    int(npc.STR),
		AttackerDEX:    int(npc.DEX),
		AttackerWeapon: int(npc.AtkDmg),
		TargetAC:       int(target.AC),
		TargetLevel:    int(target.Level),
	})

	damage := int32(res.Damage)
	if !res.IsHit || damage < 0 {
		damage = 0
	}

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcAttackFromMain(viewer.Session, npc.ID, target.CharID, damage, npc.Heading)
	}

	if damage <= 0 {
		return
	}

	target.HP -= int16(damage)
	if target.HP <= 0 {
		target.HP = 0
		handler.KillPlayer(target, deps)
		npc.AggroTarget = 0
		return
	}
	sendHPUpdateFromMain(target.Session, target.HP, target.MaxHP)
}

// npcRangedAttack handles NPC ranged attack with Lua damage formula.
func npcRangedAttack(ws *world.State, npc *world.NpcInfo, target *world.PlayerInfo, deps *handler.Deps) {
	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)

	res := deps.Scripting.CalcNpcRanged(scripting.CombatContext{
		AttackerLevel:  int(npc.Level),
		AttackerSTR:    int(npc.STR),
		AttackerDEX:    int(npc.DEX),
		AttackerWeapon: int(npc.AtkDmg),
		TargetAC:       int(target.AC),
		TargetLevel:    int(target.Level),
	})

	damage := int32(res.Damage)
	if !res.IsHit || damage < 0 {
		damage = 0
	}

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcRangedAttackFromMain(viewer.Session, npc.ID, target.CharID, damage, npc.Heading,
			npc.X, npc.Y, target.X, target.Y)
	}

	if damage <= 0 {
		return
	}

	target.HP -= int16(damage)
	if target.HP <= 0 {
		target.HP = 0
		handler.KillPlayer(target, deps)
		npc.AggroTarget = 0
		return
	}
	sendHPUpdateFromMain(target.Session, target.HP, target.MaxHP)
}

// executeNpcSkill handles an NPC using a skill on a player.
// gfxID: mob-specific spell effect override from mob_skill_list (0 = use skill's CastGfx).
func executeNpcSkill(ws *world.State, npc *world.NpcInfo, target *world.PlayerInfo, skillID, actID, gfxID int, deps *handler.Deps) {
	skill := deps.Skills.Get(int32(skillID))
	if skill == nil {
		npcMeleeAttack(ws, npc, target, deps)
		return
	}

	// Consume MP
	if skill.MpConsume > 0 {
		npc.MP -= int32(skill.MpConsume)
		if npc.MP < 0 {
			npc.MP = 0
		}
	}

	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// Spell visual effect: mob-specific gfx_id takes priority, fallback to skill's CastGfx
	gfx := skill.CastGfx
	if gfxID > 0 {
		gfx = int32(gfxID)
	}

	// Determine if this is a magic projectile (has dice/damage) or physical/buff skill
	isMagicProjectile := skill.DamageValue > 0 || skill.DamageDice > 0

	// Skill with damage
	if isMagicProjectile {
		sctx := scripting.SkillDamageContext{
			SkillID:         int(skill.SkillID),
			DamageValue:     skill.DamageValue,
			DamageDice:      skill.DamageDice,
			DamageDiceCount: skill.DamageDiceCount,
			SkillLevel:      skill.SkillLevel,
			Attr:            skill.Attr,
			AttackerLevel:   int(npc.Level),
			AttackerSTR:     int(npc.STR),
			AttackerDEX:     int(npc.DEX),
			TargetAC:        int(target.AC),
			TargetLevel:     int(target.Level),
			TargetMR:        int(target.MR),
		}
		res := deps.Scripting.CalcSkillDamage(sctx)
		damage := int32(res.Damage)
		if damage < 1 {
			damage = 1
		}

		// Magic projectile: use S_ATTACK(actionId=18) with source/target coords
		// Same format as player spell casting (sendUseAttackSkill in broadcast.go)
		useType := byte(6) // ranged magic
		if skill.Area > 0 {
			useType = 8 // AoE magic
		}
		for _, viewer := range nearby {
			sendNpcUseAttackSkillFromMain(viewer.Session, npc.ID, target.CharID,
				int16(damage), npc.Heading, gfx, useType,
				npc.X, npc.Y, target.X, target.Y)
		}

		target.HP -= int16(damage)
		if target.HP <= 0 {
			target.HP = 0
			handler.KillPlayer(target, deps)
			npc.AggroTarget = 0
			return
		}
		sendHPUpdateFromMain(target.Session, target.HP, target.MaxHP)
	} else {
		// Non-damage skill (buff/debuff): use S_EFFECT on target
		if gfx > 0 {
			for _, viewer := range nearby {
				sendSkillEffectFromMain(viewer.Session, target.CharID, gfx)
			}
		}
	}
}

// npcMoveToward moves NPC 1 tile toward a target position.
func npcMoveToward(ws *world.State, npc *world.NpcInfo, tx, ty int32, maps *data.MapDataTable) {
	dx := tx - npc.X
	dy := ty - npc.Y
	moveX := npc.X
	moveY := npc.Y
	if dx > 0 {
		moveX++
	} else if dx < 0 {
		moveX--
	}
	if dy > 0 {
		moveY++
	} else if dy < 0 {
		moveY--
	}

	heading := calcNpcHeading(npc.X, npc.Y, moveX, moveY)

	// Validate walkability
	if maps != nil && !maps.IsPassable(npc.MapID, npc.X, npc.Y, int(heading)) {
		return
	}

	npc.Heading = heading
	oldX, oldY := npc.X, npc.Y

	// Update tile collision
	if maps != nil {
		maps.SetImpassable(npc.MapID, oldX, oldY, false)
		maps.SetImpassable(npc.MapID, moveX, moveY, true)
	}

	npc.X = moveX
	npc.Y = moveY
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcMoveFromMain(viewer.Session, npc.ID, oldX, oldY, npc.Heading)
	}
}

// npcWander handles idle wandering. dir: 0-7=new direction, -1=continue, -2=toward spawn.
func npcWander(ws *world.State, npc *world.NpcInfo, dir int, maps *data.MapDataTable) {
	wanderTicks := 4
	if npc.MoveSpeed > 0 {
		wanderTicks = int(npc.MoveSpeed) / 200
		if wanderTicks < 2 {
			wanderTicks = 2
		}
	}

	if dir == -1 {
		// Continue current direction
	} else if dir == -2 {
		// Bias toward spawn
		npc.WanderDir = calcNpcHeading(npc.X, npc.Y, npc.SpawnX, npc.SpawnY)
		npc.WanderDist = rand.Intn(5) + 2
	} else {
		npc.WanderDir = int16(dir)
		npc.WanderDist = rand.Intn(5) + 2
	}

	if npc.WanderDist <= 0 {
		return
	}

	// Validate walkability before moving
	if maps != nil && !maps.IsPassable(npc.MapID, npc.X, npc.Y, int(npc.WanderDir)) {
		npc.WanderDist = 0 // stop wandering in this direction
		return
	}

	moveX := npc.X + npcHeadingDX[npc.WanderDir]
	moveY := npc.Y + npcHeadingDY[npc.WanderDir]
	npc.WanderDist--
	npc.MoveTimer = wanderTicks

	oldX, oldY := npc.X, npc.Y

	// Update tile collision
	if maps != nil {
		maps.SetImpassable(npc.MapID, oldX, oldY, false)
		maps.SetImpassable(npc.MapID, moveX, moveY, true)
	}

	npc.X = moveX
	npc.Y = moveY
	npc.Heading = npc.WanderDir

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcMoveFromMain(viewer.Session, npc.ID, oldX, oldY, npc.Heading)
	}
}

// chebyshev32 returns Chebyshev distance between two points.
func chebyshev32(x1, y1, x2, y2 int32) int32 {
	dx := abs32(x1 - x2)
	dy := abs32(y1 - y2)
	if dy > dx {
		return dy
	}
	return dx
}

func abs32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}

var npcHeadingDX = [8]int32{0, 1, 1, 1, 0, -1, -1, -1}
var npcHeadingDY = [8]int32{-1, -1, 0, 1, 1, 1, 0, -1}

func calcNpcHeading(sx, sy, tx, ty int32) int16 {
	ddx := tx - sx
	ddy := ty - sy
	if ddx > 0 {
		ddx = 1
	} else if ddx < 0 {
		ddx = -1
	}
	if ddy > 0 {
		ddy = 1
	} else if ddy < 0 {
		ddy = -1
	}
	for i := int16(0); i < 8; i++ {
		if npcHeadingDX[i] == ddx && npcHeadingDY[i] == ddy {
			return i
		}
	}
	return 0
}

func sendNpcMoveFromMain(sess *gonet.Session, npcID int32, prevX, prevY int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MOVE_OBJECT)
	w.WriteD(npcID)
	w.WriteH(uint16(prevX))
	w.WriteH(uint16(prevY))
	w.WriteC(byte(heading))
	w.WriteC(0x80) // speed byte: 0x80 = NPC movement (Java S_MoveNpcPacket; PC uses 0x81)
	w.WriteD(0)
	sess.Send(w.Bytes())
}

func sendNpcAttackFromMain(sess *gonet.Session, attackerID, targetID, damage int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(1) // actionId: 1 = normal melee
	w.WriteD(attackerID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(0)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

// npcArrowSeqNum is a sequential counter for NPC ranged attack packets.
var npcArrowSeqNum int32

func sendNpcRangedAttackFromMain(sess *gonet.Session, attackerID, targetID, damage int32, heading int16, ax, ay, tx, ty int32) {
	npcArrowSeqNum++
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(1)                    // actionId: 1
	w.WriteD(attackerID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(npcArrowSeqNum)       // sequential number (non-zero)
	w.WriteH(66)                   // arrowGfx: 66 = arrow projectile
	w.WriteC(0)                    // use_type: 0 = arrow
	w.WriteH(uint16(ax))          // attacker X
	w.WriteH(uint16(ay))          // attacker Y
	w.WriteH(uint16(tx))          // target X
	w.WriteH(uint16(ty))          // target Y
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

// sendNpcPackFromMain builds an NPC pack packet for respawn broadcasting.
func sendNpcPackFromMain(sess *gonet.Session, npc *world.NpcInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(npc.X))
	w.WriteH(uint16(npc.Y))
	w.WriteD(npc.ID)
	w.WriteH(uint16(npc.GfxID))
	w.WriteC(0)
	w.WriteC(byte(npc.Heading))
	w.WriteC(0)
	w.WriteC(0)
	w.WriteD(npc.Exp)
	w.WriteH(0)
	w.WriteS(npc.NameID)
	w.WriteS("")
	w.WriteC(0x00)
	w.WriteD(0)
	w.WriteS("")
	w.WriteS("")
	w.WriteC(0x00)
	w.WriteC(0xFF)
	w.WriteC(0x00)
	w.WriteC(byte(npc.Level))
	w.WriteC(0xFF)
	w.WriteC(0xFF)
	w.WriteC(0x00)
	sess.Send(w.Bytes())
}

// saveAllPlayers persists all online players' character data and inventory to DB.
func saveAllPlayers(ws *world.State, charRepo *persist.CharacterRepo, itemRepo *persist.ItemRepo, log *zap.Logger) {
	count := 0
	ws.AllPlayers(func(p *world.PlayerInfo) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		row := &persist.CharacterRow{
			Name:       p.Name,
			Level:      p.Level,
			Exp:        int64(p.Exp),
			HP:         p.HP,
			MP:         p.MP,
			MaxHP:      p.MaxHP,
			MaxMP:      p.MaxMP,
			X:          p.X,
			Y:          p.Y,
			MapID:      p.MapID,
			Heading:    p.Heading,
			Lawful:     p.Lawful,
			Str:        p.Str,
			Dex:        p.Dex,
			Con:        p.Con,
			Wis:        p.Wis,
			Cha:        p.Cha,
			Intel:      p.Intel,
			BonusStats: p.BonusStats,
		}
		if err := charRepo.SaveCharacter(ctx, row); err != nil {
			log.Error("自動存檔角色失敗", zap.String("name", p.Name), zap.Error(err))
			return
		}
		if err := itemRepo.SaveInventory(ctx, p.CharID, p.Inv, &p.Equip); err != nil {
			log.Error("自動存檔背包失敗", zap.String("name", p.Name), zap.Error(err))
			return
		}
		if err := charRepo.SaveBookmarks(ctx, p.Name, bookmarksToRows(p.Bookmarks)); err != nil {
			log.Error("自動存檔書籤失敗", zap.String("name", p.Name), zap.Error(err))
		}
		if err := charRepo.SaveKnownSpells(ctx, p.Name, p.KnownSpells); err != nil {
			log.Error("自動存檔魔法書失敗", zap.String("name", p.Name), zap.Error(err))
		}
		count++
	})
	if count > 0 {
		log.Info("自動存檔完成", zap.Int("玩家數", count))
	}
}

// bookmarksToRows converts world.Bookmark slice to persist.BookmarkRow slice for JSONB storage.
func bookmarksToRows(bms []world.Bookmark) []persist.BookmarkRow {
	rows := make([]persist.BookmarkRow, len(bms))
	for i, bm := range bms {
		rows[i] = persist.BookmarkRow{
			ID:    bm.ID,
			Name:  bm.Name,
			X:     bm.X,
			Y:     bm.Y,
			MapID: bm.MapID,
		}
	}
	return rows
}

// tickRegen regenerates HP/MP for all online players.
// HP regen scales with CON, MP regen scales with WIS.
// tickGroundItems removes expired ground items and broadcasts removal.
func tickGroundItems(ws *world.State) {
	expired := ws.TickGroundItems()
	for _, g := range expired {
		nearby := ws.GetNearbyPlayersAt(g.X, g.Y, g.MapID)
		for _, viewer := range nearby {
			sendRemoveObjectFromMain(viewer.Session, g.ID)
		}
	}
}

func sendActionGfxFromMain(sess *gonet.Session, objectID int32, actionCode byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ACTION)
	w.WriteD(objectID)
	w.WriteC(actionCode)
	sess.Send(w.Bytes())
}

// sendNpcUseAttackSkillFromMain sends S_ATTACK(actionId=18) for NPC magic projectile.
// Same format as player sendUseAttackSkill — includes source/target coords for projectile animation.
func sendNpcUseAttackSkillFromMain(sess *gonet.Session, casterID, targetID int32, damage int16, heading int16, gfxID int32, useType byte, cx, cy, tx, ty int32) {
	npcArrowSeqNum++
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(18)                  // actionId: 18 = ACTION_SkillAttack
	w.WriteD(casterID)            // NPC object ID
	w.WriteD(targetID)            // target char ID
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(npcArrowSeqNum)      // sequential number
	w.WriteH(uint16(gfxID))      // spell GFX ID (e.g. 1583=火箭)
	w.WriteC(useType)             // 6=ranged magic, 8=AoE magic
	w.WriteH(uint16(cx))         // caster X
	w.WriteH(uint16(cy))         // caster Y
	w.WriteH(uint16(tx))         // target X
	w.WriteH(uint16(ty))         // target Y
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

func sendHPUpdateFromMain(sess *gonet.Session, hp, maxHP int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteH(uint16(hp))
	w.WriteH(uint16(maxHP))
	sess.Send(w.Bytes())
}

func sendSkillEffectFromMain(sess *gonet.Session, objectID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(objectID)
	w.WriteH(uint16(gfxID))
	sess.Send(w.Bytes())
}

func sendRemoveObjectFromMain(sess *gonet.Session, objectID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_OBJECT)
	w.WriteD(objectID)
	sess.Send(w.Bytes())
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
