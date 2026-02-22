package handler

import (
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleVersion processes C_ClientVersion (opcode 14).
// Responds with S_ServerVersion (opcode 139) and transitions to VersionOK.
func HandleVersion(sess *net.Session, r *packet.Reader, deps *Deps) {
	// Client sends version info — we mostly ignore it and respond with server version.
	deps.Log.Debug("received client version", zap.Uint64("session", sess.ID))

	cfg := deps.Config
	now := time.Now()
	uptime := int32(now.Unix() - cfg.Server.StartTime)

	w := packet.NewWriterWithOpcode(packet.S_OPCODE_VERSION_CHECK)
	w.WriteC(0x00)                    // auth ok marker
	w.WriteC(byte(cfg.Server.ID))     // server ID
	w.WriteDU(0x07cbf4dd)             // server version 3.80C Taiwan
	w.WriteDU(0x07cbf4dd)             // cache version
	w.WriteDU(0x77fc692d)             // auth version
	w.WriteDU(0x07cbf4d9)             // npc version
	w.WriteD(int32(cfg.Server.StartTime)) // server start time (unix seconds)
	w.WriteC(0x00)                    // unknown
	w.WriteC(0x00)                    // unknown
	w.WriteC(byte(cfg.Server.Language)) // country code
	w.WriteDU(0x087f7dc2)             // server type
	w.WriteD(uptime)                  // uptime seconds
	w.WriteH(0x01)                    // unknown

	sess.Send(w.Bytes())
	sess.SetState(packet.StateVersionOK)
}
