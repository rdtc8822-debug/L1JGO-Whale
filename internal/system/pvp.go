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

// HandlePvPAttack 處理近戰 PvP 攻擊。
// Java: L1PcInstance.onAction() — Ctrl+左鍵攻擊玩家，3.80C 無 PK 模式開關。
func (s *PvPSystem) HandlePvPAttack(attacker, target *world.PlayerInfo) {
	if target.Dead {
		return
	}

	attacker.Heading = handler.CalcHeading(attacker.X, attacker.Y, target.X, target.Y)

	// 目標絕對屏障：免疫所有傷害（Java: L1AttackPc.dmg0 — AbsoluteBarrier 返回 true）
	if target.AbsoluteBarrier {
		nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
		for _, viewer := range nearby {
			handler.SendAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading)
		}
		return
	}

	// 安全區內只播放動畫，不造成傷害（Java: isSafetyZone 檢查）
	if s.inSafetyZone(attacker) || s.inSafetyZone(target) {
		nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
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
	if wpn := attacker.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			if info.DmgSmall > 0 {
				weaponDmg = info.DmgSmall
			}
		}
	}

	ctx := scripting.CombatContext{
		AttackerLevel:  int(attacker.Level),
		AttackerSTR:    int(attacker.Str),
		AttackerDEX:    int(attacker.Dex),
		AttackerWeapon: weaponDmg,
		AttackerHitMod: int(attacker.HitMod),
		AttackerDmgMod: int(attacker.DmgMod),
		TargetAC:       int(target.AC),
		TargetLevel:    int(target.Level),
		TargetMR:       0,
	}
	result := s.deps.Scripting.CalcMeleeAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)

	// 反擊屏障（skill 91）：PvP 近戰機率反彈（Java: L1AttackPc.calcCounterBarrierDamage）
	if damage > 0 && target.HasBuff(91) {
		if world.RandInt(100)+1 <= 25 {
			cbDmg := s.calcCounterBarrierDmg(target)
			if cbDmg > 0 {
				attacker.HP -= int16(cbDmg)
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

	for _, viewer := range nearby {
		handler.SendAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading)
	}

	if damage > 0 {
		target.HP -= int16(damage)
		if target.HP < 0 {
			target.HP = 0
		}
		handler.SendHpUpdate(target.Session, target)

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
		nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
		for _, viewer := range nearby {
			if viewer.SessionID == attacker.SessionID {
				continue
			}
			handler.SendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
				attacker.X, attacker.Y, target.X, target.Y)
		}
		return
	}

	// 安全區內只播放動畫
	if s.inSafetyZone(attacker) || s.inSafetyZone(target) {
		handler.SendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, 0, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
		nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
		for _, viewer := range nearby {
			if viewer.SessionID == attacker.SessionID {
				continue
			}
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

	ctx := scripting.RangedCombatContext{
		AttackerLevel:     int(attacker.Level),
		AttackerSTR:       int(attacker.Str),
		AttackerDEX:       int(attacker.Dex),
		AttackerBowDmg:    bowDmg,
		AttackerArrowDmg:  arrowDmg,
		AttackerBowHitMod: int(attacker.BowHitMod),
		AttackerBowDmgMod: int(attacker.BowDmgMod),
		TargetAC:          int(target.AC),
		TargetLevel:       int(target.Level),
		TargetMR:          0,
	}
	result := s.deps.Scripting.CalcRangedAttack(ctx)

	damage := int32(result.Damage)
	if !result.IsHit {
		damage = 0
	}

	handler.SendArrowAttackPacket(attacker.Session, attacker.CharID, target.CharID, damage, attacker.Heading,
		attacker.X, attacker.Y, target.X, target.Y)
	nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearby {
		if viewer.SessionID == attacker.SessionID {
			continue
		}
		handler.SendArrowAttackPacket(viewer.Session, attacker.CharID, target.CharID, damage, attacker.Heading,
			attacker.X, attacker.Y, target.X, target.Y)
	}

	if damage > 0 {
		target.HP -= int16(damage)
		if target.HP < 0 {
			target.HP = 0
		}
		handler.SendHpUpdate(target.Session, target)

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
	nearby := s.deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
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
	nearby := s.deps.World.GetNearbyPlayers(attacker.X, attacker.Y, attacker.MapID, attacker.SessionID)
	for _, other := range nearby {
		handler.SendPinkName(other.Session, attacker.CharID, 180)
	}

	// 通知附近守衛
	nearbyNpcs := s.deps.World.GetNearbyNpcs(attacker.X, attacker.Y, attacker.MapID)
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
		nearby := s.deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
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
		nearby := s.deps.World.GetNearbyPlayers(killer.X, killer.Y, killer.MapID, killer.SessionID)
		for _, other := range nearby {
			handler.SendLawful(other.Session, killer.CharID, killer.Lawful)
		}

		handler.SendPlayerStatus(killer.Session, killer)

		pkThresh := s.deps.Scripting.GetPKThresholds()
		if killer.PKCount >= pkThresh.Warning && killer.PKCount < pkThresh.Punish {
			handler.SendRedMessage(killer.Session, 551, fmt.Sprintf("%d", killer.PKCount), fmt.Sprintf("%d", pkThresh.Punish))
		}

		s.deps.Log.Info(fmt.Sprintf("PK 擊殺  擊殺者=%s  受害者=%s  PK次數=%d  正義值=%d", killer.Name, victim.Name, killer.PKCount, killer.Lawful))
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
	}
	s.deps.World.AddGroundItem(gndItem)

	victim.Inv.RemoveItem(item.ObjectID, 0)
	handler.SendRemoveInventoryItem(victim.Session, item.ObjectID)
	handler.SendWeightUpdate(victim.Session, victim)

	nearby := s.deps.World.GetNearbyPlayersAt(victim.X, victim.Y, victim.MapID)
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
