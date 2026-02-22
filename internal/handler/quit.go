package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandleQuit processes C_QUIT (opcode 122).
// In Java, this handler does nothing — cleanup happens on socket close.
// We just close the session; InputSystem.handleDisconnect does all cleanup.
func HandleQuit(sess *net.Session, _ *packet.Reader, deps *Deps) {
	deps.Log.Info(fmt.Sprintf("玩家登出  session=%d  帳號=%s", sess.ID, sess.AccountName))
	sess.Close()
}
