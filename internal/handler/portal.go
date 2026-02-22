package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleEnterPortal processes C_ENTER_PORTAL (opcode 219).
// Client sends the portal tile coordinates when stepping on a dungeon entrance/exit.
// Format: [H srcX][H srcY]
func HandleEnterPortal(sess *net.Session, r *packet.Reader, deps *Deps) {
	srcX := int32(r.ReadH())
	srcY := int32(r.ReadH())

	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	if deps.Portals == nil {
		return
	}

	// Look up portal destination
	portal := deps.Portals.Get(srcX, srcY, player.MapID)
	if portal == nil {
		deps.Log.Debug("no portal at location",
			zap.Int32("x", srcX),
			zap.Int32("y", srcY),
			zap.Int16("map", player.MapID),
		)
		return
	}

	// Validate player is near the portal tile (Chebyshev <= 3 for tolerance)
	dx := player.X - srcX
	dy := player.Y - srcY
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

	// Auto-cancel trade when entering portal
	cancelTradeIfActive(player, deps)

	deps.Log.Info(fmt.Sprintf("傳送門傳送  角色=%s  備註=%s  目標x=%d  目標y=%d  目標地圖=%d", player.Name, portal.Note, portal.DstX, portal.DstY, portal.DstMapID))

	teleportPlayer(sess, player, portal.DstX, portal.DstY, portal.DstMapID, portal.DstHeading, deps)
}
