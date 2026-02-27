package handler

import (
	"sync/atomic"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// yesNoCounter 是 S_Message_YN 對話框的全域序號（供 trade、party、clan 等共用）。
var yesNoCounter atomic.Int32

// sendYesNoDialog 發送 S_Message_YN (opcode 219)。
// Java 格式：[H 0x0000][D yesNoCount][H messageType][S param...]
func sendYesNoDialog(sess *net.Session, msgType uint16, args ...string) {
	count := yesNoCounter.Add(1)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_YES_NO)
	w.WriteH(0)
	w.WriteD(count)
	w.WriteH(msgType)
	for _, arg := range args {
		w.WriteS(arg)
	}
	sess.Send(w.Bytes())
}

// SendYesNoDialog 匯出 sendYesNoDialog — 供 system 套件發送 Yes/No 確認對話框。
func SendYesNoDialog(sess *net.Session, msgType uint16, args ...string) {
	sendYesNoDialog(sess, msgType, args...)
}

// HandleAskTrade 處理 C_ASK_XCHG (opcode 2) — 發起交易請求。
// 解析封包 → 找面對面目標 → 委派給 TradeSystem。
func HandleAskTrade(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.Dead {
		return
	}

	// 已在交易中
	if player.TradePartnerID != 0 {
		return
	}

	// 尋找面對面的交易對象
	target := findFaceToFace(player, deps)
	if target == nil {
		SendGlobalChat(sess, 9, "找不到交易對象。")
		return
	}

	if deps.Trade != nil {
		deps.Trade.InitiateTrade(sess, player, target)
	}
}

// handleTradeYesNo 處理目標的 Yes/No 交易確認回應。
// 由 HandleAttr (attr.go) 和 NPC 動作呼叫。
func handleTradeYesNo(sess *net.Session, player *world.PlayerInfo, partnerID int32, accepted bool, deps *Deps) {
	if deps.Trade != nil {
		deps.Trade.HandleYesNo(sess, player, partnerID, accepted)
	}
}

// HandleAddTrade 處理 C_ADD_XCHG (opcode 37) — 加入交易物品。
// 格式：[D objectID][D count]
func HandleAddTrade(sess *net.Session, r *packet.Reader, deps *Deps) {
	objectID := r.ReadD()
	count := r.ReadD()

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.Trade != nil {
		deps.Trade.AddItem(sess, player, objectID, count)
	}
}

// HandleAcceptTrade 處理 C_ACCEPT_XCHG (opcode 71) — 確認交易。
func HandleAcceptTrade(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	if deps.Trade != nil {
		deps.Trade.Accept(sess, player)
	}
}

// HandleCancelTrade 處理 C_CANCEL_XCHG (opcode 86) — 取消交易。
func HandleCancelTrade(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || player.TradePartnerID == 0 {
		return
	}

	if deps.Trade != nil {
		deps.Trade.Cancel(player)
	}
}

// CancelTradeIfActive 若玩家正在交易中則取消。Exported for system package usage.
func CancelTradeIfActive(player *world.PlayerInfo, deps *Deps) {
	cancelTradeIfActive(player, deps)
}

// cancelTradeIfActive 若玩家正在交易中則取消。
// 傳送、移動、開商店等各處呼叫此函式。
func cancelTradeIfActive(player *world.PlayerInfo, deps *Deps) {
	if player.TradePartnerID == 0 {
		return
	}
	if deps.Trade != nil {
		deps.Trade.CancelIfActive(player)
	}
}

// findFaceToFace 尋找面對面的玩家（相鄰格、反向朝向）。
// Java: L1World.findFaceToFace。
func findFaceToFace(player *world.PlayerInfo, deps *Deps) *world.PlayerInfo {
	h := player.Heading
	if h < 0 || h > 7 {
		return nil
	}

	targetX := player.X + headingDX[h]
	targetY := player.Y + headingDY[h]
	oppositeH := (h + 4) % 8

	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, player.SessionID)
	for _, other := range nearby {
		if other.X == targetX && other.Y == targetY && other.Heading == oppositeH {
			return other
		}
	}
	return nil
}
