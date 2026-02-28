package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
)

// HandleDuel processes C_DUEL (opcode 5).
// Java: C_Fight — 面對面決鬥請求。使用 FaceToFace.faceToFace(pc) 找到對面玩家。
// TODO: 實作完整決鬥系統（FaceToFace、FightID、Y/N 確認）。
func HandleDuel(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	// 暫時回傳提示訊息（決鬥系統尚未實作）
	SendServerMessageStr(sess, 79, "決鬥") // "無法使用"
}

// HandleCheckPK processes C_CHECK_PK (opcode 51).
// Server responds with S_ServerMessage(562) containing the player's PK count.
func HandleCheckPK(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	SendServerMessageN(sess, 562, player.PKCount)
}

// ========================================================================
//  封包輔助函式（供 system/pvp.go 及其他 system 呼叫）
// ========================================================================

// SendPinkName sends S_PinkName (opcode 60).
// Format: [D objID][D timeSec] — timeSec=180 to enable, 0 to remove.
func SendPinkName(sess *net.Session, charID int32, timeSec int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PINKNAME)
	w.WriteD(charID)
	w.WriteD(timeSec)
	sess.Send(w.Bytes())
}

// SendLawful sends S_Lawful (opcode 34).
// Format: [D objID][H lawful][D 0]
func SendLawful(sess *net.Session, charID int32, lawful int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_LAWFUL)
	w.WriteD(charID)
	w.WriteH(uint16(int16(lawful))) // int16 range
	w.WriteD(0)                     // padding (matches Java)
	sess.Send(w.Bytes())
}

// SendServerMessageN sends S_ServerMessage with a numeric parameter.
// Format: [H msgID][C argCount][S arg1]
func SendServerMessageN(sess *net.Session, msgID uint16, value int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(1) // 1 argument
	w.WriteS(fmt.Sprintf("%d", value))
	sess.Send(w.Bytes())
}

// SendServerMessageStr sends S_ServerMessage with one string parameter.
// Format: [H msgID][C 1][S arg]
func SendServerMessageStr(sess *net.Session, msgID uint16, arg string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE_CODE)
	w.WriteH(msgID)
	w.WriteC(1)
	w.WriteS(arg)
	sess.Send(w.Bytes())
}

// SendRedMessage sends S_RedMessage (opcode 105) — center screen red text warning.
// Wire format identical to S_ServerMessage: [H msgID][C argCount][S args...]
func SendRedMessage(sess *net.Session, msgID uint16, args ...string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REDMESSAGE)
	w.WriteH(msgID)
	w.WriteC(byte(len(args)))
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// ClampLawful clamps lawful value to int16 range [-32768, 32767].
func ClampLawful(lawful *int32) {
	if *lawful > 32767 {
		*lawful = 32767
	} else if *lawful < -32768 {
		*lawful = -32768
	}
}
