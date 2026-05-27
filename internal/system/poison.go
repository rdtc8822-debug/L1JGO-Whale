package system

import (
	"time"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

const poisonTickMillis = 200

// --- 毒系統（Java L1DamagePoison / L1SilencePoison / L1ParalysisPoison）---
//
// PoisonType 值：
//   0 = 無毒
//   1 = 傷害毒（每 15 tick 扣 20 HP，總持續 150 tick = 30 秒）
//   2 = 沉默毒（禁止施法，永久直到解毒）
//   3 = 麻痺毒延遲中（綠色色調，可移動，100 tick = 20 秒後轉階段二）
//   4 = 麻痺毒已麻痺（灰色色調，不可移動，80 tick = 16 秒後解除）

// --- 詛咒麻痺系統（Java L1CurseParalysis，與毒系統獨立）---
//
// CurseType 值：
//   0 = 無詛咒
//   1 = 詛咒延遲中（灰色色調，可移動，25 tick = 5 秒後轉階段二）
//   2 = 詛咒已麻痺（灰色色調，不可移動，20 tick = 4 秒後解除）

// TickCatapultSilence 每 tick 檢查投石車沉默砲彈到期。
// 由 BuffTickSystem (Phase 2) 呼叫。
func TickCatapultSilence(p *world.PlayerInfo) {
	if p.CatapultSilenceEnd > 0 && time.Now().Unix() >= p.CatapultSilenceEnd {
		p.CatapultSilenceEnd = 0
		// 只有在不是沉默毒的情況下才解除沉默
		if p.PoisonType != 2 {
			p.Silenced = false
		}
	}
}

// TickPlayerPoison 每 tick 處理玩家的中毒狀態計時。
// 由 BuffTickSystem (Phase 2) 呼叫。
func TickPlayerPoison(p *world.PlayerInfo, deps *handler.Deps) {
	if p.PoisonType == 0 || p.Dead {
		return
	}

	switch p.PoisonType {
	case 1: // 傷害毒
		p.PoisonTicksLeft--
		if p.PoisonTicksLeft <= 0 {
			CurePoison(p, deps)
			return
		}
		// 每 15 tick（3 秒）扣血（NPC攻擊:20, 毒咒:5）；陷阱可帶自己的 damageSpan。
		p.PoisonDmgTimer++
		interval := p.PoisonDmgIntervalTicks
		if interval <= 0 {
			interval = 15
		}
		if p.PoisonDmgTimer >= interval {
			p.PoisonDmgTimer = 0
			dmg := int32(20)
			if p.PoisonDmgAmount > 0 {
				dmg = int32(p.PoisonDmgAmount)
			}
			p.HP -= dmg
			p.Dirty = true
			if p.HP <= 0 {
				p.HP = 0
				CurePoison(p, deps)
				deps.Death.KillPlayer(p)
				return
			}
			handler.SendHpUpdate(p.Session, p)
		}

	case 2: // 沉默毒 — 無計時，永久直到解毒
		// 什麼都不做

	case 3: // 麻痺毒延遲中（綠色，可移動）
		p.PoisonTicksLeft--
		if p.PoisonTicksLeft <= 0 {
			// 進入階段二：真正麻痺
			p.PoisonType = 4
			duration := p.PoisonParalysisTicks
			if duration <= 0 {
				duration = 80
			}
			p.PoisonTicksLeft = duration
			p.PoisonParalysisTicks = 0
			p.Paralyzed = true
			// 視覺：綠色→灰色
			broadcastPlayerPoison(p, 2, deps) // 灰色
			// yiwei L1ParalysisPoison 使用 S_Paralysis(TYPE_PARALYSIS, true)。
			handler.SendParalysis(p.Session, handler.ParalysisApply)
		}

	case 4: // 麻痺毒已麻痺（灰色，不可動）
		p.PoisonTicksLeft--
		if p.PoisonTicksLeft <= 0 {
			CurePoison(p, deps)
		}
	}
}

// TickPlayerCurse 每 tick 處理玩家的詛咒麻痺狀態計時。
// 由 BuffTickSystem (Phase 2) 呼叫。
func TickPlayerCurse(p *world.PlayerInfo, deps *handler.Deps) {
	if p.CurseType == 0 || p.Dead {
		return
	}

	p.CurseTicksLeft--
	if p.CurseTicksLeft <= 0 {
		switch p.CurseType {
		case 1: // 延遲階段到期 → 進入麻痺階段
			p.CurseType = 2
			p.CurseTicksLeft = 20 // 4 秒 = 20 ticks
			p.Paralyzed = true
			handler.SendParalysis(p.Session, handler.ParalysisApply) // 0x02

		case 2: // 麻痺階段到期 → 完全解除
			CureCurseParalysis(p, deps)
		}
	}
}

// CurePoison 解除玩家的毒狀態（Java L1Character.curePoison）。
// 技能 9（解毒術）、技能 37（聖潔之光）、技能 44（魔法相消術）、死亡時呼叫。
func CurePoison(p *world.PlayerInfo, deps *handler.Deps) {
	if p.PoisonType == 0 {
		return
	}

	// 麻痺毒已麻痺 → 解除麻痺（如果沒有其他麻痺來源）
	if p.PoisonType == 4 {
		if !shouldStayParalyzed(p, true, false) {
			p.Paralyzed = false
		}
		handler.SendParalysis(p.Session, handler.ParalysisRemove) // 0x03
	}

	// 沉默毒 → 解除沉默
	if p.PoisonType == 2 {
		p.Silenced = false
		handler.SendServerMessage(p.Session, 311) // "毒的效果已經消退了。"
	}

	p.PoisonType = 0
	p.PoisonTicksLeft = 0
	p.PoisonDmgTimer = 0
	p.PoisonDmgIntervalTicks = 0
	p.PoisonDmgAmount = 0
	p.PoisonParalysisTicks = 0
	p.PoisonAttacker = 0

	// 清除色調
	broadcastPlayerPoison(p, 0, deps)
}

// CureCurseParalysis 解除玩家的詛咒麻痺（Java L1Character.cureParalaysis）。
// 技能 37（聖潔之光）、技能 44（魔法相消術）、死亡時呼叫。
func CureCurseParalysis(p *world.PlayerInfo, deps *handler.Deps) {
	if p.CurseType == 0 {
		return
	}

	// 詛咒已麻痺 → 解除麻痺（如果沒有其他麻痺來源）
	if p.CurseType == 2 {
		if !shouldStayParalyzed(p, false, true) {
			p.Paralyzed = false
		}
		handler.SendParalysis(p.Session, handler.ParalysisRemove) // 0x03
	}

	p.CurseType = 0
	p.CurseTicksLeft = 0

	// 清除灰色色調
	broadcastPlayerPoison(p, 0, deps)
}

// shouldStayParalyzed 檢查清除某個麻痺來源後，是否仍應保持 Paralyzed=true。
// skipPoison=true 表示正在清除毒麻痺，skipCurse=true 表示正在清除詛咒麻痺。
func shouldStayParalyzed(p *world.PlayerInfo, skipPoison, skipCurse bool) bool {
	if !skipPoison && p.PoisonType == 4 {
		return true
	}
	if !skipCurse && p.CurseType == 2 {
		return true
	}
	for _, b := range p.ActiveBuffs {
		if b.SetParalyzed {
			return true
		}
	}
	return false
}

// ApplyNpcPoisonAttack 怪物攻擊後的施毒判定（Java L1AttackNpc.addNpcPoisonAttack）。
// 15% 機率觸發，單毒限制（已中毒不可再次中毒）。
func ApplyNpcPoisonAttack(npc *world.NpcInfo, target *world.PlayerInfo, ws *world.State, deps *handler.Deps) {
	ApplyNpcPoisonAttackWithRoll(npc, target, deps, world.RandInt(100))
}

func ApplyNpcPoisonAttackWithRoll(npc *world.NpcInfo, target *world.PlayerInfo, deps *handler.Deps, roll int) {
	// 已中毒 → 不可再次中毒（Java: getPoison() != null → 拒絕）
	if !canApplyPoisonToPlayer(target) {
		return
	}

	// 15% 機率觸發（Java: if (15 >= _random.nextInt(100) + 1)）
	if roll >= 15 {
		return
	}

	switch npc.PoisonAtk {
	case 1: // 傷害毒（Java: L1DamagePoison.doInfection(_npc, target, 3000, 20)）
		applyDamagePoisonToPlayer(target, 0, 20, deps)
	case 2: // 沉默毒（Java: L1SilencePoison.doInfection(target)）
		applySilencePoisonToPlayer(target, deps)
	case 4: // 麻痺毒延遲（Java: L1ParalysisPoison.doInfection(target, 20000, 16000)）
		applyParalysisPoisonToPlayer(target, deps)
	}
}

// applySilencePoisonToPlayer 對玩家施加沉默毒（永久直到解毒）。
// Java: L1SilencePoison.doInfection。
func applySilencePoisonToPlayer(target *world.PlayerInfo, deps *handler.Deps) {
	target.PoisonType = 2
	target.PoisonTicksLeft = 0
	target.Silenced = true
	broadcastPlayerPoison(target, 1, deps)
	handler.SendServerMessage(target.Session, 310) // "喉嚨受到乾燥，無法發動魔法。"
}

// applyParalysisPoisonToPlayer 對玩家施加麻痺毒延遲階段（20 秒延遲後麻痺 16 秒）。
// Java: L1ParalysisPoison.doInfection(target, 20000, 16000)。
func applyParalysisPoisonToPlayer(target *world.PlayerInfo, deps *handler.Deps) {
	applyParalysisPoisonToPlayerWithTiming(target, 20000, 16000, deps)
}

func applyParalysisPoisonToPlayerWithTiming(target *world.PlayerInfo, delayMillis, timeMillis int32, deps *handler.Deps) {
	target.PoisonType = 3 // 階段一：延遲中
	target.PoisonTicksLeft = poisonTicksFromMillis(delayMillis, 100)
	target.PoisonParalysisTicks = poisonTicksFromMillis(timeMillis, 80)
	broadcastPlayerPoison(target, 1, deps)
	handler.SendServerMessage(target.Session, 212) // "你的身體漸漸麻痺。"
}

func applyEnchantVenomPoisonToPlayer(attacker, target *world.PlayerInfo, deps *handler.Deps) bool {
	return applyEnchantVenomPoisonToPlayerWithRoll(attacker, target, deps, world.RandInt(100))
}

func applyEnchantVenomPoisonToPlayerWithRoll(attacker, target *world.PlayerInfo, deps *handler.Deps, roll int) bool {
	if attacker == nil || target == nil || !attacker.HasBuff(98) || attacker.Equip.Weapon() == nil || roll >= 10 {
		return false
	}
	return applyDamagePoisonToPlayer(target, attacker.SessionID, 5, deps)
}

func applyEnchantVenomPoisonToNpc(attacker *world.PlayerInfo, npc *world.NpcInfo, deps *handler.Deps) bool {
	return applyEnchantVenomPoisonToNpcWithRoll(attacker, npc, deps, world.RandInt(100))
}

func applyEnchantVenomPoisonToNpcWithRoll(attacker *world.PlayerInfo, npc *world.NpcInfo, deps *handler.Deps, roll int) bool {
	if attacker == nil || npc == nil || !attacker.HasBuff(98) || attacker.Equip.Weapon() == nil || roll >= 10 {
		return false
	}
	return applyDamagePoisonToNpc(npc, attacker.SessionID, 5, deps)
}

func applyDamagePoisonToPlayer(target *world.PlayerInfo, attackerSID uint64, amount int16, deps *handler.Deps) bool {
	return applyDamagePoisonToPlayerWithInterval(target, attackerSID, amount, 3000, deps)
}

func applyDamagePoisonToPlayerWithInterval(target *world.PlayerInfo, attackerSID uint64, amount int16, damageSpanMillis int32, deps *handler.Deps) bool {
	if !canApplyPoisonToPlayer(target) {
		return false
	}
	target.PoisonType = 1
	target.PoisonTicksLeft = 150
	target.PoisonDmgTimer = 0
	target.PoisonDmgIntervalTicks = poisonTicksFromMillis(damageSpanMillis, 15)
	target.PoisonDmgAmount = amount
	target.PoisonAttacker = attackerSID
	broadcastPlayerPoison(target, 1, deps)
	return true
}

func poisonTicksFromMillis(milliseconds int32, fallback int) int {
	if milliseconds <= 0 {
		return fallback
	}
	ticks := int(milliseconds) / poisonTickMillis
	if ticks <= 0 {
		return 1
	}
	return ticks
}

func applyDamagePoisonToNpc(npc *world.NpcInfo, attackerSID uint64, amount int32, deps *handler.Deps) bool {
	if npc == nil || npc.Dead || npc.PoisonDmgAmt > 0 {
		return false
	}
	npc.PoisonDmgAmt = amount
	npc.PoisonDmgTimer = 0
	npc.PoisonAttackerSID = attackerSID
	if deps != nil && deps.World != nil {
		nearby := nearbyNpcPoisonX8Viewers(deps.World, npc)
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 1))
	}
	return true
}

func nearbyNpcPoisonX8Viewers(ws *world.State, npc *world.NpcInfo) []*world.PlayerInfo {
	if ws == nil || npc == nil {
		return nil
	}
	return ws.GetNearbyPlayersInShowRange(npc.X, npc.Y, npc.MapID, 0, npc.ShowID, 8)
}

func canApplyPoisonToPlayer(target *world.PlayerInfo) bool {
	if target == nil || target.PoisonType != 0 {
		return false
	}
	return !hasPoisonResistance(target)
}

func hasPoisonResistance(target *world.PlayerInfo) bool {
	return target != nil && (target.HasBuff(104) || target.HasBuff(6687))
}

// broadcastPlayerPoison 廣播 S_Poison 到附近所有玩家（含自己）。
// Java: setPoisonEffect → broadcastPacketX8(S_Poison)。
// poisonType: 0=治癒, 1=綠色, 2=灰色
func broadcastPlayerPoison(target *world.PlayerInfo, poisonType byte, deps *handler.Deps) {
	data := handler.BuildPoison(target.CharID, poisonType)
	// 發給自己
	target.Session.Send(data)
	// 發給附近觀察者
	nearby := deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, target.SessionID, target.ShowID)
	handler.BroadcastToPlayers(nearby, data)
}

// BroadcastPlayerPoison 廣播毒素色調到附近所有玩家。Exported for other system packages.
func BroadcastPlayerPoison(target *world.PlayerInfo, poisonType byte, deps *handler.Deps) {
	broadcastPlayerPoison(target, poisonType, deps)
}

// GMApplyPoison GM 指令用：對玩家施加指定毒類型，效果與怪物施毒完全等同。
// ptype 對應 npc.PoisonAtk: 1=傷害毒, 2=沉默毒（卡司特毒）, 4=麻痺毒延遲。
// 已中毒或具毒抵抗時回傳 false（與怪物施毒邏輯一致）。
func GMApplyPoison(target *world.PlayerInfo, ptype byte, deps *handler.Deps) bool {
	if !canApplyPoisonToPlayer(target) {
		return false
	}
	switch ptype {
	case 1:
		return applyDamagePoisonToPlayer(target, 0, 20, deps)
	case 2:
		applySilencePoisonToPlayer(target, deps)
		return true
	case 4:
		applyParalysisPoisonToPlayer(target, deps)
		return true
	}
	return false
}
