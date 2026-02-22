package handler

import (
	"fmt"
	"strconv"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// Message IDs for item validation
const (
	msgClassCannotUse uint16 = 264 // "你的職業無法使用此道具。"
	msgLevelTooLow    uint16 = 318 // "等級 %0以上才可使用此道具。"
)

// canClassUse checks if a player's class can use the given item.
// ClassType: 0=Prince, 1=Knight, 2=Elf, 3=Wizard, 4=DarkElf, 5=DragonKnight, 6=Illusionist
func canClassUse(classType int16, info *data.ItemInfo) bool {
	// If no class flags are set at all, item is usable by everyone
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

// checkLevelRestriction checks min/max level requirements. Returns true if OK.
func checkLevelRestriction(sess *net.Session, playerLevel int16, info *data.ItemInfo) bool {
	if info.MinLevel > 0 && int(playerLevel) < info.MinLevel {
		sendServerMessageArgs(sess, msgLevelTooLow, strconv.Itoa(info.MinLevel))
		return false
	}
	if info.MaxLevel > 0 && int(playerLevel) > info.MaxLevel {
		// "等級 %0以下才可使用此道具。" — use same message pattern
		sendServerMessageArgs(sess, msgLevelTooLow, strconv.Itoa(info.MaxLevel))
		return false
	}
	return true
}

// HandleDestroyItem processes C_DESTROY_ITEM (opcode 138) — player deletes an item.
// Format: [D objectID][D count]
func HandleDestroyItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	objectID := r.ReadD()
	count := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	item := player.Inv.FindByObjectID(objectID)
	if item == nil {
		return
	}

	// Cannot destroy equipped items
	if item.Equipped {
		return
	}

	if count <= 0 {
		count = item.Count
	}
	if count > item.Count {
		count = item.Count
	}

	removed := player.Inv.RemoveItem(objectID, count)
	if removed {
		sendRemoveInventoryItem(sess, objectID)
	} else {
		sendItemCountUpdate(sess, item)
	}
	sendWeightUpdate(sess, player)

	deps.Log.Debug("item destroyed",
		zap.String("player", player.Name),
		zap.Int32("item_id", item.ItemID),
		zap.Int32("count", count),
	)
}

// HandleDropItem processes C_DROP (opcode 25) — player drops item to ground.
// Format: [D objectID][D count]
func HandleDropItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	objectID := r.ReadD()
	count := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	item := player.Inv.FindByObjectID(objectID)
	if item == nil {
		return
	}

	// Cannot drop equipped items
	if item.Equipped {
		return
	}

	if count <= 0 {
		count = item.Count
	}
	if count > item.Count {
		count = item.Count
	}

	// Remember item info before removing from inventory
	itemID := item.ItemID
	itemName := item.Name
	enchantLvl := item.EnchantLvl

	removed := player.Inv.RemoveItem(objectID, count)
	if removed {
		sendRemoveInventoryItem(sess, objectID)
	} else {
		sendItemCountUpdate(sess, item)
	}
	sendWeightUpdate(sess, player)

	// Look up ground GFX
	grdGfx := int32(0)
	itemInfo := deps.Items.Get(itemID)
	if itemInfo != nil {
		grdGfx = itemInfo.GrdGfx
	}

	// Build display name
	displayName := itemName
	if enchantLvl > 0 {
		displayName = fmt.Sprintf("+%d %s", enchantLvl, displayName)
	}
	if count > 1 {
		displayName = fmt.Sprintf("%s (%d)", displayName, count)
	}

	// Create ground item at player's position
	gndItem := &world.GroundItem{
		ID:         world.NextGroundItemID(),
		ItemID:     itemID,
		Count:      count,
		EnchantLvl: enchantLvl,
		Name:       displayName,
		GrdGfx:     grdGfx,
		X:          player.X,
		Y:          player.Y,
		MapID:      player.MapID,
		OwnerID:    player.CharID,
		TTL:        5 * 60 * 5, // 5 minutes at 200ms ticks
	}
	deps.World.AddGroundItem(gndItem)

	// Broadcast to nearby players (including self)
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendDropItem(viewer.Session, gndItem)
	}

	deps.Log.Debug("item dropped to ground",
		zap.String("player", player.Name),
		zap.Int32("item_id", itemID),
		zap.Int32("count", count),
		zap.Int32("ground_id", gndItem.ID),
	)
}

// HandlePickupItem processes C_GET (opcode 112) — player picks up ground item.
// Format: [H x][H y][D objectID][D count]
func HandlePickupItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	_ = r.ReadH()          // x (unused, use server pos)
	_ = r.ReadH()          // y (unused)
	objectID := r.ReadD()  // ground item object ID
	_ = r.ReadD()          // count (pick up all)

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	gndItem := deps.World.GetGroundItem(objectID)
	if gndItem == nil {
		return
	}

	// Distance check (Chebyshev <= 3)
	dx := player.X - gndItem.X
	dy := player.Y - gndItem.Y
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
	if dist > 3 {
		return
	}

	// Map check
	if player.MapID != gndItem.MapID {
		return
	}

	// Check inventory space
	if player.Inv.IsFull() {
		sendServerMessage(sess, 263) // "Inventory full"
		return
	}

	// Check weight capacity
	pickupInfo := deps.Items.Get(gndItem.ItemID)
	if pickupInfo != nil {
		addWeight := pickupInfo.Weight * gndItem.Count
		maxW := world.MaxWeight(player.Str, player.Con)
		if player.Inv.IsOverWeight(addWeight, maxW) {
			sendServerMessage(sess, 82) // "此物品太重了，所以你無法攜帶。"
			return
		}
	}

	// Remove from world
	deps.World.RemoveGroundItem(objectID)

	// Broadcast removal to nearby players
	nearby := deps.World.GetNearbyPlayersAt(gndItem.X, gndItem.Y, gndItem.MapID)
	for _, viewer := range nearby {
		sendRemoveObject(viewer.Session, gndItem.ID)
	}

	// Add to player inventory
	itemInfo := deps.Items.Get(gndItem.ItemID)
	itemName := gndItem.Name
	invGfx := int32(0)
	weight := int32(0)
	stackable := false
	if itemInfo != nil {
		itemName = itemInfo.Name
		invGfx = itemInfo.InvGfx
		weight = itemInfo.Weight
		stackable = itemInfo.Stackable || gndItem.ItemID == world.AdenaItemID
	}

	existing := player.Inv.FindByItemID(gndItem.ItemID)
	wasExisting := existing != nil && stackable

	invItem := player.Inv.AddItem(
		gndItem.ItemID,
		gndItem.Count,
		itemName,
		invGfx,
		weight,
		stackable,
		gndItem.EnchantLvl,
	)
	if itemInfo != nil {
		invItem.UseType = itemInfo.UseTypeID
	}

	if wasExisting {
		sendItemCountUpdate(sess, invItem)
	} else {
		sendAddItem(sess, invItem)
	}

	// Update weight bar (Java: S_PacketBox.WEIGHT after insertItem)
	sendWeightUpdate(sess, player)

	deps.Log.Debug("item picked up",
		zap.String("player", player.Name),
		zap.Int32("item_id", gndItem.ItemID),
		zap.Int32("count", gndItem.Count),
	)
}

// HandleUseItem processes C_USE_ITEM (opcode 164) — player uses an item.
// Format: [D objectID]
func HandleUseItem(sess *net.Session, r *packet.Reader, deps *Deps) {
	objectID := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	invItem := player.Inv.FindByObjectID(objectID)
	if invItem == nil {
		return
	}

	itemInfo := deps.Items.Get(invItem.ItemID)
	if itemInfo == nil {
		return
	}

	deps.Log.Debug("C_UseItem",
		zap.String("player", player.Name),
		zap.Int32("item_id", invItem.ItemID),
		zap.String("name", invItem.Name),
		zap.String("type", itemInfo.Type),
	)

	// Teleport scrolls have additional data in the packet: [H mapID][D bookmarkID]
	if isTeleportScroll(invItem.ItemID) {
		handleUseTeleportScroll(sess, r, player, invItem, deps)
		return
	}

	switch itemInfo.Category {
	case data.CategoryWeapon:
		handleEquipWeapon(sess, player, invItem, itemInfo, deps)
	case data.CategoryArmor:
		handleEquipArmor(sess, player, invItem, itemInfo, deps)
	case data.CategoryEtcItem:
		handleUseEtcItem(sess, r, player, invItem, itemInfo, deps)
	}
}

// ---------- Equipment: Weapon ----------

func handleEquipWeapon(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo, deps *Deps) {
	if invItem.Equipped {
		// Cursed items cannot be unequipped (Java: bless == 2, message 150)
		if invItem.Bless == 2 {
			sendServerMessage(sess, 150)
			return
		}
		// Already equipped → unequip
		unequipSlot(sess, player, world.SlotWeapon, deps)
		return
	}

	// Class restriction check
	if !canClassUse(player.ClassType, itemInfo) {
		sendServerMessage(sess, msgClassCannotUse)
		return
	}

	// Level restriction check
	if !checkLevelRestriction(sess, player.Level, itemInfo) {
		return
	}

	// Unequip current weapon if any
	if cur := player.Equip.Weapon(); cur != nil {
		unequipSlot(sess, player, world.SlotWeapon, deps)
	}

	// Two-handed weapon: also unequip shield/guarder
	if world.IsTwoHanded(itemInfo.Type) {
		if player.Equip.Get(world.SlotShield) != nil {
			unequipSlot(sess, player, world.SlotShield, deps)
		}
		if player.Equip.Get(world.SlotGuarder) != nil {
			unequipSlot(sess, player, world.SlotGuarder, deps)
		}
	}

	// Equip
	invItem.Equipped = true
	player.Equip.Set(world.SlotWeapon, invItem)
	player.CurrentWeapon = world.WeaponVisualID(itemInfo.Type)

	// Send inventory status update (mark as equipped)
	sendItemNameUpdate(sess, invItem, itemInfo)
	sendEquipSlotUpdate(sess, invItem.ObjectID, world.SlotWeapon, true)

	// Recalculate all equipment stats and send updates
	recalcEquipStats(sess, player, deps)

	// Send visual update to self + nearby
	broadcastVisualUpdate(sess, player, deps)

	deps.Log.Debug("weapon equipped",
		zap.String("player", player.Name),
		zap.String("weapon", invItem.Name),
		zap.String("type", itemInfo.Type),
	)
}

// ---------- Equipment: Armor ----------

func handleEquipArmor(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo, deps *Deps) {
	slot := world.ArmorSlotFromType(itemInfo.Type)
	if slot == world.SlotNone {
		deps.Log.Debug("unknown armor type", zap.String("type", itemInfo.Type))
		return
	}

	if invItem.Equipped {
		// Cursed items cannot be unequipped (Java: bless == 2, message 150)
		if invItem.Bless == 2 {
			sendServerMessage(sess, 150)
			return
		}

		eqSlot := findEquippedSlot(player, invItem)
		if eqSlot == world.SlotNone {
			return
		}

		// Unequip layering restrictions (Java C_ItemUSe.java):
		// T-Shirt cannot be removed if Body Armor is equipped
		if eqSlot == world.SlotTShirt && player.Equip.Get(world.SlotArmor) != nil {
			sendServerMessage(sess, 127)
			return
		}
		// Body Armor or T-Shirt cannot be removed if Cloak is equipped
		if (eqSlot == world.SlotArmor || eqSlot == world.SlotTShirt) && player.Equip.Get(world.SlotCloak) != nil {
			sendServerMessage(sess, 127)
			return
		}

		unequipSlot(sess, player, eqSlot, deps)
		return
	}

	// Class restriction check
	if !canClassUse(player.ClassType, itemInfo) {
		sendServerMessage(sess, msgClassCannotUse)
		return
	}

	// Level restriction check
	if !checkLevelRestriction(sess, player.Level, itemInfo) {
		return
	}

	// Ring: choose empty slot (Ring1 or Ring2)
	if slot == world.SlotRing1 {
		if player.Equip.Get(world.SlotRing1) == nil {
			slot = world.SlotRing1
		} else if player.Equip.Get(world.SlotRing2) == nil {
			slot = world.SlotRing2
		} else {
			// Both ring slots full, unequip Ring1
			unequipSlot(sess, player, world.SlotRing1, deps)
			slot = world.SlotRing1
		}
	}

	// Shield + Belt mutual exclusivity (Java: type 7 and type 13)
	if slot == world.SlotShield || slot == world.SlotGuarder {
		if player.Equip.Get(world.SlotBelt) != nil {
			sendServerMessage(sess, 124)
			return
		}
	}
	if slot == world.SlotBelt {
		if player.Equip.Get(world.SlotShield) != nil || player.Equip.Get(world.SlotGuarder) != nil {
			sendServerMessage(sess, 124)
			return
		}
	}

	// Shield: can't equip with two-handed weapon
	if slot == world.SlotShield || slot == world.SlotGuarder {
		wpn := player.Equip.Weapon()
		if wpn != nil {
			wpnInfo := deps.Items.Get(wpn.ItemID)
			if wpnInfo != nil && world.IsTwoHanded(wpnInfo.Type) {
				// Unequip two-handed weapon first
				unequipSlot(sess, player, world.SlotWeapon, deps)
			}
		}
	}

	// Armor/TShirt/Cloak layering restrictions (Java C_ItemUSe.java)
	// T-Shirt cannot be equipped over Cloak or Body Armor
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
	// Body Armor cannot be equipped over Cloak
	if slot == world.SlotArmor {
		if player.Equip.Get(world.SlotCloak) != nil {
			sendServerMessageS(sess, 126, "$226", "$225")
			return
		}
	}

	// Unequip current item in this slot
	if cur := player.Equip.Get(slot); cur != nil {
		unequipSlot(sess, player, slot, deps)
	}

	// Equip
	invItem.Equipped = true
	player.Equip.Set(slot, invItem)

	sendItemNameUpdate(sess, invItem, itemInfo)
	sendEquipSlotUpdate(sess, invItem.ObjectID, slot, true)

	// Recalculate all equipment stats and send updates
	recalcEquipStats(sess, player, deps)

	deps.Log.Debug("armor equipped",
		zap.String("player", player.Name),
		zap.String("armor", invItem.Name),
		zap.String("slot", itemInfo.Type),
	)
}

// ---------- Equip helpers ----------

// unequipSlot removes an item from an equipment slot.
func unequipSlot(sess *net.Session, player *world.PlayerInfo, slot world.EquipSlot, deps *Deps) {
	item := player.Equip.Get(slot)
	if item == nil {
		return
	}

	item.Equipped = false
	player.Equip.Set(slot, nil)

	// If unequipping weapon, clear visual
	if slot == world.SlotWeapon {
		player.CurrentWeapon = 0
		broadcastVisualUpdate(sess, player, deps)
	}

	// Update item name (remove equipped suffix)
	itemInfo := deps.Items.Get(item.ItemID)
	sendItemNameUpdate(sess, item, itemInfo)
	sendEquipSlotUpdate(sess, item.ObjectID, slot, false)

	// Recalculate all equipment stats
	recalcEquipStats(sess, player, deps)
}

// findEquippedSlot finds which slot an item is in.
func findEquippedSlot(player *world.PlayerInfo, item *world.InvItem) world.EquipSlot {
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		if player.Equip.Get(i) == item {
			return i
		}
	}
	return world.SlotNone
}

// applyEquipStats recalculates equipment stat contributions and applies the diff
// to player fields, WITHOUT sending any packets. Used during enter-world init
// before the client has received LoginToGame.
func applyEquipStats(player *world.PlayerInfo, items *data.ItemTable) {
	old := player.EquipBonuses
	neo := CalcEquipStats(player, items)

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

// recalcEquipStats recalculates all equipment stat contributions, applies the diff,
// and sends update packets. Used on equip/unequip while in-world.
func recalcEquipStats(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	old := player.EquipBonuses
	applyEquipStats(player, deps.Items)

	// Send update packets
	sendPlayerStatus(sess, player)
	sendAbilityScores(sess, player)
	sendMagicStatus(sess, byte(player.SP), uint16(player.MR))

	// Weight capacity changes when STR/CON change
	neo := player.EquipBonuses
	if neo.AddStr != old.AddStr || neo.AddCon != old.AddCon {
		sendWeightUpdate(sess, player)
	}
}

// CalcEquipStats computes total stat bonuses from all equipped items.
// Includes enchant level in AC for non-accessory slots (Java L1EquipmentSlot.set()).
func CalcEquipStats(player *world.PlayerInfo, items *data.ItemTable) world.EquipStats {
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
		// AC: accessories don't get enchant level bonus
		if world.IsAccessorySlot(i) {
			stats.AC += info.AC
		} else {
			stats.AC += info.AC - int(invItem.EnchantLvl)
		}
		stats.HitMod += info.HitMod
		stats.DmgMod += info.DmgMod
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
	return stats
}

// ---------- Equipment packets ----------

// sendItemNameUpdate sends S_CHANGE_ITEM_DESC (opcode 100) to update item display name.
// Java appends " ($9)" for equipped weapons, " ($117)" for equipped armor.
// Format: [D objectID][S viewName]
func sendItemNameUpdate(sess *net.Session, item *world.InvItem, itemInfo *data.ItemInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_ITEM_DESC)
	w.WriteD(item.ObjectID)
	w.WriteS(buildViewName(item, itemInfo))
	sess.Send(w.Bytes())
}

// buildViewName constructs the display name (matches Java getViewName).
func buildViewName(item *world.InvItem, itemInfo *data.ItemInfo) string {
	name := item.Name
	if item.EnchantLvl > 0 {
		name = fmt.Sprintf("+%d %s", item.EnchantLvl, name)
	}
	// Stack count suffix (Java: getNumberedName — applies to ALL stackable items)
	if item.Count > 1 {
		name += fmt.Sprintf(" (%d)", item.Count)
	}
	if item.Equipped && itemInfo != nil {
		switch itemInfo.Category {
		case data.CategoryWeapon:
			name += " ($9)"   // 裝備中 (Armed)
		case data.CategoryArmor:
			name += " ($117)" // 裝備中 (Worn)
		}
	}
	return name
}

// sendEquipSlotUpdate sends S_EquipmentSlot for a single equip/unequip action.
// Java: S_OPCODE_CHARRESET (opcode 64), type=0x42 (TYPE_EQUIPACTION).
// Tells the client which visual equipment slot an item occupies.
func sendEquipSlotUpdate(sess *net.Session, itemObjID int32, slot world.EquipSlot, equipped bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_VOICE_CHAT)
	w.WriteC(0x42) // TYPE_EQUIPACTION
	w.WriteD(itemObjID)
	w.WriteC(world.EquipClientIndex(slot))
	if equipped {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	sess.Send(w.Bytes())
}

// sendEquipSlotList sends the full equipment list on login.
// Java: S_OPCODE_CHARRESET (opcode 64), type=0x41 (TYPE_EQUIPONLOGIN).
func sendEquipSlotList(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_VOICE_CHAT)
	w.WriteC(0x41) // TYPE_EQUIPONLOGIN
	count := byte(0)
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		if player.Equip.Get(i) != nil {
			count++
		}
	}
	w.WriteC(count)
	for i := world.EquipSlot(1); i < world.SlotMax; i++ {
		item := player.Equip.Get(i)
		if item != nil {
			w.WriteD(item.ObjectID)
			w.WriteD(int32(world.EquipClientIndex(i)))
		}
	}
	w.WriteD(0) // terminator
	w.WriteC(0) // terminator
	sess.Send(w.Bytes())
}

// sendServerMessageS sends S_ServerMessage (opcode 71) with string arguments.
// Java: new S_ServerMessage(msgID, arg1, arg2, ...)
// Wire format: [H msgID][C argCount][S arg1][S arg2]...
func sendServerMessageS(sess *net.Session, msgID uint16, args ...string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(byte(len(args)))
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// broadcastVisualUpdate sends S_CHANGE_DESC (opcode 119) to self + nearby players.
// Format: [D objectID][C currentWeapon][C 0xff][C 0xff]
func broadcastVisualUpdate(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendCharVisualUpdate(viewer.Session, player)
	}
	// Also send to self
	sendCharVisualUpdate(sess, player)
}

// sendCharVisualUpdate sends S_CHANGE_DESC (opcode 119).
func sendCharVisualUpdate(viewer *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_DESC)
	w.WriteD(player.CharID)
	w.WriteC(player.CurrentWeapon)
	w.WriteC(0xff)
	w.WriteC(0xff)
	viewer.Send(w.Bytes())
}

// ---------- Use EtcItem (potions, food, scrolls) ----------

// handleUseEtcItem processes consumable items.
// Potion definitions are in Lua (scripts/item/potions.lua).
func handleUseEtcItem(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo, deps *Deps) {
	// Level restriction check for consumables
	if !checkLevelRestriction(sess, player.Level, itemInfo) {
		return
	}

	// Enchant scrolls: use_type "dai" (weapon) or "zel" (armor)
	if itemInfo.UseType == "dai" || itemInfo.UseType == "zel" {
		handleEnchantScroll(sess, r, player, invItem, itemInfo, deps)
		return
	}

	// Identify scroll: use_type "identify"
	if itemInfo.UseType == "identify" {
		handleIdentifyScroll(sess, r, player, invItem, deps)
		return
	}

	// Skill book: item_type "spellbook"
	if itemInfo.ItemType == "spellbook" {
		handleUseSpellBook(sess, player, invItem, itemInfo, deps)
		return
	}

	consumed := false

	// Check Lua potion definitions first
	pot := deps.Scripting.GetPotionEffect(int(invItem.ItemID))
	if pot != nil {
		switch pot.Type {
		case "heal":
			if pot.Amount > 0 && player.HP < player.MaxHP {
				player.HP += int16(pot.Amount)
				if player.HP > player.MaxHP {
					player.HP = player.MaxHP
				}
				sendHpUpdate(sess, player)
				sendEffectOnPlayer(sess, player.CharID, 189) // blue potion effect
				consumed = true
			}

		case "mana":
			if pot.Amount > 0 && player.MP < player.MaxMP {
				player.MP += int16(pot.Amount)
				if player.MP > player.MaxMP {
					player.MP = player.MaxMP
				}
				sendMpUpdate(sess, player)
				consumed = true
			}

		case "haste":
			if pot.Duration > 0 {
				applyHaste(sess, player, pot.Duration, int32(pot.GfxID), deps)
				consumed = true
			}

		case "brave":
			if pot.Duration > 0 {
				applyBrave(sess, player, pot.Duration, byte(pot.BraveType), int32(pot.GfxID), deps)
				consumed = true
			}

		case "wisdom":
			if pot.Duration > 0 {
				applyWisdom(sess, player, pot.Duration, int16(pot.SP), int32(pot.GfxID), deps)
				consumed = true
			}
		}
	} else if itemInfo.FoodVolume > 0 {
		// Java: foodvolume1 = item.getFoodVolume() / 10; if <= 0 then 5
		addFood := int16(itemInfo.FoodVolume / 10)
		if addFood <= 0 {
			addFood = 5
		}
		if player.Food >= 225 {
			// Already full — send packet but don't increase (matches Java)
			sendFoodUpdate(sess, player.Food)
		} else {
			player.Food += addFood
			if player.Food > 225 {
				player.Food = 225
			}
			sendFoodUpdate(sess, player.Food)
		}
		consumed = true
	} else {
		deps.Log.Debug("unhandled etcitem use",
			zap.Int32("item_id", invItem.ItemID),
			zap.String("use_type", itemInfo.UseType),
		)
	}

	if consumed {
		removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
		if removed {
			sendRemoveInventoryItem(sess, invItem.ObjectID)
		} else {
			sendItemCountUpdate(sess, invItem)
		}
		sendWeightUpdate(sess, player)
	}
}

// handleEnchantScroll processes weapon/armor enchant scroll usage.
// C_USE_ITEM continuation: [D targetObjectID]
func handleEnchantScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, scroll *world.InvItem, scrollInfo *data.ItemInfo, deps *Deps) {
	targetObjID := r.ReadD()

	target := player.Inv.FindByObjectID(targetObjID)
	if target == nil {
		return
	}

	targetInfo := deps.Items.Get(target.ItemID)
	if targetInfo == nil {
		return
	}

	// Validate scroll targets correct category
	if scrollInfo.UseType == "dai" && targetInfo.Category != data.CategoryWeapon {
		return
	}
	if scrollInfo.UseType == "zel" && targetInfo.Category != data.CategoryArmor {
		return
	}

	// Must be equipped
	if !target.Equipped {
		return
	}

	// Determine category for Lua
	category := 1 // weapon
	if targetInfo.Category == data.CategoryArmor {
		category = 2
	}

	// Call Lua enchant formula
	result := deps.Scripting.CalcEnchant(scripting.EnchantContext{
		ScrollBless:  int(scroll.Bless),
		EnchantLvl:   int(target.EnchantLvl),
		SafeEnchant:  targetInfo.SafeEnchant,
		Category:     category,
		WeaponChance: deps.Config.Enchant.WeaponChance,
		ArmorChance:  deps.Config.Enchant.ArmorChance,
	})

	// Consume the scroll
	scrollRemoved := player.Inv.RemoveItem(scroll.ObjectID, 1)
	if scrollRemoved {
		sendRemoveInventoryItem(sess, scroll.ObjectID)
	} else {
		sendItemCountUpdate(sess, scroll)
	}
	sendWeightUpdate(sess, player)

	switch result.Result {
	case "success":
		target.EnchantLvl += byte(result.Amount)
		sendItemNameUpdate(sess, target, targetInfo)
		sendEffectOnPlayer(sess, player.CharID, 2583) // enchant success GFX

		msg := fmt.Sprintf("\\fY+%d %s 閃耀著光芒。", target.EnchantLvl, targetInfo.Name)
		sendGlobalChat(sess, 9, msg)

		// Recalculate AC if armor
		if targetInfo.Category == data.CategoryArmor {
			recalcEquipStats(sess, player, deps)
		}

		deps.Log.Info(fmt.Sprintf("衝裝成功  角色=%s  道具=%s  衝裝等級=%d", player.Name, targetInfo.Name, target.EnchantLvl))

	case "fail":
		// Blessed scroll: nothing happens
		sendGlobalChat(sess, 9, "沒有任何事情發生。")
		deps.Log.Info(fmt.Sprintf("衝裝失敗 (祝福保護)  角色=%s  道具=%s", player.Name, targetInfo.Name))

	case "break":
		// Equipment destroyed
		slot := findEquippedSlot(player, target)
		if slot != world.SlotNone {
			unequipSlot(sess, player, slot, deps)
		}
		player.Inv.RemoveItem(target.ObjectID, 1)
		sendRemoveInventoryItem(sess, target.ObjectID)
		sendWeightUpdate(sess, player)

		msg := fmt.Sprintf("\\fY%s 蒸發消失了。", targetInfo.Name)
		sendGlobalChat(sess, 9, msg)

		deps.Log.Info(fmt.Sprintf("衝裝碎裂  角色=%s  道具=%s", player.Name, targetInfo.Name))

	case "minus":
		// Cursed scroll: -1
		if target.EnchantLvl > 0 {
			target.EnchantLvl -= byte(result.Amount)
		}
		sendItemNameUpdate(sess, target, targetInfo)

		msg := fmt.Sprintf("\\fY%s 的強化值降低了。", targetInfo.Name)
		sendGlobalChat(sess, 9, msg)

		if targetInfo.Category == data.CategoryArmor {
			recalcEquipStats(sess, player, deps)
		}

		deps.Log.Info(fmt.Sprintf("衝裝降級  角色=%s  道具=%s  衝裝等級=%d", player.Name, targetInfo.Name, target.EnchantLvl))
	}
}

// handleIdentifyScroll processes identify scroll usage.
// C_USE_ITEM continuation: [D targetObjectID]
func handleIdentifyScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, scroll *world.InvItem, deps *Deps) {
	targetObjID := r.ReadD()

	target := player.Inv.FindByObjectID(targetObjID)
	if target == nil {
		return
	}

	targetInfo := deps.Items.Get(target.ItemID)
	if targetInfo == nil {
		return
	}

	// Set identified flag
	target.Identified = true

	// Send item status update with full status bytes (weapon/armor stats visible)
	sendItemStatusUpdate(sess, target, targetInfo)

	// Send bless color update (name brightens from unidentified=3 to actual bless)
	sendItemColor(sess, target.ObjectID, target.Bless)

	// Send identify description popup
	sendIdentifyDesc(sess, target, targetInfo)

	// Consume scroll
	removed := player.Inv.RemoveItem(scroll.ObjectID, 1)
	if removed {
		sendRemoveInventoryItem(sess, scroll.ObjectID)
	} else {
		sendItemCountUpdate(sess, scroll)
	}
	sendWeightUpdate(sess, player)
}

// sendIdentifyDesc sends S_IdentifyDesc (opcode 245) — shows item stats on identify.
// Format varies by item type (weapon/armor/etcitem), matching Java S_IdentifyDesc.
func sendIdentifyDesc(sess *net.Session, item *world.InvItem, info *data.ItemInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_IDENTIFYDESC)
	w.WriteH(uint16(info.ItemDescID))

	// Build display name with bless prefix
	name := info.Name
	switch item.Bless {
	case 0:
		name = "$227 " + name // 祝福された (Blessed)
	case 2:
		name = "$228 " + name // 呪われた (Cursed)
	}

	switch info.Category {
	case data.CategoryWeapon:
		// Format 134: weapon — name, dmgSmall+enchant, dmgLarge+enchant
		w.WriteH(134)
		w.WriteC(3) // param count
		w.WriteS(name)
		w.WriteS(fmt.Sprintf("%d+%d", info.DmgSmall, item.EnchantLvl))
		w.WriteS(fmt.Sprintf("%d+%d", info.DmgLarge, item.EnchantLvl))

	case data.CategoryArmor:
		// Format 135: armor — name, abs(ac)+enchant
		w.WriteH(135)
		w.WriteC(2) // param count
		w.WriteS(name)
		ac := info.AC
		if ac < 0 {
			ac = -ac
		}
		w.WriteS(fmt.Sprintf("%d+%d", ac, item.EnchantLvl))

	default:
		// Etcitem — format 138: name + weight
		w.WriteH(138)
		w.WriteC(2) // param count
		w.WriteS(name)
		w.WriteS(fmt.Sprintf("%d", calcItemWeight(item, info)))
	}

	sess.Send(w.Bytes())
}

// ---------- Identification helpers ----------

// sendItemColor sends S_ItemColor (opcode 240) — updates item bless/color display.
// Format: [D objectID][C bless]
func sendItemColor(sess *net.Session, objectID int32, bless byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ITEMCOLOR)
	w.WriteD(objectID)
	w.WriteC(bless)
	sess.Send(w.Bytes())
}

// sendItemStatusUpdate sends S_ItemStatus (opcode 24) with full status bytes.
// Used after identification to update the client's item display with stats.
func sendItemStatusUpdate(sess *net.Session, item *world.InvItem, info *data.ItemInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHANGE_ITEM_USE)
	w.WriteD(item.ObjectID)
	w.WriteS(buildViewName(item, info))
	w.WriteD(item.Count)
	statusBytes := buildStatusBytes(item, info)
	if len(statusBytes) > 0 {
		w.WriteC(byte(len(statusBytes)))
		w.WriteBytes(statusBytes)
	} else {
		w.WriteC(0)
	}
	sess.Send(w.Bytes())
}

// itemStatusX computes the item status bitmap for inventory packets.
// Java: L1ItemInstance.getItemStatusX()
func itemStatusX(item *world.InvItem, info *data.ItemInfo) byte {
	if !item.Identified {
		return 0
	}
	statusX := byte(1) // bit 0: identified
	if info != nil && !info.Tradeable {
		statusX |= 2 // cannot trade
	}
	if info != nil && info.SafeEnchant < 0 {
		statusX |= 8 | 16 // cannot enchant + warehouse restriction
	}
	if item.Bless >= 128 && item.Bless <= 131 {
		statusX |= 2 | 4 | 8 | 32 // sealed
	} else if item.Bless > 131 {
		statusX |= 64 // special sealed
	}
	if info != nil && info.Stackable {
		statusX |= 128 // stackable
	}
	return statusX
}

// classBitmask builds the class restriction byte for status bytes.
// bit0=Royal, bit1=Knight, bit2=Elf, bit3=Mage, bit4=DarkElf, bit5=DragonKnight, bit6=Illusionist
func classBitmask(info *data.ItemInfo) byte {
	var bits byte
	if info.UseRoyal {
		bits |= 1
	}
	if info.UseKnight {
		bits |= 2
	}
	if info.UseElf {
		bits |= 4
	}
	if info.UseMage {
		bits |= 8
	}
	if info.UseDarkElf {
		bits |= 16
	}
	if info.UseDragonKnight {
		bits |= 32
	}
	if info.UseIllusionist {
		bits |= 64
	}
	return bits
}

// calcItemWeight computes the displayed weight for an item instance.
// Java: L1ItemInstance.getWeight() = max(count * templateWeight / 1000, 1).
// Template weight is in 1/1000 units; this converts to display units.
func calcItemWeight(item *world.InvItem, info *data.ItemInfo) int32 {
	if info.Weight == 0 {
		return 0
	}
	w := item.Count * info.Weight / 1000
	if w < 1 {
		w = 1
	}
	return w
}

// buildStatusBytes generates the TLV-encoded item attribute bytes matching
// Java L1ItemInstance.getStatusBytes(). Returns nil for unidentified items.
func buildStatusBytes(item *world.InvItem, info *data.ItemInfo) []byte {
	if !item.Identified || info == nil {
		return nil
	}

	material := data.MaterialToID(info.Material)
	buf := make([]byte, 0, 48)

	switch info.Category {
	case data.CategoryWeapon:
		// [C 1][C dmgSmall][C dmgLarge][C material][D weight]
		buf = append(buf, 1, byte(info.DmgSmall), byte(info.DmgLarge))
		buf = append(buf, material)
		buf = appendInt32LE(buf, calcItemWeight(item, info))
		buf = appendEquipSuffix(buf, item, info)

	case data.CategoryArmor:
		// [C 19][C abs(ac)][C material][C grade][D weight]
		ac := info.AC
		if ac < 0 {
			ac = -ac
		}
		buf = append(buf, 19, byte(ac), material, 0) // grade=0
		buf = appendInt32LE(buf, calcItemWeight(item, info))
		buf = appendEquipSuffix(buf, item, info)

	case data.CategoryEtcItem:
		switch {
		case info.Type == "arrow":
			buf = append(buf, 1, byte(info.DmgSmall), byte(info.DmgLarge))
		case info.FoodVolume > 0:
			buf = append(buf, 21)
			buf = appendUint16LE(buf, uint16(info.FoodVolume))
		default:
			buf = append(buf, 23) // material tag
		}
		buf = append(buf, material)
		buf = appendInt32LE(buf, calcItemWeight(item, info))
	}

	return buf
}

// appendEquipSuffix appends the shared weapon/armor TLV suffix (enchant, hit, dmg, class, stats).
func appendEquipSuffix(buf []byte, item *world.InvItem, info *data.ItemInfo) []byte {
	if item.EnchantLvl != 0 {
		buf = append(buf, 2, item.EnchantLvl)
	}
	if info.Category == data.CategoryWeapon && world.IsTwoHanded(info.Type) {
		buf = append(buf, 4) // two-handed flag (no value byte)
	}
	if info.HitMod != 0 {
		buf = append(buf, 5, byte(int8(info.HitMod)))
	}
	if info.DmgMod != 0 {
		buf = append(buf, 6, byte(int8(info.DmgMod)))
	}
	buf = append(buf, 7, classBitmask(info)) // always written

	if info.AddStr != 0 {
		buf = append(buf, 8, byte(int8(info.AddStr)))
	}
	if info.AddDex != 0 {
		buf = append(buf, 9, byte(int8(info.AddDex)))
	}
	if info.AddCon != 0 {
		buf = append(buf, 10, byte(int8(info.AddCon)))
	}
	if info.AddWis != 0 {
		buf = append(buf, 11, byte(int8(info.AddWis)))
	}
	if info.AddInt != 0 {
		buf = append(buf, 12, byte(int8(info.AddInt)))
	}
	if info.AddCha != 0 {
		buf = append(buf, 13, byte(int8(info.AddCha)))
	}
	if info.AddHP != 0 {
		buf = append(buf, 14)
		buf = appendUint16LE(buf, uint16(int16(info.AddHP)))
	}
	if info.AddMP != 0 {
		buf = append(buf, 32, byte(int8(info.AddMP)))
	}
	if info.AddSP != 0 {
		buf = append(buf, 17, byte(int8(info.AddSP)))
	}
	if info.MDef != 0 {
		buf = append(buf, 15)
		buf = appendUint16LE(buf, uint16(int16(info.MDef)))
	}
	if info.AddHPR != 0 {
		buf = append(buf, 37, byte(int8(info.AddHPR)))
	}
	if info.AddMPR != 0 {
		buf = append(buf, 26, byte(int8(info.AddMPR)))
	}
	return buf
}

// buildShopStatusBytes generates status bytes for a shop listing (no actual InvItem).
// Equivalent to Java's dummy.setItem(template); dummy.getStatusBytes().
func buildShopStatusBytes(info *data.ItemInfo) []byte {
	if info == nil {
		return nil
	}
	// Create a temporary identified item with no enchant, count=1
	dummy := &world.InvItem{
		Identified: true,
		EnchantLvl: 0,
		Count:      1,
	}
	return buildStatusBytes(dummy, info)
}

func appendInt32LE(buf []byte, v int32) []byte {
	u := uint32(v)
	return append(buf, byte(u), byte(u>>8), byte(u>>16), byte(u>>24))
}

func appendUint16LE(buf []byte, v uint16) []byte {
	return append(buf, byte(v), byte(v>>8))
}

// ---------- Skill Book (技能書/精靈水晶/龍騎士書板/記憶水晶) ----------

// spellBookPrefixes maps book name prefix → nothing (just for stripping).
// Java resolves skill by matching item name "魔法書(技能名)" → skill name.
var spellBookPrefixes = []string{
	"魔法書(",       // Wizard / common (45000-45022, 40170-40225)
	"技術書(",       // Knight (40164-40166, 41147-41148)
	"精靈水晶(",     // Elf (40232-40264, 41149-41153)
	"黑暗精靈水晶(", // Dark Elf (40265-40279)
	"龍騎士書板(",   // Dragon Knight (49102-49116)
	"記憶水晶(",     // Illusionist (49117-49136)
}

// extractSkillName strips the book prefix and trailing ")" from item name.
// Returns the skill name or "" if not a valid spellbook name pattern.
func extractSkillName(itemName string) string {
	for _, prefix := range spellBookPrefixes {
		if len(itemName) > len(prefix) && itemName[:len(prefix)] == prefix {
			// Strip prefix and trailing ")"
			inner := itemName[len(prefix):]
			if len(inner) > 0 && inner[len(inner)-1] == ')' {
				return inner[:len(inner)-1]
			}
			return inner
		}
	}
	return ""
}

// spellBookLevelReq returns the required character level to use a spellbook,
// based on class and item ID range. Matches Java C_ItemUSe.useSpellBook.
// Returns 0 if this class cannot use this book.
func spellBookLevelReq(classType int16, itemID int32) int {
	// Common magic books (45000-45022, 40170-40225) — class-specific level gates
	if itemID >= 45000 && itemID <= 45022 || itemID >= 40170 && itemID <= 40225 {
		return commonBookLevelReq(classType, itemID)
	}

	switch classType {
	case 0: // Royal (Prince/Princess) — 魔法書(精準目標) etc.
		if itemID >= 40226 && itemID <= 40231 {
			return 15
		}
	case 1: // Knight — 技術書
		if itemID >= 40164 && itemID <= 40166 || itemID >= 41147 && itemID <= 41148 {
			return 50
		}
	case 2: // Elf — 精靈水晶
		return elfCrystalLevelReq(itemID)
	case 4: // Dark Elf — 黑暗精靈水晶
		switch {
		case itemID >= 40265 && itemID <= 40269:
			return 15
		case itemID >= 40270 && itemID <= 40274:
			return 30
		case itemID >= 40275 && itemID <= 40279:
			return 45
		}
	case 5: // Dragon Knight — 龍騎士書板
		switch {
		case itemID >= 49102 && itemID <= 49106:
			return 15
		case itemID >= 49107 && itemID <= 49111:
			return 30
		case itemID >= 49112 && itemID <= 49116:
			return 45
		}
	case 6: // Illusionist — 記憶水晶
		switch {
		case itemID >= 49117 && itemID <= 49121:
			return 10
		case itemID >= 49122 && itemID <= 49126:
			return 20
		case itemID >= 49127 && itemID <= 49131:
			return 30
		case itemID >= 49132 && itemID <= 49136:
			return 40
		}
	}
	return 0
}

// commonBookLevelReq returns level requirement for common magic books (45000-45022, 40170-40225).
func commonBookLevelReq(classType int16, itemID int32) int {
	switch classType {
	case 3: // Wizard
		switch {
		case itemID >= 45000 && itemID <= 45007:
			return 4
		case itemID >= 45008 && itemID <= 45015:
			return 8
		case itemID >= 45016 && itemID <= 45022:
			return 12
		case itemID >= 40170 && itemID <= 40177:
			return 16
		case itemID >= 40178 && itemID <= 40185:
			return 20
		case itemID >= 40186 && itemID <= 40193:
			return 24
		case itemID >= 40194 && itemID <= 40201:
			return 28
		case itemID >= 40202 && itemID <= 40209:
			return 32
		case itemID >= 40210 && itemID <= 40217:
			return 36
		case itemID >= 40218 && itemID <= 40225:
			return 40
		}
	case 0: // Royal
		switch {
		case itemID >= 45000 && itemID <= 45007:
			return 10
		case itemID >= 45008 && itemID <= 45015:
			return 20
		}
	case 1: // Knight
		if itemID >= 45000 && itemID <= 45007 {
			return 50
		}
	case 2: // Elf
		switch {
		case itemID >= 45000 && itemID <= 45007:
			return 8
		case itemID >= 45008 && itemID <= 45015:
			return 16
		case itemID >= 45016 && itemID <= 45022:
			return 24
		case itemID >= 40170 && itemID <= 40177:
			return 32
		case itemID >= 40178 && itemID <= 40185:
			return 40
		case itemID >= 40186 && itemID <= 40193:
			return 48
		}
	case 4: // Dark Elf
		switch {
		case itemID >= 45000 && itemID <= 45007:
			return 10
		case itemID >= 45008 && itemID <= 45015:
			return 20
		}
	}
	return 0
}

// elfCrystalLevelReq returns level requirement for Elf 精靈水晶 (40232-40264, 41149-41153).
// Each sub-range maps to a different element tier with different level requirements.
func elfCrystalLevelReq(itemID int32) int {
	switch {
	// Earth spells
	case itemID >= 40232 && itemID <= 40234:
		return 10
	case itemID >= 40235 && itemID <= 40236:
		return 20
	case itemID >= 40237 && itemID <= 40240:
		return 30
	case itemID >= 40241 && itemID <= 40243:
		return 40
	case itemID >= 40244 && itemID <= 40246:
		return 50
	// Water spells
	case itemID >= 40247 && itemID <= 40248:
		return 30
	case itemID >= 40249 && itemID <= 40250:
		return 40
	case itemID >= 40251 && itemID <= 40252:
		return 50
	// Water (single)
	case itemID == 40253:
		return 30
	case itemID == 40254:
		return 40
	case itemID == 40255:
		return 50
	// Fire spells
	case itemID == 40256:
		return 30
	case itemID == 40257:
		return 40
	case itemID >= 40258 && itemID <= 40259:
		return 50
	// Wind spells
	case itemID >= 40260 && itemID <= 40261:
		return 30
	case itemID == 40262:
		return 40
	case itemID >= 40263 && itemID <= 40264:
		return 50
	// Extended elf crystals
	case itemID >= 41149 && itemID <= 41150:
		return 50
	case itemID == 41151:
		return 40
	case itemID >= 41152 && itemID <= 41153:
		return 50
	}
	return 0
}

// handleUseSpellBook processes a spellbook item use.
// Extracts skill name from item name, validates class/level, learns skill.
func handleUseSpellBook(sess *net.Session, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo, deps *Deps) {
	// Extract skill name from item name (e.g. "魔法書(初級治癒術)" → "初級治癒術")
	skillName := extractSkillName(itemInfo.Name)
	if skillName == "" {
		deps.Log.Debug("spellbook: cannot extract skill name",
			zap.String("item_name", itemInfo.Name))
		return
	}

	// Look up the skill by name
	skill := deps.Skills.GetByName(skillName)
	if skill == nil {
		deps.Log.Debug("spellbook: skill not found",
			zap.String("skill_name", skillName))
		return
	}

	// Check class/level requirement
	reqLevel := spellBookLevelReq(player.ClassType, invItem.ItemID)
	if reqLevel == 0 {
		// This class cannot use this book
		sendServerMessage(sess, msgClassCannotUse) // 264: 你的職業無法使用此道具。
		return
	}
	if int(player.Level) < reqLevel {
		// Level too low — message 318: 等級 %0以上才可使用此道具。
		sendServerMessageStr(sess, msgLevelTooLow, strconv.Itoa(reqLevel))
		return
	}

	// Check if already learned
	for _, sid := range player.KnownSpells {
		if sid == skill.SkillID {
			// Message 78: 你已經學會了。
			sendServerMessage(sess, 78)
			return
		}
	}

	// Learn the skill
	player.KnownSpells = append(player.KnownSpells, skill.SkillID)
	sendAddSingleSkill(sess, skill)

	// Play learn effect (GFX 224)
	sendSkillEffect(sess, player.CharID, 224)
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendSkillEffect(other.Session, player.CharID, 224)
	}

	// Consume the book
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		sendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		sendItemCountUpdate(sess, invItem)
	}
	sendWeightUpdate(sess, player)

	deps.Log.Info(fmt.Sprintf("玩家從技能書學習技能  角色=%s  技能=%s  技能ID=%d  書籍=%s", player.Name, skill.Name, skill.SkillID, itemInfo.Name))
}

// applyHaste applies haste speed buff to a player (movement + attack speed).
func applyHaste(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32, deps *Deps) {
	player.MoveSpeed = 1
	player.HasteTicks = durationSec * 5 // 200ms per tick

	sendSpeedPacket(sess, player.CharID, 1, uint16(durationSec))

	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, player.CharID, 1, 0)
	}

	broadcastEffect(sess, player, gfxID, deps)
}

// applyBrave applies brave speed buff to a player (attack speed + movement speed).
func applyBrave(sess *net.Session, player *world.PlayerInfo, durationSec int, braveType byte, gfxID int32, deps *Deps) {
	player.BraveSpeed = braveType
	player.BraveTicks = durationSec * 5 // 200ms per tick

	sendSpeedPacket(sess, player.CharID, braveType, uint16(durationSec))

	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, player.CharID, braveType, 0)
	}

	broadcastVisualUpdate(sess, player, deps)
	broadcastEffect(sess, player, gfxID, deps)
}

// applyWisdom applies wisdom potion buff (SP bonus for duration).
func applyWisdom(sess *net.Session, player *world.PlayerInfo, durationSec int, sp int16, gfxID int32, deps *Deps) {
	// Remove existing wisdom SP bonus before re-applying
	if player.WisdomTicks > 0 {
		player.SP -= player.WisdomSP
	}
	player.SP += sp
	player.WisdomSP = sp
	player.WisdomTicks = durationSec * 5 // 200ms per tick

	sendPlayerStatus(sess, player)
	broadcastEffect(sess, player, gfxID, deps)
}

// broadcastEffect sends S_EFFECT to self and nearby players.
func broadcastEffect(sess *net.Session, player *world.PlayerInfo, gfxID int32, deps *Deps) {
	sendEffectOnPlayer(sess, player.CharID, gfxID)
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendEffectOnPlayer(other.Session, player.CharID, gfxID)
	}
}

// sendSpeedPacket sends S_SPEED (opcode 255) — speed buff/debuff.
// In V381, this single opcode handles both haste and brave:
//   type 0 = cancel speed effect
//   type 1 = haste (green potion — movement + attack speed)
//   type 3 = brave (brave potion — movement + attack speed, slightly faster)
func sendSpeedPacket(sess *net.Session, charID int32, speedType byte, duration uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SPEED)
	w.WriteD(charID)
	w.WriteC(speedType)
	w.WriteH(duration)
	sess.Send(w.Bytes())
}

// --- Packet helpers ---

func sendHpUpdate(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteH(uint16(player.HP))
	w.WriteH(uint16(player.MaxHP))
	sess.Send(w.Bytes())
}

func sendMpUpdate(sess *net.Session, player *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MANA_POINT)
	w.WriteH(uint16(player.MP))
	w.WriteH(uint16(player.MaxMP))
	sess.Send(w.Bytes())
}

func sendEffectOnPlayer(sess *net.Session, charID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(charID)
	w.WriteH(uint16(gfxID))
	sess.Send(w.Bytes())
}

// --- Teleport scrolls ---

// Teleport scroll item IDs (Java L1ItemId constants)
const (
	teleportScrollNormal  int32 = 40100 // Scroll of Teleportation
	teleportScrollBlessed int32 = 40099 // Blessed Scroll of Teleportation
	teleportScrollAncient int32 = 40086 // Ancient Scroll of Teleportation
	teleportScrollSpecial int32 = 40863 // Special Scroll of Teleportation
)

func isTeleportScroll(itemID int32) bool {
	switch itemID {
	case teleportScrollNormal, teleportScrollBlessed, teleportScrollAncient, teleportScrollSpecial:
		return true
	}
	return false
}

// handleUseTeleportScroll processes teleport scroll usage.
// Packet continuation after objectID: [H mapID][D bookmarkID]
func handleUseTeleportScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, invItem *world.InvItem, deps *Deps) {
	_ = r.ReadH()         // mapID from client (we verify against stored bookmark)
	bookmarkID := r.ReadD() // bookmark ID

	if player.Dead {
		return
	}

	// Find the bookmark by ID
	var target *world.Bookmark
	for i := range player.Bookmarks {
		if player.Bookmarks[i].ID == bookmarkID {
			target = &player.Bookmarks[i]
			break
		}
	}
	if target == nil {
		sendServerMessage(sess, 79) // "Nothing happens"
		return
	}

	// Consume the scroll
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		sendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		sendItemCountUpdate(sess, invItem)
	}
	sendWeightUpdate(sess, player)

	// Broadcast teleport effect (GFX 169 = teleport visual)
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendEffectOnPlayer(viewer.Session, player.CharID, 169)
	}

	// Teleport immediately
	teleportPlayer(sess, player, target.X, target.Y, target.MapID, 5, deps)

	deps.Log.Info(fmt.Sprintf("書籤傳送  角色=%s  書籤=%s  x=%d  y=%d  地圖=%d", player.Name, target.Name, target.X, target.Y, target.MapID))
}

// --- Drop system ---

// GiveDrops rolls drops for a killed NPC and adds them to the killer's inventory.
func GiveDrops(killer *world.PlayerInfo, npcID int32, deps *Deps) {
	if deps.Drops == nil {
		return
	}
	dropList := deps.Drops.Get(npcID)
	if dropList == nil {
		return
	}

	dropRate := deps.Config.Rates.DropRate
	goldRate := deps.Config.Rates.GoldRate

	for _, drop := range dropList {
		// Apply drop rate multiplier to chance
		chance := drop.Chance
		if drop.ItemID == world.AdenaItemID {
			if goldRate > 0 {
				chance = int(float64(chance) * goldRate)
			}
		} else {
			if dropRate > 0 {
				chance = int(float64(chance) * dropRate)
			}
		}
		if chance > 1000000 {
			chance = 1000000
		}

		roll := world.RandInt(1000000)
		if roll >= chance {
			continue
		}

		if killer.Inv.IsFull() {
			break
		}

		qty := int32(drop.Min)
		if drop.Max > drop.Min {
			qty = int32(drop.Min + world.RandInt(drop.Max-drop.Min+1))
		}
		if qty <= 0 {
			qty = 1
		}

		// Apply gold rate to adena quantity
		if drop.ItemID == world.AdenaItemID && goldRate > 0 {
			qty = int32(float64(qty) * goldRate)
			if qty <= 0 {
				qty = 1
			}
		}

		itemInfo := deps.Items.Get(drop.ItemID)
		if itemInfo == nil {
			continue
		}

		stackable := itemInfo.Stackable || drop.ItemID == world.AdenaItemID
		existing := killer.Inv.FindByItemID(drop.ItemID)
		wasExisting := existing != nil && stackable

		item := killer.Inv.AddItem(
			drop.ItemID,
			qty,
			itemInfo.Name,
			itemInfo.InvGfx,
			itemInfo.Weight,
			stackable,
			byte(drop.EnchantLevel),
		)
		item.UseType = itemInfo.UseTypeID
		// Equipment drops from monsters start unidentified (dark name, no stats)
		if itemInfo.Category == data.CategoryWeapon || itemInfo.Category == data.CategoryArmor {
			item.Identified = false
		}

		if wasExisting {
			sendItemCountUpdate(killer.Session, item)
		} else {
			sendAddItem(killer.Session, item)
		}
		sendWeightUpdate(killer.Session, killer)

		// Notify player about the drop
		if drop.ItemID == world.AdenaItemID {
			msg := fmt.Sprintf("獲得 %d 金幣", qty)
			sendGlobalChat(killer.Session, 9, msg)
		} else {
			name := itemInfo.Name
			if drop.EnchantLevel > 0 {
				name = fmt.Sprintf("+%d %s", drop.EnchantLevel, name)
			}
			if qty > 1 {
				msg := fmt.Sprintf("獲得 %s (%d)", name, qty)
				sendGlobalChat(killer.Session, 9, msg)
			} else {
				msg := fmt.Sprintf("獲得 %s", name)
				sendGlobalChat(killer.Session, 9, msg)
			}
		}
	}
}
