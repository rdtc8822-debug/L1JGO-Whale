package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandleRestart processes C_RESTART (opcode 177).
// Thin handler: 解析封包 → 委派至 DeathSystem.ProcessRestart。
func HandleRestart(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || !player.Dead {
		return
	}
	if deps.Death == nil {
		return
	}
	deps.Death.ProcessRestart(sess, player)
}
