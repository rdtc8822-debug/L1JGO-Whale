package handler

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// HandleGMCommand processes a "." prefixed GM command.
// Returns true if the text was a GM command (consumed), false otherwise.
func HandleGMCommand(sess *net.Session, player *world.PlayerInfo, text string, deps *Deps) bool {
	if !strings.HasPrefix(text, ".") {
		return false
	}

	// Parse command and arguments
	parts := strings.Fields(text[1:]) // strip leading "."
	if len(parts) == 0 {
		return true
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "help":
		gmHelp(sess)
	case "level":
		gmLevel(sess, player, args, deps)
	case "hp":
		gmHP(sess, player, args, deps)
	case "mp":
		gmMP(sess, player, args)
	case "heal":
		gmHeal(sess, player, deps)
	case "stat":
		gmStat(sess, player, args, deps)
	case "move", "warp", "teleport":
		gmMove(sess, player, args, deps)
	case "item":
		gmItem(sess, player, args, deps)
	case "gold", "adena":
		gmGold(sess, player, args, deps)
	case "spell":
		gmSpell(sess, player, args, deps)
	case "allskill":
		gmAllSkill(sess, player, deps)
	case "spawn":
		gmSpawn(sess, player, args, deps)
	case "kill":
		gmKill(sess, player, args, deps)
	case "killall":
		gmKillAll(sess, player, deps)
	case "speed":
		gmSpeed(sess, player, args, deps)
	case "who":
		gmWho(sess, deps)
	case "goto":
		gmGoto(sess, player, args, deps)
	case "recall":
		gmRecall(sess, player, args, deps)
	case "exp":
		gmExp(sess, player, args, deps)
	case "class":
		gmClass(sess, player, args, deps)
	case "save":
		gmSave(sess, player, deps)
	case "rez", "resurrect":
		gmRez(sess, player, args, deps)
	case "ac":
		gmShowInfo(sess, player)
	case "poly":
		gmPoly(sess, player, args, deps)
	case "polygfx":
		gmPolyGfx(sess, player, args, deps)
	case "undopoly":
		gmUndoPoly(sess, player, args, deps)
	case "loc", "pos", "coord":
		gmLoc(sess, player, args, deps)
	case "wall":
		gmWall(sess, player, args, deps)
	case "clearwall":
		gmClearWall(sess, player, deps)
	case "weather":
		gmWeather(sess, player, args, deps)
	case "buff":
		gmBuff(sess, player, args, deps)
	case "allbuff":
		gmAllBuff(sess, player, deps)
	case "stresstest":
		gmStressTest(sess, player, args, deps)
	case "cleartest":
		gmClearTest(sess, player, deps)
	case "invisible":
		gmInvisible(sess, player, deps)
	default:
		gmMsg(sess, "\\f3未知的GM指令: ."+cmd+"  輸入 .help 查看指令列表")
	}

	return true
}

// --- Helper ---

func gmMsg(sess *net.Session, msg string) {
	sendGlobalChat(sess, 9, msg) // type 9 = system message (green text)
}

func gmMsgf(sess *net.Session, format string, a ...any) {
	gmMsg(sess, fmt.Sprintf(format, a...))
}

// --- Commands ---

func gmHelp(sess *net.Session) {
	gmMsg(sess, "=== GM 指令列表 ===")
	gmMsg(sess, ".level <等級>  — 設定等級(1-99)")
	gmMsg(sess, ".hp <數值>  — 設定HP")
	gmMsg(sess, ".mp <數值>  — 設定MP")
	gmMsg(sess, ".heal  — 補滿HP/MP")
	gmMsg(sess, ".stat <str|dex|con|wis|int|cha> <數值>  — 設定屬性")
	gmMsg(sess, ".move <x> <y> [mapID]  — 傳送到座標")
	gmMsg(sess, ".item <itemID> [數量] [enchant]  — 給予物品")
	gmMsg(sess, ".gold <數量>  — 給予金幣")
	gmMsg(sess, ".spell <skillID>  — 學習技能 (0=全部)")
	gmMsg(sess, ".allskill  — 學習該職業所有技能")
	gmMsg(sess, ".spawn <npcID> [數量]  — 召喚NPC")
	gmMsg(sess, ".kill  — 殺死目標範圍內NPC")
	gmMsg(sess, ".killall  — 殺死附近所有NPC")
	gmMsg(sess, ".speed <0|1|2>  — 移動速度(0=正常,1=加速,2=勇水)")
	gmMsg(sess, ".who  — 列出線上玩家")
	gmMsg(sess, ".goto <玩家名>  — 傳送到玩家身邊")
	gmMsg(sess, ".recall <玩家名>  — 召喚玩家到身邊")
	gmMsg(sess, ".exp <數值>  — 給予經驗值")
	gmMsg(sess, ".class <0-6>  — 變更職業外觀")
	gmMsg(sess, ".rez [玩家名]  — 復活(自己或指定玩家)")
	gmMsg(sess, ".poly <polyID> [玩家名]  — 變身(自己或指定玩家)")
	gmMsg(sess, ".undopoly [玩家名]  — 解除變身")
	gmMsg(sess, ".save  — 手動存檔")
	gmMsg(sess, ".ac  — 顯示角色詳細資訊")
	gmMsg(sess, ".loc [玩家名]  — 顯示自己或指定玩家的當下座標")
	gmMsg(sess, ".wall [1|2|3]  — 測試牆壁: 1=隱形門 2=僅封包 3=可見門")
	gmMsg(sess, ".clearwall  — 清除測試牆壁")
	gmMsg(sess, ".buff <skillID>  — 強制套用buff(繞過驗證)")
	gmMsg(sess, ".allbuff  — 套用所有常用buff")
	gmMsg(sess, ".stresstest <npcID> [數量] [半徑]  — 壓力測試(預設10000隻,半徑50)")
	gmMsg(sess, ".cleartest  — 清除所有壓力測試怪物")
}

func gmLevel(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .level <等級>")
		return
	}
	lv, err := strconv.Atoi(args[0])
	if err != nil || lv < 1 || lv > 99 {
		gmMsg(sess, "\\f3等級必須在 1-99 之間")
		return
	}

	player.Level = int16(lv)
	// Set exp to match level (via Lua exp table)
	player.Exp = int32(deps.Scripting.ExpForLevel(lv))

	// Recalculate MaxHP/MaxMP based on new level
	// Base: level 1 stats + level-up gains (via Lua)
	baseHP, baseMP := calcBaseHPMP(player.ClassType, player.Level, player.Con, player.Wis, deps)
	player.MaxHP = baseHP
	player.MaxMP = baseMP
	player.HP = player.MaxHP
	player.MP = player.MaxMP

	sendPlayerStatus(sess, player)
	sendExpUpdate(sess, player.Level, player.Exp)
	sendHpUpdate(sess, player)
	sendMpUpdate(sess, player)

	gmMsgf(sess, "等級已設為 %d (HP:%d MP:%d)", lv, player.MaxHP, player.MaxMP)
}

func gmHP(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .hp <數值>")
		return
	}
	val, err := strconv.Atoi(args[0])
	if err != nil || val < 0 {
		gmMsg(sess, "\\f3無效的HP數值")
		return
	}

	player.HP = int16(val)
	if player.HP > player.MaxHP {
		player.MaxHP = player.HP
	}
	if player.HP > 0 && player.Dead {
		player.Dead = false
		player.LastMoveTime = 0
		deps.World.OccupyEntity(player.MapID, player.X, player.Y, player.CharID)
	}
	sendHpUpdate(sess, player)
	sendPlayerStatus(sess, player)
	gmMsgf(sess, "HP 已設為 %d/%d", player.HP, player.MaxHP)
}

func gmMP(sess *net.Session, player *world.PlayerInfo, args []string) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .mp <數值>")
		return
	}
	val, err := strconv.Atoi(args[0])
	if err != nil || val < 0 {
		gmMsg(sess, "\\f3無效的MP數值")
		return
	}

	player.MP = int16(val)
	if player.MP > player.MaxMP {
		player.MaxMP = player.MP
	}
	sendMpUpdate(sess, player)
	sendPlayerStatus(sess, player)
	gmMsgf(sess, "MP 已設為 %d/%d", player.MP, player.MaxMP)
}

func gmHeal(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	player.HP = player.MaxHP
	player.MP = player.MaxMP
	if player.Dead {
		player.Dead = false
		player.LastMoveTime = 0
		deps.World.OccupyEntity(player.MapID, player.X, player.Y, player.CharID)
	}
	sendHpUpdate(sess, player)
	sendMpUpdate(sess, player)
	gmMsg(sess, "HP/MP 已補滿")
}

func gmStat(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 2 {
		gmMsg(sess, "\\f3用法: .stat <str|dex|con|wis|int|cha> <數值>")
		return
	}
	val, err := strconv.Atoi(args[1])
	if err != nil || val < 1 || val > 127 {
		gmMsg(sess, "\\f3屬性數值必須在 1-127 之間")
		return
	}

	stat := strings.ToLower(args[0])
	v := int16(val)
	switch stat {
	case "str":
		player.Str = v
	case "dex":
		player.Dex = v
	case "con":
		player.Con = v
	case "wis":
		player.Wis = v
	case "int":
		player.Intel = v
	case "cha":
		player.Cha = v
	default:
		gmMsg(sess, "\\f3未知的屬性: "+stat)
		return
	}

	sendPlayerStatus(sess, player)
	gmMsgf(sess, "%s 已設為 %d", strings.ToUpper(stat), val)
}

func gmMove(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 2 {
		gmMsg(sess, "\\f3用法: .move <x> <y> [mapID]")
		return
	}
	x, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, "\\f3無效的X座標")
		return
	}
	y, err := strconv.Atoi(args[1])
	if err != nil {
		gmMsg(sess, "\\f3無效的Y座標")
		return
	}
	mapID := int(player.MapID)
	if len(args) >= 3 {
		mapID, err = strconv.Atoi(args[2])
		if err != nil {
			gmMsg(sess, "\\f3無效的地圖ID")
			return
		}
	}

	teleportPlayer(sess, player, int32(x), int32(y), int16(mapID), 5, deps)
	gmMsgf(sess, "已傳送至 (%d, %d) 地圖 %d", x, y, mapID)
}

func gmItem(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .item <itemID> [數量] [enchant]")
		return
	}
	itemID, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, "\\f3無效的物品ID")
		return
	}
	count := int32(1)
	if len(args) >= 2 {
		c, err := strconv.Atoi(args[1])
		if err == nil && c > 0 {
			count = int32(c)
		}
	}
	enchant := int8(0)
	if len(args) >= 3 {
		e, err := strconv.Atoi(args[2])
		if err == nil && e >= -7 && e <= 15 {
			enchant = int8(e)
		}
	}

	itemInfo := deps.Items.Get(int32(itemID))
	if itemInfo == nil {
		gmMsgf(sess, "\\f3找不到物品: %d", itemID)
		return
	}

	if player.Inv.IsFull() {
		gmMsg(sess, "\\f3背包已滿")
		return
	}

	stackable := itemInfo.Stackable || int32(itemID) == world.AdenaItemID
	existing := player.Inv.FindByItemID(int32(itemID))
	wasExisting := existing != nil && stackable

	invItem := player.Inv.AddItem(
		int32(itemID), count, itemInfo.Name, itemInfo.InvGfx,
		itemInfo.Weight, stackable, byte(itemInfo.Bless),
	)
	invItem.EnchantLvl = enchant
	invItem.UseType = itemInfo.UseTypeID

	if wasExisting {
		sendItemCountUpdate(sess, invItem)
	} else {
		sendAddItem(sess, invItem)
	}
	sendWeightUpdate(sess, player)

	name := itemInfo.Name
	if enchant > 0 {
		name = fmt.Sprintf("+%d %s", enchant, name)
	}
	gmMsgf(sess, "已給予 %s x%d", name, count)
}

func gmGold(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .gold <數量>")
		return
	}
	amount, err := strconv.Atoi(args[0])
	if err != nil || amount <= 0 {
		gmMsg(sess, "\\f3無效的金幣數量")
		return
	}

	adenaInfo := deps.Items.Get(world.AdenaItemID)
	if adenaInfo == nil {
		gmMsg(sess, "\\f3找不到金幣物品模板")
		return
	}

	existing := player.Inv.FindByItemID(world.AdenaItemID)
	wasExisting := existing != nil

	invItem := player.Inv.AddItem(
		world.AdenaItemID, int32(amount), adenaInfo.Name, adenaInfo.InvGfx,
		0, true, byte(adenaInfo.Bless),
	)

	if wasExisting {
		sendItemCountUpdate(sess, invItem)
	} else {
		sendAddItem(sess, invItem)
	}
	sendWeightUpdate(sess, player)

	gmMsgf(sess, "已給予 %d 金幣 (持有: %d)", amount, invItem.Count)
}

func gmSpell(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .spell <skillID>  (0 = 學全部)")
		return
	}
	skillID, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, "\\f3無效的技能ID")
		return
	}

	if skillID == 0 {
		// Learn all skills
		count := 0
		for id := int32(1); id <= 256; id++ {
			sk := deps.Skills.Get(id)
			if sk == nil {
				continue
			}
			// Check if already known
			known := false
			for _, s := range player.KnownSpells {
				if s == id {
					known = true
					break
				}
			}
			if !known {
				player.KnownSpells = append(player.KnownSpells, id)
				count++
			}
		}
		// Send full skill list
		sendAllSpells(sess, player, deps)
		gmMsgf(sess, "已學會全部技能 (新增 %d 個)", count)
		return
	}

	sk := deps.Skills.Get(int32(skillID))
	if sk == nil {
		gmMsgf(sess, "\\f3找不到技能: %d", skillID)
		return
	}

	// Check if already known
	for _, s := range player.KnownSpells {
		if s == int32(skillID) {
			gmMsgf(sess, "已經學會技能: %s (ID:%d)", sk.Name, skillID)
			return
		}
	}

	player.KnownSpells = append(player.KnownSpells, int32(skillID))

	// Send updated skill list
	sendAllSpells(sess, player, deps)
	gmMsgf(sess, "已學會技能: %s (ID:%d)", sk.Name, skillID)
}

// sendAllSpells re-sends the complete spell list to the client.
func sendAllSpells(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	if deps.Skills == nil {
		return
	}
	var spells []*data.SkillInfo
	for _, sid := range player.KnownSpells {
		if sk := deps.Skills.Get(sid); sk != nil {
			spells = append(spells, sk)
		}
	}
	sendSkillList(sess, spells)
}

// classSkillLevels maps ClassType → SkillLevel ranges for that class.
// L1J skill_level groups:
//   1-10  = Wizard    11-12 = Royal(Prince)
//   13-14 = Dark Elf  15    = Knight
//   17-22 = Elf       23-25 = Dragon Knight
//   26-28 = Illusionist
var classSkillLevels = map[int16][]int{
	0: {11, 12},             // Prince/Royal
	1: {15},                 // Knight
	2: {17, 18, 19, 20, 21, 22}, // Elf
	3: {1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, // Wizard
	4: {13, 14},             // Dark Elf
	5: {23, 24, 25},         // Dragon Knight
	6: {26, 27, 28},         // Illusionist
}

func gmAllSkill(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	levels, ok := classSkillLevels[player.ClassType]
	if !ok {
		gmMsg(sess, "\\f3未知的職業類型")
		return
	}

	levelSet := make(map[int]bool, len(levels))
	for _, lv := range levels {
		levelSet[lv] = true
	}

	// Build set of already known spells
	knownSet := make(map[int32]bool, len(player.KnownSpells))
	for _, sid := range player.KnownSpells {
		knownSet[sid] = true
	}

	count := 0
	for _, sk := range deps.Skills.All() {
		if sk.Name == "none" || sk.Name == "" {
			continue
		}
		if !levelSet[sk.SkillLevel] {
			continue
		}
		if knownSet[sk.SkillID] {
			continue
		}
		player.KnownSpells = append(player.KnownSpells, sk.SkillID)
		knownSet[sk.SkillID] = true
		count++
	}

	sendAllSpells(sess, player, deps)

	classNames := []string{"王族", "騎士", "精靈", "法師", "黑暗精靈", "龍騎士", "幻術師"}
	gmMsgf(sess, "已學會 %s 全部技能 (新增 %d 個)", classNames[player.ClassType], count)
}

func gmSpawn(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .spawn <npcID> [數量]")
		return
	}
	npcID, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, "\\f3無效的NPC ID")
		return
	}
	count := 1
	if len(args) >= 2 {
		c, err := strconv.Atoi(args[1])
		if err == nil && c > 0 && c <= 50 {
			count = c
		}
	}

	if deps.Npcs == nil {
		gmMsg(sess, "\\f3NPC模板未載入")
		return
	}

	tmpl := deps.Npcs.Get(int32(npcID))
	if tmpl == nil {
		gmMsgf(sess, "\\f3找不到NPC模板: %d", npcID)
		return
	}

	for i := 0; i < count; i++ {
		// Spawn near player with slight random offset
		x := player.X + int32(rand.Intn(5)) - 2
		y := player.Y + int32(rand.Intn(5)) - 2

		atkSpeed := tmpl.AtkSpeed
		moveSpeed := tmpl.PassiveSpeed
		if deps.SprTable != nil {
			gfx := int(tmpl.GfxID)
			if tmpl.AtkSpeed != 0 {
				if v := deps.SprTable.GetAttackSpeed(gfx, data.ActAttack); v > 0 {
					atkSpeed = int16(v)
				}
			}
			if tmpl.PassiveSpeed != 0 {
				if v := deps.SprTable.GetMoveSpeed(gfx, data.ActWalk); v > 0 {
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
			MapID:        player.MapID,
			Heading:      int16(rand.Intn(8)),
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
			SpawnMapID:   player.MapID,
			RespawnDelay: 0, // GM-spawned: no respawn
		}
		deps.World.AddNpc(npc)

		// Broadcast to nearby players
		nearby := deps.World.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		for _, viewer := range nearby {
			SendNpcPack(viewer.Session, npc)
		}
	}

	gmMsgf(sess, "已召喚 %s (ID:%d) x%d", tmpl.Name, npcID, count)
}

func gmKill(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	// Kill nearby NPCs within 3 tiles
	nearby := deps.World.GetNearbyNpcs(player.X, player.Y, player.MapID)
	killed := 0
	for _, npc := range nearby {
		if npc.Dead {
			continue
		}
		dx := player.X - npc.X
		dy := player.Y - npc.Y
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		dist := dx
		if dy > dist {
			dist = dy
		}
		if dist <= 3 {
			npc.HP = 0
			npc.Dead = true
			deps.World.NpcDied(npc)
			viewers := deps.World.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
			for _, v := range viewers {
				sendActionGfx(v.Session, npc.ID, 8)
				SendNpcDeadPack(v.Session, npc)
			}
			npc.DeleteTimer = 50 // 10 seconds for death animation
			if npc.RespawnDelay > 0 {
				npc.RespawnTimer = npc.RespawnDelay * 5
			}
			killed++
		}
	}
	gmMsgf(sess, "已擊殺 %d 個NPC", killed)
}

func gmKillAll(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	nearby := deps.World.GetNearbyNpcs(player.X, player.Y, player.MapID)
	killed := 0
	for _, npc := range nearby {
		if npc.Dead {
			continue
		}
		npc.HP = 0
		npc.Dead = true
		deps.World.NpcDied(npc)
		viewers := deps.World.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		for _, v := range viewers {
			sendActionGfx(v.Session, npc.ID, 8)
			SendNpcDeadPack(v.Session, npc)
		}
		npc.DeleteTimer = 50 // 10 seconds for death animation
		if npc.RespawnDelay > 0 {
			npc.RespawnTimer = npc.RespawnDelay * 5
		}
		killed++
	}
	gmMsgf(sess, "已擊殺附近 %d 個NPC", killed)
}

func gmSpeed(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .speed <0|1|2>  (0=正常,1=加速,2=勇水)")
		return
	}
	spd, err := strconv.Atoi(args[0])
	if err != nil || spd < 0 || spd > 3 {
		gmMsg(sess, "\\f3速度必須在 0-3 之間")
		return
	}

	switch spd {
	case 0:
		player.MoveSpeed = 0
		player.BraveSpeed = 0
		player.HasteTicks = 0
		player.BraveTicks = 0
		sendSpeedPacket(sess, player.CharID, 0, 0)
	case 1:
		player.MoveSpeed = 1
		player.HasteTicks = 3600 * 5 // 1 hour
		sendSpeedPacket(sess, player.CharID, 1, 3600)
	case 2:
		player.MoveSpeed = 1
		player.BraveSpeed = 1
		player.HasteTicks = 3600 * 5
		player.BraveTicks = 3600 * 5
		sendSpeedPacket(sess, player.CharID, 1, 3600)
		sendSpeedPacket(sess, player.CharID, 3, 3600)
	case 3:
		player.MoveSpeed = 1
		player.BraveSpeed = 3
		player.HasteTicks = 3600 * 5
		player.BraveTicks = 3600 * 5
		sendSpeedPacket(sess, player.CharID, 1, 3600)
		sendSpeedPacket(sess, player.CharID, 3, 3600)
	}

	// Broadcast to nearby
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		if spd == 0 {
			sendSpeedPacket(other.Session, player.CharID, 0, 0)
		} else {
			sendSpeedPacket(other.Session, player.CharID, 1, 0)
			if player.BraveSpeed > 0 {
				sendSpeedPacket(other.Session, player.CharID, player.BraveSpeed, 0)
			}
		}
	}

	names := []string{"正常", "加速", "二段加速", "精靈勇水"}
	gmMsgf(sess, "移動速度已設為: %s", names[spd])
}

func gmWho(sess *net.Session, deps *Deps) {
	count := 0
	deps.World.AllPlayers(func(p *world.PlayerInfo) {
		count++
		gmMsgf(sess, "  %s (Lv.%d) 位置:(%d,%d) 地圖:%d", p.Name, p.Level, p.X, p.Y, p.MapID)
	})
	gmMsgf(sess, "線上人數: %d", count)
}

func gmGoto(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .goto <玩家名>")
		return
	}
	target := deps.World.GetByName(args[0])
	if target == nil {
		gmMsgf(sess, "\\f3找不到玩家: %s", args[0])
		return
	}

	teleportPlayer(sess, player, target.X, target.Y, target.MapID, 5, deps)
	gmMsgf(sess, "已傳送至 %s 身邊 (%d,%d) 地圖:%d", target.Name, target.X, target.Y, target.MapID)
}

func gmRecall(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .recall <玩家名>")
		return
	}
	target := deps.World.GetByName(args[0])
	if target == nil {
		gmMsgf(sess, "\\f3找不到玩家: %s", args[0])
		return
	}

	teleportPlayer(target.Session, target, player.X, player.Y, player.MapID, 5, deps)
	gmMsgf(sess, "已召喚 %s 到身邊", target.Name)
	gmMsg(target.Session, "你被GM召喚了")
}

func gmExp(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .exp <數值>")
		return
	}
	val, err := strconv.Atoi(args[0])
	if err != nil || val <= 0 {
		gmMsg(sess, "\\f3無效的經驗值")
		return
	}

	deps.Combat.AddExp(player, int32(val))
	gmMsgf(sess, "已獲得 %d 經驗值 (Lv.%d Exp:%d)", val, player.Level, player.Exp)
}

func gmClass(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .class <0-6>")
		gmMsg(sess, "  0=王族 1=騎士 2=精靈 3=法師 4=黑暗精靈 5=龍騎士 6=幻術師")
		return
	}
	classType, err := strconv.Atoi(args[0])
	if err != nil || classType < 0 || classType > 6 {
		gmMsg(sess, "\\f3職業必須在 0-6 之間")
		return
	}

	// Update ClassType and ClassID (GFX) — matches Java initial class GFX IDs
	player.ClassType = int16(classType)
	switch classType {
	case 0: // Prince/Princess
		if player.ClassID >= 100 { // female range
			player.ClassID = 100
		} else {
			player.ClassID = 0
		}
	case 1: // Knight
		if player.ClassID >= 100 {
			player.ClassID = 161
		} else {
			player.ClassID = 61
		}
	case 2: // Elf
		if player.ClassID >= 100 {
			player.ClassID = 238
		} else {
			player.ClassID = 138
		}
	case 3: // Wizard
		if player.ClassID >= 100 {
			player.ClassID = 234
		} else {
			player.ClassID = 134
		}
	case 4: // Dark Elf
		if player.ClassID >= 100 {
			player.ClassID = 237
		} else {
			player.ClassID = 137
		}
	case 5: // Dragon Knight
		if player.ClassID >= 100 {
			player.ClassID = 6368
		} else {
			player.ClassID = 6275
		}
	case 6: // Illusionist
		if player.ClassID >= 100 {
			player.ClassID = 6371
		} else {
			player.ClassID = 6278
		}
	}

	// Send visual refresh
	sendPlayerStatus(sess, player)
	broadcastVisualUpdate(sess, player, deps)

	// Re-send own charpack to update appearance
	SendPutObject(sess, player)

	names := []string{"王族", "騎士", "精靈", "法師", "黑暗精靈", "龍騎士", "幻術師"}
	gmMsgf(sess, "職業已變更為: %s", names[classType])
}

func gmSave(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	gmMsg(sess, "正在存檔...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := &persist.CharacterRow{
		Name:       player.Name,
		Level:      player.Level,
		Exp:        int64(player.Exp),
		HP:         player.HP,
		MP:         player.MP,
		MaxHP:      player.MaxHP,
		MaxMP:      player.MaxMP,
		X:          player.X,
		Y:          player.Y,
		MapID:      player.MapID,
		Heading:    player.Heading,
		Lawful:     player.Lawful,
		Str:        player.Str,
		Dex:        player.Dex,
		Con:        player.Con,
		Wis:        player.Wis,
		Cha:        player.Cha,
		Intel:      player.Intel,
		BonusStats: player.BonusStats,
		ClanID:     player.ClanID,
		ClanName:   player.ClanName,
		ClanRank:   player.ClanRank,
		Title:      player.Title,
	}
	if err := deps.CharRepo.SaveCharacter(ctx, row); err != nil {
		gmMsgf(sess, "\\f3存檔失敗: %v", err)
		return
	}
	if err := deps.ItemRepo.SaveInventory(ctx, player.CharID, player.Inv, &player.Equip); err != nil {
		gmMsgf(sess, "\\f3物品存檔失敗: %v", err)
		return
	}
	if err := deps.CharRepo.SaveKnownSpells(ctx, player.Name, player.KnownSpells); err != nil {
		deps.Log.Error("儲存魔法書失敗", zap.Error(err))
	}

	gmMsg(sess, "存檔完成")
}

func gmRez(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	var target *world.PlayerInfo
	if len(args) >= 1 {
		target = deps.World.GetByName(args[0])
		if target == nil {
			gmMsgf(sess, "\\f3找不到玩家: %s", args[0])
			return
		}
	} else {
		target = player
	}

	if !target.Dead {
		gmMsgf(sess, "%s 沒有死亡", target.Name)
		return
	}

	target.Dead = false
	target.HP = target.MaxHP
	target.MP = target.MaxMP

	sendHpUpdate(target.Session, target)
	sendMpUpdate(target.Session, target)
	sendPlayerStatus(target.Session, target)

	// Refresh position
	SendPutObject(target.Session, target)

	nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		if viewer.SessionID != target.SessionID {
			SendPutObject(viewer.Session, target)
		}
	}

	if target == player {
		gmMsg(sess, "已復活")
	} else {
		gmMsgf(sess, "已復活 %s", target.Name)
		gmMsg(target.Session, "你被GM復活了")
	}
}

func gmShowInfo(sess *net.Session, player *world.PlayerInfo) {
	gmMsgf(sess, "=== %s 角色資訊 ===", player.Name)
	gmMsgf(sess, "等級:%d 職業:%d 經驗:%d", player.Level, player.ClassType, player.Exp)
	gmMsgf(sess, "HP:%d/%d MP:%d/%d AC:%d MR:%d", player.HP, player.MaxHP, player.MP, player.MaxMP, player.AC, player.MR)
	gmMsgf(sess, "STR:%d DEX:%d CON:%d WIS:%d INT:%d CHA:%d", player.Str, player.Dex, player.Con, player.Wis, player.Intel, player.Cha)
	gmMsgf(sess, "位置:(%d,%d) 地圖:%d 朝向:%d", player.X, player.Y, player.MapID, player.Heading)
	gmMsgf(sess, "命中:%d 傷害:%d 弓命中:%d 弓傷害:%d", player.HitMod, player.DmgMod, player.BowHitMod, player.BowDmgMod)
	gmMsgf(sess, "SP:%d HPR:%d MPR:%d Dodge:%d", player.SP, player.HPR, player.MPR, player.Dodge)
	gmMsgf(sess, "火抗:%d 水抗:%d 風抗:%d 地抗:%d", player.FireRes, player.WaterRes, player.WindRes, player.EarthRes)
	gmMsgf(sess, "背包物品: %d/%d", player.Inv.Size(), world.MaxInventorySize)
}

// calcBaseHPMP estimates HP/MP for a given level using Lua formulas.
func calcBaseHPMP(classType, level, con, wis int16, deps *Deps) (int16, int16) {
	// Get starting HP/MP from Lua character creation data
	initHP := int16(deps.Scripting.CalcInitHP(int(classType), int(con)))
	initMP := int16(deps.Scripting.CalcInitMP(int(classType), int(wis)))

	baseHP := initHP
	baseMP := initMP
	for lv := int16(2); lv <= level; lv++ {
		result := deps.Scripting.CalcLevelUp(int(classType), int(con), int(wis))
		baseHP += int16(result.HP)
		baseMP += int16(result.MP)
	}

	return baseHP, baseMP
}

func gmPoly(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .poly <polyID> [玩家名]")
		return
	}
	polyID, err := strconv.Atoi(args[0])
	if err != nil || polyID <= 0 {
		gmMsg(sess, "\\f3無效的變身ID")
		return
	}

	target := player
	if len(args) >= 2 {
		target = deps.World.GetByName(args[1])
		if target == nil {
			gmMsgf(sess, "\\f3找不到玩家: %s", args[1])
			return
		}
	}

	if deps.Polys == nil {
		gmMsg(sess, "\\f3變身資料未載入")
		return
	}

	poly := deps.Polys.GetByID(int32(polyID))
	if poly == nil {
		gmMsgf(sess, "\\f3找不到變身形態: %d", polyID)
		return
	}

	if deps.Polymorph != nil {
		deps.Polymorph.DoPoly(target, int32(polyID), 7200, data.PolyCauseGM)
	}
	gmMsgf(sess, "已將 %s 變身為 %s (GFX:%d)", target.Name, poly.Name, polyID)
}

// gmPolyGfx directly changes the player's sprite to any GFX ID, bypassing
// the polymorph data table. Usage: .polygfx <gfxID> [玩家名]
// Revert with .undopoly.
func gmPolyGfx(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .polygfx <gfxID> [玩家名]")
		return
	}
	gfxID, err := strconv.Atoi(args[0])
	if err != nil || gfxID <= 0 || gfxID > 65535 {
		gmMsg(sess, "\\f3無效的圖檔ID (1-65535)")
		return
	}

	target := player
	if len(args) >= 2 {
		target = deps.World.GetByName(args[1])
		if target == nil {
			gmMsgf(sess, "\\f3找不到玩家: %s", args[1])
			return
		}
	}

	// If already polymorphed, revert first
	if target.TempCharGfx > 0 && deps.Polymorph != nil {
		deps.Polymorph.UndoPoly(target)
	}

	target.TempCharGfx = int32(gfxID)
	target.PolyID = 0 // no equip restrictions for raw GFX change

	// Broadcast S_ChangeShape to self + nearby
	nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		sendChangeShape(viewer.Session, target.CharID, int32(gfxID), target.CurrentWeapon)
	}

	gmMsgf(sess, "已將 %s 變身為 GFX:%d", target.Name, gfxID)
}

func gmUndoPoly(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	target := player
	if len(args) >= 1 {
		target = deps.World.GetByName(args[0])
		if target == nil {
			gmMsgf(sess, "\\f3找不到玩家: %s", args[0])
			return
		}
	}

	if target.TempCharGfx == 0 {
		gmMsgf(sess, "%s 沒有變身", target.Name)
		return
	}

	if deps.Polymorph != nil {
		deps.Polymorph.UndoPoly(target)
	}
	if target == player {
		gmMsg(sess, "已解除變身")
	} else {
		gmMsgf(sess, "已解除 %s 的變身", target.Name)
	}
}

func gmLoc(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	target := player
	if len(args) >= 1 {
		target = deps.World.GetByName(args[0])
		if target == nil {
			gmMsgf(sess, "\\f3找不到玩家: %s", args[0])
			return
		}
	}
	gmMsgf(sess, "[%s] 座標: (%d, %d)  地圖: %d  朝向: %d",
		target.Name, target.X, target.Y, target.MapID, target.Heading)
}

// gmWall creates a collision wall (door) at the facing tile for testing.
// Usage: .wall [mode]
//   mode 1 (default): S_DoorPack(GfxId=0) + S_CHANGE_ATTR + S_REMOVE_OBJECT (invisible test)
//   mode 2: S_CHANGE_ATTR only (no door object)
//   mode 3: S_DoorPack(GfxId=0) + S_CHANGE_ATTR, keep visible (no remove)
func gmWall(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	mode := 1
	if len(args) > 0 {
		if m, err := strconv.Atoi(args[0]); err == nil && m >= 1 && m <= 6 {
			mode = m
		}
	}

	h := player.Heading
	if h < 0 || h > 7 {
		h = 0
	}
	tx := player.X + headingDX[h]
	ty := player.Y + headingDY[h]

	switch mode {
	case 1:
		// Door + S_CHANGE_ATTR + immediate S_REMOVE_OBJECT (try to keep passability but hide visual)
		door := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 0, LeftEdge: tx, RightEdge: tx, OpenStatus: world.DoorActionClose,
		}
		door2 := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 1, LeftEdge: ty, RightEdge: ty, OpenStatus: world.DoorActionClose,
		}
		deps.World.AddDoor(door)
		deps.World.AddDoor(door2)
		SendDoorPerceive(sess, door)
		SendDoorPerceive(sess, door2)
		// Immediately remove the visual objects — passability might persist
		SendRemoveObject(sess, door.ID)
		SendRemoveObject(sess, door2.ID)
		gmMsgf(sess, "模式1: 門+封包+移除視覺 (%d,%d)", tx, ty)

	case 2:
		// S_CHANGE_ATTR only (no door object)
		sendDoorAttr(sess, tx, ty, 0, false)
		sendDoorAttr(sess, tx, ty, 1, false)
		sendDoorAttr(sess, tx, ty+1, 0, false)
		sendDoorAttr(sess, tx-1, ty, 1, false)
		gmMsgf(sess, "模式2: 僅S_CHANGE_ATTR (%d,%d) 無門物件", tx, ty)

	case 3:
		// Door + S_CHANGE_ATTR, keep visible (no S_REMOVE_OBJECT) — same as old mode 1
		door := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 0, LeftEdge: tx, RightEdge: tx, OpenStatus: world.DoorActionClose,
		}
		door2 := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 1, LeftEdge: ty, RightEdge: ty, OpenStatus: world.DoorActionClose,
		}
		deps.World.AddDoor(door)
		deps.World.AddDoor(door2)
		SendDoorPerceive(sess, door)
		SendDoorPerceive(sess, door2)
		gmMsgf(sess, "模式3: 門+封包 保留視覺 (%d,%d) ID=%d,%d", tx, ty, door.ID, door2.ID)

	case 4:
		// Only S_DoorPack (no S_CHANGE_ATTR) + S_REMOVE_OBJECT — test if DoorPack alone blocks
		door := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 0, LeftEdge: tx, RightEdge: tx, OpenStatus: world.DoorActionClose,
		}
		deps.World.AddDoor(door)
		sendDoorPack(sess, door)
		SendRemoveObject(sess, door.ID)
		gmMsgf(sess, "模式4: 僅S_DoorPack+移除 無S_CHANGE_ATTR (%d,%d)", tx, ty)

	case 5:
		// S_CHANGE_ATTR comprehensive — block ALL surrounding edges
		// Block tile itself (both directions)
		sendDoorAttr(sess, tx, ty, 0, false)
		sendDoorAttr(sess, tx, ty, 1, false)
		// Block all 4 adjacent tiles' edges pointing toward (tx, ty)
		sendDoorAttr(sess, tx, ty+1, 0, false)  // south tile "/" edge
		sendDoorAttr(sess, tx, ty+1, 1, false)  // south tile "\" edge
		sendDoorAttr(sess, tx-1, ty, 0, false)  // west tile "/" edge
		sendDoorAttr(sess, tx-1, ty, 1, false)   // west tile "\" edge
		sendDoorAttr(sess, tx, ty-1, 0, false)  // north tile "/" edge
		sendDoorAttr(sess, tx, ty-1, 1, false)  // north tile "\" edge
		sendDoorAttr(sess, tx+1, ty, 0, false)  // east tile "/" edge
		sendDoorAttr(sess, tx+1, ty, 1, false)   // east tile "\" edge
		gmMsgf(sess, "模式5: 全方位S_CHANGE_ATTR (%d,%d) + 4鄰居", tx, ty)

	case 6:
		// Try S_DoorPack with dead status (37) + S_REMOVE_OBJECT — dead door might be invisible
		door := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 0, LeftEdge: tx, RightEdge: tx, OpenStatus: world.DoorActionClose,
		}
		door2 := &world.DoorInfo{
			ID: world.NextDoorID(), GfxID: 0, X: tx, Y: ty, MapID: player.MapID,
			MaxHP: 0, HP: 1, Direction: 1, LeftEdge: ty, RightEdge: ty, OpenStatus: world.DoorActionClose,
		}
		deps.World.AddDoor(door)
		deps.World.AddDoor(door2)
		// Send door pack + S_CHANGE_ATTR
		SendDoorPerceive(sess, door)
		SendDoorPerceive(sess, door2)
		// Then send "die" action to make them disappear visually
		sendDoorAction(sess, door.ID, world.DoorActionDie)
		sendDoorAction(sess, door2.ID, world.DoorActionDie)
		gmMsgf(sess, "模式6: 門+死亡動畫 (%d,%d) ID=%d,%d", tx, ty, door.ID, door2.ID)
	}
}

// gmClearWall removes all test walls/doors near the player.
func gmClearWall(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	removed := 0
	nearbyDoors := deps.World.GetNearbyDoors(player.X, player.Y, player.MapID)
	for _, d := range nearbyDoors {
		// Only remove GM-spawned doors (GfxId=0 or test doors at exact position)
		if d.GfxID == 0 || d.GfxID == 2618 {
			SendRemoveObject(sess, d.ID)
			// Make passable again
			sendDoorAttr(sess, d.EntranceX(), d.EntranceY(), d.Direction, true)
			deps.World.RemoveDoor(d.ID)
			removed++
		}
	}
	gmMsgf(sess, "已清除 %d 個測試牆壁", removed)
}

func gmWeather(sess *net.Session, _ *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, ".weather <0-3, 17-19>  (0=clear, 1-3=snow, 17-19=rain)")
		return
	}
	val, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, ".weather <0-3, 17-19>")
		return
	}
	// Validate weather type (Java: 0=clear, 1-3=snow, 17-19=rain)
	valid := false
	for _, t := range []int{0, 1, 2, 3, 17, 18, 19} {
		if val == t {
			valid = true
			break
		}
	}
	if !valid {
		gmMsg(sess, "有效值: 0,1,2,3,17,18,19")
		return
	}
	deps.World.Weather = byte(val)
	deps.World.AllPlayers(func(p *world.PlayerInfo) {
		sendWeather(p.Session, byte(val))
	})
	gmMsgf(sess, "天氣已變更為 %d", val)
}

// gmStressTest 一次生成大量怪物用於壓力測試。
// 用法: .stresstest <npcID> [數量] [半徑]
// 怪物分散在玩家周圍，不會重生（關服即消失）。
func gmStressTest(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .stresstest <npcID> [數量] [半徑]")
		return
	}

	npcID, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, "\\f3無效的NPC ID")
		return
	}

	count := 10000
	if len(args) >= 2 {
		if c, err := strconv.Atoi(args[1]); err == nil && c > 0 {
			if c > 10000 {
				c = 10000
			}
			count = c
		}
	}

	radius := int32(50)
	if len(args) >= 3 {
		if r, err := strconv.Atoi(args[2]); err == nil && r > 0 {
			if r > 100 {
				r = 100
			}
			radius = int32(r)
		}
	}

	if deps.Npcs == nil {
		gmMsg(sess, "\\f3NPC模板未載入")
		return
	}
	tmpl := deps.Npcs.Get(int32(npcID))
	if tmpl == nil {
		gmMsgf(sess, "\\f3找不到NPC模板: %d", npcID)
		return
	}

	// 查詢動畫速度（只查一次，所有 NPC 共用）
	atkSpeed := tmpl.AtkSpeed
	moveSpeed := tmpl.PassiveSpeed
	if deps.SprTable != nil {
		gfx := int(tmpl.GfxID)
		if tmpl.AtkSpeed != 0 {
			if v := deps.SprTable.GetAttackSpeed(gfx, data.ActAttack); v > 0 {
				atkSpeed = int16(v)
			}
		}
		if tmpl.PassiveSpeed != 0 {
			if v := deps.SprTable.GetMoveSpeed(gfx, data.ActWalk); v > 0 {
				moveSpeed = int16(v)
			}
		}
	}

	gmMsgf(sess, "開始生成 %d 隻 %s（半徑 %d 格）...", count, tmpl.Name, radius)

	spawned := 0
	for i := 0; i < count; i++ {
		x := player.X + int32(rand.Intn(int(radius*2+1))) - radius
		y := player.Y + int32(rand.Intn(int(radius*2+1))) - radius

		// 可行走性檢查（最多重試 3 次）
		if deps.MapData != nil {
			ok := deps.MapData.IsPassablePoint(player.MapID, x, y)
			for retry := 0; !ok && retry < 3; retry++ {
				x = player.X + int32(rand.Intn(int(radius*2+1))) - radius
				y = player.Y + int32(rand.Intn(int(radius*2+1))) - radius
				ok = deps.MapData.IsPassablePoint(player.MapID, x, y)
			}
			if !ok {
				continue
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
			MapID:        player.MapID,
			Heading:      int16(rand.Intn(8)),
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
			SpawnMapID:   player.MapID,
			RespawnDelay: 0, // 壓力測試：不重生
		}
		deps.World.AddNpc(npc)
		spawned++
	}

	gmMsgf(sess, "壓力測試完成：已生成 %d 隻 %s（半徑 %d 格）", spawned, tmpl.Name, radius)
	gmMsg(sess, "走動即可看到怪物，使用 .cleartest 清除")
}

// gmClearTest 清除所有壓力測試用怪物（RespawnDelay == 0 的非永久 NPC）。
func gmClearTest(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	removed := 0
	for _, npc := range deps.World.NpcList() {
		if npc.Dead || npc.RespawnDelay > 0 {
			continue
		}
		npc.HP = 0
		npc.Dead = true
		deps.World.NpcDied(npc)
		npc.DeleteTimer = 1 // 下一 tick 由 NpcRespawnSystem 廣播 RemoveObject
		removed++
	}
	gmMsgf(sess, "已清除 %d 隻測試怪物", removed)
}

// gmBuff 強制套用指定 buff（繞過已學/MP/材料驗證）。
// 用法: .buff <skillID>
func gmBuff(sess *net.Session, player *world.PlayerInfo, args []string, deps *Deps) {
	if len(args) < 1 {
		gmMsg(sess, "\\f3用法: .buff <skillID>")
		return
	}
	skillID, err := strconv.Atoi(args[0])
	if err != nil {
		gmMsg(sess, "\\f3技能ID必須是數字")
		return
	}
	if deps.Skill == nil {
		gmMsg(sess, "\\f3技能系統未初始化")
		return
	}
	ok := deps.Skill.ApplyGMBuff(player, int32(skillID))
	if !ok {
		gmMsgf(sess, "\\f3未知的技能ID: %d", skillID)
		return
	}
	skill := deps.Skills.Get(int32(skillID))
	name := fmt.Sprintf("%d", skillID)
	if skill != nil {
		name = skill.Name
	}
	gmMsgf(sess, "\\f=已套用 buff: %s (ID:%d)", name, skillID)
}

// gmAllBuff 套用所有常用 buff。
// 用法: .allbuff
func gmAllBuff(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	if deps.Skill == nil {
		gmMsg(sess, "\\f3技能系統未初始化")
		return
	}
	// 常用 buff 列表
	buffList := []int32{
		3,  // 保護罩 Shield (AC-2)
		8,  // 神聖武器 Holy Weapon (dmg+1, hit+1)
		42, // 體魄強健術 Physical Enchant STR (STR+5)
		43, // 加速術 Haste (移動加速)
		32, // 冥想術 Meditation (MPR+5)
		14, // 負重強化 Decrease Weight (負重+180)
	}
	count := 0
	for _, sid := range buffList {
		if deps.Skill.ApplyGMBuff(player, sid) {
			count++
		}
	}
	gmMsgf(sess, "\\f=已套用 %d 個常用 buff", count)
}

// gmInvisible 切換 GM 隱身狀態（不受 Cancellation 影響的純旗標隱身）。
func gmInvisible(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	player.Invisible = !player.Invisible
	SendInvisible(sess, player.CharID, player.Invisible)

	ws := deps.World
	nearby := ws.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)

	if player.Invisible {
		// 隱身：周圍玩家移除我的角色顯示
		for _, other := range nearby {
			SendRemoveObject(other.Session, player.CharID)
		}
		gmMsg(sess, "\\f2GM 隱身已開啟。")
	} else {
		// 解除隱身：周圍玩家重新顯示我
		for _, other := range nearby {
			SendPutObject(other.Session, player)
		}
		gmMsg(sess, "\\f2GM 隱身已關閉。")
	}
}
