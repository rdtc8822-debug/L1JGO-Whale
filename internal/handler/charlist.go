package handler

import (
	"context"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"go.uber.org/zap"
)

// sendCharacterList sends S_CharAmount + S_CharPacks.
// Java: L1CharList / C_CommonClick — 只發送 S_CharAmount + S_CharPacks，
// 不發送 S_CharSynAck（opcode 64 SYN/ACK）。
func sendCharacterList(sess *net.Session, deps *Deps) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Clean expired deletions first
	if _, err := deps.CharRepo.CleanExpiredDeletions(ctx, sess.AccountName); err != nil {
		deps.Log.Error("清理過期刪除記錄", zap.Error(err))
	}

	// Load characters
	chars, err := deps.CharRepo.LoadByAccount(ctx, sess.AccountName)
	if err != nil {
		deps.Log.Error("載入角色列表", zap.Error(err))
		return
	}

	// Load account for slot info
	account, err := deps.AccountRepo.Load(ctx, sess.AccountName)
	if err != nil || account == nil {
		deps.Log.Error("載入帳號(角色列表)", zap.Error(err))
		return
	}

	maxSlots := deps.Config.Character.DefaultSlots + int(account.CharacterSlot)

	// S_CharAmount (opcode 178)
	sendCharAmount(sess, len(chars), maxSlots)

	// S_CharPacks for each character (opcode 93)
	for i := range chars {
		sendCharPack(sess, &chars[i])
	}
}

func sendCharAmount(sess *net.Session, count, maxSlots int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_NUM_CHARACTER)
	w.WriteC(byte(count))
	w.WriteC(byte(maxSlots))
	sess.Send(w.Bytes())
}

func sendCharPack(sess *net.Session, c *persist.CharacterRow) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARACTER_INFO)
	w.WriteS(c.Name)
	w.WriteS(c.ClanName)
	w.WriteC(byte(c.ClassType))
	w.WriteC(byte(c.Sex))
	w.WriteH(uint16(c.Lawful))
	w.WriteH(uint16(c.MaxHP))
	w.WriteH(uint16(c.MaxMP))
	w.WriteC(byte(c.AC))
	w.WriteC(byte(c.Level))
	w.WriteC(byte(c.Str))
	w.WriteC(byte(c.Dex))
	w.WriteC(byte(c.Con))
	w.WriteC(byte(c.Wis))
	w.WriteC(byte(c.Cha))
	w.WriteC(byte(c.Intel))
	w.WriteC(0x00) // admin flag
	w.WriteD(c.Birthday)
	// XOR checksum
	xor := byte(c.Level) ^ byte(c.Str) ^ byte(c.Dex) ^ byte(c.Con) ^ byte(c.Wis) ^ byte(c.Cha) ^ byte(c.Intel)
	w.WriteC(xor)
	sess.Send(w.Bytes())
}
