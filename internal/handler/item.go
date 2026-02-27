package handler

import (
	"fmt"
	"strconv"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// Message IDs for item validation
const (
	msgClassCannotUse uint16 = 264 // "你的職業無法使用此道具。"
	msgLevelTooLow    uint16 = 318 // "等級 %0以上才可使用此道具。"
)

// Virtual SkillIDs for potion-based buffs (matching Java L1SkillId.java STATUS_* constants).
// These are NOT real spell IDs — they are virtual IDs used by setSkillEffect to track
// potion durations in the same system as spell buffs.
const (
	SkillStatusBrave            int32 = 1000 // 勇敢藥水 (brave type 1, atk speed 1.33x)
	SkillStatusHaste            int32 = 1001 // 自我加速藥水 (move speed 1.33x)
	SkillStatusBluePotion       int32 = 1002 // 藍色藥水 (MP regen boost)
	SkillStatusUnderwaterBreath int32 = 1003 // 伊娃的祝福 (underwater breathing)
	SkillStatusWisdomPotion     int32 = 1004 // 慎重藥水 (SP +2)
	SkillStatusElfBrave         int32 = 1016 // 精靈餅乾 (brave type 3, atk speed 1.15x)
	SkillStatusRiBrave          int32 = 1017 // 生命之樹果實 (DK/IL brave)
	SkillStatusThirdSpeed       int32 = 1027 // 三段加速 (char speed 1.15x)

	SkillDecayPotion int32 = 71 // 腐敗藥水 debuff — blocks all potion use
	SkillCurseBlind  int32 = 10 // CURSE_BLIND — blind curse effect
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
	} else if enchantLvl < 0 {
		displayName = fmt.Sprintf("%d %s", enchantLvl, displayName)
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
		SendDropItem(viewer.Session, gndItem)
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
		SendRemoveObject(viewer.Session, gndItem.ID)
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

	bless := byte(0)
	if itemInfo != nil {
		bless = byte(itemInfo.Bless)
	}
	invItem := player.Inv.AddItem(
		gndItem.ItemID,
		gndItem.Count,
		itemName,
		invGfx,
		weight,
		stackable,
		bless,
	)
	invItem.EnchantLvl = gndItem.EnchantLvl
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
		if deps.ItemUse != nil {
			deps.ItemUse.UseTeleportScroll(sess, r, player, invItem)
		}
		return
	}

	// Home scrolls (回家卷軸): no extra packet data, teleport to respawn location
	if isHomeScroll(invItem.ItemID) {
		if deps.ItemUse != nil {
			deps.ItemUse.UseHomeScroll(sess, player, invItem)
		}
		return
	}

	// Fixed-destination teleport scrolls (指定傳送卷軸): loc_x/loc_y/map_id set in etcitem YAML
	if itemInfo.LocX != 0 && itemInfo.Category == data.CategoryEtcItem {
		if deps.ItemUse != nil {
			deps.ItemUse.UseFixedTeleportScroll(sess, player, invItem, itemInfo)
		}
		return
	}

	// Polymorph scrolls have additional data in the packet: [S monsterName]
	if IsPolyScroll(invItem.ItemID) {
		handlePolyScroll(sess, r, player, invItem, deps)
		return
	}

	switch itemInfo.Category {
	case data.CategoryWeapon:
		if deps.Equip != nil {
			deps.Equip.EquipWeapon(sess, player, invItem, itemInfo)
		}
	case data.CategoryArmor:
		if deps.Equip != nil {
			deps.Equip.EquipArmor(sess, player, invItem, itemInfo)
		}
	case data.CategoryEtcItem:
		handleUseEtcItem(sess, r, player, invItem, itemInfo, deps)
	}
}

// ---------- 裝備系統委派（穿脫 + 屬性計算委派 EquipSystem） ----------

// unequipSlot 脫下指定欄位的裝備。委派給 EquipSystem。
func unequipSlot(sess *net.Session, player *world.PlayerInfo, slot world.EquipSlot, deps *Deps) {
	if deps.Equip != nil {
		deps.Equip.UnequipSlot(sess, player, slot)
	}
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

// recalcEquipStats 重新計算裝備屬性並發送更新封包。委派給 EquipSystem。
func recalcEquipStats(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	if deps.Equip != nil {
		deps.Equip.RecalcEquipStats(sess, player)
	}
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
	} else if item.EnchantLvl < 0 {
		name = fmt.Sprintf("%d %s", item.EnchantLvl, name)
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

// ---------- Use EtcItem (thin dispatcher) ----------

// handleUseEtcItem 路由消耗品至對應系統。
// 寵物/魔法娃娃留在 handler，其餘委派給 ItemUseSystem。
func handleUseEtcItem(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, invItem *world.InvItem, itemInfo *data.ItemInfo, deps *Deps) {
	// Level restriction check for consumables
	if !checkLevelRestriction(sess, player.Level, itemInfo) {
		return
	}

	// Enchant scrolls: use_type "dai" (weapon) or "zel" (armor)
	if itemInfo.UseType == "dai" || itemInfo.UseType == "zel" {
		if deps.ItemUse != nil {
			deps.ItemUse.EnchantItem(sess, r, player, invItem, itemInfo)
		}
		return
	}

	// Identify scroll: use_type "identify"
	if itemInfo.UseType == "identify" {
		if deps.ItemUse != nil {
			deps.ItemUse.IdentifyItem(sess, r, player, invItem)
		}
		return
	}

	// Skill book: item_type "spellbook"
	if itemInfo.ItemType == "spellbook" {
		if deps.ItemUse != nil {
			deps.ItemUse.UseSpellBook(sess, player, invItem, itemInfo)
		}
		return
	}

	// Pet collar items: summon/collect pet
	if isPetCollar(invItem.ItemID) {
		handleUsePetCollar(sess, player, invItem, deps)
		return
	}

	// Magic doll items: check doll table before potions/consumables
	if deps.Dolls != nil {
		if dd := deps.Dolls.Get(invItem.ItemID); dd != nil {
			handleUseDoll(sess, player, invItem, dd, deps)
			return
		}
	}

	// All other consumables (potions, food) → ItemUseSystem
	if deps.ItemUse != nil {
		deps.ItemUse.UseConsumable(sess, player, invItem, itemInfo)
	}
}

// ---------- Identification packets ----------

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
		w.WriteS(fmt.Sprintf("%d%+d", info.DmgSmall, item.EnchantLvl))
		w.WriteS(fmt.Sprintf("%d%+d", info.DmgLarge, item.EnchantLvl))

	case data.CategoryArmor:
		// Format 135: armor — name, abs(ac)+enchant
		w.WriteH(135)
		w.WriteC(2) // param count
		w.WriteS(name)
		ac := info.AC
		if ac < 0 {
			ac = -ac
		}
		w.WriteS(fmt.Sprintf("%d%+d", ac, item.EnchantLvl))

	default:
		// Etcitem — format 138: name + weight
		w.WriteH(138)
		w.WriteC(2) // param count
		w.WriteS(name)
		w.WriteS(fmt.Sprintf("%d", calcItemWeight(item, info)))
	}

	sess.Send(w.Bytes())
}

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
		buf = append(buf, 2, byte(item.EnchantLvl))
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

// ---------- Packet helpers (shared with other handler files) ----------

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

// sendBravePacket sends S_SkillBrave (opcode 67) — brave/二段加速 buff.
// type 0 = cancel, type 1 = brave (勇敢藥水), type 3 = elf brave (精靈餅乾).
func sendBravePacket(sess *net.Session, charID int32, braveType byte, duration uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SKILLBRAVE)
	w.WriteD(charID)
	w.WriteC(braveType)
	w.WriteH(duration)
	sess.Send(w.Bytes())
}

// sendSpeedPacket sends S_SkillHaste (opcode 255) — haste/一段加速 buff.
// type 0 = cancel, type 1 = haste (移動+攻擊加速).
func sendSpeedPacket(sess *net.Session, charID int32, speedType byte, duration uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SPEED)
	w.WriteD(charID)
	w.WriteC(speedType)
	w.WriteH(duration)
	sess.Send(w.Bytes())
}

// ---------- Teleport scroll routing ----------

// Teleport scroll item IDs (Java L1ItemId constants)
const (
	teleportScrollNormal     int32 = 40100  // Scroll of Teleportation
	teleportScrollBlessedAlt int32 = 140100 // Blessed Scroll of Teleportation (prefix variant)
	teleportScrollBlessed    int32 = 40099  // Blessed Scroll of Teleportation
	teleportScrollAncient    int32 = 40086  // Ancient Scroll of Teleportation
	teleportScrollSpecial    int32 = 40863  // Special Scroll of Teleportation
)

func isTeleportScroll(itemID int32) bool {
	switch itemID {
	case teleportScrollNormal, teleportScrollBlessedAlt, teleportScrollBlessed, teleportScrollAncient, teleportScrollSpecial:
		return true
	}
	return false
}

// Home scroll item IDs (Java: 回家卷軸)
const (
	homeScrollNormal int32 = 40079 // Scroll of Return (傳送回家的卷軸)
	homeScrollIvory  int32 = 40095 // Ivory Tower Return Scroll (象牙塔傳送回家的卷軸)
	homeScrollElf    int32 = 40521 // Elf Wings (精靈羽翼)
)

func isHomeScroll(itemID int32) bool {
	switch itemID {
	case homeScrollNormal, homeScrollIvory, homeScrollElf:
		return true
	}
	return false
}

// sendTeleportUnlock sends S_Paralysis(TYPE_TELEPORT_UNLOCK) to unfreeze the client.
// Java: S_Paralysis.java — TYPE_TELEPORT_UNLOCK = 7, writeC(7)
// MUST be sent after every teleport scroll use, even on error.
func sendTeleportUnlock(sess *net.Session) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PARALYSIS)
	w.WriteC(7) // TYPE_TELEPORT_UNLOCK
	sess.Send(w.Bytes())
}

// ---------- 委派給 ItemUseSystem 的薄層 ----------

// GiveDrops 為擊殺的 NPC 擲骰掉落物品。委派給 ItemUseSystem。
func GiveDrops(killer *world.PlayerInfo, npcID int32, deps *Deps) {
	if deps.ItemUse != nil {
		deps.ItemUse.GiveDrops(killer, npcID)
	}
}

// broadcastEffect 向自己和附近玩家廣播特效。委派給 ItemUseSystem。
func broadcastEffect(sess *net.Session, player *world.PlayerInfo, gfxID int32, deps *Deps) {
	if deps.ItemUse != nil {
		deps.ItemUse.BroadcastEffect(sess, player, gfxID)
	}
}

// applyHaste 套用加速效果。委派給 ItemUseSystem。
func applyHaste(sess *net.Session, player *world.PlayerInfo, durationSec int, gfxID int32, deps *Deps) {
	if deps.ItemUse != nil {
		deps.ItemUse.ApplyHaste(sess, player, durationSec, gfxID)
	}
}

// ---------- Exported wrappers for system package ----------

// BroadcastVisualUpdate 廣播角色外觀更新。Exported for system package usage.
func BroadcastVisualUpdate(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	broadcastVisualUpdate(sess, player, deps)
}

// SendItemStatusUpdate sends S_ItemStatus. Exported for system package usage.
func SendItemStatusUpdate(sess *net.Session, item *world.InvItem, info *data.ItemInfo) {
	sendItemStatusUpdate(sess, item, info)
}

// SendItemNameUpdate sends S_CHANGE_ITEM_DESC. Exported for system package usage.
func SendItemNameUpdate(sess *net.Session, item *world.InvItem, info *data.ItemInfo) {
	sendItemNameUpdate(sess, item, info)
}

// SendItemColor sends S_ItemColor. Exported for system package usage.
func SendItemColor(sess *net.Session, objectID int32, bless byte) {
	sendItemColor(sess, objectID, bless)
}

// SendIdentifyDesc sends S_IdentifyDesc. Exported for system package usage.
func SendIdentifyDesc(sess *net.Session, item *world.InvItem, info *data.ItemInfo) {
	sendIdentifyDesc(sess, item, info)
}

// BuildViewName 建構物品顯示名稱。Exported for system package usage.
func BuildViewName(item *world.InvItem, info *data.ItemInfo) string {
	return buildViewName(item, info)
}
