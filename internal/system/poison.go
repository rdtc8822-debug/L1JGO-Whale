package system

import (
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

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
		// 每 15 tick（3 秒）扣血（NPC攻擊:20, 毒咒:5）
		p.PoisonDmgTimer++
		if p.PoisonDmgTimer >= 15 {
			p.PoisonDmgTimer = 0
			dmg := int16(20)
			if p.PoisonDmgAmount > 0 {
				dmg = p.PoisonDmgAmount
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
			p.PoisonTicksLeft = 80 // 16 秒 = 80 ticks
			p.Paralyzed = true
			// 視覺：綠色→灰色
			broadcastPlayerPoison(p, 2, deps) // 灰色
			// S_Paralysis: 怪物麻痺毒施加（Java TYPE_PARALYSIS2, true → 0x04）
			handler.SendParalysis(p.Session, handler.ParalysisMobApply)
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
		handler.SendParalysis(p.Session, handler.ParalysisMobRemove) // 0x05
	}

	// 沉默毒 → 解除沉默
	if p.PoisonType == 2 {
		p.Silenced = false
		handler.SendServerMessage(p.Session, 311) // "毒的效果已經消退了。"
	}

	p.PoisonType = 0
	p.PoisonTicksLeft = 0
	p.PoisonDmgTimer = 0
	p.PoisonDmgAmount = 0
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
	// 已中毒 → 不可再次中毒（Java: getPoison() != null → 拒絕）
	if target.PoisonType != 0 {
		return
	}

	// TODO: 防毒免疫檢查（venom_resist 道具、VENOM_RESIST 技能、DRAGON5 技能）
	// 這些系統尚未實現，預留檢查位

	// 15% 機率觸發（Java: if (15 >= _random.nextInt(100) + 1)）
	if world.RandInt(100) >= 15 {
		return
	}

	switch npc.PoisonAtk {
	case 1: // 傷害毒（Java: L1DamagePoison.doInfection(_npc, target, 3000, 20)）
		target.PoisonType = 1
		target.PoisonTicksLeft = 150 // 30 秒 = 150 ticks
		target.PoisonDmgTimer = 0
		target.PoisonDmgAmount = 20 // NPC 攻擊型傷害毒：每次 20
		target.PoisonAttacker = 0   // NPC 攻擊暫不追蹤歸屬
		broadcastPlayerPoison(target, 1, deps) // 綠色

	case 2: // 沉默毒（Java: L1SilencePoison.doInfection(target)）
		target.PoisonType = 2
		target.PoisonTicksLeft = 0 // 永久（直到解毒）
		target.Silenced = true
		broadcastPlayerPoison(target, 1, deps) // 綠色
		handler.SendServerMessage(target.Session, 310) // "喉嚨受到乾燥，無法發動魔法。"

	case 4: // 麻痺毒延遲（Java: L1ParalysisPoison.doInfection(target, 20000, 16000)）
		target.PoisonType = 3 // 階段一：延遲中
		target.PoisonTicksLeft = 100 // 20 秒 = 100 ticks
		broadcastPlayerPoison(target, 1, deps) // 綠色
		handler.SendServerMessage(target.Session, 212) // "你的身體漸漸麻痺。"
	}
}

// broadcastPlayerPoison 廣播 S_Poison 到附近所有玩家（含自己）。
// Java: setPoisonEffect → broadcastPacketX8(S_Poison)。
// poisonType: 0=治癒, 1=綠色, 2=灰色
func broadcastPlayerPoison(target *world.PlayerInfo, poisonType byte, deps *handler.Deps) {
	// 發給自己
	handler.SendPoison(target.Session, target.CharID, poisonType)
	// 發給附近觀察者
	nearby := deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
	for _, viewer := range nearby {
		handler.SendPoison(viewer.Session, target.CharID, poisonType)
	}
}

// BroadcastPlayerPoison 廣播毒素色調到附近所有玩家。Exported for other system packages.
func BroadcastPlayerPoison(target *world.PlayerInfo, poisonType byte, deps *handler.Deps) {
	broadcastPlayerPoison(target, poisonType, deps)
}
