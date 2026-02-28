package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// CalcHeading 計算兩點間的朝向（handler 內部及 system 使用）。
func CalcHeading(sx, sy, tx, ty int32) int16 {
	ddx := tx - sx
	ddy := ty - sy
	if ddx > 0 {
		ddx = 1
	} else if ddx < 0 {
		ddx = -1
	}
	if ddy > 0 {
		ddy = 1
	} else if ddy < 0 {
		ddy = -1
	}
	for i := int16(0); i < 8; i++ {
		if headingDX[i] == ddx && headingDY[i] == ddy {
			return i
		}
	}
	return 0
}

// FindArrow 在玩家背包中找到可用的箭矢（handler 內部及 system 使用）。
func FindArrow(player *world.PlayerInfo, deps *Deps) *world.InvItem {
	for _, item := range player.Inv.Items {
		info := deps.Items.Get(item.ItemID)
		if info != nil && info.ItemType == "arrow" && item.Count > 0 {
			return item
		}
	}
	return nil
}

// HandleAttack processes C_ATTACK (opcode 229).
// Thin handler: parse packet → queue to CombatSystem (Phase 2).
// Format: [D targetID][H x][H y]
func HandleAttack(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()
	_ = r.ReadH() // target x (unused, we use server position)
	_ = r.ReadH() // target y (unused)

	if deps.Combat == nil {
		return
	}
	deps.Combat.QueueAttack(AttackRequest{
		AttackerSessionID: sess.ID,
		TargetID:          targetID,
		IsMelee:           true,
	})
}

// HandleFarAttack processes C_FAR_ATTACK (opcode 123) — bow/ranged attacks.
// Thin handler: parse packet → queue to CombatSystem (Phase 2).
// Format: [D targetID][H x][H y]
func HandleFarAttack(sess *net.Session, r *packet.Reader, deps *Deps) {
	targetID := r.ReadD()
	_ = r.ReadH()
	_ = r.ReadH()

	if deps.Combat == nil {
		return
	}
	deps.Combat.QueueAttack(AttackRequest{
		AttackerSessionID: sess.ID,
		TargetID:          targetID,
		IsMelee:           false,
	})
}
