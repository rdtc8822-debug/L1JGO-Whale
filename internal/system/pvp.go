package system

import (
	"fmt"
	"math/rand"

	"github.com/l1jgo/server/internal/core/event"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// PvPSystem 負責 PvP 戰鬥邏輯（攻擊、粉紅名、善惡值、PK 擊殺與掉落）。
// 實作 handler.PvPManager 介面。
type PvPSystem struct {
	deps *handler.Deps
}

// NewPvPSystem 建立 PvP 系統。
func NewPvPSystem(deps *handler.Deps) *PvPSystem {
	return &PvPSystem{deps: deps}
}

func buildPvPMeleeCombatContext(attacker, target *world.PlayerInfo, weaponDmg int) scripting.CombatContext {
	return scripting.CombatContext{
		AttackerLevel:   int(attacker.Level),
		AttackerSTR:     int(attacker.Str),
		AttackerBaseSTR: calcPlayerBaseStrLikeJava(attacker),
		AttackerDEX:     int(attacker.Dex),
		AttackerWeapon:  weaponDmg,
		AttackerHitMod:  int(attacker.HitMod),
		AttackerDmgMod:  int(attacker.DmgMod),
		TargetAC:        int(target.AC),
		TargetLevel:     int(target.Level),
		TargetMR:        0,
		TargetClassType: int(target.ClassType),
	}
}

func buildPvPRangedCombatContext(attacker, target *world.PlayerInfo, bowDmg, arrowDmg int) scripting.RangedCombatContext {
	return scripting.RangedCombatContext{
		AttackerLevel:     int(attacker.Level),
		AttackerSTR:       int(attacker.Str),
		AttackerDEX:       int(attacker.Dex),
		AttackerBaseDEX:   calcPlayerBaseDexLikeJava(attacker),
		AttackerBowDmg:    bowDmg,
		AttackerArrowDmg:  arrowDmg,
		AttackerBowHitMod: int(attacker.BowHitMod),
		AttackerBowDmgMod: int(attacker.BowDmgMod),
		TargetAC:          int(target.AC),
		TargetLevel:       int(target.Level),
		TargetMR:          0,
		TargetClassType:   int(target.ClassType),
		TargetDodge:       int(calcPlayerErLikeYiwei(target)),
	}
}

// HandlePvPAttack 處理近戰 PvP 攻擊。
// Java: L1PcInstance.onAction() — Ctrl+左鍵攻擊玩家，3.80C 無 PK 模式開關。
func (s *PvPSystem) HandlePvPAttack(attacker, target *world.PlayerInfo) {
	if target.Dead {
		return
	}
	if attacker.ShowID != target.ShowID {
		return
	}

	attacker.Heading = handler.CalcHeading(attacker.X, attacker.Y, target.X, target.Y)

	// 目標絕對屏障：免疫所有傷害（Java: L1AttackPc.dmg0 — AbsoluteBarrier 返回 true）
	if target.AbsoluteBarrier {
		nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)
		for _, viewer := range nearby {
			handler.SendAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading)
		}
		return
	}

	// 安全區內只播放動畫，不造成傷害（Java: isSafetyZone 檢查）
	if s.inSafetyZone(attacker) || s.inSafetyZone(target) {
		nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)
		for _, viewer := range nearby {
			handler.SendAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading)
		}
		return
	}

	// 被攻擊時解除睡眠（Java: L1PcInstance.receiveDamage → wakeUp）
	if target.Sleeped {
		s.breakPlayerSleep(target)
	}

	s.triggerPinkName(attacker, target)

	// 近戰傷害計算
	weaponDmg := 4 // 空手
	weaponType := ""
	if wpn := attacker.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			weaponType = info.Type
			if info.DmgSmall > 0 {
				weaponDmg = info.DmgSmall
			}
		}
	}

	ctx := buildPvPMeleeCombatContext(attacker, target, weaponDmg)
	result := s.deps.Scripting.CalcMeleeAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}
	if damage > 0 {
		s.applyDragonKnightWeaknessFromMelee(attacker, target.CharID)
	}

	// 精準目標（skill 113）增傷（Java: L1AttackPc.java:1509-1511 + 1580-1584）：
	//   line 1509: `dmg *= ConfigSkill.STRIKER_DMG (1.2)`
	//   line 1580: `dmg *= (attackerLv / 15) / 100 + 1.01D`
	// 兩段同條件 `_targetPc.hasSkillEffect(TRUE_TARGET)`，分散在 calcPcDamage 但都在主要減免（line 1590 ArmorR 等）前。
	if damage > 0 && target.HasBuff(113) {
		damage = int32(float64(damage) * 1.2)
		damage = int32(float64(damage) * (float64(attacker.Level)/1500.0 + 1.01))
	}
	// 破壞盔甲傷害倍率（Java: L1AttackPc.java:1516-1518 — `_targetPc.hasSkillEffect(ARMOR_BREAK) && isShortDistance()`
	// 才套用 ConfigSkill.ARMOR_BREAK_DMG(1.58)；`isShortDistance()` 等價於 `_weaponType != 20 && _weaponType != 62`
	// 即排除 bow(20) 與 claw(62)）。
	if damage > 0 && target.HasBuff(112) && weaponType != "bow" && weaponType != "claw" {
		damage = int32(float64(damage) * 1.58)
	}
	damage = darkElfPhysicalDamage(attacker, damage, weaponType)
	damage = elfMeleeDamage(attacker, damage, weaponType)
	damage = braveAuraDamage(attacker, damage)
	damage = applyDollRandomDamageAdd(attacker, damage)
	damage = applyImmuneToHarmDamage(target, damage)
	damage = applyReductionArmorDamage(target, damage, true)

	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)

	// 燃燒擊砍（182）：一次性 +10 + 廣播 S_EffectLocation + 消耗 buff（Java L1AttackPc.calcBuffDamage:2434-2438）
	if damage > 0 {
		newDmg, consumed := burningSlashDamage(s.deps, attacker, damage, weaponType)
		damage = newDmg
		if consumed {
			handler.BroadcastToPlayers(nearby, buildEffectLocation(target.X, target.Y, 6591))
		}
	}

	// 反擊屏障（skill 91）：PvP 近戰機率反彈（Java: L1AttackPc.calcCounterBarrierDamage）
	// Java probability 公式（L1MagicPc.java:670-674 case COUNTER_BARRIER）：
	//   probability = l1skills.probabilityValue (SQL=25) + (target.Level - attacker.Level) + COUNTER_BARRIER_ROM (33)
	// 即 58 + lvlDiff。yiwei 預設 `各職業技能相關設置.properties:13 COUNTER_BARRIER_ROM = 33`，
	// skill 91 SQL probability_value=25。
	if damage > 0 && target.HasBuff(91) {
		prob := 25 + int(target.Level) - int(attacker.Level) + 33
		if world.RandInt(100)+1 <= prob {
			cbDmg := s.calcCounterBarrierDmg(target)
			// Java `L1AttackPc.commitCounterBarrier()` 第 3339-3341 行：若攻擊者持有 IMMUNE_TO_HARM(68)
			// 反彈傷害減半。Go 套用同一濾鏡（applyImmuneToHarmDamage 以接受傷害者為對象）。
			cbDmg = applyImmuneToHarmDamage(attacker, cbDmg)
			if cbDmg > 0 {
				attacker.HP -= int32(cbDmg)
				if attacker.HP < 0 {
					attacker.HP = 0
				}
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, 10710))
				handler.SendHpUpdate(attacker.Session, attacker)
				damage = 0 // 反彈後原傷害歸零
				if attacker.HP <= 0 {
					s.deps.Death.KillPlayer(attacker)
				}
			}
		}
	}

	// 致命身軀（skill 191）：PvP 近戰 23% 機率反彈 40 傷害
	// 對齊 Java `L1PcInstance.java:2775-2798`：在 CounterBarrier 後檢查（`if (!isCounterBarrier)`），
	// 若觸發 → 攻擊者扣 40 HP（聖界 68 減半），原始傷害歸零。
	if newDmg, reflected := mortalBodyReflectPvP(target, attacker, damage, nearby); reflected {
		damage = newDmg
		if attacker.HP <= 0 {
			s.deps.Death.KillPlayer(attacker)
		}
	}

	for _, viewer := range nearby {
		handler.SendAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading)
	}

	// 浮動傷害數字（PvP 近戰）
	if attacker.AttackView {
		handler.SendDamageNumbers(attacker.Session, target.CharID, damage)
	}

	// 疼痛的歡愉（218）：攻擊者持有 buff 時依 target 既有失血量反傷攻擊者
	// Java `L1PcInstance.receiveDamage:2737-2773` 在 PC→PC 所有傷害源（含 melee）觸發
	if damage > 0 && s.deps.Skill != nil {
		s.deps.Skill.ApplyJoyOfPainBacklash(attacker, target, nearby)
	}

	if damage > 0 {
		target.HP -= int32(damage)
		if target.HP < 0 {
			target.HP = 0
		}
		handler.SendHpUpdate(target.Session, target)

		// 尖刺盔甲（skill 89）：PvP 近戰命中時 10% 機率破壞攻擊者武器
		// Java: L1AttackPc.damagePcWeaponDurability — hasSkillEffect(89) → 10% → receiveDamage
		if target.HasBuff(89) && world.RandInt(100) < 10 {
			damagePlayerWeaponDurability(attacker, s.deps)
		}

		// 武器附毒（skill 98）：PvP 近戰命中時 10% 機率對目標施加毒素
		// Java: L1AttackPc.addPcPoisonAttack — hasSkillEffect(98) → 10% → doInfection(3000, 5)
		applyEnchantVenomPoisonToPlayer(attacker, target, s.deps)

		if target.HP <= 0 {
			// 在 KillPlayer 之前保存決鬥狀態（KillPlayer 會清除 FightId）
			isDuel := attacker.FightId == target.CharID && target.FightId == attacker.CharID
			s.deps.Death.KillPlayer(target)
			if !isDuel {
				s.processPKKill(attacker, target)
			}
		}
	}
}

// HandlePvPFarAttack 處理遠程 PvP 攻擊。
// Java: L1PcInstance.onAction() — Ctrl+左鍵遠程攻擊玩家。
func (s *PvPSystem) HandlePvPFarAttack(attacker, target *world.PlayerInfo) {
	if target.Dead {
		return
	}
	if attacker.ShowID != target.ShowID {
		return
	}

	attacker.Heading = handler.CalcHeading(attacker.X, attacker.Y, target.X, target.Y)

	// 距離判定
	dx := attacker.X - target.X
	dy := attacker.Y - target.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	dist := dx
	if dy > dist {
		dist = dy
	}
	if dist > 10 {
		return
	}

	// 目標絕對屏障：免疫所有傷害
	if target.AbsoluteBarrier {
		handler.SendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
		nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, attacker.SessionID, target.ShowID)
		for _, viewer := range nearby {
			handler.SendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
				attacker.X, attacker.Y, target.X, target.Y)
		}
		return
	}

	// 安全區內只播放動畫
	if s.inSafetyZone(attacker) || s.inSafetyZone(target) {
		handler.SendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
		nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, attacker.SessionID, target.ShowID)
		for _, viewer := range nearby {
			handler.SendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
				attacker.X, attacker.Y, target.X, target.Y)
		}
		return
	}

	// 被攻擊時解除睡眠
	if target.Sleeped {
		s.breakPlayerSleep(target)
	}

	s.triggerPinkName(attacker, target)

	// 消耗箭矢
	arrow := handler.FindArrow(attacker, s.deps)
	if arrow == nil {
		handler.SendGlobalChat(attacker.Session, 9, "\\f3沒有箭矢。")
		return
	}
	arrowRemoved := attacker.Inv.RemoveItem(arrow.ObjectID, 1)
	if arrowRemoved {
		handler.SendRemoveInventoryItem(attacker.Session, arrow.ObjectID)
	} else {
		handler.SendItemCountUpdate(attacker.Session, arrow)
	}

	arrowDmg := 0
	if arrowInfo := s.deps.Items.Get(arrow.ItemID); arrowInfo != nil {
		arrowDmg = arrowInfo.DmgSmall
	}

	bowDmg := 1
	if wpn := attacker.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			if info.DmgSmall > 0 {
				bowDmg = info.DmgSmall
			}
		}
	}

	ctx := buildPvPRangedCombatContext(attacker, target, bowDmg, arrowDmg)
	result := s.deps.Scripting.CalcRangedAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}
	damage = strikerGaleRangedDamage(target, damage)
	// 精準目標（skill 113）增傷（Java: L1AttackPc.java:1509-1511 + 1580-1584）：
	// calcPcDamage 涵蓋近戰與遠程，兩段同條件 _targetPc.hasSkillEffect(TRUE_TARGET)。
	//   line 1509: dmg *= ConfigSkill.STRIKER_DMG (1.2)
	//   line 1580: dmg *= (attackerLv / 15) / 100 + 1.01
	if damage > 0 && target.HasBuff(113) {
		damage = int32(float64(damage) * 1.2)
		damage = int32(float64(damage) * (float64(attacker.Level)/1500.0 + 1.01))
	}
	// 黑妖燃燒鬥志（102）：Java L1AttackPc.BuffDmgUp 在 calcPcDamage 涵蓋近戰與遠程，
	// 弓矢攻擊命中時 15% 機率 1.5x 傷害；DOUBLE_BREAK 由 doubleBreakChance("bow")=0 自然排除。
	damage = darkElfPhysicalDamage(attacker, damage, "bow")
	damage = braveAuraDamage(attacker, damage)
	damage = applyDollRandomDamageAdd(attacker, damage)
	damage = applyImmuneToHarmDamage(target, damage)
	damage = applyReductionArmorDamage(target, damage, true)

	handler.SendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, damage, attacker.Heading,
		attacker.X, attacker.Y, target.X, target.Y)
	nearby := s.deps.World.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, attacker.SessionID, target.ShowID)
	for _, viewer := range nearby {
		handler.SendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
	}

	// 浮動傷害數字（PvP 遠程）
	if attacker.AttackView {
		handler.SendDamageNumbers(attacker.Session, target.CharID, damage)
	}

	// 疼痛的歡愉（218）：攻擊者持有 buff 時依 target 既有失血量反傷攻擊者
	// Java `L1PcInstance.receiveDamage:2737-2773` 在 PC→PC 所有傷害源（含 ranged）觸發
	if damage > 0 && s.deps.Skill != nil {
		s.deps.Skill.ApplyJoyOfPainBacklash(attacker, target, nearby)
	}

	if damage > 0 {
		target.HP -= int32(damage)
		if target.HP < 0 {
			target.HP = 0
		}
		handler.SendHpUpdate(target.Session, target)

		// 武器附毒（skill 98）：玩家遠程命中時 10% 機率對目標施加傷害毒
		// Java: L1AttackPc.addPcPoisonAttack（L1AttackPc 涵蓋近戰與遠程）→ L1DamagePoison.doInfection(3000, 5)
		applyEnchantVenomPoisonToPlayer(attacker, target, s.deps)

		if target.HP <= 0 {
			isDuel := attacker.FightId == target.CharID && target.FightId == attacker.CharID
			s.deps.Death.KillPlayer(target)
			if !isDuel {
				s.processPKKill(attacker, target)
			}
		}
	}
}

// AddLawfulFromNpc 根據 NPC 善惡值增加擊殺者的善惡值。
// Java: add_lawful = npc.lawful * RATE_LA * -1
func (s *PvPSystem) AddLawfulFromNpc(killer *world.PlayerInfo, npcLawful int32) {
	if npcLawful == 0 {
		return
	}
	rate := s.deps.Config.Rates.LawfulRate
	if rate <= 0 {
		rate = 1.0
	}
	addLawful := int32(float64(npcLawful) * rate * -1)
	if addLawful == 0 {
		return
	}
	killer.Lawful += addLawful
	handler.ClampLawful(&killer.Lawful)

	handler.SendLawful(killer.Session, killer.CharID, killer.Lawful)
	nearby := s.deps.World.GetNearbyPlayersInShow(killer.X, killer.Y, killer.MapID, killer.SessionID, killer.ShowID)
	for _, other := range nearby {
		handler.SendLawful(other.Session, killer.CharID, killer.Lawful)
	}
}

// ========================================================================
//  內部函式
// ========================================================================

// inSafetyZone 檢查玩家是否在安全區。
func (s *PvPSystem) inSafetyZone(p *world.PlayerInfo) bool {
	if s.deps.MapData == nil {
		return false
	}
	return s.deps.MapData.IsSafetyZone(p.MapID, p.X, p.Y)
}

func (s *PvPSystem) TriggerPinkName(attacker, victim *world.PlayerInfo) {
	if attacker == nil || victim == nil {
		return
	}
	s.triggerPinkName(attacker, victim)
}

// triggerPinkName 攻擊藍名玩家時觸發粉紅名。
func (s *PvPSystem) triggerPinkName(attacker, victim *world.PlayerInfo) {
	// 決鬥對象不觸發粉紅名（Java: L1PinkName.onAction 決鬥豁免）
	if attacker.FightId == victim.CharID {
		return
	}
	if attacker.PinkName {
		return
	}
	if attacker.Lawful < 0 {
		return
	}
	if victim.Lawful < 0 || victim.PinkName {
		return
	}

	attacker.PinkName = true
	attacker.PinkNameTicks = s.deps.Scripting.GetPKTimers().PinkNameTicks

	handler.SendPinkName(attacker.Session, attacker.CharID, 180)
	nearby := s.deps.World.GetNearbyPlayersInShow(attacker.X, attacker.Y, attacker.MapID, attacker.SessionID, attacker.ShowID)
	for _, other := range nearby {
		handler.SendPinkName(other.Session, attacker.CharID, 180)
	}

	// 通知附近守衛
	nearbyNpcs := s.deps.World.GetNearbyNpcsInShow(attacker.X, attacker.Y, attacker.MapID, attacker.ShowID)
	for _, guard := range nearbyNpcs {
		if guard.Impl == "L1Guard" && !guard.Dead && guard.AggroTarget == 0 {
			guard.AggroTarget = attacker.SessionID
		}
	}
}

// processPKKill 處理 PK 擊殺後果（PK 次數、善惡值、物品掉落）。
func (s *PvPSystem) processPKKill(killer, victim *world.PlayerInfo) {
	// 取消粉紅名
	if killer.PinkName {
		killer.PinkName = false
		killer.PinkNameTicks = 0
		handler.SendPinkName(killer.Session, killer.CharID, 0)
		nearby := s.deps.World.GetNearbyPlayersInShow(killer.X, killer.Y, killer.MapID, killer.SessionID, killer.ShowID)
		for _, other := range nearby {
			handler.SendPinkName(other.Session, killer.CharID, 0)
		}
	}

	// 只有受害者是藍名（非粉紅）才算 PK
	if victim.Lawful >= 0 && !victim.PinkName {
		killer.WantedTicks = s.deps.Scripting.GetPKTimers().WantedTicks

		if killer.Lawful < 30000 {
			killer.PKCount++
		}

		pkResult := s.deps.Scripting.CalcPKLawfulPenalty(int(killer.Level), killer.Lawful)
		killer.Lawful = pkResult.NewLawful

		handler.SendLawful(killer.Session, killer.CharID, killer.Lawful)
		nearby := s.deps.World.GetNearbyPlayersInShow(killer.X, killer.Y, killer.MapID, killer.SessionID, killer.ShowID)
		for _, other := range nearby {
			handler.SendLawful(other.Session, killer.CharID, killer.Lawful)
		}

		handler.SendPlayerStatus(killer.Session, killer)

		pkThresh := s.deps.Scripting.GetPKThresholds()
		if killer.PKCount >= pkThresh.Warning && killer.PKCount < pkThresh.Punish {
			handler.SendRedMessage(killer.Session, 551, fmt.Sprintf("%d", killer.PKCount), fmt.Sprintf("%d", pkThresh.Punish))
		}

		// Karma 修改（Java: PK 藍名玩家 → karma 下降）
		killer.Karma -= 100
		handler.SendKarma(killer.Session, killer.Karma)

		s.deps.Log.Info(fmt.Sprintf("PK 擊殺  擊殺者=%s  受害者=%s  PK次數=%d  正義值=%d  善惡值=%d", killer.Name, victim.Name, killer.PKCount, killer.Lawful, killer.Karma))
	}

	// 發出 PlayerKilled 事件
	if s.deps.Bus != nil {
		event.Emit(s.deps.Bus, event.PlayerKilled{
			KillerCharID: killer.CharID,
			VictimCharID: victim.CharID,
			MapID:        victim.MapID,
			X:            victim.X,
			Y:            victim.Y,
		})
	}

	s.dropItemsOnPKDeath(victim)

	// 擊殺公告（Java: S_GreenMessage — 受害者等級 ≥ KillMessageLevel 時全伺服器廣播）
	s.broadcastKillMessage(killer, victim)
}

// dropItemsOnPKDeath 根據 Lua 公式從受害者身上掉落物品。
func (s *PvPSystem) dropItemsOnPKDeath(victim *world.PlayerInfo) {
	dropResult := s.deps.Scripting.CalcPKItemDrop(victim.Lawful)
	if !dropResult.ShouldDrop || dropResult.Count <= 0 {
		return
	}

	for i := 0; i < dropResult.Count; i++ {
		s.dropOneItem(victim)
	}
}

// dropOneItem 隨機選擇一個物品從受害者背包掉落到地面。
func (s *PvPSystem) dropOneItem(victim *world.PlayerInfo) {
	if len(victim.Inv.Items) == 0 {
		return
	}

	idx := rand.Intn(len(victim.Inv.Items))
	item := victim.Inv.Items[idx]

	if item.ItemID == world.AdenaItemID {
		return
	}

	itemInfo := s.deps.Items.Get(item.ItemID)
	if itemInfo == nil || !itemInfo.Tradeable {
		return
	}

	// 脫裝備
	if item.Equipped {
		slot := s.deps.Equip.FindEquippedSlot(victim, item)
		if slot != world.SlotNone {
			s.deps.Equip.UnequipSlot(victim.Session, victim, slot)
		}
	}

	dropCount := int32(1)
	if item.Stackable && item.Count > 0 {
		dropCount = item.Count
	}

	gndItem := &world.GroundItem{
		ID:         item.ObjectID,
		ItemID:     item.ItemID,
		Count:      dropCount,
		EnchantLvl: item.EnchantLvl,
		Name:       itemInfo.Name,
		GrdGfx:     itemInfo.GrdGfx,
		X:          victim.X,
		Y:          victim.Y,
		MapID:      victim.MapID,
		ShowID:     victim.ShowID,
	}
	s.deps.World.AddGroundItem(gndItem)

	victim.Inv.RemoveItem(item.ObjectID, 0)
	handler.SendRemoveInventoryItem(victim.Session, item.ObjectID)
	handler.SendWeightUpdate(victim.Session, victim)

	nearby := s.deps.World.GetNearbyPlayersInShow(victim.X, victim.Y, victim.MapID, 0, victim.ShowID)
	for _, viewer := range nearby {
		handler.SendDropItem(viewer.Session, gndItem)
	}

	handler.SendServerMessageStr(victim.Session, 638, itemInfo.Name)

	s.deps.Log.Info(fmt.Sprintf("PK 死亡掉落物品  受害者=%s  道具=%s  數量=%d", victim.Name, itemInfo.Name, dropCount))
}

// breakPlayerSleep 被攻擊時解除玩家睡眠狀態（Java: L1PcInstance.wakeUp）。
func (s *PvPSystem) breakPlayerSleep(target *world.PlayerInfo) {
	target.Sleeped = false
	target.RemoveBuff(62)  // 沉睡之霧（視覺）
	target.RemoveBuff(66)  // 沉睡之霧
	target.RemoveBuff(103) // 暗黑盲咒
	handler.SendParalysis(target.Session, handler.SleepRemove)
}

// broadcastKillMessage 全伺服器廣播 PvP 擊殺訊息。
// Java: S_GreenMessage — 受害者等級 ≥ KillMessageLevel 時觸發。
// 訊息包含殺手名稱、武器名稱與強化等級。
func (s *PvPSystem) broadcastKillMessage(killer, victim *world.PlayerInfo) {
	minLevel := s.deps.Config.Gameplay.KillMessageLevel
	if minLevel <= 0 {
		return // 功能關閉
	}
	if int(victim.Level) < minLevel {
		return
	}

	// 組裝武器資訊
	weaponName := "空手"
	enchantLvl := 0
	if wpn := killer.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			weaponName = info.Name
			enchantLvl = int(wpn.EnchantLvl)
		}
	}

	// Java 格式：\\f3殺人公告:\\f2☆★強者\\f3【殺手】使用武器【+N武器名】\\f2將☆★可憐的弱者\\f3【受害者】\\f2給打趴在地上★☆
	msg := fmt.Sprintf("\\f3殺人公告:\\f2☆★強者\\f3【%s】使用武器【+%d %s】\\f2將☆★可憐的弱者\\f3【%s】\\f2給打趴在地上★☆",
		killer.Name, enchantLvl, weaponName, victim.Name)

	data := handler.BuildGreenMessage(msg)
	s.deps.World.AllPlayers(func(p *world.PlayerInfo) {
		p.Session.Send(data)
	})
}

// calcCounterBarrierDmg 計算 PvP 反擊屏障的反彈傷害。
// Java: L1AttackMode.calcCounterBarrierDamage（PC 版本）
// 公式：(武器大傷 + 強化等級 + 傷害修正) × 2 × 1.5 倍率
func (s *PvPSystem) calcCounterBarrierDmg(target *world.PlayerInfo) int32 {
	wpn := target.Equip.Weapon()
	if wpn == nil {
		return 0
	}
	info := s.deps.Items.Get(wpn.ItemID)
	if info == nil {
		return 0
	}
	dmg := int32((info.DmgLarge + int(wpn.EnchantLvl) + info.DmgMod) << 1)
	dmg = dmg * 3 / 2 // 倍率 1.5（Java: ConfigSkill.COUNTER_BARRIER_DMG）
	if dmg < 0 {
		dmg = 0
	}
	return dmg
}
