package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleAttr processes C_ATTR (opcode 121) — S_Message_YN yes/no dialog response.
// Java C_Attr format: [H unknown][D count][H messageType][H response(0=No, 1=Yes)]
// Special case: if first H == 479, it's a different sub-type (ignored).
func HandleAttr(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	firstH := r.ReadH()
	if firstH == 479 {
		// Special sub-type, not a yes/no response
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
	}
}
