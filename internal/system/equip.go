package system

import (
	"fmt"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// EquipSystem 負責所有裝備邏輯（穿脫武器/防具、套裝系統、屬性計算）。
// 實作 handler.EquipManager 介面。
type EquipSystem struct {
	deps *handler.Deps
}

// NewEquipSystem 建立裝備系統。
func NewEquipSystem(deps *handler.Deps) *EquipSystem {
	return &EquipSystem{deps: deps}
}

// ==================== 裝備武器 ====================

// EquipWeapon 裝備武器或脫下已裝備的武器。
func (s *EquipSystem) EquipWeapon(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) {
	if invItem.Equipped {
		// 詛咒物品不可脫下（Java: bless == 2, message 150）
		if invItem.Bless == 2 {
			handler.SendServerMessage(sess, 150)
			return
		}
		// 已裝備 → 脫下
		s.UnequipSlot(sess, player, world.SlotWeapon)
		return
	}

	// 職業限制
	if !canClassUse(player.ClassType, itemInfo) {
		handler.SendServerMessage(sess, 264) // "你的職業無法使用此道具。"
		return
	}

	// 等級限制
	if !checkLevelRestriction(sess, player.Level, itemInfo) {
		return
	}

	// 變身武器限制
	if player.PolyID != 0 && s.deps.Polys != nil {
		poly := s.deps.Polys.GetByID(player.PolyID)
		if poly != nil && !poly.IsWeaponEquipable(itemInfo.Type) {
			handler.SendServerMessage(sess, 285) // "此形態無法裝備此武器。"
			return
		}
	}

	// 脫下當前武器
	if cur := player.Equip.Weapon(); cur != nil {
		s.UnequipSlot(sess, player, world.SlotWeapon)
	}

	// 雙手武器：同時脫下盾牌/防衛器
	if world.IsTwoHanded(itemInfo.Type) {
		if player.Equip.Get(world.SlotShield) != nil {
			s.UnequipSlot(sess, player, world.SlotShield)
		}
		if player.Equip.Get(world.SlotGuarder) != nil {
			s.UnequipSlot(sess, player, world.SlotGuarder)
		}
	}

	// 裝備
	invItem.Equipped = true
	player.Equip.Set(world.SlotWeapon, invItem)
	player.CurrentWeapon = world.WeaponVisualID(itemInfo.Type)

	// 傳送背包狀態更新
	sendItemNameUpdate(sess, invItem, itemInfo)
	sendEquipSlotUpdate(sess, invItem.ObjectID, world.SlotWeapon, true)

	// 套裝偵測
	newSetPoly, oldSetPoly := s.updateArmorSetOnEquip(player, invItem.ItemID)

	// 重新計算裝備屬性
	s.RecalcEquipStats(sess, player)

	// 廣播視覺更新
	s.broadcastVisualUpdate(sess, player)

	// 套裝變身
	if oldSetPoly > 0 {
		if s.deps.Polymorph != nil {
			s.deps.Polymorph.UndoPoly(player)
		}
	}
	if newSetPoly > 0 {
		if s.deps.Polymorph != nil {
			s.deps.Polymorph.DoPoly(player, newSetPoly, 0, data.PolyCauseNPC)
		}
	}

	s.deps.Log.Debug("武器裝備",
		zap.String("player", player.Name),
		zap.String("weapon", invItem.Name),
		zap.String("type", itemInfo.Type),
	)
}

// ==================== 裝備防具 ====================

// EquipArmor 裝備防具或脫下已裝備的防具。
func (s *EquipSystem) EquipArmor(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo) {
	slot := world.ArmorSlotFromType(itemInfo.Type)
	if slot == world.SlotNone {
		s.deps.Log.Debug("未知防具類型", zap.String("type", itemInfo.Type))
		return
	}

	if invItem.Equipped {
		// 詛咒物品不可脫下
		if invItem.Bless == 2 {
			handler.SendServerMessage(sess, 150)
			return
		}

		eqSlot := s.FindEquippedSlot(player, invItem)
		if eqSlot == world.SlotNone {
			return
		}

		// 脫下分層限制（Java C_ItemUSe.java）
		// T恤不可在身體防具穿著時脫下
		if eqSlot == world.SlotTShirt && player.Equip.Get(world.SlotArmor) != nil {
			handler.SendServerMessage(sess, 127)
			return
		}
		// 身體防具或T恤不可在斗篷穿著時脫下
		if (eqSlot == world.SlotArmor || eqSlot == world.SlotTShirt) && player.Equip.Get(world.SlotCloak) != nil {
			handler.SendServerMessage(sess, 127)
			return
		}

		s.UnequipSlot(sess, player, eqSlot)
		return
	}

	// 職業限制
	if !canClassUse(player.ClassType, itemInfo) {
		handler.SendServerMessage(sess, 264)
		return
	}

	// 等級限制
	if !checkLevelRestriction(sess, player.Level, itemInfo) {
		return
	}

	// 變身防具限制
	if player.PolyID != 0 && s.deps.Polys != nil {
		poly := s.deps.Polys.GetByID(player.PolyID)
		if poly != nil && !poly.IsArmorEquipable(itemInfo.Type) {
			handler.SendServerMessage(sess, 285)
			return
		}
	}

	// 戒指：選擇空欄位（Ring1 或 Ring2）
	if slot == world.SlotRing1 {
		if player.Equip.Get(world.SlotRing1) == nil {
			slot = world.SlotRing1
		} else if player.Equip.Get(world.SlotRing2) == nil {
			slot = world.SlotRing2
		} else {
			s.UnequipSlot(sess, player, world.SlotRing1)
			slot = world.SlotRing1
		}
	}

	// 盾牌 + 腰帶互斥（Java: type 7 和 type 13）
	if slot == world.SlotShield || slot == world.SlotGuarder {
		if player.Equip.Get(world.SlotBelt) != nil {
			handler.SendServerMessage(sess, 124)
			return
		}
	}
	if slot == world.SlotBelt {
		if player.Equip.Get(world.SlotShield) != nil || player.Equip.Get(world.SlotGuarder) != nil {
			handler.SendServerMessage(sess, 124)
			return
		}
	}

	// 盾牌：不可與雙手武器同時裝備
	if slot == world.SlotShield || slot == world.SlotGuarder {
		wpn := player.Equip.Weapon()
		if wpn != nil {
			wpnInfo := s.deps.Items.Get(wpn.ItemID)
			if wpnInfo != nil && world.IsTwoHanded(wpnInfo.Type) {
				s.UnequipSlot(sess, player, world.SlotWeapon)
			}
		}
	}

	// 穿著分層限制（Java C_ItemUSe.java）
	if slot == world.SlotTShirt {
		if player.Equip.Get(world.SlotCloak) != nil {
			sendServerMessageS(sess, 126, "$224", "$225")
			return
		}
		if player.Equip.Get(world.SlotArmor) != nil {
			sendServerMessageS(sess, 126, "$224", "$226")
			return
		}
	}
	if slot == world.SlotArmor {
		if player.Equip.Get(world.SlotCloak) != nil {
			sendServerMessageS(sess, 126, "$226", "$225")
			return
		}
	}

	// 脫下此欄位的現有裝備
	if cur := player.Equip.Get(slot); cur != nil {
		s.UnequipSlot(sess, player, slot)
	}

	// 裝備
	invItem.Equipped = true
	player.Equip.Set(slot, invItem)

	sendItemNameUpdate(sess, invItem, itemInfo)
	sendEquipSlotUpdate(sess, invItem.ObjectID, slot, true)

	// 套裝偵測
	newSetPoly, oldSetPoly := s.updateArmorSetOnEquip(player, invItem.ItemID)

	// 重新計算裝備屬性
	s.RecalcEquipStats(sess, player)

	// 套裝變身
	if oldSetPoly > 0 {
		if s.deps.Polymorph != nil {
			s.deps.Polymorph.UndoPoly(player)
		}
	}
	if newSetPoly > 0 {
		if s.deps.Polymorph != nil {
			s.deps.Polymorph.DoPoly(player, newSetPoly, 0, data.PolyCauseNPC)
		}
	}

	s.deps.Log.Debug("防具裝備",
		zap.String("player", player.Name),
		zap.String("armor", invItem.Name),
		zap.String("slot", itemInfo.Type),
	)
}

// ==================== 脫下裝備 ====================

// UnequipSlot 脫下指定欄位的裝備。
func (s *EquipSystem) UnequipSlot(sess *net.Session, player *world.PlayerInfo, slot world.EquipSlot) {
	item := player.Equip.Get(slot)
	if item == nil {
		return
	}

	item.Equipped = false
	player.Equip.Set(slot, nil)

	// 檢查是否破壞了護甲套裝
	brokenSetPoly := s.updateArmorSetOnUnequip(player)

	// 脫下武器時清除視覺
	if slot == world.SlotWeapon {
		player.CurrentWeapon = 0
		s.broadcastVisualUpdate(sess, player)
	}

	// 更新物品名稱
	itemInfo := s.deps.Items.Get(item.ItemID)
	sendItemNameUpdate(sess, item, itemInfo)
	sendEquipSlotUpdate(sess, item.ObjectID, slot, false)

	// 重新計算屬性
	s.RecalcEquipStats(sess, player)

	// 套裝破壞時還原變身
	if brokenSetPoly > 0 {
		if s.deps.Polymorph != nil {
			s.deps.Polymorph.UndoPoly(player)
		}
	}
}

// FindEquippedSlot 找到物品所在的裝備欄位。
func (s *EquipSystem) FindEquippedSlot(player *world.PlayerInfo, item *world.InvItem) world.EquipSlot {
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		if player.Equip.Get(i) == item {
			return i
		}
	}
	return world.SlotNone
}

// ==================== 屬性計算 ====================

// RecalcEquipStats 重新計算裝備屬性並發送更新封包。
func (s *EquipSystem) RecalcEquipStats(sess *net.Session, player *world.PlayerInfo) {
	old := player.EquipBonuses
	applyEquipStats(player, s.deps.Items, s.deps.ArmorSets)

	// 發送更新封包
	handler.SendPlayerStatus(sess, player)
	handler.SendAbilityScores(sess, player)
	handler.SendMagicStatus(sess, byte(player.SP), uint16(player.MR))

	// 力量/體質變化時更新負重上限
	neo := player.EquipBonuses
	if neo.AddStr != old.AddStr || neo.AddCon != old.AddCon {
		handler.SendWeightUpdate(sess, player)
	}
}

// InitEquipStats 進入世界時初始化裝備屬性（不發送封包）：
//  1. 設定基礎 AC
//  2. 偵測護甲套裝
//  3. 計算裝備屬性加成
func (s *EquipSystem) InitEquipStats(player *world.PlayerInfo) {
	player.AC = int16(s.deps.Config.Gameplay.BaseAC)
	detectActiveArmorSet(player, s.deps.ArmorSets)
	applyEquipStats(player, s.deps.Items, s.deps.ArmorSets)
}

// SendEquipList 發送完整裝備欄位列表封包（登入時用）。
func (s *EquipSystem) SendEquipList(sess *net.Session, player *world.PlayerInfo) {
	sendEquipSlotList(sess, player)
}

// ==================== 套裝系統 ====================

// equippedItemSet 回傳目前所有已裝備物品的 ID 集合。
func equippedItemSet(player *world.PlayerInfo) map[int32]bool {
	m := make(map[int32]bool, 14)
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		if item := player.Equip.Get(i); item != nil {
			m[item.ItemID] = true
		}
	}
	return m
}

// detectActiveArmorSet 偵測玩家是否穿著完整套裝。
func detectActiveArmorSet(player *world.PlayerInfo, armorSets *data.ArmorSetTable) {
	if armorSets == nil {
		return
	}
	equipped := equippedItemSet(player)
	checked := make(map[int]bool)
	for itemID := range equipped {
		for _, set := range armorSets.GetSetsForItem(itemID) {
			if checked[set.ID] {
				continue
			}
			checked[set.ID] = true
			count := 0
			for _, sid := range set.Items {
				if equipped[sid] {
					count++
				}
			}
			if count >= len(set.Items) {
				player.ActiveSetID = set.ID
				return
			}
		}
	}
}

// updateArmorSetOnEquip 裝備物品時偵測套裝完成。
func (s *EquipSystem) updateArmorSetOnEquip(player *world.PlayerInfo, itemID int32) (newPolyID, oldPolyID int32) {
	armorSets := s.deps.ArmorSets
	if armorSets == nil {
		return 0, 0
	}
	equipped := equippedItemSet(player)
	for _, set := range armorSets.GetSetsForItem(itemID) {
		count := 0
		for _, sid := range set.Items {
			if equipped[sid] {
				count++
			}
		}
		if count >= len(set.Items) && player.ActiveSetID != set.ID {
			if player.ActiveSetID != 0 {
				if old := armorSets.GetByID(player.ActiveSetID); old != nil {
					oldPolyID = old.PolyID
				}
			}
			player.ActiveSetID = set.ID
			return set.PolyID, oldPolyID
		}
	}
	return 0, 0
}

// updateArmorSetOnUnequip 脫下物品後偵測套裝是否破壞。
func (s *EquipSystem) updateArmorSetOnUnequip(player *world.PlayerInfo) (brokenPolyID int32) {
	armorSets := s.deps.ArmorSets
	if armorSets == nil || player.ActiveSetID == 0 {
		return 0
	}
	set := armorSets.GetByID(player.ActiveSetID)
	if set == nil {
		player.ActiveSetID = 0
		return 0
	}
	equipped := equippedItemSet(player)
	count := 0
	for _, sid := range set.Items {
		if equipped[sid] {
			count++
		}
	}
	if count < len(set.Items) {
		player.ActiveSetID = 0
		return set.PolyID
	}
	return 0
}

// applyEquipStats 計算裝備屬性加成並應用到玩家（不發送封包）。
func applyEquipStats(player *world.PlayerInfo, items *data.ItemTable, armorSets *data.ArmorSetTable) {
	old := player.EquipBonuses
	neo := calcEquipStats(player, items, armorSets)

	player.AC += int16(neo.AC - old.AC)
	player.Str += int16(neo.AddStr - old.AddStr)
	player.Dex += int16(neo.AddDex - old.AddDex)
	player.Con += int16(neo.AddCon - old.AddCon)
	player.Intel += int16(neo.AddInt - old.AddInt)
	player.Wis += int16(neo.AddWis - old.AddWis)
	player.Cha += int16(neo.AddCha - old.AddCha)
	player.MaxHP += int16(neo.AddHP - old.AddHP)
	player.MaxMP += int16(neo.AddMP - old.AddMP)
	player.HitMod += int16(neo.HitMod - old.HitMod)
	player.DmgMod += int16(neo.DmgMod - old.DmgMod)
	player.BowHitMod += int16(neo.BowHitMod - old.BowHitMod)
	player.BowDmgMod += int16(neo.BowDmgMod - old.BowDmgMod)
	player.HPR += int16(neo.AddHPR - old.AddHPR)
	player.MPR += int16(neo.AddMPR - old.AddMPR)
	player.SP += int16(neo.AddSP - old.AddSP)
	player.MR += int16(neo.MDef - old.MDef)

	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	if player.MP > player.MaxMP {
		player.MP = player.MaxMP
	}

	player.EquipBonuses = neo
}

// calcEquipStats 計算玩家所有裝備的屬性加成總和（含套裝加成）。
func calcEquipStats(player *world.PlayerInfo, items *data.ItemTable, armorSets *data.ArmorSetTable) world.EquipStats {
	var stats world.EquipStats
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		invItem := player.Equip.Get(i)
		if invItem == nil {
			continue
		}
		info := items.Get(invItem.ItemID)
		if info == nil {
			continue
		}
		// AC：飾品不加衝裝等級加成
		if world.IsAccessorySlot(i) {
			stats.AC += info.AC
		} else {
			stats.AC += info.AC - int(invItem.EnchantLvl)
		}
		stats.HitMod += info.HitMod
		stats.DmgMod += info.DmgMod
		// 武器衝裝加成
		if i == world.SlotWeapon && invItem.EnchantLvl > 0 {
			stats.HitMod += int(invItem.EnchantLvl) / 2
			stats.DmgMod += int(invItem.EnchantLvl)
		}
		// NPC 魔法附加加成
		if invItem.DmgByMagic > 0 && invItem.DmgMagicExpiry > 0 {
			stats.DmgMod += int(invItem.DmgByMagic)
		}
		if invItem.AcByMagic > 0 && invItem.AcMagicExpiry > 0 {
			stats.AC -= int(invItem.AcByMagic)
		}
		stats.BowHitMod += info.BowHitMod
		stats.BowDmgMod += info.BowDmgMod
		stats.AddStr += info.AddStr
		stats.AddDex += info.AddDex
		stats.AddCon += info.AddCon
		stats.AddInt += info.AddInt
		stats.AddWis += info.AddWis
		stats.AddCha += info.AddCha
		stats.AddHP += info.AddHP
		stats.AddMP += info.AddMP
		stats.AddHPR += info.AddHPR
		stats.AddMPR += info.AddMPR
		stats.AddSP += info.AddSP
		stats.MDef += info.MDef
	}
	// 套裝加成
	if player.ActiveSetID > 0 && armorSets != nil {
		if set := armorSets.GetByID(player.ActiveSetID); set != nil {
			stats.AC += set.AC
			stats.AddHP += set.HP
			stats.AddMP += set.MP
			stats.AddHPR += set.HPR
			stats.AddMPR += set.MPR
			stats.MDef += set.MR
			stats.AddStr += set.Str
			stats.AddDex += set.Dex
			stats.AddCon += set.Con
			stats.AddInt += set.Intl
			stats.AddWis += set.Wis
			stats.AddCha += set.Cha
			stats.HitMod += set.Hit
			stats.DmgMod += set.Dmg
			stats.BowHitMod += set.BowHit
			stats.BowDmgMod += set.BowDmg
			stats.AddSP += set.SP
		}
	}
	return stats
}

// ==================== 輔助函式 ====================

// canClassUse 檢查職業是否可使用物品。
func canClassUse(classType int16, info *data.ItemInfo) bool {
	if !info.UseRoyal && !info.UseKnight && !info.UseElf && !info.UseMage &&
		!info.UseDarkElf && !info.UseDragonKnight && !info.UseIllusionist {
		return true
	}
	switch classType {
	case 0:
		return info.UseRoyal
	case 1:
		return info.UseKnight
	case 2:
		return info.UseElf
	case 3:
		return info.UseMage
	case 4:
		return info.UseDarkElf
	case 5:
		return info.UseDragonKnight
	case 6:
		return info.UseIllusionist
	}
	return false
}

// checkLevelRestriction 檢查等級限制。
func checkLevelRestriction(sess *net.Session, playerLevel int16, info *data.ItemInfo) bool {
	if info.MinLevel > 0 && int(playerLevel) < info.MinLevel {
		handler.SendServerMessageArgs(sess, 318, fmt.Sprintf("%d", info.MinLevel))
		return false
	}
	if info.MaxLevel > 0 && int(playerLevel) > info.MaxLevel {
		handler.SendServerMessageArgs(sess, 318, fmt.Sprintf("%d", info.MaxLevel))
		return false
	}
	return true
}

// ==================== 裝備封包建構 ====================

// sendItemNameUpdate 發送 S_CHANGE_ITEM_DESC (opcode 100) — 更新物品顯示名稱。
func sendItemNameUpdate(sess *net.Session, item *world.InvItem, itemInfo *data.ItemInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_ITEM_DESC)
	w.WriteD(item.ObjectID)
	w.WriteS(buildViewNameEquip(item, itemInfo))
	sess.Send(w.Bytes())
}

// buildViewNameEquip 建構裝備物品顯示名稱（精簡版，用於裝備名稱更新）。
func buildViewNameEquip(item *world.InvItem, itemInfo *data.ItemInfo) string {
	name := item.Name
	if item.EnchantLvl > 0 {
		name = fmt.Sprintf("+%d %s", item.EnchantLvl, name)
	} else if item.EnchantLvl < 0 {
		name = fmt.Sprintf("%d %s", item.EnchantLvl, name)
	}
	if item.Count > 1 {
		name += fmt.Sprintf(" (%d)", item.Count)
	}
	if item.Equipped && itemInfo != nil {
		switch itemInfo.Category {
		case data.CategoryWeapon:
			name += " ($9)"
		case data.CategoryArmor:
			name += " ($117)"
		}
	}
	return name
}

// sendEquipSlotUpdate 發送 S_EquipmentSlot — 單次穿脫動作。
func sendEquipSlotUpdate(sess *net.Session, itemObjID int32, slot world.EquipSlot, equipped bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_VOICE_CHAT)
	w.WriteC(0x42)
	w.WriteD(itemObjID)
	w.WriteC(world.EquipClientIndex(slot))
	if equipped {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	sess.Send(w.Bytes())
}

// sendEquipSlotList 發送完整裝備欄位列表（登入時用）。
// 使用 Java S_EquipmentWindow 格式（type 0x42）逐件發送。
func sendEquipSlotList(sess *net.Session, player *world.PlayerInfo) {
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		item := player.Equip.Get(i)
		if item != nil {
			sendEquipSlotUpdate(sess, item.ObjectID, i, true)
		}
	}
}

// sendServerMessageS 發送帶 $xxx 參數的伺服器訊息。
func sendServerMessageS(sess *net.Session, msgID uint16, args ...string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(byte(len(args)))
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// broadcastVisualUpdate 廣播玩家視覺更新到自己 + 附近玩家。
func (s *EquipSystem) broadcastVisualUpdate(sess *net.Session, player *world.PlayerInfo) {
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendCharVisualUpdate(viewer.Session, player)
	}
	sendCharVisualUpdate(sess, player)
}

// sendCharVisualUpdate 發送 S_CHANGE_DESC (opcode 119) — 角色視覺更新。
func sendCharVisualUpdate(viewer *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_DESC)
	w.WriteD(player.CharID)
	w.WriteC(player.CurrentWeapon)
	w.WriteC(0xff)
	w.WriteC(0xff)
	viewer.Send(w.Bytes())
}
