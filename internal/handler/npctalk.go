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

	npc := deps.World.GetNpc(objID)
	if npc == nil {
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
