package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandleTeleport processes C_TELEPORT (opcode 52) — teleport confirmation.
// In Java, the client sends this empty packet after receiving S_Teleport to confirm
// the teleport should execute. In V381, teleport scrolls execute immediately,
// so this handler serves as a fallback for the HasTeleport pre-stored destination.
// Format: opcode only, no payload.
func HandleTeleport(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// Check for pre-stored teleport destination
	if !player.HasTeleport {
		return
	}

	// Auto-cancel trade when teleporting
	cancelTradeIfActive(player, deps)

	player.HasTeleport = false

	teleportPlayer(sess, player,
		player.TeleportX, player.TeleportY,
		player.TeleportMapID, player.TeleportHeading, deps)
}
