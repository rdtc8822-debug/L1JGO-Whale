package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandleAction processes C_ACTION (opcode 120) — player emote / animation command.
// Format: [C actionCode]
// Action codes: 66=Think(Alt+4), 67=Aggress(Alt+3), 68=Salute(Alt+1), 69=Cheer(Alt+2), etc.
func HandleAction(sess *net.Session, r *packet.Reader, deps *Deps) {
	actionCode := r.ReadC()

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Broadcast animation to all nearby players (not self — client plays it locally)
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, player.CharID, actionCode)
	}
}
