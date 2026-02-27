package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleNpcTalk processes C_DIALOG (opcode 34) — player clicks an NPC.
// Looks up the NPC's dialog HTML ID and sends S_HYPERTEXT (opcode 39).
func HandleNpcTalk(sess *net.Session, r *packet.Reader, deps *Deps) {
	objID := r.ReadD()

	// Check if target is a summon — show summon control menu
	if sum := deps.World.GetSummon(objID); sum != nil {
		player := deps.World.GetBySession(sess.ID)
		if player != nil && sum.OwnerCharID == player.CharID {
			sendSummonMenu(sess, sum)
		}
		return
	}

	// Check if target is a pet — show pet control menu ("anicom")
	if pet := deps.World.GetPet(objID); pet != nil {
		player := deps.World.GetBySession(sess.ID)
		if player != nil && pet.OwnerCharID == player.CharID {
			sendPetMenu(sess, pet, petExpPercent(pet, deps))
		}
		return
	}

	npc := deps.World.GetNpc(objID)
	if npc == nil {
		return
	}

	// Check if this is a paginated teleporter NPC (e.g., NPC 91053)
	if deps.TeleportPages != nil && deps.TeleportPages.IsPageTeleportNpc(npc.NpcID) {
		player := deps.World.GetBySession(sess.ID)
		if player != nil {
			handlePagedTeleportTalk(sess, player, objID, deps)
		}
		return
	}

	// L1Dwarf（倉庫 NPC）— Java L1DwarfInstance.onTalkAction() 對所有倉庫 NPC
	// 強制回傳 "storage"（3.53C 新版倉庫介面），客戶端內建索回＋存放兩個 tab。
	// 只有 NPC 60028（精靈倉庫）對非精靈玩家回傳 "elCE1" 拒絕訊息。
	if npc.Impl == "L1Dwarf" {
		player := deps.World.GetBySession(sess.ID)
		if player == nil {
			return
		}
		htmlID := "storage"
		if npc.NpcID == 60028 && player.ClassType != 2 { // 2=精靈
			htmlID = "elCE1"
		}
		sendHypertext(sess, objID, htmlID)
		return
	}

	// Look up dialog data for this NPC template
	action := deps.NpcActions.Get(npc.NpcID)
	if action == nil {
		deps.Log.Debug("NPC has no dialog action",
			zap.Int32("npc_id", npc.NpcID),
			zap.String("name", npc.Name),
		)
		return
	}

	// Java: player.getLawful() < -1000 → chaotic action, else normal action.
	// For pledge NPCs both fields are "bpledge2"; the CLIENT handles clan/no-clan UI internally.
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	htmlID := action.NormalAction
	if player.Lawful < -1000 && action.CaoticAction != "" {
		htmlID = action.CaoticAction
	}
	if htmlID == "" {
		return
	}

	// Send S_HYPERTEXT (opcode 39) — NPC dialog
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HYPERTEXT)
	w.WriteD(objID)    // NPC object ID
	w.WriteS(htmlID)   // HTML identifier (client looks up built-in HTML)
	w.WriteH(0x00)     // no arguments marker
	w.WriteH(0)        // argument count = 0
	sess.Send(w.Bytes())
}
