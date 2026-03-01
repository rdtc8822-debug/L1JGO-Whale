package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"go.uber.org/zap"
)

const (
	loginActionLogin  = 0x06
	loginActionChange = 0x0b
	loginActionLogout = 0x1c
)

const (
	loginOK              byte = 0x00
	loginAlreadyExists   byte = 0x07
	loginWrongPass       byte = 0x08 // REASON_ACCESS_FAILED — 一般登入失敗
	loginAccountInUse    byte = 0x16
	loginAutoNoAccount   byte = 155  // EVENT_ERROR_USER — BeanFun 自動登入帳號不存在
	loginAutoWrongPass   byte = 149  // EVENT_ERROR_PASS — BeanFun 自動登入密碼錯誤
)

// HandleAuthBeanFun processes C_BeanFunLogin (opcode 210).
// Format: [opcode][action byte][account\0][password\0]
func HandleAuthBeanFun(sess *net.Session, r *packet.Reader, deps *Deps) {
	action := r.ReadC()

	switch action {
	case loginActionLogin:
		handleLogin(sess, r, deps, true)
	case loginActionChange:
		if sess.AccountName != "" {
			sendCharacterList(sess, deps)
		}
	case loginActionLogout:
		sess.Close()
	}
}

// HandleAuthDirect processes C_LoginPacket (opcode 119).
// Format: [opcode][account\0][password\0] — no action byte.
func HandleAuthDirect(sess *net.Session, r *packet.Reader, deps *Deps) {
	handleLogin(sess, r, deps, false)
}

func handleLogin(sess *net.Session, r *packet.Reader, deps *Deps, auto bool) {
	accountName := strings.ToLower(r.ReadS())
	password := r.ReadS()
	ip := sess.IP

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 根據來源選擇錯誤碼（Java: auto=true → 155/149, auto=false → 8）
	noAccountCode := loginWrongPass
	wrongPassCode := loginWrongPass
	if auto {
		noAccountCode = loginAutoNoAccount
		wrongPassCode = loginAutoWrongPass
	}

	// Load account
	account, err := deps.AccountRepo.Load(ctx, accountName)
	if err != nil {
		deps.Log.Error("載入帳號資料庫錯誤", zap.Error(err))
		sendLoginResult(sess, wrongPassCode)
		return
	}

	// Auto-create if enabled
	if account == nil {
		if deps.Config.Character.AutoCreateAccounts {
			account, err = deps.AccountRepo.Create(ctx, accountName, password, ip, ip)
			if err != nil {
				deps.Log.Error("建立帳號資料庫錯誤", zap.Error(err))
				sendLoginResult(sess, wrongPassCode)
				return
			}
			deps.Log.Info(fmt.Sprintf("自動建立帳號  帳號=%s", accountName))
		} else {
			sendLoginResult(sess, noAccountCode)
			return
		}
	} else {
		// Validate password
		if !deps.AccountRepo.ValidatePassword(account.PasswordHash, password) {
			sendLoginResult(sess, wrongPassCode)
			return
		}
	}

	// Check banned
	if account.Banned {
		deps.Log.Info(fmt.Sprintf("被封鎖帳號嘗試登入  帳號=%s", accountName))
		sendLoginResult(sess, loginWrongPass)
		return
	}

	// Check already online
	if account.Online {
		sendLoginResult(sess, loginAlreadyExists)
		return
	}

	// Success — mark online
	if err := deps.AccountRepo.SetOnline(ctx, accountName, true); err != nil {
		deps.Log.Error("設定上線狀態資料庫錯誤", zap.Error(err))
	}
	if err := deps.AccountRepo.UpdateLastActive(ctx, accountName, ip); err != nil {
		deps.Log.Error("更新最後活動時間資料庫錯誤", zap.Error(err))
	}

	sess.AccountName = accountName
	sendLoginResult(sess, loginOK)

	// Transition to Authenticated
	sess.SetState(packet.StateAuthenticated)

	// Send character list
	sendCharacterList(sess, deps)

	deps.Log.Info(fmt.Sprintf("登入成功  帳號=%s  ip=%s", accountName, ip))
}

// sendLoginResult 發送 S_LoginResult。
// Java 格式: [C opcode][H reason][D 0][D 0][D 0]
func sendLoginResult(sess *net.Session, reason byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_LOGIN_CHECK)
	w.WriteH(uint16(reason)) // Java: writeH(reason) — 2 bytes
	w.WriteD(0)              // padding
	w.WriteD(0)
	w.WriteD(0)
	sess.Send(w.Bytes())
}
