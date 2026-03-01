package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// HandleDuel processes C_DUEL (opcode 5).
// Java: C_Fight — 面對面決鬥請求。使用 FaceToFace.faceToFace(pc) 找到對面玩家。
// 協議流程：C_DUEL → 找對面玩家 → 設定 FightId → S_Message_YN(630) → 等待回應。
func HandleDuel(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// 尋找面對面的玩家（已有函式：trade.go findFaceToFace）
	target := findFaceToFace(player, deps)
	if target == nil {
		return
	}

	// 驗證：雙方都不在決鬥中（Java: getFightId() != 0 → msg 633/634）
	if player.FightId != 0 {
		SendServerMessage(sess, 633) // "你正在決鬥中。"
		return
	}
	if target.FightId != 0 {
		SendServerMessage(sess, 634) // "對方正在與其他人決鬥中。"
		return
	}

	// 設定 FightId（Java: 在送 Y/N 前就設定，雙方互指）
	player.FightId = target.CharID
	target.FightId = player.CharID

	// 記錄 pending Y/N 類型，供 C_ATTR 回應時路由
	target.PendingYesNoType = 630
	target.PendingYesNoData = player.CharID

	// 發送 S_Message_YN(630) 給目標：「%0 要與你決鬥。你是否同意？(Y/N)」
	SendYesNoDialog(target.Session, 630, player.Name)
}

// HandleDuelResponse 處理決鬥 Y/N 回應（由 C_ATTR case 630 呼叫）。
func HandleDuelResponse(sess *net.Session, player *world.PlayerInfo, partnerCharID int32, accepted bool, deps *Deps) {
	partner := deps.World.GetByCharID(partnerCharID)
	if partner == nil {
		// 對方已離線，清除自己的決鬥狀態
		player.FightId = 0
		return
	}

	if !accepted {
		// 拒絕決鬥：清除雙方 FightId
		player.FightId = 0
		partner.FightId = 0
		SendServerMessageStr(partner.Session, 631, player.Name) // "%0 拒絕了與你的決鬥。"
		return
	}

	// 接受決鬥：發送決鬥通知給雙方（觸發決鬥音樂）
	SendDuelNotify(partner.Session, partner.FightId, partner.CharID)
	SendDuelNotify(player.Session, player.FightId, player.CharID)
}

// ClearDuelOnDeath 玩家死亡時清理決鬥狀態。供 DeathSystem 呼叫。
// 返回 true 表示此次死亡為決鬥死亡（呼叫方應跳過 PK 後果）。
func ClearDuelOnDeath(player *world.PlayerInfo, ws *world.State) bool {
	if player.FightId == 0 {
		return false
	}

	isDuel := false
	partner := ws.GetByCharID(player.FightId)
	if partner != nil && partner.FightId == player.CharID {
		// 互相指向 → 正式決鬥死亡
		isDuel = true
		partner.FightId = 0
		SendDuelNotify(partner.Session, 0, 0)
	}
	player.FightId = 0
	return isDuel
}

// ClearDuelOnDisconnect 玩家斷線時清理對手的決鬥狀態。供 InputSystem 呼叫。
func ClearDuelOnDisconnect(player *world.PlayerInfo, ws *world.State) {
	if player.FightId == 0 {
		return
	}
	partner := ws.GetByCharID(player.FightId)
	if partner != nil {
		partner.FightId = 0
		SendDuelNotify(partner.Session, 0, 0)
	}
	player.FightId = 0
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

// SendDuelNotify 發送 S_PacketBox(MSG_DUEL=5) — 決鬥開始/結束通知。
// 開始：fightId=對手角色ID, ownId=自己角色ID（觸發決鬥音樂）。
// 結束：fightId=0, ownId=0（停止決鬥音樂）。
func SendDuelNotify(sess *net.Session, fightId, ownId int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteC(5) // MSG_DUEL subcode
	w.WriteD(fightId)
	w.WriteD(ownId)
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
