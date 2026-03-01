package handler

import (
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// 角色重置相關常數
const (
	resetItemCandle int32 = 49142 // 回憶蠟燭

	// S_CharReset 格式類型（opcode 64 的 sub-type）
	resetFormatInit   byte = 1 // 初始化：顯示職業基礎屬性
	resetFormatLevel  byte = 2 // 升級回應：顯示當前等級/屬性
	resetFormatElixir byte = 3 // 萬能藥覆寫模式

	// 重置完成後傳送座標（Java: L1Teleport 32628, 32772, map 4）
	resetEndX     int32 = 32628
	resetEndY     int32 = 32772
	resetEndMapID int16 = 4
)

// HandleCharReset 處理 C_CharReset (opcode 98)。
// Java: C_CharReset.java — 三階段角色重置（洗點）狀態機。
//
// Stage 0x01：選擇初始屬性分配（讀 6 個 readC: STR, INT, WIS, DEX, CON, CHA）
// Stage 0x02：逐級升級分配（讀 type2 + 可選屬性）
// Stage 0x03：萬能藥重配（讀 6 個 readC: 目標屬性值）
func HandleCharReset(sess *net.Session, r *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil || !player.InCharReset {
		return
	}

	stage := r.ReadC()

	switch stage {
	case 0x01:
		handleResetStage1(sess, r, player, deps)
	case 0x02:
		handleResetStage2(sess, r, player, deps)
	case 0x03:
		handleResetStage3(sess, r, player, deps)
	}
}

// handleResetStage1 處理初始屬性選擇。
// 客戶端送來 6 個 readC 作為初始點數分配（職業基礎 + 自由分配）。
func handleResetStage1(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, deps *Deps) {
	newStr := int16(r.ReadC())
	newInt := int16(r.ReadC())
	newWis := int16(r.ReadC())
	newDex := int16(r.ReadC())
	newCon := int16(r.ReadC())
	newCha := int16(r.ReadC())

	// 取得職業基礎數據驗證
	classData := deps.Scripting.GetCharCreateData(int(player.ClassType))
	if classData == nil {
		return
	}

	// 驗證：每個屬性不得低於職業基礎值
	if newStr < int16(classData.BaseSTR) || newInt < int16(classData.BaseINT) ||
		newWis < int16(classData.BaseWIS) || newDex < int16(classData.BaseDEX) ||
		newCon < int16(classData.BaseCON) || newCha < int16(classData.BaseCHA) {
		return
	}

	// 驗證：總點數 = 職業基礎總和 + bonus（初始可分配點數）
	baseTotal := classData.BaseSTR + classData.BaseDEX + classData.BaseCON +
		classData.BaseWIS + classData.BaseCHA + classData.BaseINT
	expectedTotal := baseTotal + classData.BonusAmount
	actualTotal := int(newStr + newInt + newWis + newDex + newCon + newCha)
	if actualTotal != expectedTotal {
		return
	}

	// 套用新屬性
	player.Str = newStr
	player.Intel = newInt
	player.Wis = newWis
	player.Dex = newDex
	player.Con = newCon
	player.Cha = newCha

	// 重算 HP/MP（Java: CalcInitHpMp）
	player.MaxHP = int16(deps.Scripting.CalcInitHP(int(player.ClassType), int(player.Con)))
	player.MaxMP = int16(deps.Scripting.CalcInitMP(int(player.ClassType), int(player.Wis)))
	player.HP = player.MaxHP
	player.MP = player.MaxMP

	// 設定 tempLevel = 1
	player.ResetTempLevel = 1
	player.Level = 1

	// 回應 S_CharReset 格式 2（Java: new S_CharReset(pc, 1, hp, mp, ac, str, int, wis, dex, con, cha)）
	sendCharResetLevel(sess, player)
}

// handleResetStage2 處理逐級升級。
// type2: 0x00=升1級(不加屬性), 0x01-0x06=升1級+加屬性, 0x07=升10級, 0x08=完成。
func handleResetStage2(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, deps *Deps) {
	type2 := r.ReadC()

	switch type2 {
	case 0x00: // 升 1 級（不加屬性）
		if !resetLevelUp(player, deps, 1) {
			return
		}
		sendCharResetLevel(sess, player)

	case 0x01: // STR +1 + 升 1 級
		player.Str++
		if !resetLevelUp(player, deps, 1) {
			player.Str--
			return
		}
		sendCharResetLevel(sess, player)

	case 0x02: // INT +1 + 升 1 級
		player.Intel++
		if !resetLevelUp(player, deps, 1) {
			player.Intel--
			return
		}
		sendCharResetLevel(sess, player)

	case 0x03: // WIS +1 + 升 1 級
		player.Wis++
		if !resetLevelUp(player, deps, 1) {
			player.Wis--
			return
		}
		sendCharResetLevel(sess, player)

	case 0x04: // DEX +1 + 升 1 級
		player.Dex++
		if !resetLevelUp(player, deps, 1) {
			player.Dex--
			return
		}
		sendCharResetLevel(sess, player)

	case 0x05: // CON +1 + 升 1 級
		player.Con++
		if !resetLevelUp(player, deps, 1) {
			player.Con--
			return
		}
		sendCharResetLevel(sess, player)

	case 0x06: // CHA +1 + 升 1 級
		player.Cha++
		if !resetLevelUp(player, deps, 1) {
			player.Cha--
			return
		}
		sendCharResetLevel(sess, player)

	case 0x07: // 升 10 級
		remaining := player.ResetMaxLevel - player.ResetTempLevel
		if remaining < 10 {
			return // 剩餘不足 10 級
		}
		if !resetLevelUp(player, deps, 10) {
			return
		}
		sendCharResetLevel(sess, player)

	case 0x08: // 完成
		// 讀取最後一個加點屬性（Java: readC 最後的屬性）
		lastAttr := r.ReadC()
		applyLastAttr(player, lastAttr)

		// 檢查萬能藥點數
		if player.ResetElixirStats > 0 {
			// 發送格式 3：進入萬能藥覆寫模式
			sendCharResetElixir(sess, int(player.ResetElixirStats))
		} else {
			// 直接完成重置
			finishCharReset(sess, player, deps)
		}
	}
}

// handleResetStage3 處理萬能藥屬性覆寫。
func handleResetStage3(sess *net.Session, r *packet.Reader, player *world.PlayerInfo, deps *Deps) {
	newStr := int16(r.ReadC())
	newInt := int16(r.ReadC())
	newWis := int16(r.ReadC())
	newDex := int16(r.ReadC())
	newCon := int16(r.ReadC())
	newCha := int16(r.ReadC())

	// 直接覆寫屬性（Java: addBaseStr(read1 - getBaseStr())）
	player.Str = newStr
	player.Intel = newInt
	player.Wis = newWis
	player.Dex = newDex
	player.Con = newCon
	player.Cha = newCha

	finishCharReset(sess, player, deps)
}

// resetLevelUp 執行 N 次升級的 HP/MP 增加。回傳 false 如果超過上限。
func resetLevelUp(player *world.PlayerInfo, deps *Deps, levels int) bool {
	for i := 0; i < levels; i++ {
		if player.ResetTempLevel >= player.ResetMaxLevel {
			return false
		}
		player.ResetTempLevel++
		player.Level = player.ResetTempLevel

		// 每級增加 HP/MP（Java: CalcStat.calcStatHp/Mp）
		result := deps.Scripting.CalcLevelUp(int(player.ClassType), int(player.Con), int(player.Wis))
		player.MaxHP += int16(result.HP)
		player.MaxMP += int16(result.MP)
	}
	player.HP = player.MaxHP
	player.MP = player.MaxMP
	return true
}

// applyLastAttr 套用最後一個屬性加點。
func applyLastAttr(player *world.PlayerInfo, attr byte) {
	switch attr {
	case 0x01:
		player.Str++
	case 0x02:
		player.Intel++
	case 0x03:
		player.Wis++
	case 0x04:
		player.Dex++
	case 0x05:
		player.Con++
	case 0x06:
		player.Cha++
	}
}

// finishCharReset 完成角色重置：消耗回憶蠟燭、設定 BonusStats、傳送出去。
func finishCharReset(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	player.InCharReset = false

	// 同步等級和經驗值
	player.Level = player.ResetTempLevel
	if deps.Scripting != nil {
		expResult := deps.Scripting.ExpForLevel(int(player.Level))
		player.Exp = int32(expResult)
	}

	// BonusStats = level - 50（若 > 50）
	if player.Level > 50 {
		player.BonusStats = player.Level - 50
	} else {
		player.BonusStats = 0
	}

	// 充滿 HP/MP
	player.HP = player.MaxHP
	player.MP = player.MaxMP

	// 消耗回憶蠟燭
	if candle := player.Inv.FindByItemID(resetItemCandle); candle != nil {
		removed := player.Inv.RemoveItem(candle.ObjectID, 1)
		if removed {
			sendRemoveInventoryItem(sess, candle.ObjectID)
		} else {
			sendItemCountUpdate(sess, candle)
		}
	}

	// 標記需要存檔
	player.Dirty = true

	// 解除凍結
	sendResetFreeze(sess, 4, false)

	// 發送更新封包
	sendPlayerStatus(sess, player)
	sendAbilityScores(sess, player)
	sendMagicStatus(sess, byte(player.SP), uint16(player.MR))

	// 傳送至重置完成點
	if deps.World != nil {
		player.X = resetEndX
		player.Y = resetEndY
		player.MapID = resetEndMapID
		sendMapID(sess, uint16(resetEndMapID), true)
		sendOwnCharPackFromPlayer(sess, player)
	}

	// 重置暫存欄位
	player.ResetTempLevel = 0
	player.ResetMaxLevel = 0
	player.ResetElixirStats = 0
}

// ============================================================
//  S_CharReset 封包建構
// ============================================================

// sendCharResetLevel 發送 S_CharReset 格式 2（升級回應）。
// Java: S_CharReset(pc, lv, hp, mp, ac, str, int, wis, dex, con, cha)
func sendCharResetLevel(sess *net.Session, p *world.PlayerInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARSYNACK)
	w.WriteC(resetFormatLevel) // 格式 2
	w.WriteC(byte(p.ResetTempLevel))
	w.WriteC(byte(p.ResetMaxLevel))
	w.WriteH(uint16(p.MaxHP))
	w.WriteH(uint16(p.MaxMP))
	w.WriteH(uint16(p.AC))
	w.WriteC(byte(p.Str))
	w.WriteC(byte(p.Intel))
	w.WriteC(byte(p.Wis))
	w.WriteC(byte(p.Dex))
	w.WriteC(byte(p.Con))
	w.WriteC(byte(p.Cha))
	sess.Send(w.Bytes())
}

// sendCharResetInit 發送 S_CharReset 格式 1（初始化：進入重置 UI）。
// Java: S_CharReset(pc) — 傳送職業基礎 HP/MP 和目標等級。
func sendCharResetInit(sess *net.Session, initHP, initMP int, maxLevel int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARSYNACK)
	w.WriteC(resetFormatInit) // 格式 1
	w.WriteH(uint16(initHP))
	w.WriteH(uint16(initMP))
	w.WriteC(10) // AC = 10（基礎）
	w.WriteC(byte(maxLevel))
	sess.Send(w.Bytes())
}

// sendCharResetElixir 發送 S_CharReset 格式 3（萬能藥覆寫模式）。
func sendCharResetElixir(sess *net.Session, point int) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_CHARSYNACK)
	w.WriteC(resetFormatElixir) // 格式 3
	w.WriteC(byte(point))
	sess.Send(w.Bytes())
}

// sendResetFreeze 發送 S_Paralysis（角色重置凍結/解凍）。
// Java: S_Paralysis(type=4, freeze) — type 4 帶 enable/disable 標記。
func sendResetFreeze(sess *net.Session, paraType byte, freeze bool) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PARALYSIS)
	w.WriteC(paraType)
	if freeze {
		w.WriteC(1)
	} else {
		w.WriteC(0)
	}
	sess.Send(w.Bytes())
}

// ============================================================
//  NPC 動作觸發（HandleNpcAction 中呼叫）
// ============================================================

// StartCharReset 啟動角色重置流程。由 NPC 動作 "ent" 觸發。
// Java: Npc_BaseReset2 — 檢查回憶蠟燭 → 清 buff → 凍結 → 初始化屬性。
func StartCharReset(sess *net.Session, player *world.PlayerInfo, deps *Deps) {
	// 檢查是否已在重置中
	if player.InCharReset {
		return
	}

	// 檢查回憶蠟燭
	if player.Inv.FindByItemID(resetItemCandle) == nil {
		SendServerMessage(sess, 1290) // "缺少必要道具。"
		return
	}

	classData := deps.Scripting.GetCharCreateData(int(player.ClassType))
	if classData == nil {
		return
	}

	// 計算目標等級（Java: maxLevel 計算）
	initTotal := 75 + int(player.ElixirStats) // 基礎 75 + 萬能藥
	currentTotal := int(player.Str + player.Intel + player.Wis + player.Dex + player.Con + player.Cha)

	// 50+ 升級的已使用屬性點（Java: 若 level > 50, pcStatusPoint += level - 50 - bonusStats）
	if player.Level > 50 {
		currentTotal += int(player.Level - 50 - player.BonusStats)
	}

	diff := currentTotal - initTotal
	var maxLevel int16
	if diff > 0 {
		maxLevel = int16(min(50+diff, 99))
	} else {
		maxLevel = player.Level
	}

	// 設定重置狀態
	player.InCharReset = true
	player.ResetMaxLevel = maxLevel
	player.ResetTempLevel = 1
	player.ResetElixirStats = player.ElixirStats

	// 重置屬性為職業基礎值
	player.Str = int16(classData.BaseSTR)
	player.Intel = int16(classData.BaseINT)
	player.Wis = int16(classData.BaseWIS)
	player.Dex = int16(classData.BaseDEX)
	player.Con = int16(classData.BaseCON)
	player.Cha = int16(classData.BaseCHA)

	// 重算初始 HP/MP
	initHP := deps.Scripting.CalcInitHP(int(player.ClassType), int(player.Con))
	initMP := deps.Scripting.CalcInitMP(int(player.ClassType), int(player.Wis))
	player.MaxHP = int16(initHP)
	player.MaxMP = int16(initMP)
	player.HP = player.MaxHP
	player.MP = player.MaxMP
	player.Level = 1

	// 凍結玩家
	sendResetFreeze(sess, 4, true)

	// 發送狀態更新
	sendPlayerStatus(sess, player)
	sendAbilityScores(sess, player)
	sendMagicStatus(sess, byte(player.SP), uint16(player.MR))

	// 發送 S_CharReset 格式 1（初始化 UI）
	sendCharResetInit(sess, initHP, initMP, maxLevel)
}

// sendOwnCharPackFromPlayer 使用 PlayerInfo 發送自己角色封包（重置傳送用）。
func sendOwnCharPackFromPlayer(sess *net.Session, p *world.PlayerInfo) {
	gfx := PlayerGfx(p)
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(p.X))
	w.WriteH(uint16(p.Y))
	w.WriteD(p.CharID)
	w.WriteH(uint16(gfx))
	w.WriteC(p.CurrentWeapon)
	w.WriteC(byte(p.Heading))
	w.WriteC(0) // light
	w.WriteD(0) // speed
	w.WriteD(0) // exp
	w.WriteH(uint16(p.Lawful))
	w.WriteS(p.Name)
	w.WriteS(p.Title)
	w.WriteC(0)    // status flags
	w.WriteD(0)    // clanId
	w.WriteS("")   // clanName
	w.WriteS("")   // hpBar
	w.WriteC(0xff) // armor type
	w.WriteC(0)    // pledge status
	w.WriteC(0)    // bonus food
	w.WriteH(0)    // end
	sess.Send(w.Bytes())
}
