package system

import (
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

const (
	immuneToHarmSkillID   int32 = 68
	advanceSpiritSkillID  int32 = 79
	reductionArmorSkillID int32 = 88
)

func applyImmuneToHarmDamage(target *world.PlayerInfo, damage int32) int32 {
	if target == nil || damage <= 0 || !target.HasBuff(immuneToHarmSkillID) {
		return damage
	}
	return damage / 2
}

// applyReductionArmorDamage 套用 REDUCTION_ARMOR(88, 增幅防禦) flat 傷害減免。
// Java `L1AttackPc.java:1617-1620` (PvP physical) 公式為 `dmg -= (max(targetLvl,50)-50)/5 + 10`，
// 其他三條路徑（L1AttackNpc NPC→PC physical、L1MagicPc/L1MagicNpc magic）公式為
// `dmg -= (max(targetLvl,50)-50)/5 + 1`。pvpPhysical=true 套用 +10，否則 +1。
func applyReductionArmorDamage(target *world.PlayerInfo, damage int32, pvpPhysical bool) int32 {
	if target == nil || damage <= 0 {
		return damage
	}
	reduction := int32(target.EquipBonuses.DmgReduction)
	if target.HasBuff(reductionArmorSkillID) {
		lvl := int32(target.Level)
		if lvl < 50 {
			lvl = 50
		}
		reduction += (lvl-50)/5 + 1
		if pvpPhysical {
			reduction += 9
		}
	}
	damage -= reduction
	if damage < 0 {
		damage = 0
	}
	return applyDollRandomDamageReduction(target, damage)
}

func (s *SkillSystem) validateSolidCarriage(player *world.PlayerInfo) bool {
	if player == nil {
		return false
	}
	return player.Equip.Get(world.SlotShield) != nil || player.Equip.Get(world.SlotGuarder) != nil
}

func (s *SkillSystem) calcAdvanceSpiritDeltas(target *world.PlayerInfo) (int32, int32) {
	if target == nil {
		return 0, 0
	}
	baseMaxHP := target.MaxHP - int32(target.EquipBonuses.AddHP)
	baseMaxMP := target.MaxMP - int32(target.EquipBonuses.AddMP)
	for _, buff := range target.ActiveBuffs {
		baseMaxHP -= buff.DeltaMaxHP
		baseMaxMP -= buff.DeltaMaxMP
	}
	if s != nil && s.deps != nil && s.deps.World != nil {
		for _, doll := range s.deps.World.GetDollsByOwner(target.CharID) {
			baseMaxHP -= int32(doll.BonusHP)
			baseMaxMP -= int32(doll.BonusMP)
		}
	}
	if baseMaxHP < 0 {
		baseMaxHP = 0
	}
	if baseMaxMP < 0 {
		baseMaxMP = 0
	}
	return baseMaxHP / 5, baseMaxMP / 5
}

// ========================================================================
//  Buff 管理
// ========================================================================

// sendBuffIcon 發送適當的 buff 圖示封包。
func (s *SkillSystem) sendBuffIcon(target *world.PlayerInfo, skillID int32, durationSec uint16) {
	if s.deps.BuffIcons == nil {
		return
	}
	icon := s.deps.BuffIcons.Get(skillID)
	if icon == nil {
		return
	}
	sess := target.Session
	switch icon.Type {
	case "shield":
		handler.SendIconShield(sess, durationSec, icon.Param)
	case "strup":
		// yiwei `L1SkillUse.java:2456` 對 DRESS_MIGHTY(109) cast 改為 type=2（`原本3修改2 琮善`），
		// `L1SkillStop.java:441` stop 仍 type=3。Cast 走 yaml param=2，cancel 覆寫為 3。
		iconParam := icon.Param
		if durationSec == 0 && skillID == 109 {
			iconParam = 3
		}
		handler.SendIconStrup(sess, durationSec, byte(target.Str), iconParam)
	case "dexup":
		// yiwei `L1SkillUse.java:2449` 對 DRESS_DEXTERITY(110) cast 改為 type=2，
		// `L1SkillStop.java:433` stop 仍 type=3。Cast 走 yaml param=2，cancel 覆寫為 3。
		iconParam := icon.Param
		if durationSec == 0 && skillID == 110 {
			iconParam = 3
		}
		handler.SendIconDexup(sess, durationSec, byte(target.Dex), iconParam)
	case "aura":
		handler.SendIconAura(sess, byte(skillID-1), durationSec)
	case "gfx":
		handler.SendIconGfx(sess, icon.Param, durationSec)
	case "invis":
		handler.SendInvisible(sess, target.CharID, durationSec > 0)
	case "wisdom":
		handler.SendWisdomPotionIcon(sess, durationSec)
	case "blue_potion":
		handler.SendBluePotionIcon(sess, durationSec)
	}
}

// cancelBuffIcon 取消 buff 圖示。
func (s *SkillSystem) cancelBuffIcon(target *world.PlayerInfo, skillID int32) {
	s.sendBuffIcon(target, skillID, 0)
}

// applyBuffEffect 套用屬性變化並註冊 buff 計時器。
func (s *SkillSystem) applyBuffEffect(target *world.PlayerInfo, skill *data.SkillInfo) {
	if skill.BuffDuration <= 0 {
		return
	}

	buff := &world.ActiveBuff{
		SkillID:   skill.SkillID,
		TicksLeft: skill.BuffDuration * 5,
	}

	eff := s.deps.Scripting.GetBuffEffect(int(skill.SkillID), int(target.Level))

	if eff != nil {
		// 移除衝突 buff
		for _, exID := range eff.Exclusions {
			s.removeBuffAndRevert(target, int32(exID))
		}

		// 設定屬性差值
		buff.DeltaAC = int16(eff.AC)
		buff.DeltaStr = int16(eff.Str)
		buff.DeltaDex = int16(eff.Dex)
		buff.DeltaCon = int16(eff.Con)
		buff.DeltaWis = int16(eff.Wis)
		buff.DeltaIntel = int16(eff.Intel)
		buff.DeltaCha = int16(eff.Cha)
		buff.DeltaMaxHP = int32(eff.MaxHP)
		buff.DeltaMaxMP = int32(eff.MaxMP)
		if skill.SkillID == advanceSpiritSkillID {
			buff.DeltaMaxHP, buff.DeltaMaxMP = s.calcAdvanceSpiritDeltas(target)
		}
		buff.DeltaHitMod = int16(eff.HitMod)
		buff.DeltaDmgMod = int16(eff.DmgMod)
		buff.DeltaSP = int16(eff.SP)
		buff.DeltaMR = int16(eff.MR)
		buff.DeltaHPR = int16(eff.HPR)
		buff.DeltaMPR = int16(eff.MPR)
		buff.DeltaBowHit = int16(eff.BowHit)
		buff.DeltaBowDmg = int16(eff.BowDmg)
		buff.DeltaWeightPct = int16(eff.WeightPct)
		buff.DeltaDodge = int16(eff.Dodge)
		buff.DeltaRegistSustain = int16(eff.RegistSustain)
		buff.DeltaRegistFreeze = int16(eff.RegistFreeze)
		buff.DeltaRegistStun = int16(eff.RegistStun)
		buff.DeltaRegistStone = int16(eff.RegistStone)
		buff.DeltaRegistBlind = int16(eff.RegistBlind)
		buff.DeltaRegistSleep = int16(eff.RegistSleep)
		buff.DeltaMagicCritical = int16(eff.MagicCritical)
		buff.DeltaFireRes = int16(eff.FireRes)
		buff.DeltaWaterRes = int16(eff.WaterRes)
		buff.DeltaWindRes = int16(eff.WindRes)
		buff.DeltaEarthRes = int16(eff.EarthRes)
		if skill.SkillID == 147 {
			applyElementalProtectionDelta(target, buff)
		}

		// 套用屬性差值
		target.AC += buff.DeltaAC
		target.Str += buff.DeltaStr
		target.Dex += buff.DeltaDex
		target.Con += buff.DeltaCon
		target.Wis += buff.DeltaWis
		target.Intel += buff.DeltaIntel
		target.Cha += buff.DeltaCha
		target.MaxHP += buff.DeltaMaxHP
		target.MaxMP += buff.DeltaMaxMP
		target.HitMod += buff.DeltaHitMod
		target.DmgMod += buff.DeltaDmgMod
		target.SP += buff.DeltaSP
		target.MR += buff.DeltaMR
		target.HPR += buff.DeltaHPR
		target.MPR += buff.DeltaMPR
		target.BowHitMod += buff.DeltaBowHit
		target.BowDmgMod += buff.DeltaBowDmg
		target.WeightBonusPct += buff.DeltaWeightPct
		target.Dodge += buff.DeltaDodge
		target.RegistSustain += buff.DeltaRegistSustain
		target.RegistFreeze += buff.DeltaRegistFreeze
		target.RegistStun += buff.DeltaRegistStun
		target.RegistStone += buff.DeltaRegistStone
		target.RegistBlind += buff.DeltaRegistBlind
		target.RegistSleep += buff.DeltaRegistSleep
		target.MagicCritical += buff.DeltaMagicCritical
		target.FireRes += buff.DeltaFireRes
		target.WaterRes += buff.DeltaWaterRes
		target.WindRes += buff.DeltaWindRes
		target.EarthRes += buff.DeltaEarthRes

		// 速度互抵邏輯
		if eff.MoveSpeed > 0 {
			if eff.MoveSpeed == 2 && target.MoveSpeed == 1 {
				s.cancelSpeedBuffs(target, 1)
				target.MoveSpeed = 0
				target.HasteTicks = 0
				s.sendSpeedToAll(target, 0, 0)
			} else if eff.MoveSpeed == 1 && target.MoveSpeed == 2 {
				s.cancelSpeedBuffs(target, 2)
				target.MoveSpeed = 0
				target.HasteTicks = 0
				s.sendSpeedToAll(target, 0, 0)
			} else {
				buff.SetMoveSpeed = byte(eff.MoveSpeed)
				target.MoveSpeed = byte(eff.MoveSpeed)
				target.HasteTicks = buff.TicksLeft
				s.sendSpeedToAll(target, byte(eff.MoveSpeed), uint16(skill.BuffDuration))
			}
		}
		if eff.BraveSpeed > 0 {
			buff.SetBraveSpeed = byte(eff.BraveSpeed)
			target.BraveSpeed = byte(eff.BraveSpeed)
			s.sendBraveToAll(target, byte(eff.BraveSpeed), uint16(skill.BuffDuration))
		}
		// Dodge 變化通知。Java 區分兩條 packet 路徑：
		//   - SOLID_CARRIAGE(90)/DRESS_EVASION(111) skillmode 送 `S_PacketBox(UPDATE_ER, getEr())`（迴避率更新）
		//   - UNCANNY_DODGE(106)/MIRROR_IMAGE/DRAGONEYE_* skillmode 送 `S_PacketBoxIcon1(true, get_dodge())`（dodge icon）
		// 兩條 packet opcode 不同（UPDATE_ER vs 0x58 dodge icon），不可混用。
		if buff.DeltaDodge > 0 {
			switch skill.SkillID {
			case 90, 111:
				handler.SendUpdateER(target.Session, calcPlayerErLikeYiwei(target))
			case 160:
				// 160 AQUA_PROTECTER：Java `skillmode/AQUA_PROTECTER.start()` 只 `setSkillEffect(160, ...)`，
				// 不送任何 packet；ER +5 透過 `L1PcInstance.getEr():3399-3401` getter override 即時計算。
				// Go 採加法 DeltaDodge 路徑（功能等價），但不送 dodge icon 對齊 Java。
				// UPDATE_ER 屬 sendGrfx _targetList 通用 status refresh 廣域 gap，由其他系統處理。
			default:
				handler.SendDodgeIcon(target.Session, target.Dodge, true)
			}
		}
		if eff.Invisible {
			buff.SetInvisible = true
			target.Invisible = true
		}
		if eff.Paralyzed {
			buff.SetParalyzed = true
			target.Paralyzed = true
			switch skill.SkillID {
			case 87, 508:
				handler.SendParalysis(target.Session, handler.StunApply)
			case 157, 50, 80, 30, 194:
				handler.SendParalysis(target.Session, handler.FreezeApply)
				// 凍結類：廣播灰色色調給附近所有玩家（Java: S_Poison type=2）
				broadcastPlayerPoison(target, 2, s.deps)
			case 192:
				handler.SendParalysis(target.Session, handler.BindApply)
			default:
				handler.SendParalysis(target.Session, handler.ParalysisApply)
			}
		}
		if eff.Sleeped {
			buff.SetSleeped = true
			target.Sleeped = true
			handler.SendParalysis(target.Session, handler.SleepApply)
		}
	}
	if skill.SkillID == 64 {
		buff.SetSilenced = true
		target.Silenced = true
	}

	// 註冊 buff（替換舊的）
	old := target.AddBuff(buff)
	if old != nil {
		s.revertBuffStats(target, old)
	}

	// 屬性變化時發送更新
	if buff.DeltaStr != 0 || buff.DeltaDex != 0 || buff.DeltaCon != 0 ||
		buff.DeltaWis != 0 || buff.DeltaIntel != 0 || buff.DeltaCha != 0 ||
		buff.DeltaMaxHP != 0 || buff.DeltaMaxMP != 0 || buff.DeltaAC != 0 ||
		buff.DeltaDmgMod != 0 || buff.DeltaHitMod != 0 {
		handler.SendPlayerStatus(target.Session, target)
	}
	// MR/SP 變化時送 S_SPMR (Java: SHADOW_ARMOR/RESIST_MAGIC/ILLUSION_LICH 等
	// `pc.sendPackets(new S_SPMR(pc))` 對齊；原本只送 S_STATUS 不含 MR/SP)。
	if buff.DeltaMR != 0 || buff.DeltaSP != 0 {
		handler.SendMagicStatus(target.Session, byte(target.SP), uint16(target.MR))
	}
	// 元素抗性變化時送 S_OwnCharAttrDef（Java `L1SkillUse.java:2545` RESIST_ELEMENTAL cast
	// `pc.sendPackets(new S_OwnCharAttrDef(pc))` 對齊；同時為 ELEMENTAL_PROTECTION/147 cast 補上
	// Java 漏送的 UI 更新——client 顯示與資料一致，行為比 Java 嚴格收緊）。
	if target.Session != nil && (buff.DeltaFireRes != 0 || buff.DeltaWaterRes != 0 || buff.DeltaWindRes != 0 || buff.DeltaEarthRes != 0) {
		handler.SendAbilityScores(target.Session, target)
	}
	s.sendBuffStatUpdatePackets(target, buff)

	s.sendBuffIcon(target, skill.SkillID, uint16(skill.BuffDuration))

	// 日光術（技能 2）：上 buff 後更新光源
	if skill.SkillID == 2 {
		handler.UpdatePlayerLight(target, s.deps.World)
	}
}

func (s *SkillSystem) sendBuffStatUpdatePackets(target *world.PlayerInfo, buff *world.ActiveBuff) {
	if target.Session == nil {
		return
	}
	if buff.DeltaMaxHP != 0 {
		sendHPUpdatePacket(target.Session, target.HP, target.MaxHP)
	}
	if buff.DeltaMaxMP != 0 {
		sendMPUpdatePacket(target.Session, target.MP, target.MaxMP)
	}
	if buff.DeltaWeightPct != 0 {
		handler.SendWeightUpdate(target.Session, target)
	}
}

func applyElementalProtectionDelta(target *world.PlayerInfo, buff *world.ActiveBuff) {
	if target == nil || buff == nil {
		return
	}
	switch target.ElfAttr {
	case 1:
		buff.DeltaEarthRes = 50
	case 2:
		buff.DeltaFireRes = 50
	case 4:
		buff.DeltaWaterRes = 50
	case 8:
		buff.DeltaWindRes = 50
	}
}

// ApplyNpcDebuff NPC 對玩家施放 debuff 技能（麻痺/睡眠/減速等）。
// 實際委派給 applyBuffEffect，由 NpcAISystem 透過 SkillManager 介面呼叫。
func (s *SkillSystem) ApplyNpcDebuff(target *world.PlayerInfo, skill *data.SkillInfo) {
	s.applyBuffEffect(target, skill)
}

// cancelAbsoluteBarrier 解除絕對屏障效果（Java: L1BuffUtil.cancelAbsoluteBarrier）。
// 被攻擊/施法/使用道具時呼叫。移動時不解除。
func (s *SkillSystem) cancelAbsoluteBarrier(player *world.PlayerInfo) {
	s.removeBuffAndRevert(player, 78)
	// removeBuffAndRevert → revertBuffStats 會清除 AbsoluteBarrier flag
}

// CancelAbsoluteBarrier 匯出版本，供 handler（movement/item）呼叫。
func (s *SkillSystem) CancelAbsoluteBarrier(player *world.PlayerInfo) {
	if player.AbsoluteBarrier {
		s.cancelAbsoluteBarrier(player)
	}
}

// cancelInvisibility 解除隱身效果（Java: L1BuffUtil.cancelInvisibility）。
// 攻擊/施法時呼叫。移除隱身 buff 並通知周圍玩家重新顯示此角色。
func (s *SkillSystem) cancelInvisibility(player *world.PlayerInfo) {
	// 移除隱身術 (60) 和暗隱術 (97) 的 buff
	s.removeBuffAndRevert(player, 60)
	s.removeBuffAndRevert(player, 97)
	// removeBuffAndRevert → revertBuffStats 會清除 Invisible flag

	// 通知玩家自己已解除隱身
	handler.SendInvisible(player.Session, player.CharID, false)

	s.sendPlayerReappearToVisible(player)
}

// CancelInvisibility 匯出版本，供 combat/handler 呼叫。
func (s *SkillSystem) CancelInvisibility(player *world.PlayerInfo) {
	if player.Invisible {
		s.cancelInvisibility(player)
	}
}

func (s *SkillSystem) sendPlayerReappearToVisible(player *world.PlayerInfo) {
	nearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, player.SessionID, player.ShowID)
	for _, viewer := range nearby {
		if viewer.CharID == player.CharID {
			continue
		}
		handler.SendPutObject(viewer.Session, player)
	}
}

// ApplyGMBuff GM 強制套用 buff（繞過已學/MP/材料驗證）。
func (s *SkillSystem) ApplyGMBuff(player *world.PlayerInfo, skillID int32) bool {
	skill := s.deps.Skills.Get(skillID)
	if skill == nil {
		return false
	}
	s.applyBuffEffect(player, skill)
	dur := uint16(skill.BuffDuration)
	if dur == 0 {
		dur = 300 // 預設 5 分鐘
	}
	s.sendBuffIcon(player, skillID, dur)
	handler.SendPlayerStatus(player.Session, player)
	// 負重強化：套用時更新負重
	if skillID == 14 || skillID == 218 {
		handler.SendWeightUpdate(player.Session, player)
	}
	return true
}

// ApplyGMBuffWithDuration 以指定秒數套用 GM buff，用於 yiwei type 5 陷阱的 skillTimeSeconds。
func (s *SkillSystem) ApplyGMBuffWithDuration(player *world.PlayerInfo, skillID int32, durationSec int32) bool {
	skill := s.deps.Skills.Get(skillID)
	if skill == nil {
		return false
	}
	effectiveSkill := *skill
	if durationSec > 0 {
		effectiveSkill.BuffDuration = int(durationSec)
	}
	s.applyBuffEffect(player, &effectiveSkill)
	dur := uint16(effectiveSkill.BuffDuration)
	if dur == 0 {
		dur = 300 // 預設 5 分鐘
	}
	s.sendBuffIcon(player, skillID, dur)
	handler.SendPlayerStatus(player.Session, player)
	// 負重強化：套用時更新負重
	if skillID == 14 || skillID == 218 {
		handler.SendWeightUpdate(player.Session, player)
	}
	return true
}

// counterMagicExempt 魔法屏障不可抵擋的技能清單（Java: EXCEPT_COUNTER_MAGIC[]）。
// 這些技能穿透魔法屏障，不會被抵消。
var counterMagicExempt = map[int32]bool{
	1: true, 2: true, 3: true, 5: true, 8: true, 9: true, 12: true, 13: true, 14: true,
	19: true, 21: true, 26: true, 31: true, 32: true, 35: true, 37: true, 42: true,
	43: true, 44: true, 48: true, 49: true, 52: true, 54: true, 55: true, 57: true,
	60: true, 61: true, 63: true, 67: true, 68: true, 69: true, 72: true, 73: true,
	75: true, 78: true, 79: true, 87: true, 88: true, 89: true, 90: true, 91: true,
	97: true, 98: true, 99: true, 100: true, 101: true, 102: true, 104: true, 105: true,
	106: true, 107: true, 109: true, 110: true, 111: true, 113: true, 114: true, 115: true,
	116: true, 117: true, 118: true, 129: true, 130: true, 131: true, 132: true, 134: true,
	137: true, 138: true, 146: true, 147: true, 148: true, 149: true, 150: true, 151: true,
	155: true, 156: true, 158: true, 159: true, 161: true, 163: true, 164: true, 165: true,
	166: true, 168: true, 169: true, 170: true, 171: true, 175: true, 176: true, 181: true,
	185: true, 190: true, 194: true, 195: true, 201: true, 204: true, 209: true, 211: true,
	213: true, 214: true, 216: true, 219: true,
	10026: true, 10027: true, 10028: true, 10029: true, 41472: true,
}

// tryCounterMagic 檢查目標是否有魔法屏障（buff 31），若有則觸發抵消。
// 回傳 true 表示技能被抵消，呼叫方應跳過該目標的效果。
// Java 參考: L1SkillUse.isUseCounterMagic()
func (s *SkillSystem) tryCounterMagic(target *world.PlayerInfo, skillID int32) bool {
	// 豁免技能不受魔法屏障影響
	if counterMagicExempt[skillID] {
		return false
	}
	// 目標沒有魔法屏障
	if !target.HasBuff(31) {
		return false
	}
	// 觸發：移除魔法屏障 buff + 播放 GFX
	s.removeBuffAndRevert(target, 31)
	// 取得 castGfx2（魔法屏障觸發動畫）
	gfx := int32(10702) // 預設值
	if sk := s.deps.Skills.Get(31); sk != nil && sk.CastGfx2 > 0 {
		gfx = sk.CastGfx2
	}
	// 廣播觸發動畫給附近玩家 + 目標自己
	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)
	data := handler.BuildSkillEffect(target.CharID, gfx)
	handler.BroadcastToPlayers(nearby, data)
	return true
}

// removeBuffAndRevert 移除衝突 buff 並還原屬性。
func (s *SkillSystem) removeBuffAndRevert(target *world.PlayerInfo, skillID int32) {
	old := target.RemoveBuff(skillID)
	if old != nil {
		s.revertBuffStats(target, old)
		s.cancelBuffIcon(target, skillID)
		// 日光術（技能 2）：buff 被取消後更新光源
		if skillID == 2 {
			handler.UpdatePlayerLight(target, s.deps.World)
		}
		// 風之枷鎖（技能 167）：buff 被取消後清除客戶端效果
		if skillID == 167 {
			handler.SendWindShackle(target.Session, target.CharID, 0)
		}
		// 水之元氣（技能 170）：buff 被取消後送出 S_PacketBoxWaterLife 取消圖示
		// 對齊 Java L1SkillStop case 170。
		if skillID == 170 {
			handler.SendWaterLifeCancel(target.Session)
		}
		// 精準射擊（技能 174）：buff 被取消後送出 UPDATE_ER 還原客戶端 ER 顯示
		// 對齊 Java L1SkillStop case STRIKER_GALE。
		if skillID == 174 {
			handler.SendUpdateER(target.Session, calcPlayerErLikeYiwei(target))
		}
		// 法利昂覺醒（190）外部移除（exclusions/解除）時連帶清除 Physical Power（169）。
		// 對齊 Java `skillmode/AWAKEN_FAFURION.java:36-40 stop()` 的 `killSkillEffectTimer(169)`，
		// 與 `tickPlayerBuffs:803-806` 自然到期路徑共用此 cleanup（避免 185 exclusions 移除 190 後 169 殘留）。
		if skillID == 190 {
			s.removeBuffAndRevert(target, 169)
		}

		if s.deps.Skills != nil {
			if sk := s.deps.Skills.Get(skillID); sk != nil && sk.SysMsgStop > 0 {
				handler.SendServerMessage(target.Session, uint16(sk.SysMsgStop))
			}
		}
	}
}

// cancelSpeedBuffs 移除指定速度類型的所有 buff。
func (s *SkillSystem) cancelSpeedBuffs(target *world.PlayerInfo, speedType byte) {
	if target.ActiveBuffs == nil {
		return
	}
	for skillID, b := range target.ActiveBuffs {
		if b.SetMoveSpeed == speedType {
			s.revertBuffStats(target, b)
			delete(target.ActiveBuffs, skillID)
		}
	}
}

// revertBuffStats 還原 buff 的所有屬性修改。
func (s *SkillSystem) revertBuffStats(target *world.PlayerInfo, buff *world.ActiveBuff) {
	target.AC -= buff.DeltaAC
	target.Str -= buff.DeltaStr
	target.Dex -= buff.DeltaDex
	target.Con -= buff.DeltaCon
	target.Wis -= buff.DeltaWis
	target.Intel -= buff.DeltaIntel
	target.Cha -= buff.DeltaCha
	target.MaxHP -= buff.DeltaMaxHP
	target.MaxMP -= buff.DeltaMaxMP
	target.HitMod -= buff.DeltaHitMod
	target.DmgMod -= buff.DeltaDmgMod
	target.SP -= buff.DeltaSP
	target.MR -= buff.DeltaMR
	target.HPR -= buff.DeltaHPR
	target.MPR -= buff.DeltaMPR
	target.BowHitMod -= buff.DeltaBowHit
	target.BowDmgMod -= buff.DeltaBowDmg
	target.WeightBonusPct -= buff.DeltaWeightPct
	target.FireRes -= buff.DeltaFireRes
	target.WaterRes -= buff.DeltaWaterRes
	target.WindRes -= buff.DeltaWindRes
	target.EarthRes -= buff.DeltaEarthRes
	target.Dodge -= buff.DeltaDodge
	// Dodge buff 過期：對齊 Java skillmode（MIRROR_IMAGE/DRAGONEYE_*/UNCANNY_DODGE）stop()
	// 一律送 `S_PacketBoxIcon1(true, get_dodge())` = 0x58 + 當前 dodge 總值。
	// 0x65（_dodge_down）是另一個 dodge_down 計數器（RESIST_FEAR 專用），不可混用。
	if buff.DeltaDodge > 0 && target.Session != nil {
		// 與 applyBuffEffect 對稱：90/111 走 UPDATE_ER 路徑；其餘 dodge buff（106/MIRROR/DRAGONEYE）走 dodge icon。
		// Java SOLID_CARRIAGE.stop() line 47 與 DRESS_EVASION.stop() line 31 都送 S_PacketBox(UPDATE_ER, getEr())。
		switch buff.SkillID {
		case 90, 111:
			handler.SendUpdateER(target.Session, calcPlayerErLikeYiwei(target))
		case 160:
			// 160 AQUA_PROTECTER：Java `skillmode/AQUA_PROTECTER.stop()` empty 不送任何 packet。
			// Go 不送 dodge icon/UPDATE_ER 對齊 Java（同 apply 路徑）。
		default:
			handler.SendDodgeIcon(target.Session, target.Dodge, true)
		}
	}
	target.RegistSustain -= buff.DeltaRegistSustain
	target.RegistFreeze -= buff.DeltaRegistFreeze
	target.RegistStun -= buff.DeltaRegistStun
	target.RegistStone -= buff.DeltaRegistStone
	target.RegistBlind -= buff.DeltaRegistBlind
	target.RegistSleep -= buff.DeltaRegistSleep
	target.MagicCritical -= buff.DeltaMagicCritical
	if target.HP > target.MaxHP && target.MaxHP > 0 {
		target.HP = target.MaxHP
	}
	if target.MP > target.MaxMP && target.MaxMP > 0 {
		target.MP = target.MaxMP
	}
	if buff.SetInvisible {
		target.Invisible = false
	}
	if buff.SetParalyzed {
		target.Paralyzed = false
	}
	if buff.SetSleeped {
		target.Sleeped = false
	}
	if buff.SetSilenced {
		target.Silenced = false
	}
	if buff.SetAbsoluteBarrier {
		target.AbsoluteBarrier = false
	}
	// MR/SP 還原時送 S_SPMR (對齊 Java SHADOW_ARMOR/RESIST_MAGIC 等 stop()
	// `pc.sendPackets(new S_SPMR(pc))`；否則玩家 UI 仍顯示 buff 期間的 MR/SP)。
	if target.Session != nil && (buff.DeltaMR != 0 || buff.DeltaSP != 0) {
		handler.SendMagicStatus(target.Session, byte(target.SP), uint16(target.MR))
	}
	// 元素抗性還原時送 S_OwnCharAttrDef（Java `L1SkillStop` 133/138/147 與
	// `ELEMENTAL_FALL_DOWN.stop()` 對 PC 都在 `addEarth/Fire/Water/Wind` 後送 `S_OwnCharAttrDef(pc)`；
	// 否則玩家 UI 仍顯示 buff 期間的低抗性）。
	if target.Session != nil && (buff.DeltaFireRes != 0 || buff.DeltaWaterRes != 0 || buff.DeltaWindRes != 0 || buff.DeltaEarthRes != 0) {
		handler.SendAbilityScores(target.Session, target)
	}
	s.sendBuffStatUpdatePackets(target, buff)
}

// RevertBuffStats implements handler.SkillManager — 還原 buff 的所有屬性修改（Exported）。
func (s *SkillSystem) RevertBuffStats(target *world.PlayerInfo, buff *world.ActiveBuff) {
	s.revertBuffStats(target, buff)
}

// ConsumeSkillResources implements handler.SkillManager — 扣除 MP/HP/材料並設定冷卻（Exported）。
func (s *SkillSystem) ConsumeSkillResources(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo) {
	s.consumeSkillResources(sess, player, skill)
}

// ApplyBuffStats implements handler.SkillManager — 套用 buff 屬性加成（靜默，不發送封包）。
func (s *SkillSystem) ApplyBuffStats(player *world.PlayerInfo, buff *world.ActiveBuff) {
	player.AC += buff.DeltaAC
	player.Str += buff.DeltaStr
	player.Dex += buff.DeltaDex
	player.Con += buff.DeltaCon
	player.Wis += buff.DeltaWis
	player.Intel += buff.DeltaIntel
	player.Cha += buff.DeltaCha
	player.MaxHP += buff.DeltaMaxHP
	player.MaxMP += buff.DeltaMaxMP
	player.HitMod += buff.DeltaHitMod
	player.DmgMod += buff.DeltaDmgMod
	player.SP += buff.DeltaSP
	player.MR += buff.DeltaMR
	player.HPR += buff.DeltaHPR
	player.MPR += buff.DeltaMPR
	player.BowHitMod += buff.DeltaBowHit
	player.BowDmgMod += buff.DeltaBowDmg
	player.WeightBonusPct += buff.DeltaWeightPct
	player.Dodge += buff.DeltaDodge
	player.FireRes += buff.DeltaFireRes
	player.WaterRes += buff.DeltaWaterRes
	player.WindRes += buff.DeltaWindRes
	player.EarthRes += buff.DeltaEarthRes
	player.RegistSustain += buff.DeltaRegistSustain
	player.RegistFreeze += buff.DeltaRegistFreeze
	player.RegistStun += buff.DeltaRegistStun
	player.RegistStone += buff.DeltaRegistStone
	player.RegistBlind += buff.DeltaRegistBlind
	player.RegistSleep += buff.DeltaRegistSleep
	player.MagicCritical += buff.DeltaMagicCritical
}

// sendSpeedToAll 向自己和附近玩家發送速度封包。
func (s *SkillSystem) sendSpeedToAll(target *world.PlayerInfo, speedType byte, duration uint16) {
	sendSpeedPacket(target.Session, target.CharID, speedType, duration)
	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, target.SessionID, target.ShowID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, target.CharID, speedType, 0)
	}
}

// sendBraveToAll 向自己和附近玩家發送勇敢封包。
func (s *SkillSystem) sendBraveToAll(target *world.PlayerInfo, braveType byte, duration uint16) {
	sendBravePacket(target.Session, target.CharID, braveType, duration)
	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, target.SessionID, target.ShowID)
	for _, other := range nearby {
		sendBravePacket(other.Session, target.CharID, braveType, 0)
	}
}

// cancelAllBuffs 移除所有可取消的 buff。
func (s *SkillSystem) cancelAllBuffs(target *world.PlayerInfo) {
	needHasteItemMoveSpeedClear := target.HasteItemEquipped > 0
	if target.ActiveBuffs == nil {
		s.clearHasteItemMoveSpeedForCancellation(target, false)
		return
	}

	// 追蹤需要發送的客戶端通知（迴圈結束後統一發送）
	needFreezeRemove := false
	needStunRemove := false
	needParalysisRemove := false
	needSleepRemove := false
	needBindRemove := false
	needInvisRemove := false
	speedClearSent := false

	for skillID, buff := range target.ActiveBuffs {
		if s.deps.Scripting.IsNonCancellable(int(skillID)) {
			continue
		}
		s.revertBuffStats(target, buff)
		delete(target.ActiveBuffs, skillID)
		s.cancelBuffIcon(target, skillID)

		if skillID == handler.SkillShapeChange && s.deps.Polymorph != nil {
			s.deps.Polymorph.UndoPoly(target)
		}

		if buff.SetMoveSpeed > 0 {
			target.MoveSpeed = 0
			target.HasteTicks = 0
			s.sendSpeedToAll(target, 0, 0)
			speedClearSent = true
		}
		if buff.SetBraveSpeed > 0 {
			target.BraveSpeed = 0
			s.sendBraveToAll(target, 0, 0)
		}

		// 追蹤麻痺/凍結/暈眩類型
		if buff.SetParalyzed {
			switch skillID {
			case 87, 208, 508:
				// 87 SHOCK_STUN、208 BONE_BREAK、508 都是「衝擊之暈」類型
				// （Java `S_Paralysis(5, false)` → wire 0x17 StunRemove）。
				needStunRemove = true
			case 157, 50, 80, 30, 194:
				needFreezeRemove = true
			case 192:
				needBindRemove = true
			default:
				needParalysisRemove = true
			}
		}
		if buff.SetSleeped {
			needSleepRemove = true
		}
		if buff.SetInvisible {
			needInvisRemove = true
		}
	}
	if needHasteItemMoveSpeedClear {
		s.clearHasteItemMoveSpeedForCancellation(target, speedClearSent)
	}

	// 凍結解除通知（控制鎖 + 灰色色調）
	if needFreezeRemove {
		handler.SendParalysis(target.Session, handler.FreezeRemove)
		broadcastPlayerPoison(target, 0, s.deps)
	}
	if needStunRemove {
		handler.SendParalysis(target.Session, handler.StunRemove)
	}
	if needParalysisRemove {
		handler.SendParalysis(target.Session, handler.ParalysisRemove)
	}
	// 睡眠解除通知
	if needSleepRemove {
		handler.SendParalysis(target.Session, handler.SleepRemove)
	}
	if needBindRemove {
		handler.SendParalysis(target.Session, handler.BindRemove)
	}
	// 隱身解除通知 + 周圍玩家重新顯示
	if needInvisRemove {
		handler.SendInvisible(target.Session, target.CharID, false)
		s.sendPlayerReappearToVisible(target)
	}

	// 重新檢查是否仍有非 buff 來源的麻痺（毒麻痺/詛咒麻痺）
	if shouldStayParalyzed(target, false, false) {
		target.Paralyzed = true
	}

	handler.SendPlayerStatus(target.Session, target)
}

func (s *SkillSystem) clearHasteItemMoveSpeedForCancellation(target *world.PlayerInfo, speedClearSent bool) {
	if target.HasteItemEquipped <= 0 {
		return
	}
	target.MoveSpeed = 0
	target.HasteTicks = 0
	if !speedClearSent {
		s.sendSpeedToAll(target, 0, 0)
	}
}

// ========================================================================
//  Buff 計時器
// ========================================================================

// tickPlayerBuffs 每 tick 遞減 buff 計時器並處理到期。
func (s *SkillSystem) tickPlayerBuffs(p *world.PlayerInfo) {
	if p.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range p.ActiveBuffs {
		if buff.TicksLeft <= 0 {
			continue
		}
		buff.TicksLeft--
		if buff.TicksLeft <= 0 {
			s.revertBuffStats(p, buff)
			delete(p.ActiveBuffs, skillID)

			s.cancelBuffIcon(p, skillID)

			if skillID == handler.SkillShapeChange && s.deps.Polymorph != nil {
				s.deps.Polymorph.UndoPoly(p)
			}

			if buff.SetMoveSpeed > 0 {
				p.MoveSpeed = 0
				p.HasteTicks = 0
				s.sendSpeedToAll(p, 0, 0)
			}
			if buff.SetBraveSpeed > 0 {
				p.BraveSpeed = 0
				p.BraveTicks = 0
				s.sendBraveToAll(p, 0, 0)
			}

			// 麻痺/睡眠/致盲到期
			if buff.SetParalyzed {
				switch skillID {
				case 87, 508:
					handler.SendParalysis(p.Session, handler.StunRemove)
				case 157, 50, 80, 30, 194:
					handler.SendParalysis(p.Session, handler.FreezeRemove)
					// 清除灰色色調
					broadcastPlayerPoison(p, 0, s.deps)
				case 192:
					handler.SendParalysis(p.Session, handler.BindRemove)
				default:
					handler.SendParalysis(p.Session, handler.ParalysisRemove)
				}
			}
			if buff.SetSleeped {
				handler.SendParalysis(p.Session, handler.SleepRemove)
			}
			if skillID == 20 || skillID == 40 {
				handler.SendCurseBlind(p.Session, 0)
			}

			// 慎重藥水到期
			if skillID == handler.SkillStatusWisdomPotion {
				p.WisdomSP = 0
				p.WisdomTicks = 0
			}

			// 負重強化到期：更新負重顯示
			if skillID == 14 || skillID == 218 {
				handler.SendWeightUpdate(p.Session, p)
			}

			// 日光術（技能 2）：到期後更新光源
			if skillID == 2 {
				handler.UpdatePlayerLight(p, s.deps.World)
			}

			// 風之枷鎖（技能 167）：到期後清除客戶端效果
			if skillID == 167 {
				handler.SendWindShackle(p.Session, p.CharID, 0)
			}
			// 水之元氣（技能 170）：到期後送出 S_PacketBoxWaterLife 取消圖示
			// 對齊 Java L1SkillStop case 170。
			if skillID == 170 {
				handler.SendWaterLifeCancel(p.Session)
			}
			// 精準射擊（技能 174）：到期後送出 UPDATE_ER 還原客戶端 ER 顯示
			// 對齊 Java L1SkillStop case STRIKER_GALE。
			if skillID == 174 {
				handler.SendUpdateER(p.Session, calcPlayerErLikeYiwei(p))
			}

			// 法利昂覺醒（190）到期時連帶清除 Physical Power（169）
			if skillID == 190 {
				s.removeBuffAndRevert(p, 169)
			}

			if s.deps.Skills != nil {
				if sk := s.deps.Skills.Get(skillID); sk != nil && sk.SysMsgStop > 0 {
					handler.SendServerMessage(p.Session, uint16(sk.SysMsgStop))
				}
			}

			handler.SendPlayerStatus(p.Session, p)
		} else if buff.SetParalyzed && buff.TicksLeft%25 == 0 {
			// 3.80C 客戶端灰色色調會自動淡出，每 5 秒重發維持視覺
			switch skillID {
			case 157, 50, 80, 30, 194:
				broadcastPlayerPoison(p, 2, s.deps)
			}
		}
	}

	// 同步藥水倒數
	if p.HasteTicks > 0 {
		p.HasteTicks--
	}
	if p.BraveTicks > 0 {
		p.BraveTicks--
	}
	if p.WisdomTicks > 0 {
		p.WisdomTicks--
	}

	// PK 粉紅名到期
	if p.PinkNameTicks > 0 {
		p.PinkNameTicks--
		if p.PinkNameTicks <= 0 {
			p.PinkName = false
		}
	}

	// 通緝狀態到期
	if p.WantedTicks > 0 {
		p.WantedTicks--
	}
}

// ========================================================================
//  Buff 技能
// ========================================================================

// executeBuffSkill 處理治療與 buff 類技能。
func (s *SkillSystem) executeBuffSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32, textArg ...string) {
	ws := s.deps.World
	text := ""
	if len(textArg) > 0 {
		text = textArg[0]
	}

	if skill.SkillID == 116 || skill.SkillID == 118 {
		// Owner: skill_clan.go
		s.executeClanTargetSkill(sess, player, skill, targetID, "", false)
		return
	}

	// 檢查目標是否為 NPC（debuff 路徑）
	if targetID != 0 && targetID != player.CharID {
		if npc := ws.GetNpc(targetID); npc != nil && !npc.Dead {
			s.executeNpcDebuffSkill(sess, player, skill, npc)
			return
		}
	}

	// 檢查目標是否為自己的寵物/召喚物
	// Java: TARGET_TO_PET — 治療/復活技能可對自己的寵物/召喚物生效
	if targetID != 0 && targetID != player.CharID {
		isResurrect := skill.SkillID == 61 || skill.SkillID == 75 // 返生術 / 終極返生術
		if pet := ws.GetPet(targetID); pet != nil && pet.OwnerCharID == player.CharID {
			if isResurrect && pet.Dead {
				s.resurrectPet(sess, player, skill, pet)
				return
			}
			if !isResurrect && !pet.Dead {
				s.healCompanion(sess, player, skill, pet.ID, &pet.HP, pet.MaxHP, pet.X, pet.Y, pet.MapID)
				return
			}
		}
		if sum := ws.GetSummon(targetID); sum != nil && sum.OwnerCharID == player.CharID {
			if !sum.Dead {
				s.healCompanion(sess, player, skill, sum.ID, &sum.HP, sum.MaxHP, sum.X, sum.Y, sum.MapID)
			}
			return
		}
	}

	target := player
	if targetID != 0 && targetID != player.CharID {
		if other := ws.GetByCharID(targetID); other != nil {
			if other.MapID != player.MapID || other.Dead || (skill.SkillID == 87 && other.ShowID != player.ShowID) {
				return
			}
			maxRange := int32(20)
			if skill.SkillID == 87 {
				maxRange = shockStunRange(skill)
			}
			if chebyshevDist(player.X, player.Y, other.X, other.Y) > maxRange {
				return
			}
			if skill.SkillID == 87 && s.shockStunLineOfSightBlocked(player, other.X, other.Y) {
				return
			}
			target = other
		}
	}
	if skill.SkillID == 87 && target.CharID != player.CharID && isGMInvisible(target) {
		return
	}
	if skill.SkillID == 87 && target.CharID != player.CharID && target.AbsoluteBarrier {
		return
	}

	// 魔法屏障攔截：對其他玩家施放非豁免技能時，檢查目標是否有 Counter Magic（buff 31）
	if target.CharID != player.CharID && s.tryCounterMagic(target, skill.SkillID) {
		// 技能被抵消，仍播放施法動畫但不產生效果
		nearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
		return
	}
	if skill.SkillID == 87 && target.CharID != player.CharID {
		s.queueShockStunOnAction(sess, target.CharID)
		s.clearShockStunSleepEffects(target)
		s.clearShockStunEraseMagic(target)
		if s.shockStunSafetyZoneBlocked(sess, player.MapID, player.X, player.Y, target.MapID, target.X, target.Y) {
			return
		}
		if target.HasBuff(50) || target.HasBuff(157) {
			return
		}
	}

	// 玩家 debuff MR 抗性判定：對其他玩家施放 debuff 時必須通過 MR 檢查
	if target.CharID != player.CharID && playerDebuffSkills[skill.SkillID] {
		success := false
		if skill.SkillID == 87 {
			success = s.checkShockStunPlayerSuccess(player, target)
		} else {
			success = s.checkPlayerMRResist(player, target)
		}
		if !success {
			if skill.SkillID != 87 {
				s.sendCastFail(sess)
			}
			if skill.SkillID == 87 {
				return
			}
			// 仍播放施法動畫（Java: 一般 debuff miss 會播放動畫）
			nearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)
			handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
			if skill.CastGfx > 0 {
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
			}
			return
		}
	}

	nearby := s.deps.World.GetNearbyPlayersInShow(player.X, player.Y, player.MapID, 0, player.ShowID)

	// 廣播施法動畫
	if skill.SkillID != 87 {
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
	}

	// 變形術：開啟怪物列表對話框
	if skill.SkillID == handler.SkillShapeChange {
		// Owner: skill_polymorph.go
		s.executeShapeChangePlayer(sess, player, target, skill)
		return
	}

	// 即時效果技能
	switch skill.SkillID {
	case 9: // 解毒術
		CurePoison(target, s.deps)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 11: // 毒咒 — 對玩家施加傷害毒（Java: L1DamagePoison.doInfection(attacker, target, 3000, 5)）
		if target.CharID != player.CharID {
			applyDamagePoisonToPlayer(target, sess.ID, 5, s.deps)
		}

	case 27: // 壞物術 — Owner: skill_weapon.go；破壞玩家目標已裝備武器耐久
		weapon := target.Equip.Weapon()
		if weapon != nil {
			changed := applyWeaponBreakDurability(weapon, calcWeaponBreakDurabilityDamage(player))
			handler.SendServerMessageArgs(target.Session, 268, itemLogName(weapon))
			if s.deps.Items != nil {
				if itemInfo := s.deps.Items.Get(weapon.ItemID); itemInfo != nil {
					handler.SendItemStatusUpdate(target.Session, weapon, itemInfo)
				} else {
					handler.SendItemCountUpdate(target.Session, weapon)
				}
			} else {
				handler.SendItemCountUpdate(target.Session, weapon)
			}
			if changed {
				target.Dirty = true
			}
		}

	case 20, 40: // 闇盲咒術 / 黑闇之影 — Owner: skill_status.go
		s.applyCurseBlindEffect(target, skill)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 87: // 衝擊之暈 — Owner: skill_status.go；玩家目標需要雙手劍
		if s.applyShockStunToPlayer(sess, player, target, skill, nearby) {
			return
		}

	case 33: // 木乃伊詛咒 — 對玩家施加詛咒麻痺
		if target.CharID != player.CharID && !target.Paralyzed && target.CurseType == 0 &&
			!target.HasBuff(157) && !target.HasBuff(50) && !target.HasBuff(80) {
			target.CurseType = 1
			target.CurseTicksLeft = 25
			BroadcastPlayerPoison(target, 2, s.deps)
			handler.SendServerMessage(target.Session, 212)
		}

	case 37: // 聖潔之光 — 解毒 + 解詛咒 + 解麻痺/睡眠/致盲
		CurePoison(target, s.deps)
		if target.CurseType > 0 {
			CureCurseParalysis(target, s.deps)
		}
		if target.Paralyzed {
			target.Paralyzed = false
			handler.SendParalysis(target.Session, handler.ParalysisRemove)
		}
		if target.Sleeped {
			target.Sleeped = false
			handler.SendParalysis(target.Session, handler.SleepRemove)
		}
		s.removeCurseBlindEffect(target)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 39: // 魔力奪取
		drain := int32(5 + world.RandInt(10))
		if target.MP >= drain {
			target.MP -= drain
			player.MP += drain
			if player.MP > player.MaxMP {
				player.MP = player.MaxMP
			}
			sendMpUpdate(target.Session, target)
			sendMpUpdate(sess, player)
		}

	case 44: // 魔法相消術 — 解除目標所有 buff + 毒 + 詛咒
		if target != nil {
			CurePoison(target, s.deps)
			CureCurseParalysis(target, s.deps)
			s.cancelAllBuffs(target)
		}
		// 施法時自身也解除隱身（Java: if srcpc.isInvisble() → srcpc.delInvis()）
		if player.Invisible {
			s.cancelInvisibility(player)
		}
		if target != nil {
			if skill.CastGfx > 0 {
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
			}
			s.sendYiweiPostCastStatusRefresh(target)
		}
		return

	case 71: // 藥水霜化術 — 通知目標無法使用藥水
		if target.CharID != player.CharID {
			handler.SendServerMessage(target.Session, 698) // "喉嚨灼熱，無法喝東西。"
		}

	case 103: // 暗黑盲咒 — Java 使用 66 睡眠效果與睡眠操作鎖
		sleepSkill := *skill
		sleepSkill.SkillID = 66
		s.applyBuffEffect(target, &sleepSkill)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 212: // 幻想 — Java skillmode/PHANTASM.java:22 對 PC `setSkillEffect(66, integer*1000)`
		// 使用 FOG_OF_SLEEPING(66) 而非 PHANTASM(212) 作為實際 buff key，讓
		// `hasSkillEffect(66)` cross-skill 查詢能正確命中（同 case 103 模式）。
		sleepSkill := *skill
		sleepSkill.SkillID = 66
		s.applyBuffEffect(target, &sleepSkill)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 113: // 精準目標 — Owner: skill_clan.go；目標狀態 + S_TrueTarget 給血盟成員
		s.applyTrueTargetEffect(player, target, skill, text)
		return

	case 133: // 弱化屬性 — Owner: skill_elemental.go
		if player.ElfAttr == 0 {
			handler.SendServerMessage(sess, 79)
			return
		}
		s.applyElementalFallDownToPlayer(player, target, skill)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 157: // 大地屏障：Java EARTH_BIND，命中後 1-12 秒凍結。
		if target.HasBuff(157) || target.Paralyzed {
			return
		}
		earthBind := *skill
		earthBind.BuffDuration = 1 + world.RandInt(12)
		s.applyBuffEffect(target, &earthBind)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 112: // 破壞盔甲（黑暗妖精 debuff）
		// Java: ARMOR_BREAK.java — 自定義機率系統，非標準 MR 判定
		if target.CharID == player.CharID {
			return // 不可對自己施放
		}
		if !s.calcArmorBreakProb(player, target) {
			s.sendCastFail(sess)
			return
		}
		// 移除舊的破壞盔甲效果（Java: killSkillEffectTimer + 重新 setSkillEffect）
		if target.HasBuff(112) {
			s.removeBuffAndRevert(target, 112)
		}
		// 套用 buff 效果（8 秒計時器）
		s.applyBuffEffect(target, skill)
		// 廣播技能音效 GFX 3400
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, 3400))
		// Buff 圖示（Java: S_PacketBoxIconAura(119, 8)）
		handler.SendIconAura(target.Session, 119, 8)
		// 成功訊息
		handler.SendGlobalChat(sess, 9, "\\f2破壞盔甲 施放成功!")
		s.sendYiweiPostCastStatusRefresh(target)
		return

	case 167: // 風之枷鎖 — 降低目標攻擊速度
		// Java: WIND_SHACKLE.java — 不可重複施加；發送 S_PacketBoxWindShackle
		if !target.HasBuff(167) {
			s.applyBuffEffect(target, skill)
			handler.SendWindShackle(target.Session, target.CharID, skill.BuffDuration)
		}
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		s.sendYiweiPostCastStatusRefresh(target)
		return
	}

	// 覺醒系統（185/190/195）：對齊 Java skillmode AWAKEN_ANTHARAS/FAFURION/VALAKAS
	// - 首次施放：套用屬性 buff（exclusions 由 buffs.lua 處理）+ 廣播音效 + 同步狀態
	// - 再施放（已有效）：僅廣播音效，不刷新計時器（Java skillmode 的 `if (!hasSkillEffect(N))` 守衛跳過 setSkillEffect/stat 修改，但 sendPacketsX8 仍執行）
	// Java 並無「再施放解除」行為（_awakeSkillId 從未被 setAwakeSkillId 設定，L1SkillUse2:1678 條件永遠為 false）
	if skill.SkillID == 185 || skill.SkillID == 190 || skill.SkillID == 195 {
		if !target.HasBuff(skill.SkillID) {
			s.applyBuffEffect(target, skill)
			// 法利昂（190）啟動時同時設定 Physical Power（169）timer（Java AWAKEN_FAFURION skillmode setSkillEffect(169)）
			if skill.SkillID == 190 {
				if sk169 := s.deps.Skills.Get(169); sk169 != nil {
					s.applyBuffEffect(target, sk169)
				}
			}
			handler.SendPlayerStatus(target.Session, target)
			if skill.SysMsgHappen > 0 {
				handler.SendServerMessage(target.Session, uint16(skill.SysMsgHappen))
			}
		}
		// 無論首次或再施放都廣播音效（Java skillmode sendPacketsX8）
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
		}
		return
	}

	if hasteSkillTargetBlockedLikeYiwei(target, skill.SkillID) {
		return
	}
	if slowSkillTargetBlockedLikeYiwei(target, skill.SkillID) {
		return
	}
	if s.handleOppositeMoveSpeedSkill(target, skill.SkillID) {
		return
	}

	// 治療效果
	if skill.Type == 16 || skill.DamageValue > 0 || skill.DamageDice > 0 {
		casterINT := int(player.Intel)
		casterLawful := int(player.Lawful)

		if skill.Area == -1 {
			// 範圍治療
			for _, p := range nearby {
				heal := int32(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterLawful, 10))
				heal = s.applyElfWaterHealingModifiers(p, heal)
				applyPlayerHealDeltaLikeJava(s.deps, p, heal)
			}
		} else {
			// 單目標治療
			heal := int32(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterLawful, 10))
			heal = s.applyElfWaterHealingModifiers(target, heal)
			applyPlayerHealDeltaLikeJava(s.deps, target, heal)
		}
	}

	// 套用 buff 效果
	s.applyBuffEffect(target, skill)

	// 174 STRIKER_GALE：Java `L1PcInstance.getEr()` 第 3396-3398 行對持有 STRIKER_GALE 的玩家
	// 直接 `return 0`，L1SkillUse2 第 1498 行通用 cast 路徑送出 `S_PacketBox(UPDATE_ER, getEr())`
	// 使客戶端 ER 顯示變 0。Go 端 player.Dodge 為儲存值，無 getter override，需在 174 套用後
	// 顯式送 UPDATE_ER(0)。對齊 Java 行為。
	if skill.SkillID == 174 && target.Session != nil {
		handler.SendUpdateER(target.Session, calcPlayerErLikeYiwei(target))
	}

	// 158 NATURES_TOUCH（生命之泉）：Java `L1SkillUse.java:2113-2118` 在恢復系技能清單
	// (HEAL/EXTRA_HEAL/GREATER_HEAL/FULL_HEAL/HEAL_ALL/NATURES_TOUCH/NATURES_BLESSING)
	// cast 後 `cha.removeSkillEffect(WATER_LIFE)`。其他 HEAL 類 1/19/35/49/57/164 yaml type=16
	// 走 `applyElfWaterHealingModifiers` 已處理；158 yaml type=2（buff，HPR aura），無 heal 計算
	// 路徑，需顯式在 buff cast 後移除 WATER_LIFE。
	if skill.SkillID == 158 && target.HasBuff(170) {
		s.removeBuffAndRevert(target, 170)
	}

	// 效果 GFX
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
	}

	if skill.SysMsgHappen > 0 {
		handler.SendServerMessage(target.Session, uint16(skill.SysMsgHappen))
	}
	s.sendYiweiPostCastStatusRefresh(target)
}
