package system

import (
	"fmt"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

// 技能相關訊息 ID
const (
	skillMsgNotEnoughMP uint16 = 278 // "因魔力不足而無法使用魔法。"
	skillMsgNotEnoughHP uint16 = 279 // "因體力不足而無法使用魔法。"
	skillMsgCastFail    uint16 = 280 // "施展魔法失敗。"
)

// SkillSystem processes queued skill requests in Phase 2.
// 管理技能執行、buff 套用/到期、NPC debuff。
type SkillSystem struct {
	deps     *handler.Deps
	requests []handler.SkillRequest
}

// NewSkillSystem 建立 SkillSystem。
func NewSkillSystem(deps *handler.Deps) *SkillSystem {
	return &SkillSystem{deps: deps}
}

// Phase 回傳系統執行階段。
func (s *SkillSystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

// QueueSkill implements handler.SkillManager.
func (s *SkillSystem) QueueSkill(req handler.SkillRequest) {
	s.requests = append(s.requests, req)
}

// Update 處理所有排隊的技能請求。
func (s *SkillSystem) Update(_ time.Duration) {
	for _, req := range s.requests {
		s.processSkill(req.SessionID, req.SkillID, req.TargetID)
	}
	s.requests = s.requests[:0]
}

// CancelAllBuffs implements handler.SkillManager.
func (s *SkillSystem) CancelAllBuffs(target *world.PlayerInfo) {
	s.cancelAllBuffs(target)
}

// RemoveBuffAndRevert implements handler.SkillManager.
func (s *SkillSystem) RemoveBuffAndRevert(target *world.PlayerInfo, skillID int32) {
	s.removeBuffAndRevert(target, skillID)
}

// TickPlayerBuffs implements handler.SkillManager.
func (s *SkillSystem) TickPlayerBuffs(p *world.PlayerInfo) {
	s.tickPlayerBuffs(p)
}

// ========================================================================
//  技能處理主流程
// ========================================================================

// processSkill 驗證並執行技能請求。由 Update() 在 Phase 2 呼叫。
func (s *SkillSystem) processSkill(sessID uint64, skillID, targetID int32) {
	player := s.deps.World.GetBySession(sessID)
	if player == nil || player.Dead {
		return
	}
	sess := player.Session

	skill := s.deps.Skills.Get(skillID)
	if skill == nil {
		s.deps.Log.Debug("unknown skill", zap.Int32("skill_id", skillID))
		return
	}

	s.deps.Log.Debug("C_UseSpell",
		zap.String("player", player.Name),
		zap.Int32("skill_id", skillID),
		zap.String("skill", skill.Name),
		zap.String("target_type", skill.Target),
		zap.Int32("target", targetID),
	)

	// --- 驗證 ---

	// 麻痺/暈眩/凍結/睡眠/沉默時無法施法
	if player.Paralyzed || player.Sleeped || player.Silenced {
		return
	}

	// 變形限制：部分形態無法施法
	if player.PolyID != 0 && s.deps.Polys != nil {
		poly := s.deps.Polys.GetByID(player.PolyID)
		if poly != nil && !poly.CanUseSkill {
			handler.SendServerMessage(sess, 285) // "此形態無法使用魔法。"
			return
		}
	}

	// 檢查是否已學會此法術
	if !s.playerKnowsSpell(player, skillID) {
		handler.SendServerMessage(sess, skillMsgCastFail)
		return
	}

	// 全域施法冷卻
	now := time.Now()
	if now.Before(player.SkillDelayUntil) {
		return
	}

	// HP 消耗檢查
	if skill.HpConsume > 0 && player.HP <= int16(skill.HpConsume) {
		handler.SendServerMessage(sess, skillMsgNotEnoughHP)
		return
	}

	// MP 消耗檢查
	if skill.MpConsume > 0 && player.MP < int16(skill.MpConsume) {
		handler.SendServerMessage(sess, skillMsgNotEnoughMP)
		return
	}

	// --- 材料消耗檢查（Java: isItemConsume）---
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		needItemID := int32(skill.ItemConsumeID)
		slot := player.Inv.FindByItemID(needItemID)
		if slot == nil || slot.Count < int32(skill.ItemConsumeCount) {
			haveCount := int32(0)
			if slot != nil {
				haveCount = slot.Count
			}
			var invIDs []int32
			for i, it := range player.Inv.Items {
				if i >= 10 {
					break
				}
				invIDs = append(invIDs, it.ItemID)
			}
			s.deps.Log.Warn("skill blocked: insufficient materials",
				zap.Int32("skill_id", skillID),
				zap.String("skill_name", skill.Name),
				zap.Int32("need_item_id", needItemID),
				zap.Int("need_count", skill.ItemConsumeCount),
				zap.Bool("slot_found", slot != nil),
				zap.Int32("have_count", haveCount),
				zap.Int("inv_size", player.Inv.Size()),
				zap.Int32s("inv_first10", invIDs))
			handler.SendServerMessage(sess, 299) // 施放魔法所需材料不足。
			return
		}
	}

	// --- 傳送技能：在消耗 MP 前特殊路由 ---
	if skillID == 5 || skillID == 69 {
		s.executeTeleportSpell(sess, player, skill, targetID)
		return
	}

	// --- 召喚技能：特殊路由（資源消耗在內部驗證後處理）---
	switch skillID {
	case 51:
		handler.ExecuteSummonMonster(sess, player, skill, targetID, s.deps)
		return
	case 36:
		handler.ExecuteTamingMonster(sess, player, skill, targetID, s.deps)
		return
	case 41:
		handler.ExecuteCreateZombie(sess, player, skill, targetID, s.deps)
		return
	case 145:
		handler.ExecuteReturnToNature(sess, player, skill, s.deps)
		return
	}

	// --- 消耗資源（MP、HP、材料）---
	if skill.MpConsume > 0 {
		player.MP -= int16(skill.MpConsume)
		sendMpUpdate(sess, player)
	}
	if skill.HpConsume > 0 {
		player.HP -= int16(skill.HpConsume)
		sendHpUpdate(sess, player)
	}
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot != nil {
			removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
			if removed {
				handler.SendRemoveInventoryItem(sess, slot.ObjectID)
			} else {
				handler.SendItemCountUpdate(sess, slot)
			}
			handler.SendWeightUpdate(sess, player)
		}
	}

	// --- 設定全域冷卻 ---
	delay := skill.ReuseDelay
	if delay <= 0 {
		delay = 1000
	}
	player.SkillDelayUntil = now.Add(time.Duration(delay) * time.Millisecond)

	// --- 復活技能：特殊路由 ---
	if s.isResurrectionSkill(skill) {
		s.executeResurrection(sess, player, skill, targetID)
		return
	}

	// --- 依目標類型執行 ---
	switch skill.Target {
	case "attack":
		s.executeAttackSkill(sess, player, skill, targetID)
	case "buff":
		s.executeBuffSkill(sess, player, skill, targetID)
	default:
		s.executeSelfSkill(sess, player, skill)
	}
}

// consumeSkillResources 扣除 MP/HP/材料並設定冷卻。
func (s *SkillSystem) consumeSkillResources(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo) {
	if skill.MpConsume > 0 {
		player.MP -= int16(skill.MpConsume)
		sendMpUpdate(sess, player)
	}
	if skill.HpConsume > 0 {
		player.HP -= int16(skill.HpConsume)
		sendHpUpdate(sess, player)
	}
	if skill.ItemConsumeID > 0 && skill.ItemConsumeCount > 0 {
		slot := player.Inv.FindByItemID(int32(skill.ItemConsumeID))
		if slot != nil {
			removed := player.Inv.RemoveItem(slot.ObjectID, int32(skill.ItemConsumeCount))
			if removed {
				handler.SendRemoveInventoryItem(sess, slot.ObjectID)
			} else {
				handler.SendItemCountUpdate(sess, slot)
			}
			handler.SendWeightUpdate(sess, player)
		}
	}
	delay := skill.ReuseDelay
	if delay <= 0 {
		delay = 1000
	}
	player.SkillDelayUntil = time.Now().Add(time.Duration(delay) * time.Millisecond)
}

// ========================================================================
//  復活技能
// ========================================================================

// isResurrectionSkill 檢查是否為復活型技能（定義在 Lua）。
func (s *SkillSystem) isResurrectionSkill(skill *data.SkillInfo) bool {
	fn := s.deps.Scripting
	if fn == nil {
		return false
	}
	return fn.GetResurrectEffect(int(skill.SkillID)) != nil
}

// executeResurrection 處理復活技能（18, 75, 131, 165）。
func (s *SkillSystem) executeResurrection(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	// 廣播施法動畫
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	switch skill.SkillID {
	case 131: // 世界樹的呼喚 — 範圍復活
		resurrected := false
		for _, p := range nearby {
			if p.Dead {
				s.resurrectPlayer(p, player, skill)
				resurrected = true
			}
		}
		if !resurrected {
			handler.SendServerMessage(sess, skillMsgCastFail)
		}

	default: // 18, 75, 165 — 單目標復活
		if targetID == 0 {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		target := s.deps.World.GetByCharID(targetID)
		if target == nil || !target.Dead {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		if target.MapID != player.MapID {
			return
		}

		// 技能 18 有機率檢查
		if skill.ProbabilityDice > 0 {
			if world.RandInt(10) >= skill.ProbabilityDice {
				handler.SendServerMessage(sess, skillMsgCastFail)
				return
			}
		}

		s.resurrectPlayer(target, player, skill)
	}

	// 施法特效
	if skill.CastGfx > 0 {
		for _, viewer := range nearby {
			handler.SendSkillEffect(viewer.Session, player.CharID, skill.CastGfx)
		}
	}
}

// resurrectPlayer 復活死亡玩家，HP/MP 依復活技能定義。
func (s *SkillSystem) resurrectPlayer(target *world.PlayerInfo, caster *world.PlayerInfo, skill *data.SkillInfo) {
	target.Dead = false

	eff := s.deps.Scripting.GetResurrectEffect(int(skill.SkillID))
	if eff != nil {
		if eff.FixedHP == -1 {
			target.HP = int16(caster.Level)
		} else if eff.FixedHP > 0 {
			target.HP = int16(eff.FixedHP)
		} else {
			target.HP = int16(float64(target.MaxHP) * eff.HPRatio)
			target.MP = int16(float64(target.MaxMP) * eff.MPRatio)
		}
	} else {
		target.HP = int16(target.Level)
	}

	if target.HP < 1 {
		target.HP = 1
	}
	if target.HP > target.MaxHP {
		target.HP = target.MaxHP
	}
	if target.MP > target.MaxMP {
		target.MP = target.MaxMP
	}

	sendHpUpdate(target.Session, target)
	sendMpUpdate(target.Session, target)
	handler.SendPlayerStatus(target.Session, target)
	handler.SendPutObject(target.Session, target)

	nearbyTarget := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
	for _, viewer := range nearbyTarget {
		if viewer.SessionID != target.SessionID {
			handler.SendPutObject(viewer.Session, target)
		}
	}

	s.deps.Log.Info(fmt.Sprintf("玩家復活  目標=%s  施法者=%s  技能ID=%d", target.Name, caster.Name, skill.SkillID))
}

// ========================================================================
//  攻擊技能
// ========================================================================

// executeAttackSkill 處理傷害型技能（目標為 NPC）。
func (s *SkillSystem) executeAttackSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	ws := s.deps.World

	npc := ws.GetNpc(targetID)
	if npc == nil || npc.Dead {
		return
	}
	if npc.MapID != player.MapID {
		return
	}

	// 距離檢查
	maxRange := int32(skill.Ranged)
	if skill.SkillID == 132 {
		maxRange = 10
	} else if maxRange <= 0 {
		maxRange = 2
	}
	dist := chebyshevDist(player.X, player.Y, npc.X, npc.Y)
	if dist > maxRange+2 {
		return
	}

	player.Heading = handler.CalcHeading(player.X, player.Y, npc.X, npc.Y)

	// Triple Arrow (132)：消耗 1 箭矢
	if skill.SkillID == 132 {
		arrow := handler.FindArrow(player, s.deps)
		if arrow == nil {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		if player.Inv.RemoveItem(arrow.ObjectID, 1) {
			handler.SendRemoveInventoryItem(sess, arrow.ObjectID)
		} else {
			handler.SendItemCountUpdate(sess, arrow)
		}
	}

	// 武器傷害
	weaponDmg := 4 // 拳頭
	targetSize := npc.Size
	if targetSize == "" {
		targetSize = "small"
	}
	if wpn := player.Equip.Weapon(); wpn != nil {
		if info := s.deps.Items.Get(wpn.ItemID); info != nil {
			if targetSize == "large" && info.DmgLarge > 0 {
				weaponDmg = info.DmgLarge
			} else if info.DmgSmall > 0 {
				weaponDmg = info.DmgSmall
			}
		}
	}

	// Lua 傷害計算 context 建構
	buildCtx := func(n *world.NpcInfo) scripting.SkillDamageContext {
		return scripting.SkillDamageContext{
			SkillID:         int(skill.SkillID),
			DamageValue:     skill.DamageValue,
			DamageDice:      skill.DamageDice,
			DamageDiceCount: skill.DamageDiceCount,
			SkillLevel:      skill.SkillLevel,
			Attr:            skill.Attr,
			AttackerLevel:   int(player.Level),
			AttackerSTR:     int(player.Str),
			AttackerDEX:     int(player.Dex),
			AttackerINT:     int(player.Intel),
			AttackerWIS:     int(player.Wis),
			AttackerSP:      int(player.SP),
			AttackerDmgMod:  int(player.DmgMod),
			AttackerHitMod:  int(player.HitMod),
			AttackerWeapon:  weaponDmg,
			AttackerHP:      int(player.HP),
			AttackerMaxHP:   int(player.MaxHP),
			TargetAC:        int(n.AC),
			TargetLevel:     int(n.Level),
			TargetMR:        int(n.MR),
			TargetMP:        int(n.MP),
		}
	}

	type hitTarget struct {
		npc      *world.NpcInfo
		dmg      int32
		hitCount int
		drainMP  int32
	}

	res := s.deps.Scripting.CalcSkillDamage(buildCtx(npc))
	hits := []hitTarget{{npc: npc, dmg: int32(res.Damage), hitCount: res.HitCount, drainMP: int32(res.DrainMP)}}

	if skill.Area > 0 {
		allNpcs := ws.GetNearbyNpcs(npc.X, npc.Y, npc.MapID)
		for _, other := range allNpcs {
			if other.ID == npc.ID || other.Dead {
				continue
			}
			if chebyshevDist(npc.X, npc.Y, other.X, other.Y) <= int32(skill.Area) {
				r := s.deps.Scripting.CalcSkillDamage(buildCtx(other))
				hits = append(hits, hitTarget{npc: other, dmg: int32(r.Damage), hitCount: r.HitCount, drainMP: int32(r.DrainMP)})
			}
		}
	}

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	isPhysicalSkill := skill.DamageValue == 0 && skill.DamageDice == 0

	useType := byte(6)
	if skill.Area > 0 {
		useType = 8
	}

	for _, t := range hits {
		hitsToApply := t.hitCount
		if hitsToApply < 1 {
			hitsToApply = 1
		}

		for h := 0; h < hitsToApply; h++ {
			dmg := t.dmg

			if isPhysicalSkill {
				for _, viewer := range nearby {
					handler.SendAttackPacket(viewer.Session, player.CharID, t.npc.ID, dmg, player.Heading)
				}
				if skill.CastGfx > 0 {
					for _, viewer := range nearby {
						handler.SendSkillEffect(viewer.Session, t.npc.ID, skill.CastGfx)
					}
				}
			} else {
				gfxID := int32(skill.CastGfx)
				if gfxID <= 0 {
					gfxID = int32(skill.ActionID)
				}
				for _, viewer := range nearby {
					handler.SendUseAttackSkill(viewer.Session, player.CharID, t.npc.ID,
						int16(dmg), player.Heading, gfxID, useType,
						int32(player.X), int32(player.Y), int32(t.npc.X), int32(t.npc.Y))
				}
			}

			t.npc.HP -= dmg
			if t.npc.HP < 0 {
				t.npc.HP = 0
			}

			// 受傷時解除睡眠
			if t.npc.Sleeped {
				handler.BreakNpcSleep(t.npc, ws)
			}

			// Mind Break: 吸收 MP
			if h == 0 && t.drainMP > 0 && t.npc.MP >= t.drainMP {
				t.npc.MP -= t.drainMP
			}

			if t.npc.AggroTarget == 0 {
				t.npc.AggroTarget = sess.ID
			}

			hpRatio := int16(0)
			if t.npc.MaxHP > 0 {
				hpRatio = int16((t.npc.HP * 100) / t.npc.MaxHP)
			}
			for _, viewer := range nearby {
				handler.SendHpMeter(viewer.Session, t.npc.ID, hpRatio)
			}

			if t.npc.HP <= 0 {
				handler.HandleNpcDeath(t.npc, player, nearby, s.deps)
				break
			}
		}
	}
}

// ========================================================================
//  Buff 技能
// ========================================================================

// executeBuffSkill 處理治療與 buff 類技能。
func (s *SkillSystem) executeBuffSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, targetID int32) {
	ws := s.deps.World

	// 檢查目標是否為 NPC（debuff 路徑）
	if targetID != 0 && targetID != player.CharID {
		if npc := ws.GetNpc(targetID); npc != nil && !npc.Dead {
			s.executeNpcDebuffSkill(sess, player, skill, npc)
			return
		}
	}

	target := player
	if targetID != 0 && targetID != player.CharID {
		if other := ws.GetByCharID(targetID); other != nil {
			if other.MapID != player.MapID || other.Dead {
				return
			}
			if chebyshevDist(player.X, player.Y, other.X, other.Y) > 20 {
				return
			}
			target = other
		}
	}

	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	// 廣播施法動畫
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	// 變形術：開啟怪物列表對話框
	if skill.SkillID == handler.SkillShapeChange {
		handler.SendShowPolyList(sess, player.CharID)
		return
	}

	// 即時效果技能
	switch skill.SkillID {
	case 9: // 解毒術
		handler.CurePoison(target, s.deps)

	case 23: // 能量感測
		// TODO: 發送目標屬性給施法者

	case 20, 40: // 闇盲咒術 / 黑闇之影
		handler.SendCurseBlind(target.Session, 1)

	case 33: // 木乃伊詛咒 — 對玩家施加詛咒麻痺
		if target.CharID != player.CharID && !target.Paralyzed && target.CurseType == 0 &&
			!target.HasBuff(157) && !target.HasBuff(50) && !target.HasBuff(80) {
			target.CurseType = 1
			target.CurseTicksLeft = 25
			handler.BroadcastPlayerPoison(target, 2, s.deps)
			handler.SendServerMessage(target.Session, 212)
		}

	case 37: // 聖潔之光 — 解毒 + 解詛咒 + 解麻痺/睡眠/致盲
		handler.CurePoison(target, s.deps)
		if target.CurseType > 0 {
			handler.CureCurseParalysis(target, s.deps)
		}
		if target.Paralyzed {
			target.Paralyzed = false
			handler.SendParalysis(target.Session, handler.ParalysisRemove)
		}
		if target.Sleeped {
			target.Sleeped = false
			handler.SendParalysis(target.Session, handler.SleepRemove)
		}
		handler.SendCurseBlind(target.Session, 0)

	case 39: // 魔力奪取
		drain := int16(5 + world.RandInt(10))
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
		if targetID != 0 && targetID != player.CharID {
			handler.CurePoison(target, s.deps)
			handler.CureCurseParalysis(target, s.deps)
			s.cancelAllBuffs(target)
		}

	case 153: // 魔法消除 — 解除 buff
		s.cancelAllBuffs(target)
	}

	// 治療效果
	if skill.Type == 16 || skill.DamageValue > 0 || skill.DamageDice > 0 {
		casterINT := int(player.Intel)
		casterSP := int(player.SP)

		if skill.Area == -1 {
			// 範圍治療
			for _, p := range nearby {
				heal := int16(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
				if heal > 0 && p.HP < p.MaxHP {
					p.HP += heal
					if p.HP > p.MaxHP {
						p.HP = p.MaxHP
					}
					sendHpUpdate(p.Session, p)
				}
			}
		} else {
			// 單目標治療
			heal := int16(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
			if heal > 0 && target.HP < target.MaxHP {
				target.HP += heal
				if target.HP > target.MaxHP {
					target.HP = target.MaxHP
				}
				sendHpUpdate(target.Session, target)
			}
		}
	}

	// 套用 buff 效果
	s.applyBuffEffect(target, skill)

	// 效果 GFX
	if skill.CastGfx > 0 {
		for _, viewer := range nearby {
			handler.SendSkillEffect(viewer.Session, target.CharID, skill.CastGfx)
		}
	}

	if skill.SysMsgHappen > 0 {
		handler.SendServerMessage(target.Session, uint16(skill.SysMsgHappen))
	}
}

// ========================================================================
//  NPC Debuff
// ========================================================================

// executeNpcDebuffSkill 對 NPC 施加 debuff 技能。
func (s *SkillSystem) executeNpcDebuffSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, npc *world.NpcInfo) {
	ws := s.deps.World

	maxRange := int32(skill.Ranged)
	if maxRange <= 0 {
		maxRange = 10
	}
	if chebyshevDist(player.X, player.Y, npc.X, npc.Y) > maxRange+2 {
		return
	}

	player.Heading = handler.CalcHeading(player.X, player.Y, npc.X, npc.Y)

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	switch skill.SkillID {
	case 87: // 衝擊之暈 — 需要雙手劍
		wpn := player.Equip.Weapon()
		if wpn == nil {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		if info := s.deps.Items.Get(wpn.ItemID); info == nil || info.Type != "tohandsword" {
			handler.SendGlobalChat(sess, 9, "\\f3請使用雙手劍。")
			return
		}
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := 1 + world.RandInt(6)
		npc.Paralyzed = true
		npc.AddDebuff(87, dur*5)
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
		s.deps.Log.Info(fmt.Sprintf("衝擊之暈  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur))

	case 157: // 大地屏障 — 凍結 + 灰色色調
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := 1 + world.RandInt(12)
		npc.Paralyzed = true
		npc.AddDebuff(157, dur*5)
		for _, viewer := range nearby {
			handler.SendPoison(viewer.Session, npc.ID, 2)
		}
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
		s.deps.Log.Info(fmt.Sprintf("大地屏障  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur))

	case 103: // 暗黑盲咒
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 3
		}
		npc.Sleeped = true
		npc.AddDebuff(103, dur*5)
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
		s.deps.Log.Info(fmt.Sprintf("暗黑盲咒  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur))

	case 66: // 沉睡之霧
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 10
		}
		npc.Sleeped = true
		npc.AddDebuff(66, dur*5)
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
		s.deps.Log.Info(fmt.Sprintf("沉睡之霧  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur))

	case 33: // 木乃伊詛咒（NPC 版）— 階段一：灰色延遲
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		if npc.Paralyzed || npc.HasDebuff(33) || npc.HasDebuff(4001) {
			return
		}
		npc.AddDebuff(33, 25)
		for _, viewer := range nearby {
			handler.SendPoison(viewer.Session, npc.ID, 2)
		}
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
		s.deps.Log.Info(fmt.Sprintf("木乃伊詛咒(階段一)  施法者=%s  NPC=%s  延遲=5秒", player.Name, npc.Name))

	case 29, 76, 152: // 緩速系列
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 64
		}
		npc.AddDebuff(skill.SkillID, dur*5)
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
		s.deps.Log.Info(fmt.Sprintf("緩速術  施法者=%s  NPC=%s  技能=%d  持續=%d秒", player.Name, npc.Name, skill.SkillID, dur))

	default:
		if skill.CastGfx > 0 {
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
		}
	}
}

// checkNpcMRResist 檢查 NPC 魔法抗性。
func (s *SkillSystem) checkNpcMRResist(caster *world.PlayerInfo, npc *world.NpcInfo, _ int32) bool {
	prob := 50 + (int(caster.Level)-int(npc.Level))*5 + int(caster.Intel)*2 - int(npc.MR)
	if prob < 5 {
		prob = 5
	}
	if prob > 95 {
		prob = 95
	}
	return world.RandInt(100) < prob
}

// ========================================================================
//  自身技能
// ========================================================================

// executeSelfSkill 處理自身目標技能（護盾、光明、冥想等）。
func (s *SkillSystem) executeSelfSkill(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo) {
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	switch skill.SkillID {
	case 2: // 日光術
		// 有持續時間但無屬性變化，由 applyBuffEffect 處理

	case 13: // 無所遁形術
		// TODO: 清除附近隱身玩家

	case 44: // 魔法相消術（自身）
		s.cancelAllBuffs(player)

	case 72: // 強力無所遁形術

	case 130: // 心靈轉換 — HP 轉 MP
		transfer := player.HP / 4
		if transfer > 0 && player.HP > transfer {
			player.HP -= transfer
			player.MP += transfer
			if player.MP > player.MaxMP {
				player.MP = player.MaxMP
			}
			sendHpUpdate(sess, player)
			sendMpUpdate(sess, player)
		}

	case 146: // 魂體轉換 — MP 轉 HP
		transfer := player.MP / 4
		if transfer > 0 && player.MP > transfer {
			player.MP -= transfer
			player.HP += transfer
			if player.HP > player.MaxHP {
				player.HP = player.MaxHP
			}
			sendHpUpdate(sess, player)
			sendMpUpdate(sess, player)
		}

	case 172: // 暴風疾走
		for _, conflictID := range []int32{
			handler.SkillStatusBrave, handler.SkillStatusElfBrave,
			42,  // HOLY_WALK
			101, // MOVING_ACCELERATION
			150, // WIND_WALK
			52,  // BLOODLUST
		} {
			s.removeBuffAndRevert(player, conflictID)
		}
		stormBuff := &world.ActiveBuff{
			SkillID:       172,
			TicksLeft:     300 * 5,
			SetBraveSpeed: 4,
		}
		old172 := player.AddBuff(stormBuff)
		if old172 != nil {
			s.revertBuffStats(player, old172)
		}
		player.BraveSpeed = 4
		player.BraveTicks = stormBuff.TicksLeft
		s.sendSpeedToAll(player, 4, 300)
	}

	// 廣播施法動畫
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	// 自身範圍治療
	if skill.Type == 16 && (skill.DamageValue > 0 || skill.DamageDice > 0) {
		casterINT := int(player.Intel)
		casterSP := int(player.SP)

		if skill.Area == -1 {
			heal := int16(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
			if heal > 0 && player.HP < player.MaxHP {
				player.HP += heal
				if player.HP > player.MaxHP {
					player.HP = player.MaxHP
				}
				sendHpUpdate(sess, player)
			}
			for _, p := range nearby {
				if p.SessionID == sess.ID {
					continue
				}
				h := int16(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
				if h > 0 && p.HP < p.MaxHP {
					p.HP += h
					if p.HP > p.MaxHP {
						p.HP = p.MaxHP
					}
					sendHpUpdate(p.Session, p)
				}
			}
		} else {
			heal := int16(s.deps.Scripting.CalcHeal(skill.DamageValue, skill.DamageDice, skill.DamageDiceCount, casterINT, casterSP))
			if heal > 0 && player.HP < player.MaxHP {
				player.HP += heal
				if player.HP > player.MaxHP {
					player.HP = player.MaxHP
				}
				sendHpUpdate(sess, player)
			}
		}
	}

	// 自身範圍 AoE 傷害
	if skill.Type == 64 && skill.Area > 0 && skill.DamageValue > 0 {
		nearbyNpcs := s.deps.World.GetNearbyNpcs(player.X, player.Y, player.MapID)
		for _, npc := range nearbyNpcs {
			if npc.Dead {
				continue
			}
			if chebyshevDist(player.X, player.Y, npc.X, npc.Y) > int32(skill.Area) {
				continue
			}
			ctx := scripting.SkillDamageContext{
				SkillID:         int(skill.SkillID),
				DamageValue:     skill.DamageValue,
				DamageDice:      skill.DamageDice,
				DamageDiceCount: skill.DamageDiceCount,
				SkillLevel:      skill.SkillLevel,
				Attr:            skill.Attr,
				AttackerLevel:   int(player.Level),
				AttackerSTR:     int(player.Str),
				AttackerDEX:     int(player.Dex),
				AttackerINT:     int(player.Intel),
				AttackerWIS:     int(player.Wis),
				AttackerSP:      int(player.SP),
				AttackerDmgMod:  int(player.DmgMod),
				AttackerHitMod:  int(player.HitMod),
				TargetAC:        int(npc.AC),
				TargetLevel:     int(npc.Level),
				TargetMR:        int(npc.MR),
			}
			res := s.deps.Scripting.CalcSkillDamage(ctx)
			dmg := int32(res.Damage)
			for _, viewer := range nearby {
				handler.SendSkillEffect(viewer.Session, npc.ID, skill.CastGfx)
			}
			npc.HP -= dmg
			if npc.HP < 0 {
				npc.HP = 0
			}
			if npc.AggroTarget == 0 {
				npc.AggroTarget = sess.ID
			}
			hpRatio := int16(0)
			if npc.MaxHP > 0 {
				hpRatio = int16((npc.HP * 100) / npc.MaxHP)
			}
			for _, viewer := range nearby {
				handler.SendHpMeter(viewer.Session, npc.ID, hpRatio)
			}
			if npc.HP <= 0 {
				handler.HandleNpcDeath(npc, player, nearby, s.deps)
			}
		}
	}

	// 套用 buff 效果
	s.applyBuffEffect(player, skill)

	// 效果 GFX
	if skill.CastGfx > 0 {
		for _, viewer := range nearby {
			handler.SendSkillEffect(viewer.Session, player.CharID, skill.CastGfx)
		}
	}

	if skill.SysMsgHappen > 0 {
		handler.SendServerMessage(sess, uint16(skill.SysMsgHappen))
	}
}

// ========================================================================
//  傳送技能
// ========================================================================

// executeTeleportSpell 處理傳送技能（5: 瞬間移動, 69: 集體瞬間移動）。
func (s *SkillSystem) executeTeleportSpell(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, bookmarkID int32) {
	var destX, destY int32
	var destMapID int16
	var destHeading int16 = 5

	if bookmarkID != 0 {
		// --- 書籤傳送 ---
		if s.deps.MapData != nil {
			if mi := s.deps.MapData.GetInfo(player.MapID); mi != nil && !mi.Escapable {
				handler.SendServerMessage(sess, 79)
				handler.SendParalysis(sess, handler.TeleportUnlock)
				return
			}
		}

		var found *world.Bookmark
		for i := range player.Bookmarks {
			if player.Bookmarks[i].ID == bookmarkID {
				found = &player.Bookmarks[i]
				break
			}
		}
		if found == nil {
			handler.SendParalysis(sess, handler.TeleportUnlock)
			return
		}
		destX = found.X
		destY = found.Y
		destMapID = found.MapID
	} else {
		// --- 隨機傳送 ---
		if s.deps.MapData != nil {
			if mi := s.deps.MapData.GetInfo(player.MapID); mi != nil && !mi.Teleportable {
				handler.SendServerMessage(sess, 276)
				handler.SendParalysis(sess, handler.TeleportUnlock)
				return
			}
		}

		destMapID = player.MapID
		destX = player.X
		destY = player.Y

		minRX := player.X - 200
		maxRX := player.X + 200
		minRY := player.Y - 200
		maxRY := player.Y + 200
		if s.deps.MapData != nil {
			if mi := s.deps.MapData.GetInfo(destMapID); mi != nil {
				if minRX < mi.StartX {
					minRX = mi.StartX
				}
				if maxRX > mi.EndX {
					maxRX = mi.EndX
				}
				if minRY < mi.StartY {
					minRY = mi.StartY
				}
				if maxRY > mi.EndY {
					maxRY = mi.EndY
				}
			}
		}

		diffX := maxRX - minRX
		diffY := maxRY - minRY
		if diffX > 0 && diffY > 0 {
			for attempt := 0; attempt < 40; attempt++ {
				rx := minRX + int32(world.RandInt(int(diffX)+1))
				ry := minRY + int32(world.RandInt(int(diffY)+1))
				if s.deps.MapData != nil && s.deps.MapData.IsInMap(destMapID, rx, ry) &&
					s.deps.MapData.IsPassablePoint(destMapID, rx, ry) {
					destX = rx
					destY = ry
					break
				}
			}
		}
	}

	// --- 驗證通過，消耗 MP ---
	if skill.MpConsume > 0 {
		player.MP -= int16(skill.MpConsume)
		sendMpUpdate(sess, player)
	}

	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, player.CharID, byte(skill.ActionID))
	}

	handler.SendSkillEffect(sess, player.CharID, int32(skill.CastGfx))
	for _, viewer := range nearby {
		if viewer.SessionID != sess.ID {
			handler.SendSkillEffect(viewer.Session, player.CharID, int32(skill.CastGfx))
		}
	}

	// 傳送時取消交易
	handler.CancelTradeIfActive(player, s.deps)

	handler.TeleportPlayer(sess, player, destX, destY, destMapID, destHeading, s.deps)
}

// ========================================================================
//  Buff 管理
// ========================================================================

// sendBuffIcon 發送適當的 buff 圖示封包。
func (s *SkillSystem) sendBuffIcon(target *world.PlayerInfo, skillID int32, durationSec uint16) {
	icon := s.deps.BuffIcons.Get(skillID)
	if icon == nil {
		return
	}
	sess := target.Session
	switch icon.Type {
	case "shield":
		handler.SendIconShield(sess, durationSec, icon.Param)
	case "strup":
		handler.SendIconStrup(sess, durationSec, byte(target.Str), icon.Param)
	case "dexup":
		handler.SendIconDexup(sess, durationSec, byte(target.Dex), icon.Param)
	case "aura":
		handler.SendIconAura(sess, byte(skillID-1), durationSec)
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
		buff.DeltaMaxHP = int16(eff.MaxHP)
		buff.DeltaMaxMP = int16(eff.MaxMP)
		buff.DeltaHitMod = int16(eff.HitMod)
		buff.DeltaDmgMod = int16(eff.DmgMod)
		buff.DeltaSP = int16(eff.SP)
		buff.DeltaMR = int16(eff.MR)
		buff.DeltaHPR = int16(eff.HPR)
		buff.DeltaMPR = int16(eff.MPR)
		buff.DeltaBowHit = int16(eff.BowHit)
		buff.DeltaBowDmg = int16(eff.BowDmg)
		buff.DeltaDodge = int16(eff.Dodge)
		buff.DeltaFireRes = int16(eff.FireRes)
		buff.DeltaWaterRes = int16(eff.WaterRes)
		buff.DeltaWindRes = int16(eff.WindRes)
		buff.DeltaEarthRes = int16(eff.EarthRes)

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
		target.Dodge += buff.DeltaDodge
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
		if eff.Invisible {
			buff.SetInvisible = true
			target.Invisible = true
		}
		if eff.Paralyzed {
			buff.SetParalyzed = true
			target.Paralyzed = true
			switch skill.SkillID {
			case 87:
				handler.SendParalysis(target.Session, handler.StunApply)
			case 157:
				handler.SendParalysis(target.Session, handler.FreezeApply)
			case 50:
				handler.SendParalysis(target.Session, handler.FreezeApply)
			case 80:
				handler.SendParalysis(target.Session, handler.FreezeApply)
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

	// 註冊 buff（替換舊的）
	old := target.AddBuff(buff)
	if old != nil {
		s.revertBuffStats(target, old)
	}

	// 屬性變化時發送更新
	if buff.DeltaStr != 0 || buff.DeltaDex != 0 || buff.DeltaCon != 0 ||
		buff.DeltaWis != 0 || buff.DeltaIntel != 0 || buff.DeltaCha != 0 ||
		buff.DeltaMaxHP != 0 || buff.DeltaMaxMP != 0 || buff.DeltaAC != 0 {
		handler.SendPlayerStatus(target.Session, target)
	}

	s.sendBuffIcon(target, skill.SkillID, uint16(skill.BuffDuration))
}

// removeBuffAndRevert 移除衝突 buff 並還原屬性。
func (s *SkillSystem) removeBuffAndRevert(target *world.PlayerInfo, skillID int32) {
	old := target.RemoveBuff(skillID)
	if old != nil {
		s.revertBuffStats(target, old)
		s.cancelBuffIcon(target, skillID)
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
	target.FireRes -= buff.DeltaFireRes
	target.WaterRes -= buff.DeltaWaterRes
	target.WindRes -= buff.DeltaWindRes
	target.EarthRes -= buff.DeltaEarthRes
	target.Dodge -= buff.DeltaDodge
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
}

// sendSpeedToAll 向自己和附近玩家發送速度封包。
func (s *SkillSystem) sendSpeedToAll(target *world.PlayerInfo, speedType byte, duration uint16) {
	sendSpeedPacket(target.Session, target.CharID, speedType, duration)
	nearby := s.deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
	for _, other := range nearby {
		sendSpeedPacket(other.Session, target.CharID, speedType, 0)
	}
}

// sendBraveToAll 向自己和附近玩家發送勇敢封包。
func (s *SkillSystem) sendBraveToAll(target *world.PlayerInfo, braveType byte, duration uint16) {
	sendBravePacket(target.Session, target.CharID, braveType, duration)
	nearby := s.deps.World.GetNearbyPlayers(target.X, target.Y, target.MapID, target.SessionID)
	for _, other := range nearby {
		sendBravePacket(other.Session, target.CharID, braveType, 0)
	}
}

// cancelAllBuffs 移除所有可取消的 buff。
func (s *SkillSystem) cancelAllBuffs(target *world.PlayerInfo) {
	if target.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range target.ActiveBuffs {
		if s.deps.Scripting.IsNonCancellable(int(skillID)) {
			continue
		}
		s.revertBuffStats(target, buff)
		delete(target.ActiveBuffs, skillID)
		s.cancelBuffIcon(target, skillID)

		if skillID == handler.SkillShapeChange {
			handler.UndoPoly(target, s.deps)
		}

		if buff.SetMoveSpeed > 0 {
			target.MoveSpeed = 0
			target.HasteTicks = 0
			s.sendSpeedToAll(target, 0, 0)
		}
		if buff.SetBraveSpeed > 0 {
			target.BraveSpeed = 0
			s.sendBraveToAll(target, 0, 0)
		}
	}
	handler.SendPlayerStatus(target.Session, target)
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

			if skillID == handler.SkillShapeChange {
				handler.UndoPoly(p, s.deps)
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
				case 87:
					handler.SendParalysis(p.Session, handler.StunRemove)
				case 157, 50, 80:
					handler.SendParalysis(p.Session, handler.FreezeRemove)
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

			if s.deps.Skills != nil {
				if sk := s.deps.Skills.Get(skillID); sk != nil && sk.SysMsgStop > 0 {
					handler.SendServerMessage(p.Session, uint16(sk.SysMsgStop))
				}
			}

			handler.SendPlayerStatus(p.Session, p)
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
//  工具函式
// ========================================================================

// playerKnowsSpell 檢查玩家是否已學會指定法術。
func (s *SkillSystem) playerKnowsSpell(player *world.PlayerInfo, skillID int32) bool {
	for _, sid := range player.KnownSpells {
		if sid == skillID {
			return true
		}
	}
	return false
}

// chebyshevDist 計算兩點間的切比雪夫距離。
func chebyshevDist(x1, y1, x2, y2 int32) int32 {
	dx := x1 - x2
	dy := y1 - y2
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}
