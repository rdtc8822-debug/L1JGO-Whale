package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
)

const (
	MailTypeNormal  int16 = 0
	MailTypeClan    int16 = 1
	MailTypeStorage int16 = 2
)

// HandleMail processes C_MAIL (opcode 87).
// 薄層：解析封包子類型 → 委派 deps.Mail。
func HandleMail(sess *net.Session, r *packet.Reader, deps *Deps) {
	if deps.Mail == nil {
		return
	}

	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}

	mailType := r.ReadC()

	switch mailType {
	case 0x00, 0x02: // 開啟信箱: 0=一般, 2=保管箱
		deps.Mail.OpenMailbox(sess, player, int16(mailType))

	case 0x01: // 血盟信件（靜默忽略，客戶端登入時會自動請求）
		return

	case 0x10, 0x12: // 讀取信件: 0x10=一般, 0x12=保管箱
		mailID := r.ReadD()
		deps.Mail.ReadMail(sess, player, mailID, int16(mailType-0x10))

	case 0x11: // 血盟信件讀取 — 尚未實作
		SendSystemMessage(sess, "血盟信件目前尚未開放使用。")

	case 0x20: // 寄出一般信件
		_ = r.ReadH() // worldMailCount（未使用）
		receiverName := r.ReadS()
		rawText := r.ReadBytes(r.Remaining())
		deps.Mail.SendMail(sess, player, receiverName, rawText)

	case 0x21: // 寄出血盟信件 — 尚未實作
		SendSystemMessage(sess, "血盟信件目前尚未開放使用。")

	case 0x30, 0x31, 0x32: // 刪除信件
		mailID := r.ReadD()
		deps.Mail.DeleteMail(sess, player, mailID, mailType)

	case 0x40, 0x41: // 搬移至保管箱
		mailID := r.ReadD()
		deps.Mail.MoveToStorage(sess, player, mailID, mailType)

	case 0x60, 0x61, 0x62: // 批次刪除
		count := r.ReadD()
		if count <= 0 || count > 100 {
			return
		}
		ids := make([]int32, 0, count)
		for i := int32(0); i < count; i++ {
			ids = append(ids, r.ReadD())
		}
		deps.Mail.BulkDelete(sess, player, mailType, ids)
	}
}

// ========================================================================
//  封包建構器（供 system/mail.go 呼叫）
// ========================================================================

// SendMailList sends S_Mail (opcode 186) — 信箱列表。
// Format: [C type][H count]{[D mailID][C readStatus][D dateSec][C isSender][S otherName][rawSubject]} × count
func SendMailList(sess *net.Session, player *world.PlayerInfo, mails []persist.MailRow, mailType int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MAIL)
	w.WriteC(byte(mailType))
	w.WriteH(uint16(len(mails)))
	for _, m := range mails {
		w.WriteD(m.ID)
		w.WriteC(byte(m.ReadStatus))
		w.WriteD(int32(m.Date.Unix()))
		// isSender: 1 = 此玩家是寄件者, 0 = 收件者
		if m.Sender == player.Name {
			w.WriteC(1)
			w.WriteS(m.Receiver)
		} else {
			w.WriteC(0)
			w.WriteS(m.Sender)
		}
		// 主旨以原始 bytes（Big5，已含 0x0000 終止符）
		w.WriteBytes(m.Subject)
	}
	sess.Send(w.Bytes())
}

// SendMailContent sends S_Mail (opcode 186) — 讀取信件內容。
// Format: [C type][D mailID][rawContent]
func SendMailContent(sess *net.Session, mailID int32, readType byte, content []byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MAIL)
	w.WriteC(readType)
	w.WriteD(mailID)
	if content != nil {
		w.WriteBytes(content)
	}
	sess.Send(w.Bytes())
}

// SendMailResult sends S_Mail (opcode 186) — 寄信結果。
// Format: [C type][C success]
func SendMailResult(sess *net.Session, mailType byte, success bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MAIL)
	w.WriteC(mailType)
	if success {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	sess.Send(w.Bytes())
}

// SendMailAck sends S_Mail (opcode 186) — 刪除/搬移確認。
// Format: [C type][D mailID][C 1]
func SendMailAck(sess *net.Session, mailID int32, ackType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MAIL)
	w.WriteC(ackType)
	w.WriteD(mailID)
	w.WriteC(1)
	sess.Send(w.Bytes())
}

// SendMailNotify sends S_Mail (opcode 186) subtype 0x50 — 新信通知。
// Format: [C 0x50][D mailID][C isDraft][S senderName][rawSubject]
func SendMailNotify(sess *net.Session, senderName string, mailID int32, isDraft bool, subject []byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MAIL)
	w.WriteC(0x50)
	w.WriteD(mailID)
	if isDraft {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	w.WriteS(senderName)
	if subject != nil {
		w.WriteBytes(subject)
	}
	sess.Send(w.Bytes())
}

// SendMailSound sends a skill sound effect (opcode 22) — 新信音效。
// Java uses skill sound ID 1091.
func SendMailSound(sess *net.Session, objID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_SOUND_EFFECT)
	w.WriteD(objID)
	w.WriteH(1091)
	sess.Send(w.Bytes())
}

// SendSystemMessage sends a plain text system message via S_GlobalChat (opcode 243).
// 用於「尚未開放」等提示訊息。
func SendSystemMessage(sess *net.Session, text string) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MESSAGE)
	w.WriteC(9) // system type
	w.WriteS(text)
	sess.Send(w.Bytes())
}
