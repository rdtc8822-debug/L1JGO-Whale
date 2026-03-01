package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleAttr processes C_ATTR (opcode 121) — 多用途封包。
// Java C_Attr 格式：
//   mode = readH()
//   if mode == 0 { readD(); mode = readH() }  ← 前綴跳過
//   switch(mode) { case 479: 加點; case 97/252/630/951/953/954: yes/no 回應 }
func HandleAttr(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	mode := r.ReadH()

	// Java: mode == 0 時，先讀 D（target objID）再讀一次 H 取得真正 mode
	if mode == 0 {
		_ = r.ReadD() // tgobjid（RaiseAttr 帶的 charID）
		mode = r.ReadH()
	}

	if mode == statAllocAttrCode { // 479 = 加點（Java C_Attr case 479）
		confirm := r.ReadC()
		handleStatAlloc(sess, mode, confirm, r, deps)
		return
	}

	_ = r.ReadD()          // count (yesNoCount we sent in S_Message_YN)
	msgType := r.ReadH()   // message type (252=trade, 953=party, etc.)
	response := r.ReadH()  // 0=No, 1=Yes

	accepted := response != 0

	deps.Log.Debug("C_Attr yes/no response",
		zap.Uint16("msgType", msgType),
		zap.Uint16("response", response),
		zap.Bool("accepted", accepted),
	)

	// Clear pending state
	player.PendingYesNoType = 0
	data := player.PendingYesNoData
	player.PendingYesNoData = 0

	switch msgType {
	case 252: // Trade confirmation
		handleTradeYesNo(sess, player, data, accepted, deps)

	case 951: // Chat party invite: 您要接受玩家 %0 提出的隊伍對話邀請嗎？(Y/N)
		HandleChatPartyInviteResponse(player, data, accepted, deps)

	case 953: // Normal party invite: 玩家 %0 邀請您加入隊伍？(Y/N)
		HandlePartyInviteResponse(player, data, accepted, deps)

	case 954: // Auto-share party invite: 玩家 %0 邀請您加入自動分配隊伍？(Y/N)
		HandlePartyInviteResponse(player, data, accepted, deps)

	case 97: // Clan join request: %0想加入你的血盟，是否同意？(Y/N)
		HandleClanJoinResponse(sess, player, data, accepted, deps)

	case 630: // 決鬥確認: %0 要與你決鬥。你是否同意？(Y/N)
		HandleDuelResponse(sess, player, data, accepted, deps)
	}
}
