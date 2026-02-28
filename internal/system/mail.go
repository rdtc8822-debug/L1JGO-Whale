package system

import (
	"context"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// MailSystem 負責信件業務邏輯（讀取/寫入/刪除/搬移、金幣扣除、線上通知）。
// 實作 handler.MailManager 介面。
type MailSystem struct {
	deps *handler.Deps
}

// NewMailSystem 建立信件系統。
func NewMailSystem(deps *handler.Deps) *MailSystem {
	return &MailSystem{deps: deps}
}

// OpenMailbox 從 DB 載入信件並發送列表封包。
func (s *MailSystem) OpenMailbox(sess *net.Session, player *world.PlayerInfo, mailType int16) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mails, err := s.deps.MailRepo.LoadByInbox(ctx, player.CharID, mailType)
	if err != nil {
		s.deps.Log.Error("讀取信箱失敗", zap.Error(err))
		return
	}

	handler.SendMailList(sess, player, mails, mailType)
}

// ReadMail 讀取信件內容並標記已讀。
func (s *MailSystem) ReadMail(sess *net.Session, player *world.PlayerInfo, mailID int32, mailType int16) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mail, err := s.deps.MailRepo.GetByID(ctx, mailID)
	if err != nil {
		s.deps.Log.Error("讀取信件失敗", zap.Error(err))
		return
	}
	if mail == nil {
		return
	}

	// 只有信箱擁有者可以讀取
	if mail.InboxID != player.CharID {
		return
	}

	// 標記已讀
	if mail.ReadStatus == 0 {
		if err := s.deps.MailRepo.SetReadStatus(ctx, mailID); err != nil {
			s.deps.Log.Error("標記信件已讀失敗", zap.Error(err))
		}
	}

	// 發送內容
	readType := byte(0x10) + byte(mailType)
	handler.SendMailContent(sess, mailID, readType, mail.Content)
}

// SendMail 寄出一封一般信件。
func (s *MailSystem) SendMail(sess *net.Session, player *world.PlayerInfo, receiverName string, rawText []byte) {
	// 解析主旨與內文
	subject, content := parseMailText(rawText)

	// 扣除寄信費用
	if !consumeAdena(player, int32(s.deps.Config.Gameplay.MailSendCost)) {
		handler.SendServerMessage(sess, 189) // "金幣不足。"
		return
	}
	sendAdenaUpdate(sess, player)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 查詢收件人 — 先找線上，再查 DB
	receiver := s.deps.World.GetByName(receiverName)
	var receiverCharID int32

	if receiver != nil {
		receiverCharID = receiver.CharID
	} else {
		// 離線：從 DB 查詢
		charRow, err := s.deps.CharRepo.LoadByName(ctx, receiverName)
		if err != nil {
			s.deps.Log.Error("查詢收件人失敗", zap.Error(err))
			handler.SendMailResult(sess, 0x20, false)
			return
		}
		if charRow == nil {
			handler.SendServerMessage(sess, 109) // "沒有這個人。"
			return
		}
		receiverCharID = charRow.ID
	}

	// 檢查收件箱上限
	count, err := s.deps.MailRepo.CountByInbox(ctx, receiverCharID, handler.MailTypeNormal)
	if err != nil {
		s.deps.Log.Error("查詢收件箱數量失敗", zap.Error(err))
		handler.SendMailResult(sess, 0x20, false)
		return
	}
	if count >= s.deps.Config.Gameplay.MailMaxPerBox {
		handler.SendMailResult(sess, 0x20, false)
		return
	}

	now := time.Now()

	// 寫入寄件備份
	senderMail := &persist.MailRow{
		Type:       handler.MailTypeNormal,
		Sender:     player.Name,
		Receiver:   receiverName,
		Date:       now,
		ReadStatus: 0,
		InboxID:    player.CharID,
		Subject:    subject,
		Content:    content,
	}
	senderMailID, err := s.deps.MailRepo.Write(ctx, senderMail)
	if err != nil {
		s.deps.Log.Error("寫入寄件備份失敗", zap.Error(err))
		handler.SendMailResult(sess, 0x20, false)
		return
	}

	// 寫入收件信
	receiverMail := &persist.MailRow{
		Type:       handler.MailTypeNormal,
		Sender:     player.Name,
		Receiver:   receiverName,
		Date:       now,
		ReadStatus: 0,
		InboxID:    receiverCharID,
		Subject:    subject,
		Content:    content,
	}
	receiverMailID, err := s.deps.MailRepo.Write(ctx, receiverMail)
	if err != nil {
		s.deps.Log.Error("寫入收件信失敗", zap.Error(err))
		handler.SendMailResult(sess, 0x20, false)
		return
	}

	// 通知寄件者（備份）
	handler.SendMailNotify(sess, player.Name, senderMailID, true, subject)

	// 通知收件者（若線上）
	if receiver != nil {
		handler.SendMailNotify(receiver.Session, player.Name, receiverMailID, false, subject)
		// 音效通知（skill sound 1091）
		handler.SendMailSound(receiver.Session, receiver.CharID)
	}

	s.deps.Log.Info(fmt.Sprintf("信件寄出  寄件=%s  收件=%s  senderID=%d  receiverID=%d",
		player.Name, receiverName, senderMailID, receiverMailID))

	handler.SendMailResult(sess, 0x20, true)
}

// DeleteMail 刪除單封信件。
func (s *MailSystem) DeleteMail(sess *net.Session, player *world.PlayerInfo, mailID int32, subtype byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 驗證所有權
	mail, err := s.deps.MailRepo.GetByID(ctx, mailID)
	if err != nil {
		s.deps.Log.Error("查詢信件失敗", zap.Error(err))
		return
	}
	if mail == nil || mail.InboxID != player.CharID {
		return
	}

	if err := s.deps.MailRepo.Delete(ctx, mailID); err != nil {
		s.deps.Log.Error("刪除信件失敗", zap.Error(err))
		return
	}

	handler.SendMailAck(sess, mailID, subtype)
}

// MoveToStorage 搬移信件至保管箱。
func (s *MailSystem) MoveToStorage(sess *net.Session, player *world.PlayerInfo, mailID int32, subtype byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 驗證所有權
	mail, err := s.deps.MailRepo.GetByID(ctx, mailID)
	if err != nil {
		s.deps.Log.Error("查詢信件失敗", zap.Error(err))
		return
	}
	if mail == nil || mail.InboxID != player.CharID {
		return
	}

	// 檢查保管箱上限
	count, err := s.deps.MailRepo.CountByInbox(ctx, player.CharID, handler.MailTypeStorage)
	if err != nil {
		s.deps.Log.Error("查詢保管箱數量失敗", zap.Error(err))
		return
	}
	if count >= s.deps.Config.Gameplay.MailMaxPerBox {
		return
	}

	if err := s.deps.MailRepo.SetType(ctx, mailID, handler.MailTypeStorage); err != nil {
		s.deps.Log.Error("移動信件至保管箱失敗", zap.Error(err))
		return
	}

	handler.SendMailAck(sess, mailID, 0x40)
}

// BulkDelete 批次刪除信件。
func (s *MailSystem) BulkDelete(sess *net.Session, player *world.PlayerInfo, subtype byte, mailIDs []int32) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// subtype 對應刪除確認類型：0x60→0x30, 0x61→0x31, 0x62→0x32
	deleteAckType := subtype - 0x30

	for _, mailID := range mailIDs {
		// 驗證所有權
		mail, err := s.deps.MailRepo.GetByID(ctx, mailID)
		if err != nil {
			s.deps.Log.Error("批次刪除查詢失敗", zap.Error(err))
			continue
		}
		if mail == nil || mail.InboxID != player.CharID {
			continue
		}

		if err := s.deps.MailRepo.Delete(ctx, mailID); err != nil {
			s.deps.Log.Error("批次刪除信件失敗", zap.Error(err))
			continue
		}

		handler.SendMailAck(sess, mailID, deleteAckType)
	}
}

// --- 輔助函式 ---

// consumeAdena 扣除玩家金幣。成功回傳 true，不足回傳 false。
func consumeAdena(player *world.PlayerInfo, amount int32) bool {
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena == nil || adena.Count < amount {
		return false
	}
	adena.Count -= amount
	return true
}

// sendAdenaUpdate 發送金幣數量更新封包。
func sendAdenaUpdate(sess *net.Session, player *world.PlayerInfo) {
	adena := player.Inv.FindByItemID(world.AdenaItemID)
	if adena != nil {
		handler.SendItemCountUpdate(sess, adena)
	}
}

// parseMailText 拆分原始信件 bytes 為主旨和內文。
// 客戶端格式: [subject bytes][0x00 0x00][content bytes][0x00 0x00]
// 回傳主旨（含尾部 0x0000）和內文 bytes。
func parseMailText(raw []byte) (subject, content []byte) {
	sp1 := -1
	sp2 := -1

	// 以 2-byte 邊界掃描雙 null 分隔符
	for i := 0; i+1 < len(raw); i += 2 {
		if raw[i] == 0 && raw[i+1] == 0 {
			if sp1 < 0 {
				sp1 = i
			} else if sp2 < 0 {
				sp2 = i
				break
			}
		}
	}

	if sp1 < 0 {
		// 找不到分隔符：整段視為主旨
		return raw, nil
	}

	// 主旨包含分隔符（sp1 + 2 bytes）
	subject = raw[:sp1+2]

	if sp2 > sp1 {
		content = raw[sp1+2 : sp2]
	} else if sp1+2 < len(raw) {
		content = raw[sp1+2:]
	} else {
		content = []byte{0}
	}

	return subject, content
}
