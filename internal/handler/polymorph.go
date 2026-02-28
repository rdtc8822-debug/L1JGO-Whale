package handler

import (
	"math/rand/v2"

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

// PlayerGfx returns the visual GFX ID for a player.
// If polymorphed (TempCharGfx > 0), returns the polymorph GFX; otherwise ClassID.
func PlayerGfx(p *world.PlayerInfo) int32 {
	if p.TempCharGfx > 0 {
		return p.TempCharGfx
	}
	return p.ClassID
}

// ==================== 封包發送 ====================

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

// SendChangeShape 廣播變身封包。Exported for system package usage.
func SendChangeShape(viewer *net.Session, objID int32, polyGfx int32, weapon byte) {
	sendChangeShape(viewer, objID, polyGfx, weapon)
}

// sendPolyIcon sends S_PacketBox sub 35 — polymorph duration icon.
// Java: S_PacketBox(35, durationSec). durationSec=0 cancels the icon.
func sendPolyIcon(sess *net.Session, durationSec uint16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(35) // subcode: polymorph timer
	w.WriteH(durationSec)
	sess.Send(w.Bytes())
}

// SendPolyIcon 發送變身計時圖示。Exported for system package usage.
func SendPolyIcon(sess *net.Session, durationSec uint16) {
	sendPolyIcon(sess, durationSec)
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

// ==================== 變身卷軸處理 ====================

// handlePolyScroll processes polymorph scroll/potion usage.
// Called from HandleUseItem when the item is a polymorph scroll (40088, 40096, 49308, 140088).
// Java: C_ItemUSe.usePolyScroll()
// Packet continuation: [S monsterName] — client shows monlist dialog, sends selected name.
func handlePolyScroll(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, invItem *world.InvItem, deps *Deps) {
	if deps.Polymorph == nil {
		return
	}
	monsterName := r.ReadS()
	deps.Polymorph.UsePolyScroll(sess, player, invItem, monsterName)
}

// ==================== Hypertext 封包處理 ====================

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

	if deps.Polymorph == nil {
		return
	}

	// Otherwise: monlist polymorph dialog — format: [D objectID][S monsterName]
	_ = r.ReadD()      // objectID (player's charID)
	input := r.ReadS() // monster name entered by player
	deps.Polymorph.UsePolySkill(sess, player, input)
}

// ==================== 輔助函式 ====================

// PolyScrollDuration returns the polymorph duration in seconds for the given scroll type.
// Java: C_ItemUSe.usePolyScroll() lines 3166-3174
// Exported for system package usage.
func PolyScrollDuration(itemID int32) int {
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
