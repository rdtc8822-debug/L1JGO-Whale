package handler

import (
	"fmt"
	"math/rand/v2"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// SkillShapeChange is the skill ID for the Shape Change spell (變形術).
const SkillShapeChange int32 = 67

// Polymorph scroll / potion item IDs (Java: C_ItemUSe lines 678-684)
const (
	ItemPolyScroll        int32 = 40088  // 變形卷軸 (30 min)
	ItemIvoryTowerPoly    int32 = 40096  // 象牙塔變身卷軸 (30 min)
	ItemWelfarePolyPotion int32 = 49308  // 福利變形藥水 (random 40-80 min)
	ItemBlessedPolyScroll int32 = 140088 // 受祝福的變形卷軸 (35 min)
)

// IsPolyScroll returns true if the item is a polymorph scroll/potion.
func IsPolyScroll(itemID int32) bool {
	switch itemID {
	case ItemPolyScroll, ItemIvoryTowerPoly, ItemWelfarePolyPotion, ItemBlessedPolyScroll:
		return true
	}
	return false
}

// polyScrollDuration returns the polymorph duration in seconds for the given scroll type.
// Java: C_ItemUSe.usePolyScroll() lines 3166-3174
func polyScrollDuration(itemID int32) int {
	switch itemID {
	case ItemPolyScroll, ItemIvoryTowerPoly:
		return 1800 // 30 minutes
	case ItemBlessedPolyScroll:
		return 2100 // 35 minutes
	case ItemWelfarePolyPotion:
		return 2401 + rand.IntN(2400) // 2401-4800 seconds (40-80 min)
	}
	return 1800
}

// PlayerGfx returns the visual GFX ID for a player.
// If polymorphed (TempCharGfx > 0), returns the polymorph GFX; otherwise ClassID.
func PlayerGfx(p *world.PlayerInfo) int32 {
	if p.TempCharGfx > 0 {
		return p.TempCharGfx
	}
	return p.ClassID
}

// DoPoly polymorphs a player into the given form.
// cause: PolyCauseMagic(1), PolyCauseGM(2), PolyCauseNPC(4). cause=0 bypasses cause check.
// durationSec: buff duration in seconds (0 = permanent until cancelled).
func DoPoly(player *world.PlayerInfo, polyID int32, durationSec int, cause int, deps *Deps) {
	if player.Dead {
		return
	}
	if deps.Polys == nil {
		return
	}

	poly := deps.Polys.GetByID(polyID)
	if poly == nil {
		return
	}

	// Cause check (Java: isMatchCause)
	if !poly.IsMatchCause(cause) {
		return
	}

	// If already polymorphed, revert first
	if player.TempCharGfx > 0 {
		UndoPoly(player, deps)
	}

	// Set polymorph state
	player.TempCharGfx = polyID
	player.PolyID = polyID

	// Check weapon compatibility — if current weapon not allowed, clear visual
	if player.CurrentWeapon != 0 {
		wpn := player.Equip.Weapon()
		if wpn != nil {
			wpnInfo := deps.Items.Get(wpn.ItemID)
			if wpnInfo != nil && !poly.IsWeaponEquipable(wpnInfo.Type) {
				player.CurrentWeapon = 0
			}
		}
	}

	// Broadcast visual change to self + nearby
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendChangeShape(viewer.Session, player.CharID, polyID, player.CurrentWeapon)
	}

	// Force unequip incompatible items
	forceUnequipIncompat(player, poly, deps)

	// Register as buff with skillID=67 (Shape Change)
	if durationSec > 0 {
		buff := &world.ActiveBuff{
			SkillID:   SkillShapeChange,
			TicksLeft: durationSec * 5, // seconds → ticks (200ms each)
		}
		old := player.AddBuff(buff)
		if old != nil {
			revertBuffStats(player, old)
		}

		// Send buff icon: S_PacketBox sub 35 (polymorph timer)
		sendPolyIcon(player.Session, uint16(durationSec))
	}

	deps.Log.Info(fmt.Sprintf("玩家變身  角色=%s  形態=%s(GFX:%d)  持續=%d秒",
		player.Name, poly.Name, polyID, durationSec))
}

// UndoPoly reverts a player's polymorph, restoring original appearance.
func UndoPoly(player *world.PlayerInfo, deps *Deps) {
	if player.TempCharGfx == 0 {
		return // not polymorphed
	}

	player.TempCharGfx = 0
	player.PolyID = 0

	// Restore weapon visual from equipped weapon
	if wpn := player.Equip.Weapon(); wpn != nil {
		wpnInfo := deps.Items.Get(wpn.ItemID)
		if wpnInfo != nil {
			player.CurrentWeapon = world.WeaponVisualID(wpnInfo.Type)
		}
	}

	// Broadcast original appearance to self + nearby
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendChangeShape(viewer.Session, player.CharID, player.ClassID, player.CurrentWeapon)
	}

	// Cancel polymorph buff icon
	sendPolyIcon(player.Session, 0)

	// Remove the Shape Change buff entry
	player.RemoveBuff(SkillShapeChange)

	deps.Log.Info(fmt.Sprintf("玩家解除變身  角色=%s", player.Name))
}

// sendChangeShape sends S_ChangeShape (opcode 76) to the viewer.
// Java format: [D objID][H polyGfx][C weapon][C 0xff][C 0xff]
func sendChangeShape(viewer *net.Session, objID int32, polyGfx int32, weapon byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_POLY)
	w.WriteD(objID)
	w.WriteH(uint16(polyGfx))
	w.WriteC(weapon)
	w.WriteC(0xff)
	w.WriteC(0xff)
	viewer.Send(w.Bytes())
}

// sendPolyIcon sends S_PacketBox sub 35 — polymorph duration icon.
// Java: S_PacketBox(35, durationSec). durationSec=0 cancels the icon.
func sendPolyIcon(sess *net.Session, durationSec uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(35) // subcode: polymorph timer
	w.WriteH(durationSec)
	sess.Send(w.Bytes())
}

// forceUnequipIncompat unequips all items that are incompatible with the current polymorph.
// Java: L1PolyMorph.doPoly() → takeoff loop
func forceUnequipIncompat(player *world.PlayerInfo, poly *data.PolymorphInfo, deps *Deps) {
	sess := player.Session

	for slot := world.EquipSlot(1); slot < world.SlotMax; slot++ {
		item := player.Equip.Get(slot)
		if item == nil {
			continue
		}

		itemInfo := deps.Items.Get(item.ItemID)
		if itemInfo == nil {
			continue
		}

		shouldUnequip := false

		if slot == world.SlotWeapon {
			// Check weapon compatibility
			if !poly.IsWeaponEquipable(itemInfo.Type) {
				shouldUnequip = true
			}
		} else {
			// Check armor compatibility
			if !poly.IsArmorEquipable(itemInfo.Type) {
				shouldUnequip = true
			}
		}

		if shouldUnequip {
			// Cursed items (bless == 2) cannot be unequipped even by polymorph
			if item.Bless == 2 {
				continue
			}
			unequipSlot(sess, player, slot, deps)
		}
	}
}

// sendShowPolyList sends S_HYPERTEXT (opcode 39) with "monlist" to open the polymorph selection dialog.
// Java: S_ShowPolyList → sends htmlId "monlist" to client.
func sendShowPolyList(sess *net.Session, charID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(charID)
	w.WriteS("monlist")
	w.WriteH(0)
	w.WriteH(0)
	sess.Send(w.Bytes())
}

// SendShowPolyList 開啟變形對話框。Exported for system package usage.
func SendShowPolyList(sess *net.Session, charID int32) {
	sendShowPolyList(sess, charID)
}

// handlePolyScroll processes polymorph scroll/potion usage.
// Called from HandleUseItem when the item is a polymorph scroll (40088, 40096, 49308, 140088).
// Java: C_ItemUSe.usePolyScroll()
// Packet continuation: [S monsterName] — client shows monlist dialog, sends selected name.
func handlePolyScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, invItem *world.InvItem, deps *Deps) {
	monsterName := r.ReadS()

	// Empty string = cancel current polymorph (Java: s.equals(""))
	if monsterName == "" {
		if player.TempCharGfx != 0 {
			UndoPoly(player, deps)
			// Consume 1 scroll
			removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
			if removed {
				sendRemoveInventoryItem(sess, invItem.ObjectID)
			} else {
				sendItemCountUpdate(sess, invItem)
			}
			sendWeightUpdate(sess, player)
		}
		return
	}

	if deps.Polys == nil {
		return
	}

	// Lookup polymorph form by name
	poly := deps.Polys.GetByName(monsterName)
	if poly == nil {
		sendServerMessage(sess, 181) // "無法變成你指定的怪物。"
		return
	}

	// Level check (Java: poly.getMinLevel() <= pc.getLevel())
	if poly.MinLevel > 0 && int(player.Level) < poly.MinLevel {
		sendServerMessage(sess, 181)
		return
	}

	// Cause check — scrolls use PolyCauseMagic (1)
	if !poly.IsMatchCause(data.PolyCauseMagic) {
		sendServerMessage(sess, 181)
		return
	}

	// Determine duration based on scroll type
	duration := polyScrollDuration(invItem.ItemID)

	// Apply polymorph
	DoPoly(player, poly.PolyID, duration, data.PolyCauseMagic, deps)

	// Consume 1 scroll
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		sendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		sendItemCountUpdate(sess, invItem)
	}
	sendWeightUpdate(sess, player)
}

// HandleHypertextInputResult processes C_HYPERTEXT_INPUT_RESULT (opcode 11).
// This opcode is shared between two use cases:
// 1. Monlist polymorph dialog: [D objectID][S monsterName]
// 2. Crafting batch (C_Amount): [D npcObjID][D amount][C unknown][S actionStr]
// We distinguish by checking player.PendingCraftAction — set when S_InputAmount was sent.
func HandleHypertextInputResult(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Route to crafting amount handler if a batch dialog is pending
	if player.PendingCraftAction != "" {
		HandleCraftAmount(sess, r, player, deps)
		return
	}

	// Otherwise: monlist polymorph dialog — format: [D objectID][S monsterName]
	_ = r.ReadD()       // objectID (player's charID)
	input := r.ReadS()  // monster name entered by player

	if deps.Polys == nil {
		return
	}

	poly := deps.Polys.GetByName(input)
	if poly == nil {
		sendServerMessage(sess, 181) // "此怪物名稱不正確。"
		return
	}

	// Level check
	if poly.MinLevel > 0 && int(player.Level) < poly.MinLevel {
		sendServerMessage(sess, 181)
		return
	}

	// Cause check — skill 67 is magic cause
	if !poly.IsMatchCause(data.PolyCauseMagic) {
		sendServerMessage(sess, 181)
		return
	}

	// Consume material component (item 40318 for skill 67)
	skill := deps.Skills.Get(SkillShapeChange)
	if skill != nil && skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			sendServerMessage(sess, msgCastFail)
			return
		}
		removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
		if removed {
			sendRemoveInventoryItem(sess, slot.ObjectID)
		} else {
			sendItemCountUpdate(sess, slot)
		}
		sendWeightUpdate(sess, player)
	}

	// Apply polymorph: 7200 seconds = 2 hours (Java default)
	DoPoly(player, poly.PolyID, 7200, data.PolyCauseMagic, deps)
}
