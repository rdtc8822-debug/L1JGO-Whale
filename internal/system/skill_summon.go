package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// SummonSystem 處理召喚相關技能邏輯。
type SummonSystem struct {
	deps *handler.Deps
}

func NewSummonSystem(deps *handler.Deps) *SummonSystem {
	return &SummonSystem{deps: deps}
}

// calcUsedPetCost 委派到 PetLifecycleManager 計算已使用的 CHA 消耗。
func (s *SummonSystem) calcUsedPetCost(charID int32) int {
	if s.deps.PetLife != nil {
		return s.deps.PetLife.CalcUsedPetCost(charID)
	}
	return 0
}

// ========================================================================
//  訊息 ID 常數
// ========================================================================

const (
	msgSummonNotEnoughMP uint16 = 278 // "因魔力不足而無法使用魔法。"
	msgSummonNotEnoughHP uint16 = 279 // "因體力不足而無法使用魔法。"
	msgSummonCastFail    uint16 = 280 // "施展魔法失敗。"
	msgInvalidTarget     uint16 = 79  // "無效的目標。"
	msgCannotSummonHere  uint16 = 353 // "此處無法召喚怪物。"
	msgLevelTooLow       uint16 = 743 // "等級太低而無法召喚怪物。"
	msgTooManyPets       uint16 = 319 // "你不能擁有太多的怪物。"
	msgMaterialShort     uint16 = 299 // "施放魔法所需材料不足"
)

// ========================================================================
//  Skill 51: Summon Monster — Java: SUMMON_MONSTER.java
// ========================================================================

// 無戒指的召喚查找表：等級 → {npc_id, pet_cost}
var summonByLevel = []struct {
	maxLevel int16
	npcID    int32
	cost     int
}{
	{31, 81210, 6},
	{35, 81213, 6},
	{39, 81216, 6},
	{43, 81219, 6},
	{47, 81222, 6},
	{51, 81225, 6},
	{127, 81228, 6}, // 52+（最大哨兵值）
}

// summonRingEntry 對應客戶端的召喚選擇 ID 到 NPC 模板 + 需求。
type summonRingEntry struct {
	npcID    int32
	minLevel int16
	chaCost  int
}

// summonRingTable: summonID（客戶端送來） → {npcID, minLevel, chaCost}
// Java 陣列: summon_id[], summon_npcid[], summon_lv[], petcost[]
var summonRingTable = map[int32]summonRingEntry{
	7: {81210, 28, 8}, 263: {81211, 28, 8}, 519: {81212, 28, 8},
	8: {81213, 32, 8}, 264: {81214, 32, 8}, 520: {81215, 32, 8},
	9: {81216, 36, 8}, 265: {81217, 36, 8}, 521: {81218, 36, 8},
	10: {81219, 40, 8}, 266: {81220, 40, 8}, 522: {81221, 40, 8},
	11: {81222, 44, 8}, 267: {81223, 44, 8}, 523: {81224, 44, 8},
	12: {81225, 48, 8}, 268: {81226, 48, 8}, 524: {81227, 48, 8},
	13: {81228, 52, 8}, 269: {81229, 52, 8}, 525: {81230, 52, 8},
	14: {81231, 56, 10}, 270: {81232, 56, 10}, 526: {81233, 56, 10},
	15: {81234, 60, 12}, 271: {81235, 60, 12}, 527: {81236, 60, 12},
	16: {81237, 64, 20},
	17: {81238, 68, 42},
	18: {81239, 72, 42},
	274: {81240, 72, 50},
}

// specialSummonNpcIDs 不能與其他寵物/召喚共存的特殊召喚。
// Java: 變形怪(81238), 黑豹(81239), 巨大牛人(81240)
var specialSummonNpcIDs = map[int32]bool{
	81238: true,
	81239: true,
	81240: true,
}

// ExecuteSummonMonster 處理技能 51（召喚怪物）。
// 無戒指時依施法者等級自動選擇 NPC；有戒指時依 targetID 選擇。
// Java: SUMMON_MONSTER.java
func (s *SummonSystem) ExecuteSummonMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	ws := s.deps.World

	s.deps.Log.Info("executeSummonMonster",
		zap.String("player", player.Name),
		zap.Int16("level", player.Level),
		zap.Int16("mapID", player.MapID),
		zap.Int32("targetID", targetID))

	// 檢查地圖是否允許召喚（RecallPets）
	if s.deps.MapData != nil {
		md := s.deps.MapData.GetInfo(player.MapID)
		if md != nil && !md.RecallPets {
			s.deps.Log.Info("summon blocked: map !RecallPets", zap.Int16("map", player.MapID))
			handler.SendServerMessage(sess, msgCannotSummonHere)
			return
		}
	}

	// 等級檢查（最低 28 級）
	if player.Level < 28 {
		s.deps.Log.Info("summon blocked: level < 28", zap.Int16("level", player.Level))
		handler.SendServerMessage(sess, msgLevelTooLow)
		return
	}

	var npcID int32
	var petCost int

	ringEquipped := hasSummonRing(player)

	if ringEquipped && targetID > 0 {
		// 戒指路徑：玩家透過 targetID（C_UseSkill）選擇特定召喚
		entry, ok := summonRingTable[targetID]
		if !ok {
			s.deps.Log.Warn("summon blocked: unknown ring summon ID", zap.Int32("summonID", targetID))
			handler.SendServerMessage(sess, msgSummonCastFail)
			return
		}
		if player.Level < entry.minLevel {
			s.deps.Log.Info("summon blocked: level too low for ring summon",
				zap.Int16("level", player.Level), zap.Int16("minLevel", entry.minLevel))
			handler.SendServerMessage(sess, msgLevelTooLow)
			return
		}
		// 特殊召喚（81238/81239/81240）不能與其他寵物/召喚共存
		if specialSummonNpcIDs[entry.npcID] {
			existingSummons := ws.GetSummonsByOwner(player.CharID)
			existingPets := ws.GetPetsByOwner(player.CharID)
			if len(existingSummons) > 0 || len(existingPets) > 0 {
				handler.SendServerMessage(sess, msgTooManyPets)
				return
			}
		}
		npcID = entry.npcID
		petCost = entry.chaCost
	} else if ringEquipped {
		// 有戒指但尚未選擇（targetID == 0）：顯示 "summonlist" 選擇對話框
		player.SummonSelectionMode = true
		handler.SendHypertext(sess, player.CharID, "summonlist")
		s.deps.Log.Info("summon ring: showing selection dialog")
		return
	} else {
		// 無戒指路徑：依等級自動選擇 NPC
		for _, entry := range summonByLevel {
			if player.Level <= entry.maxLevel {
				npcID = entry.npcID
				petCost = entry.cost
				break
			}
		}
	}

	// 計算可用 CHA
	baseCHA := int(player.Cha) + 6 // 召喚術固定 +6
	usedCHA := s.calcUsedPetCost(player.CharID)
	availCHA := baseCHA - usedCHA

	s.deps.Log.Info("summon CHA check",
		zap.Int("baseCHA", baseCHA), zap.Int("usedCHA", usedCHA),
		zap.Int("availCHA", availCHA), zap.Int("petCost", petCost),
		zap.Int32("npcID", npcID))

	if availCHA < petCost {
		handler.SendServerMessage(sess, msgTooManyPets)
		return
	}

	// 查找 NPC 模板
	tmpl := s.deps.Npcs.Get(npcID)
	if tmpl == nil {
		s.deps.Log.Warn("summon blocked: NPC template not found", zap.Int32("npcID", npcID))
		handler.SendServerMessage(sess, msgSummonCastFail)
		return
	}

	// 計算可召喚數量
	count := availCHA / petCost
	if count <= 0 {
		handler.SendServerMessage(sess, msgTooManyPets)
		return
	}

	// 特殊召喚上限 1 隻
	if specialSummonNpcIDs[npcID] && count > 1 {
		count = 1
	}

	// 所有驗證通過 — 消耗資源
	handler.ConsumeSkillResources(sess, player, skill)

	// 清除召喚選擇模式
	player.SummonSelectionMode = false

	masterName := player.Name
	for i := 0; i < count; i++ {
		sum := &world.SummonInfo{
			ID:          world.NextNpcID(),
			OwnerCharID: player.CharID,
			NpcID:       npcID,
			GfxID:       tmpl.GfxID,
			NameID:      tmpl.NameID,
			Name:        tmpl.Name,
			Level:       tmpl.Level,
			HP:          tmpl.HP,
			MaxHP:       tmpl.HP,
			MP:          tmpl.MP,
			MaxMP:       tmpl.MP,
			AC:          tmpl.AC,
			STR:         tmpl.STR,
			DEX:         tmpl.DEX,
			MR:          tmpl.MR,
			AtkDmg:      int32(tmpl.Level) + int32(tmpl.STR)/3,
			AtkSpeed:    tmpl.AtkSpeed,
			MoveSpd:     tmpl.PassiveSpeed,
			Ranged:      tmpl.Ranged,
			Lawful:      tmpl.Lawful,
			Size:        tmpl.Size,
			PetCost:     petCost,
			X:           player.X + int32(world.RandInt(5)) - 2,
			Y:           player.Y + int32(world.RandInt(5)) - 2,
			MapID:       player.MapID,
			Heading:     player.Heading,
			Status:      world.SummonAggressive,
			Tamed:       false,
			TimerTicks:  3600 * 5, // 3600 秒 × 5 tick/秒 = 18000 ticks
		}

		ws.AddSummon(sum)

		nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
		for _, viewer := range nearby {
			isOwner := viewer.CharID == player.CharID
			handler.SendSummonPack(viewer.Session, sum, isOwner, masterName)
		}
		handler.SendSummonPack(sess, sum, true, masterName)
	}

	// 顯示第一隻召喚獸的控制選單
	summons := ws.GetSummonsByOwner(player.CharID)
	if len(summons) > 0 {
		handler.SendSummonMenu(sess, summons[0])
	}
}

// ========================================================================
//  Skill 36: Taming Monster — Java: L1SkillUse taming section
// ========================================================================

// ExecuteTamingMonster 處理技能 36（馴服怪物）。
// 馴服活著的 NPC 並轉為施法者擁有的召喚獸。
func (s *SummonSystem) ExecuteTamingMonster(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	ws := s.deps.World

	// 目標必須是活著的 NPC
	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		handler.SendServerMessage(sess, msgInvalidTarget)
		return
	}

	// 檢查可馴服標記
	tmpl := s.deps.Npcs.Get(npc.NpcID)
	if tmpl == nil || !tmpl.Tameable {
		handler.SendServerMessage(sess, msgInvalidTarget)
		return
	}

	// 只對 L1Monster 有效
	if npc.Impl != "L1Monster" {
		handler.SendServerMessage(sess, msgInvalidTarget)
		return
	}

	// CHA 檢查（含職業加成）
	charisma := int(player.Cha)
	switch player.ClassType {
	case 2: // Elf
		charisma += 12
	case 3: // Wizard
		charisma += 6
	}
	usedCHA := s.calcUsedPetCost(player.CharID)
	availCHA := charisma - usedCHA
	if availCHA < 6 {
		handler.SendServerMessage(sess, msgTooManyPets)
		return
	}

	// 所有驗證通過 — 消耗資源
	handler.ConsumeSkillResources(sess, player, skill)

	// 從 NPC 建立召喚獸
	sum := &world.SummonInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		NpcID:       npc.NpcID,
		GfxID:       npc.GfxID,
		NameID:      npc.NameID,
		Name:        npc.Name,
		Level:       npc.Level,
		HP:          npc.HP,
		MaxHP:       npc.MaxHP,
		MP:          npc.MP,
		MaxMP:       npc.MaxMP,
		AC:          npc.AC,
		STR:         npc.STR,
		DEX:         npc.DEX,
		MR:          npc.MR,
		AtkDmg:      npc.AtkDmg,
		AtkSpeed:    npc.AtkSpeed,
		MoveSpd:     npc.MoveSpeed,
		Ranged:      npc.Ranged,
		Lawful:      npc.Lawful,
		Size:        npc.Size,
		PetCost:     6,
		X:           npc.X,
		Y:           npc.Y,
		MapID:       npc.MapID,
		Heading:     npc.Heading,
		Status:      world.SummonRest, // 馴服的召喚獸預設休憩
		Tamed:       true,
		TimerTicks:  0, // 永久（馴服無計時器）
	}

	// 移除原始 NPC + 解鎖格子
	npc.Dead = true
	ws.NpcDied(npc)
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, npc.ID)
	}

	// 加入召喚獸
	ws.AddSummon(sum)

	masterName := player.Name
	nearby = ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		handler.SendSummonPack(viewer.Session, sum, isOwner, masterName)
	}
	handler.SendSummonPack(sess, sum, true, masterName)
	handler.SendSummonMenu(sess, sum)
}

// ========================================================================
//  Skill 41: Create Zombie — Java: L1SummonInstance zombie constructor
// ========================================================================

// 法師殭屍查找表
var zombieByWizardLevel = []struct {
	minLevel, maxLevel int16
	npcID              int32
}{
	{24, 31, 81183},
	{32, 39, 81184},
	{40, 43, 81185},
	{44, 47, 81186},
	{48, 51, 81187},
	{52, 127, 81188},
}

// ExecuteCreateZombie 處理技能 41（創造殭屍）。
// 將死亡 NPC 屍體復活為不死召喚獸。
func (s *SummonSystem) ExecuteCreateZombie(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	ws := s.deps.World

	// 目標必須是死亡的 NPC
	npc := ws.GetNpc(targetID)
	if npc == nil || !npc.Dead {
		handler.SendServerMessage(sess, msgInvalidTarget)
		return
	}

	// CHA 檢查（含職業加成）
	charisma := int(player.Cha)
	switch player.ClassType {
	case 2: // Elf
		charisma += 12
	case 3: // Wizard
		charisma += 6
	}
	usedCHA := s.calcUsedPetCost(player.CharID)
	availCHA := charisma - usedCHA
	if availCHA < 6 {
		handler.SendServerMessage(sess, msgTooManyPets)
		return
	}

	// 依職業 + 等級選擇殭屍 NPC 模板
	var zombieNpcID int32 = 45065 // 預設人形殭屍
	switch player.ClassType {
	case 3: // Wizard
		for _, entry := range zombieByWizardLevel {
			if player.Level >= entry.minLevel && player.Level <= entry.maxLevel {
				zombieNpcID = entry.npcID
				break
			}
		}
	case 2: // Elf
		if player.Level >= 48 {
			zombieNpcID = 81183
		}
	}

	tmpl := s.deps.Npcs.Get(zombieNpcID)
	if tmpl == nil {
		return
	}

	// 所有驗證通過 — 消耗資源
	handler.ConsumeSkillResources(sess, player, skill)

	sum := &world.SummonInfo{
		ID:          world.NextNpcID(),
		OwnerCharID: player.CharID,
		NpcID:       zombieNpcID,
		GfxID:       tmpl.GfxID,
		NameID:      tmpl.NameID,
		Name:        tmpl.Name,
		Level:       tmpl.Level,
		HP:          tmpl.HP,
		MaxHP:       tmpl.HP,
		MP:          tmpl.MP,
		MaxMP:       tmpl.MP,
		AC:          tmpl.AC,
		STR:         tmpl.STR,
		DEX:         tmpl.DEX,
		MR:          tmpl.MR,
		AtkDmg:      int32(tmpl.Level) + int32(tmpl.STR)/3,
		AtkSpeed:    tmpl.AtkSpeed,
		MoveSpd:     tmpl.PassiveSpeed,
		Ranged:      tmpl.Ranged,
		Lawful:      tmpl.Lawful,
		Size:        tmpl.Size,
		PetCost:     6,
		X:           npc.X,
		Y:           npc.Y,
		MapID:       npc.MapID,
		Heading:     npc.Heading,
		Status:      world.SummonRest,
		Tamed:       true,
		TimerTicks:  0, // 永久
	}

	// 移除屍體
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		handler.SendRemoveObject(viewer.Session, npc.ID)
	}

	// 加入召喚獸
	ws.AddSummon(sum)

	masterName := player.Name
	nearby = ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		isOwner := viewer.CharID == player.CharID
		handler.SendSummonPack(viewer.Session, sum, isOwner, masterName)
	}
	handler.SendSummonPack(sess, sum, true, masterName)
	handler.SendSummonMenu(sess, sum)
}

// ========================================================================
//  Skill 145: Return to Nature — Java: L1SummonInstance.returnToNature()
// ========================================================================

// ExecuteReturnToNature 處理技能 145（歸返自然）。
// 非馴服的召喚獸銷毀；馴服的釋放回 NPC 型態。
func (s *SummonSystem) ExecuteReturnToNature(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo) {
	ws := s.deps.World
	summons := ws.GetSummonsByOwner(player.CharID)
	if len(summons) == 0 {
		return
	}

	// 驗證通過（有召喚獸） — 消耗資源
	handler.ConsumeSkillResources(sess, player, skill)

	for _, sum := range summons {
		if sum.Tamed {
			s.liberateSummon(sum)
		} else {
			s.killSummon(sum)
		}
	}
}

// DismissSummon 自願解散召喚獸（從 NPC 選單觸發）。
// 馴服的釋放；技能召喚的銷毀。
func (s *SummonSystem) DismissSummon(sum *world.SummonInfo, player *world.PlayerInfo) {
	if sum.Tamed {
		s.liberateSummon(sum)
	} else {
		s.killSummon(sum)
	}
}

// ========================================================================
//  內部輔助函式
// ========================================================================

// liberateSummon 將馴服的召喚獸轉回普通 NPC。
func (s *SummonSystem) liberateSummon(sum *world.SummonInfo) {
	ws := s.deps.World

	// 從世界移除召喚獸
	ws.RemoveSummon(sum.ID)
	nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		handler.SendCompanionEffect(viewer.Session, sum.ID, 2245) // 歸返自然音效
		handler.SendRemoveObject(viewer.Session, sum.ID)
	}

	// 查找原始 NPC 模板來重建
	tmpl := s.deps.Npcs.Get(sum.NpcID)
	if tmpl == nil {
		return
	}

	// 在召喚獸位置建立新 NPC
	npcID := world.NextNpcID()
	npc := &world.NpcInfo{
		ID:        npcID,
		NpcID:     sum.NpcID,
		Impl:      "L1Monster",
		GfxID:     tmpl.GfxID,
		Name:      tmpl.Name,
		NameID:    tmpl.NameID,
		Level:     sum.Level,
		HP:        sum.HP,
		MaxHP:     sum.MaxHP,
		MP:        sum.MP,
		MaxMP:     sum.MaxMP,
		AC:        sum.AC,
		STR:       sum.STR,
		DEX:       sum.DEX,
		MR:        sum.MR,
		PoisonAtk: tmpl.PoisonAtk,
		Exp:       0, // 釋放的 NPC 不給經驗
		Lawful:    sum.Lawful,
		Size:      sum.Size,
		AtkDmg:    sum.AtkDmg,
		Ranged:    sum.Ranged,
		X:         sum.X,
		Y:         sum.Y,
		MapID:     sum.MapID,
		Heading:   sum.Heading,
	}

	ws.AddNpc(npc)
	nearby = ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		handler.SendNpcPack(viewer.Session, npc)
	}
}

// killSummon 銷毀技能召喚的生物。
func (s *SummonSystem) killSummon(sum *world.SummonInfo) {
	ws := s.deps.World
	ws.RemoveSummon(sum.ID)
	nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	for _, viewer := range nearby {
		handler.SendCompanionEffect(viewer.Session, sum.ID, 169) // 召喚獸消失音效
		handler.SendRemoveObject(viewer.Session, sum.ID)
	}
}

// hasSummonRing 檢查玩家是否裝備召喚控制戒指。
func hasSummonRing(player *world.PlayerInfo) bool {
	for _, slot := range []world.EquipSlot{world.SlotRing1, world.SlotRing2} {
		item := player.Equip.Get(slot)
		if item != nil && (item.ItemID == 20284 || item.ItemID == 120284) {
			return true
		}
	}
	return false
}
