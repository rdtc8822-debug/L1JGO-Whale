package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"go.uber.org/zap"
)

const (
	charCreateOK          byte = 0x02
	charCreateNameExists  byte = 0x06
	charCreateInvalidName byte = 0x09
	charCreateWrongAmount byte = 0x15
)

const (
	startX     int32 = 32689
	startY     int32 = 32842
	startMapID int16 = 2005
)

// HandleCreateChar processes C_CreateChar (opcode 84).
// Class stat tables and initial HP/MP formulas are in Lua (scripts/character/creation.lua).
func HandleCreateChar(sess *net.Session, r *packet.Reader, deps *Deps) {
	name := r.ReadS()
	classType := int16(r.ReadC())
	sex := int16(r.ReadC())
	str := int16(r.ReadC())
	dex := int16(r.ReadC())
	con := int16(r.ReadC())
	wis := int16(r.ReadC())
	cha := int16(r.ReadC())
	intel := int16(r.ReadC())

	// Validate name
	if len(name) == 0 {
		sendCharCreateStatus(sess, charCreateInvalidName)
		return
	}

	// Validate class type
	if classType < 0 || classType > 6 {
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}

	// Validate sex
	if sex != 0 && sex != 1 {
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}

	// Get class data from Lua
	classData := deps.Scripting.GetCharCreateData(int(classType))
	if classData == nil {
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}

	// Validate stats: each must be >= base and <= base + bonus
	bonus := int16(classData.BonusAmount)
	baseStr := int16(classData.BaseSTR)
	baseDex := int16(classData.BaseDEX)
	baseCon := int16(classData.BaseCON)
	baseWis := int16(classData.BaseWIS)
	baseCha := int16(classData.BaseCHA)
	baseInt := int16(classData.BaseINT)

	if str < baseStr || str > baseStr+bonus ||
		dex < baseDex || dex > baseDex+bonus ||
		con < baseCon || con > baseCon+bonus ||
		wis < baseWis || wis > baseWis+bonus ||
		cha < baseCha || cha > baseCha+bonus ||
		intel < baseInt || intel > baseInt+bonus {
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}

	// Validate total stats == 75
	if str+dex+con+wis+cha+intel != 75 {
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check name uniqueness
	exists, err := deps.CharRepo.NameExists(ctx, name)
	if err != nil {
		deps.Log.Error("檢查角色名稱", zap.Error(err))
		sendCharCreateStatus(sess, charCreateInvalidName)
		return
	}
	if exists {
		sendCharCreateStatus(sess, charCreateNameExists)
		return
	}

	// Check slot limit
	account, err := deps.AccountRepo.Load(ctx, sess.AccountName)
	if err != nil || account == nil {
		deps.Log.Error("載入帳號(建立角色)", zap.Error(err))
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}
	count, err := deps.CharRepo.CountByAccount(ctx, sess.AccountName)
	if err != nil {
		deps.Log.Error("計算角色數量", zap.Error(err))
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}
	maxSlots := deps.Config.Character.DefaultSlots + int(account.CharacterSlot)
	if count >= maxSlots {
		sendCharCreateStatus(sess, charCreateWrongAmount)
		return
	}

	// Assign GFX from Lua data
	var classID int32
	if sex == 0 {
		classID = int32(classData.MaleGFX)
	} else {
		classID = int32(classData.FemaleGFX)
	}

	// Calculate init HP/MP via Lua
	initHP := int16(deps.Scripting.CalcInitHP(int(classType), int(con)))
	initMP := int16(deps.Scripting.CalcInitMP(int(classType), int(wis)))

	// Birthday as yyyyMMdd integer
	now := time.Now()
	birthday := int32(now.Year()*10000 + int(now.Month())*100 + now.Day())

	// Build row
	row := &persist.CharacterRow{
		AccountName: sess.AccountName,
		Name:        name,
		ClassType:   classType,
		Sex:         sex,
		ClassID:     classID,
		Str:         str,
		Dex:         dex,
		Con:         con,
		Wis:         wis,
		Cha:         cha,
		Intel:       intel,
		Level:       1,
		HP:          initHP,
		MP:          initMP,
		MaxHP:       initHP,
		MaxMP:       initMP,
		AC:          10,
		X:           startX,
		Y:           startY,
		MapID:       startMapID,
		Food:        40,
		Birthday:    birthday,
	}

	if err := deps.CharRepo.Create(ctx, row); err != nil {
		deps.Log.Error("建立角色", zap.Error(err))
		sendCharCreateStatus(sess, charCreateNameExists)
		return
	}

	// Grant initial spells from Lua data
	if len(classData.InitialSpells) > 0 {
		spells := make([]int32, len(classData.InitialSpells))
		for i, s := range classData.InitialSpells {
			spells[i] = int32(s)
		}
		if err := deps.CharRepo.SaveKnownSpells(ctx, name, spells); err != nil {
			deps.Log.Error("儲存初始魔法", zap.Error(err))
		}
	}

	deps.Log.Info(fmt.Sprintf("角色建立成功  帳號=%s  角色=%s  職業=%d", sess.AccountName, name, classType))

	// Send success status
	sendCharCreateStatus(sess, charCreateOK)

	// Send S_NewCharPacket (opcode 127) — same structure as S_CharPacks
	sendNewCharPack(sess, row)
}

func sendCharCreateStatus(sess *net.Session, reason byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CREATE_CHARACTER_CHECK)
	w.WriteC(reason)
	w.WriteD(0) // padding
	w.WriteH(0) // padding
	sess.Send(w.Bytes())
}

func sendNewCharPack(sess *net.Session, c *persist.CharacterRow) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_NEW_CHAR_INFO)
	w.WriteS(c.Name)
	w.WriteS("") // empty clan
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
	xor := byte(c.Level) ^ byte(c.Str) ^ byte(c.Dex) ^ byte(c.Con) ^ byte(c.Wis) ^ byte(c.Cha) ^ byte(c.Intel)
	w.WriteC(xor)
	sess.Send(w.Bytes())
}
