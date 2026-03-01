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

	// 船舶碼頭驗證（Java: DungeonTable.dg() 船舶判定）
	// 碼頭傳送門需要航線時間窗口 + 持有船票才允許通過
	isDock, allowed := CheckShipDock(srcX, srcY, player.MapID, player)
	if isDock && !allowed {
		// 不在航線時間或沒有船票 → 靜默拒絕（匹配 Java 行為）
		return
	}

	// Auto-cancel trade when entering portal
	cancelTradeIfActive(player, deps)

	deps.Log.Info(fmt.Sprintf("傳送門傳送  角色=%s  備註=%s  目標x=%d  目標y=%d  目標地圖=%d", player.Name, portal.Note, portal.DstX, portal.DstY, portal.DstMapID))

	teleportPlayer(sess, player, portal.DstX, portal.DstY, portal.DstMapID, portal.DstHeading, deps)
}
