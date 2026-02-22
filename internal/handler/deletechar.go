package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

// HandleDeleteChar processes C_DeleteChar (opcode 162).
func HandleDeleteChar(sess *net.Session, r *packet.Reader, deps *Deps) {
	charName := r.ReadS()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify the character belongs to this account
	ch, err := deps.CharRepo.LoadByName(ctx, charName)
	if err != nil || ch == nil {
		deps.Log.Warn("刪除角色: 找不到", zap.String("name", charName))
		return
	}
	if ch.AccountName != sess.AccountName {
		deps.Log.Warn("刪除角色: 帳號不符",
			zap.String("char", charName),
			zap.String("account", sess.AccountName),
		)
		return
	}

	cfg := deps.Config.Character

	// Determine immediate vs 7-day delayed deletion
	if cfg.Delete7Days && ch.Level >= int16(cfg.Delete7DaysMinLevel) {
		// Soft delete (7 day delay)
		if err := deps.CharRepo.SoftDelete(ctx, charName); err != nil {
			deps.Log.Error("角色軟刪除失敗", zap.Error(err))
			return
		}
		sendDeleteCharResult(sess, 0x51) // delayed
		deps.Log.Info(fmt.Sprintf("角色已軟刪除 (7天)  角色=%s  等級=%d", charName, ch.Level))
	} else {
		// Hard delete (immediate)
		if err := deps.CharRepo.HardDelete(ctx, charName); err != nil {
			deps.Log.Error("角色硬刪除失敗", zap.Error(err))
			return
		}
		sendDeleteCharResult(sess, 0x05) // immediate
		deps.Log.Info(fmt.Sprintf("角色已立即刪除  角色=%s", charName))
	}
}

func sendDeleteCharResult(sess *net.Session, result byte) {
	// S_DeleteCharOK (opcode 6)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_DELETE_CHAR_OK)
	w.WriteC(result) // 0x05=immediate, 0x51=7-day
	sess.Send(w.Bytes())
}
