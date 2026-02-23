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
		gmHP(sess, player, args)
	case "mp":
		gmMP(sess, player, args)
	case "heal":
		gmHeal(sess, player)
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
	gmMsg(sess, ".save  — 手動存檔")
	gmMsg(sess, ".ac  — 顯示角色詳細資訊")
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

func gmHP(sess *net.Session, player *world.PlayerInfo, args []string) {
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
	if player.HP > 0 {
		player.Dead = false
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

func gmHeal(sess *net.Session, player *world.PlayerInfo) {
	player.HP = player.MaxHP
	player.MP = player.MaxMP
	if player.Dead {
		player.Dead = false
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
	enchant := byte(0)
	if len(args) >= 3 {
		e, err := strconv.Atoi(args[2])
		if err == nil && e >= 0 && e <= 15 {
			enchant = byte(e)
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
			AtkSpeed:     tmpl.AtkSpeed,
			MoveSpeed:    tmpl.PassiveSpeed,
			SpawnX:       x,
			SpawnY:       y,
			SpawnMapID:   player.MapID,
			RespawnDelay: 0, // GM-spawned: no respawn
		}
		deps.World.AddNpc(npc)

		// Broadcast to nearby players
		nearby := deps.World.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		for _, viewer := range nearby {
			sendNpcPack(viewer.Session, npc)
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
			viewers := deps.World.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
			for _, v := range viewers {
				sendActionGfx(v.Session, npc.ID, 8)
				sendRemoveObject(v.Session, npc.ID)
			}
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
		viewers := deps.World.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		for _, v := range viewers {
			sendActionGfx(v.Session, npc.ID, 8)
			sendRemoveObject(v.Session, npc.ID)
		}
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

	addExp(player, int32(val), deps)
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
	sendPutObject(sess, player)

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
	sendPutObject(target.Session, target)

	nearby := deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		if viewer.SessionID != target.SessionID {
			sendPutObject(viewer.Session, target)
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
