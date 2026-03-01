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

// ClearAllBuffsOnDeath implements handler.SkillManager — 死亡時清除所有 buff（含不可取消的）。
func (s *SkillSystem) ClearAllBuffsOnDeath(target *world.PlayerInfo) {
	if target.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range target.ActiveBuffs {
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
		}
		if buff.SetBraveSpeed > 0 {
			target.BraveSpeed = 0
			s.sendBraveToAll(target, 0, 0)
		}
	}
	handler.SendPlayerStatus(target.Session, target)
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

	// 絕對屏障：施法時自動解除（Java: C_UseSkill.java 第 353-358 行）
	if player.AbsoluteBarrier {
		s.cancelAbsoluteBarrier(player)
	}

	// 隱身：施法時自動解除（Java: L1BuffUtil.cancelInvisibility 在 C_UseSkill）
	if player.Invisible {
		s.cancelInvisibility(player)
	}

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

	// --- 召喚技能：委派 SummonSystem（資源消耗在內部驗證後處理）---
	if s.deps.Summon != nil {
		switch skillID {
		case 51:
			s.deps.Summon.ExecuteSummonMonster(sess, player, skill, targetID)
			return
		case 36:
			s.deps.Summon.ExecuteTamingMonster(sess, player, skill, targetID)
			return
		case 41:
			s.deps.Summon.ExecuteCreateZombie(sess, player, skill, targetID)
			return
		case 145:
			s.deps.Summon.ExecuteReturnToNature(sess, player, skill)
			return
		}
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

	// --- 物品強化技能：targetID = 背包物品 ObjectID ---
	// Java: 這些技能將 target_id 解釋為物品 ObjectID，不是角色/NPC ID
	switch skillID {
	case 21: // BLESSED_ARMOR（鎧甲護持）— 鎧甲 AC-3
		s.executeArmorEnchant(sess, player, skill, targetID)
		return
	case 12, 107: // ENCHANT_WEAPON（擬似魔法武器）/ SHADOW_FANG（暗影之牙）— 武器強化 buff
		s.executeWeaponEnchant(sess, player, skill, targetID)
		return
	case 73: // CREATE_MAGICAL_WEAPON（創造魔法武器）— 武器強化 +1
		s.executeCreateMagicalWeapon(sess, player, skill, targetID)
		return
	case 100: // BRING_STONE（提煉魔石）— 魔石升級鏈
		s.executeBringStone(sess, player, skill, targetID)
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
	actData := handler.BuildActionGfx(player.CharID, byte(skill.ActionID))
	handler.BroadcastToPlayers(nearby, actData)

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
		effData := handler.BuildSkillEffect(player.CharID, skill.CastGfx)
		handler.BroadcastToPlayers(nearby, effData)
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

	player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)

	// 起死回生術 (18)：對不死族 NPC 機率即死
	if skill.SkillID == 18 {
		s.executeTurnUndead(sess, player, skill, npc)
		return
	}

	// Triple Arrow (132)：消耗 1 箭矢
	if skill.SkillID == 132 {
		arrow := FindArrow(player, s.deps)
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
				atkData := handler.BuildAttackPacket(player.CharID, t.npc.ID, dmg, player.Heading)
				handler.BroadcastToPlayers(nearby, atkData)
				if skill.CastGfx > 0 {
					effData := handler.BuildSkillEffect(t.npc.ID, skill.CastGfx)
					handler.BroadcastToPlayers(nearby, effData)
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
				BreakNpcSleep(t.npc, ws)
			}

			// Mind Break: 吸收 MP
			if h == 0 && t.drainMP > 0 && t.npc.MP >= t.drainMP {
				t.npc.MP -= t.drainMP
			}

			// 技能傷害累加仇恨
			AddHate(t.npc, sess.ID, dmg)

			hpRatio := int16(0)
			if t.npc.MaxHP > 0 {
				hpRatio = int16((t.npc.HP * 100) / t.npc.MaxHP)
			}
			hpData := handler.BuildHpMeter(t.npc.ID, hpRatio)
			handler.BroadcastToPlayers(nearby, hpData)

			if t.npc.HP <= 0 {
				handleNpcDeath(t.npc, player, nearby, s.deps)
				break
			}
		}
	}

	// 吸血系技能：傷害轉為治療（Java: CHILL_TOUCH / VAMPIRIC_TOUCH — heal = this._dmg）
	if skill.SkillID == 28 || skill.SkillID == 10 {
		totalDmg := int16(0)
		for _, t := range hits {
			totalDmg += int16(t.dmg)
		}
		if totalDmg > 0 {
			player.HP += totalDmg
			if player.HP > player.MaxHP {
				player.HP = player.MaxHP
			}
			sendHpUpdate(sess, player)
		}
	}

	// 凍結類攻擊技能：傷害後 MR 判定凍結（Java: setFrozen + S_Poison 灰色）
	// 22=寒冰氣息, 30=岩牢, 80=冰雪颶風
	if skill.SkillID == 22 || skill.SkillID == 30 || skill.SkillID == 80 {
		for _, t := range hits {
			if t.npc.Dead || t.npc.Paralyzed || t.npc.HasDebuff(22) || t.npc.HasDebuff(30) || t.npc.HasDebuff(50) || t.npc.HasDebuff(80) {
				continue
			}
			if s.checkNpcMRResist(player, t.npc, skill.SkillID) {
				dur := skill.BuffDuration
				if dur <= 0 {
					switch skill.SkillID {
					case 22:
						dur = 8
					case 30:
						dur = 10
					case 80:
						dur = 16
					}
				}
				t.npc.Paralyzed = true
				t.npc.AddDebuff(skill.SkillID, (dur+1)*5)
				handler.BroadcastToPlayers(nearby, handler.BuildPoison(t.npc.ID, 2))
			}
		}
	}
}

// executeTurnUndead 起死回生術（skill 18）— 對不死族 NPC 機率即死。
// Java 參考: L1SkillUse.java TYPE_CURSE 分支，undeadType == 1 || 3 時 _dmg = currentHp。
// GFX：不走攻擊動畫，走 ActionGfx + SkillEffect（Java 明確排除 Turn Undead 的 S_UseAttackSkill）。
func (s *SkillSystem) executeTurnUndead(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, npc *world.NpcInfo) {
	ws := s.deps.World
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// 施法動畫（Java: S_DoActionGFX）
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))

	// 前置判定：目標必須是不死族（Java: undeadType == 1 || 3 且 isTU == true）
	if !npc.Undead {
		// 非不死族：施法動畫播放但不造成傷害
		return
	}

	// 目標特效（Java: S_SkillSound(targetId, castGfx=754)）
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
	}

	// 機率判定（Java: calcProbabilityMagic → default 分支）
	// diceCount = max(magicBonus + magicLevel, 1)
	// probability = sum of diceCount rolls of d7
	// 成功條件: probability >= random(1~100)
	magicLevel := int(player.Level) / 4 // 簡化：施法者等級 / 4
	magicBonus := int(player.Intel) - 12
	if magicBonus < 0 {
		magicBonus = 0
	}
	diceCount := magicLevel + magicBonus
	if diceCount < 1 {
		diceCount = 1
	}
	probability := 0
	for i := 0; i < diceCount; i++ {
		probability += world.RandInt(7) + 1 // 1~7
	}
	rnd := world.RandInt(100) + 1 // 1~100
	if probability < rnd {
		// 失敗
		handler.SendServerMessage(sess, skillMsgCastFail)
		return
	}

	// 成功：傷害 = 目標當前 HP（即死）
	dmg := npc.HP
	npc.HP = 0

	// 受傷時解除睡眠
	if npc.Sleeped {
		BreakNpcSleep(npc, ws)
	}

	// 即死傷害累加仇恨
	AddHate(npc, sess.ID, dmg)

	hpData := handler.BuildHpMeter(npc.ID, 0)
	handler.BroadcastToPlayers(nearby, hpData)

	_ = dmg // 即死傷害值，用於 handleNpcDeath 的經驗計算
	handleNpcDeath(npc, player, nearby, s.deps)
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

	// 魔法屏障攔截：對其他玩家施放非豁免技能時，檢查目標是否有 Counter Magic（buff 31）
	if target.CharID != player.CharID && s.tryCounterMagic(target, skill.SkillID) {
		// 技能被抵消，仍播放施法動畫但不產生效果
		nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
		return
	}

	// 玩家 debuff MR 抗性判定：對其他玩家施放 debuff 時必須通過 MR 檢查
	if target.CharID != player.CharID && playerDebuffSkills[skill.SkillID] {
		if !s.checkPlayerMRResist(player, target) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			// 仍播放施法動畫（Java: 即使 miss 也會播放動畫）
			nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
			handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
			if skill.CastGfx > 0 {
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
			}
			return
		}
	}

	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)

	// 廣播施法動畫
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))

	// 變形術：開啟怪物列表對話框
	if skill.SkillID == handler.SkillShapeChange {
		handler.SendShowPolyList(sess, player.CharID)
		return
	}

	// 即時效果技能
	switch skill.SkillID {
	case 9: // 解毒術
		CurePoison(target, s.deps)

	case 11: // 毒咒 — 對玩家施加傷害毒（Java: L1DamagePoison.doInfection(attacker, target, 3000, 5)）
		if target.CharID != player.CharID && target.PoisonType == 0 {
			target.PoisonType = 1
			target.PoisonTicksLeft = 150 // 30 秒 = 150 ticks
			target.PoisonDmgTimer = 0
			target.PoisonDmgAmount = 5 // 毒咒：每 3 秒扣 5 HP
			target.PoisonAttacker = sess.ID
			BroadcastPlayerPoison(target, 1, s.deps) // 綠色
		}

	case 23: // 能量感測 — 顯示目標的等級、HP/MP、屬性抗性等資訊
		if target.CharID != player.CharID {
			msg := fmt.Sprintf("\\f2【%s】 Lv.%d  HP:%d/%d  MP:%d/%d  AC:%d  MR:%d  火:%d 水:%d 風:%d 地:%d",
				target.Name, target.Level, target.HP, target.MaxHP, target.MP, target.MaxMP,
				target.AC, target.MR, target.FireRes, target.WaterRes, target.WindRes, target.EarthRes)
			handler.SendGlobalChat(sess, 9, msg)
		}

	case 20, 40: // 闇盲咒術 / 黑闇之影
		handler.SendCurseBlind(target.Session, 1)

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
			CurePoison(target, s.deps)
			CureCurseParalysis(target, s.deps)
			s.cancelAllBuffs(target)
		}
		// 施法時自身也解除隱身（Java: if srcpc.isInvisble() → srcpc.delInvis()）
		if player.Invisible {
			s.cancelInvisibility(player)
		}

	case 71: // 藥水霜化術 — 通知目標無法使用藥水
		if target.CharID != player.CharID {
			handler.SendServerMessage(target.Session, 698) // "喉嚨灼熱，無法喝東西。"
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
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, skill.CastGfx))
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

	player.Heading = CalcHeading(player.X, player.Y, npc.X, npc.Y)

	// 對 NPC 施放 debuff 技能 → 累加仇恨（讓 NPC 追擊施法者）
	AddHate(npc, sess.ID, 1)

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))

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
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
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
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
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
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
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
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
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
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("木乃伊詛咒(階段一)  施法者=%s  NPC=%s  延遲=5秒", player.Name, npc.Name))

	case 11: // 毒咒 — 對 NPC 施加傷害毒（Java: L1DamagePoison.doInfection, 3000ms, 5dmg）
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		npc.PoisonDmgAmt = 5
		npc.PoisonDmgTimer = 0
		npc.PoisonAttackerSID = sess.ID // 仇恨歸屬
		AddHate(npc, sess.ID, 1)
		npc.AddDebuff(11, 150) // 30 秒 = 150 ticks
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 1))
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("毒咒  施法者=%s  NPC=%s  持續=30秒  每次=5傷害", player.Name, npc.Name))

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
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("緩速術  施法者=%s  NPC=%s  技能=%d  持續=%d秒", player.Name, npc.Name, skill.SkillID, dur))

	case 50: // 冰矛 — NPC 凍結（Java: setFrozen + S_Poison 灰色）
		if npc.Paralyzed || npc.HasDebuff(50) || npc.HasDebuff(80) || npc.HasDebuff(22) || npc.HasDebuff(30) {
			break // 已被凍結
		}
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 8
		}
		npc.Paralyzed = true
		npc.AddDebuff(50, (dur+1)*5)
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("冰矛凍結  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur+1))

	case 80: // 冰雪颶風 — 對 NPC 施加凍結（Java: setFrozen + S_Poison 灰色）
		if npc.Paralyzed || npc.HasDebuff(50) || npc.HasDebuff(80) {
			break // 已被凍結/冰矛
		}
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			break // 抗性判定失敗不阻止傷害，只是不凍結
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 16
		}
		npc.Paralyzed = true
		npc.AddDebuff(80, (dur+1)*5) // Java: buffDuration + 1
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("冰雪颶風凍結  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur+1))

	case 47: // 弱化術 — NPC debuff（Java: DMG-5, HIT-1）
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 64
		}
		npc.AddDebuff(47, dur*5)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("弱化術  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur))

	case 56: // 疾病術 — NPC debuff（Java: DMG-6, AC+12）
		if !s.checkNpcMRResist(player, npc, skill.SkillID) {
			handler.SendServerMessage(sess, skillMsgCastFail)
			return
		}
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 64
		}
		npc.AddDebuff(56, dur*5)
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("疾病術  施法者=%s  NPC=%s  持續=%d秒", player.Name, npc.Name, dur))

	case 44: // 魔法相消術 — 解除 NPC 所有 debuff + 狀態（Java: CANCELLATION.java:158-167）
		// 清除所有 debuffs
		for debuffID := range npc.ActiveDebuffs {
			delete(npc.ActiveDebuffs, debuffID)
		}
		// 清除毒
		npc.PoisonDmgAmt = 0
		npc.PoisonDmgTimer = 0
		// 清除麻痺/凍結/睡眠
		npc.Paralyzed = false
		npc.Sleeped = false
		// 清除所有視覺效果（毒色/灰色）
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 0))
		// 施法特效
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
		}
		s.deps.Log.Info(fmt.Sprintf("魔法相消術(NPC)  施法者=%s  NPC=%s", player.Name, npc.Name))

	default:
		if skill.CastGfx > 0 {
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
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

// playerDebuffSkills 需要對玩家目標進行 MR 抗性判定的 debuff 技能。
// 這些技能對其他玩家施放時，必須通過魔法抗性檢查才能命中。
var playerDebuffSkills = map[int32]bool{
	11:  true, // 毒咒
	20:  true, // 闇盲咒術
	29:  true, // 緩速術
	33:  true, // 木乃伊詛咒
	40:  true, // 黑闇之影
	47:  true, // 弱化術
	56:  true, // 疾病術
	66:  true, // 沉睡之霧
	71:  true, // 藥水霜化術
	76:  true, // 集體緩速術
	103: true, // 暗黑盲咒
	152: true, // 究極緩速術
}

// checkPlayerMRResist 對玩家目標的魔法抗性判定（debuff 用）。
// 簡化版公式（Java L1MagicPc.calcProbabilityMagic 的核心概念）：
//
//	prob = 50 + (casterLevel - targetLevel) * 3 + casterINT - targetMR
//	clamp(prob, 10, 90)
//	success = rand(100) < prob
func (s *SkillSystem) checkPlayerMRResist(caster, target *world.PlayerInfo) bool {
	prob := 50 + (int(caster.Level)-int(target.Level))*3 + int(caster.Intel) - int(target.MR)
	if prob < 10 {
		prob = 10
	}
	if prob > 90 {
		prob = 90
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

	case 13, 72: // 無所遁形術 / 強力無所遁形術 — 揭示附近隱身玩家
		// Java 參考: L1SkillUse.detection() — 移除 buff 60（隱身術）和 97（暗影閃避）
		// 注意: GM 隱身免疫（isGmInvis），待 GM 系統實作後加入
		for _, tgt := range nearby {
			if tgt.CharID == player.CharID {
				continue
			}
			if tgt.HasBuff(60) || tgt.HasBuff(97) {
				s.removeBuffAndRevert(tgt, 60)
				s.removeBuffAndRevert(tgt, 97)
				// 通知被揭示者：你不再隱身
				handler.SendInvisible(tgt.Session, tgt.CharID, false)
				// 向附近玩家廣播角色出現（讓被揭示者重新顯示在其他人畫面上）
				nearbyOfTarget := s.deps.World.GetNearbyPlayersAt(tgt.X, tgt.Y, tgt.MapID)
				for _, viewer := range nearbyOfTarget {
					if viewer.CharID != tgt.CharID {
						handler.SendPutObject(viewer.Session, tgt)
					}
				}
			}
		}
		// 施法者自己若在隱身中也會被揭示
		if player.HasBuff(60) || player.HasBuff(97) {
			s.removeBuffAndRevert(player, 60)
			s.removeBuffAndRevert(player, 97)
			handler.SendInvisible(sess, player.CharID, false)
		}

	case 44: // 魔法相消術（自身）
		s.cancelAllBuffs(player)

	case 78: // 絕對屏障 — 免疫所有傷害，停止 HP/MP 回復
		// Java: 攻擊/施法/使用道具/裝備武器時解除；移動時不解除
		player.AbsoluteBarrier = true
		dur := skill.BuffDuration
		if dur <= 0 {
			dur = 12
		}
		abBuff := &world.ActiveBuff{
			SkillID:            skill.SkillID,
			TicksLeft:          dur * 5,
			SetAbsoluteBarrier: true,
		}
		old78 := player.AddBuff(abBuff)
		if old78 != nil {
			s.revertBuffStats(player, old78)
		}

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
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))

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
			handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(npc.ID, skill.CastGfx))
			npc.HP -= dmg
			if npc.HP < 0 {
				npc.HP = 0
			}
			// 攻擊技能傷害累加仇恨
			AddHate(npc, sess.ID, dmg)
			hpRatio := int16(0)
			if npc.MaxHP > 0 {
				hpRatio = int16((npc.HP * 100) / npc.MaxHP)
			}
			handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
			if npc.HP <= 0 {
				handleNpcDeath(npc, player, nearby, s.deps)
				continue
			}

			// 冰雪颶風：傷害後凍結判定（Java: calcProbabilityMagic → setFrozen + S_Poison 灰色）
			if skill.SkillID == 80 && !npc.Paralyzed && !npc.HasDebuff(50) && !npc.HasDebuff(80) {
				if s.checkNpcMRResist(player, npc, skill.SkillID) {
					dur := skill.BuffDuration
					if dur <= 0 {
						dur = 16
					}
					npc.Paralyzed = true
					npc.AddDebuff(80, (dur+1)*5)
					handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
				}
			}
		}
	}

	// 套用 buff 效果
	s.applyBuffEffect(player, skill)

	// 負重強化：套用時立即更新負重顯示
	if skill.SkillID == 14 || skill.SkillID == 218 {
		handler.SendWeightUpdate(sess, player)
	}

	// 效果 GFX
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
	}

	if skill.SysMsgHappen > 0 {
		handler.SendServerMessage(sess, uint16(skill.SysMsgHappen))
	}
}

// ========================================================================
//  鎧甲強化技能
// ========================================================================

// executeArmorEnchant 處理鎧甲護持（skill 21）— 物品強化技能。
// Java: targetID = 背包物品 ObjectID。檢查物品是否為身體鎧甲（type2=2, type=2），
// 是 → AC-3 buff + 訊息 161；否 → 訊息 79「沒有任何事情發生。」
func (s *SkillSystem) executeArmorEnchant(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, itemObjID int32) {
	// 查找背包物品
	invItem := player.Inv.FindByObjectID(itemObjID)
	if invItem == nil {
		handler.SendServerMessage(sess, 79) // 沒有任何事情發生。
		return
	}

	// 查詢物品模板 — 必須為身體鎧甲（Java: type2==2 && type==2）
	itemInfo := s.deps.Items.Get(invItem.ItemID)
	if itemInfo == nil || itemInfo.Category != data.CategoryArmor || itemInfo.Type != "armor" {
		handler.SendServerMessage(sess, 79) // 沒有任何事情發生。
		return
	}

	// 施法動畫 + GFX
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
	}

	// 套用 AC-3 buff（簡化：Java 是物品級 enchant，Go 用玩家 buff 代替）
	s.applyBuffEffect(player, skill)

	// 成功訊息（Java: S_ServerMessage 161 "{item} 的 {效果} 增加了。"）
	handler.SendServerMessage(sess, 161)
}

// executeWeaponEnchant 處理擬似魔法武器（skill 12）和暗影之牙（skill 107）— 武器強化 buff。
// Java: targetID = 背包物品 ObjectID。檢查物品是否為武器（type2=1），
// 是 → 套用武器強化 buff + icon；否 → 訊息 79。
func (s *SkillSystem) executeWeaponEnchant(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, itemObjID int32) {
	invItem := player.Inv.FindByObjectID(itemObjID)
	if invItem == nil {
		handler.SendServerMessage(sess, 79) // 沒有任何事情發生。
		return
	}

	// 查詢物品模板 — 必須為武器（Java: type2==1）
	itemInfo := s.deps.Items.Get(invItem.ItemID)
	if itemInfo == nil || itemInfo.Category != data.CategoryWeapon {
		handler.SendServerMessage(sess, 79)
		return
	}

	// 施法動畫 + GFX
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
	}

	// 套用 buff 效果
	s.applyBuffEffect(player, skill)

	handler.SendServerMessage(sess, 161)
}

// executeCreateMagicalWeapon 處理創造魔法武器（skill 73）— 武器強化 +1。
// Java: 僅可對 safe_enchant > 0 且 enchant_level == 0 的武器使用。
// Go 簡化：驗證物品為武器即可，完整強化邏輯待後續實作。
func (s *SkillSystem) executeCreateMagicalWeapon(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, itemObjID int32) {
	invItem := player.Inv.FindByObjectID(itemObjID)
	if invItem == nil {
		handler.SendServerMessage(sess, 79)
		return
	}

	itemInfo := s.deps.Items.Get(invItem.ItemID)
	if itemInfo == nil || itemInfo.Category != data.CategoryWeapon {
		handler.SendServerMessage(sess, 79)
		return
	}

	// safe_enchant 檢查（Java: safe_enchant <= 0 → msg 79）
	if itemInfo.SafeEnchant <= 0 {
		handler.SendServerMessage(sess, 79)
		return
	}

	// 只對未強化武器有效（Java: enchant_level != 0 → msg 79）
	if invItem.EnchantLvl != 0 {
		handler.SendServerMessage(sess, 79)
		return
	}

	// 廣播施法動畫
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
	}

	// 成功：強化 +1（100% 成功率）
	invItem.EnchantLvl = 1

	// 發送 msg 161：「%0 閃耀 %1 %2 的光芒」（Java: item_name, "$245", "$247"）
	itemName := invItem.Name
	if invItem.Identified {
		itemName = "+0 " + invItem.Name
	}
	handler.SendServerMessageArgs(sess, 161, itemName, "$245", "$247")

	// 更新物品名稱顯示
	handler.SendItemNameUpdate(sess, invItem, itemInfo)
	player.Dirty = true
}

// executeBringStone 處理提煉魔石（skill 100）— 魔石升級鏈。
// Java: 40320→40321→40322→40323→40324，各有不同成功率。
// Go 簡化：驗證物品為魔石即可，完整升級邏輯待後續實作。
func (s *SkillSystem) executeBringStone(sess *net.Session, player *world.PlayerInfo, skill *data.SkillInfo, itemObjID int32) {
	invItem := player.Inv.FindByObjectID(itemObjID)
	if invItem == nil {
		handler.SendServerMessage(sess, 79)
		return
	}

	// 檢查是否為可升級的魔石（Java: 40320/40321/40322/40323）
	switch invItem.ItemID {
	case 40320, 40321, 40322, 40323:
		// 有效的魔石
	default:
		handler.SendServerMessage(sess, 79)
		return
	}

	// 計算成功率與結果物品 ID
	rate, resultID, msgArg := calcBringStoneRate(player, invItem.ItemID)
	if resultID == 0 {
		handler.SendServerMessage(sess, 79)
		return
	}

	// 廣播施法動畫
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))
	if skill.CastGfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, skill.CastGfx))
	}

	// 消耗原石（Java: 無論成功失敗都消耗）
	removed := player.Inv.RemoveItem(invItem.ObjectID, 1)
	if removed {
		handler.SendRemoveInventoryItem(sess, invItem.ObjectID)
	} else {
		handler.SendItemCountUpdate(sess, invItem)
	}
	// 更新負重
	handler.SendWeightUpdate(sess, player)

	// 擲骰判定（Java: random.nextInt(100)+1，即 1~100）
	if world.RandInt(100)+1 <= rate {
		// 成功：新增升級石到背包
		resultInfo := s.deps.Items.Get(resultID)
		if resultInfo == nil {
			handler.SendServerMessage(sess, 280)
			player.Dirty = true
			return
		}
		newItem := player.Inv.AddItem(resultID, 1, resultInfo.Name, resultInfo.InvGfx, resultInfo.Weight, resultInfo.Stackable, byte(resultInfo.Bless))
		handler.SendAddItem(sess, newItem, resultInfo)
		handler.SendServerMessageStr(sess, 403, msgArg)
	} else {
		// 失敗：魔法失敗了
		handler.SendServerMessage(sess, 280)
	}
	handler.SendWeightUpdate(sess, player)
	player.Dirty = true
}

// calcBringStoneRate 計算提煉魔石的成功率。
// Java 公式：dark = floor(10 + level*0.8 + (wis-6)*1.2)，逐級除以常數。
func calcBringStoneRate(p *world.PlayerInfo, itemID int32) (rate int, resultID int32, msgArg string) {
	dark := int(10 + float64(p.Level)*0.8 + float64(p.Wis-6)*1.2)
	brave := int(float64(dark) / 2.1)
	wise := int(float64(brave) / 2.0)
	kayser := int(float64(wise) / 1.9)

	switch itemID {
	case 40320:
		return dark, 40321, "$2475"
	case 40321:
		return brave, 40322, "$2476"
	case 40322:
		return wise, 40323, "$2477"
	case 40323:
		return kayser, 40324, "$2478"
	}
	return 0, 0, ""
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
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(player.CharID, byte(skill.ActionID)))

	// 施法者在 nearby 中（GetNearbyPlayersAt 不排除），直接廣播即可
	handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(player.CharID, int32(skill.CastGfx)))

	// 傳送時取消交易
	handler.CancelTradeIfActive(player, s.deps)

	// --- 集體傳送(69)：施法者傳送前先收集同公會成員 ---
	var clanMembers []*world.PlayerInfo
	if skill.SkillID == 69 && player.ClanID != 0 {
		for _, member := range nearby {
			if member.CharID == player.CharID {
				continue
			}
			if member.ClanID != player.ClanID {
				continue
			}
			if chebyshevDist(player.X, player.Y, member.X, member.Y) > 3 {
				continue
			}
			clanMembers = append(clanMembers, member)
		}
	}

	handler.TeleportPlayer(sess, player, destX, destY, destMapID, destHeading, s.deps)

	// --- 集體傳送(69)：傳送血盟成員到相同目的地 ---
	for _, member := range clanMembers {
		handler.CancelTradeIfActive(member, s.deps)
		handler.TeleportPlayer(member.Session, member, destX, destY, destMapID, destHeading, s.deps)
	}
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
			case 157, 50, 80, 22, 30:
				handler.SendParalysis(target.Session, handler.FreezeApply)
				// 凍結類：廣播灰色色調給附近所有玩家（Java: S_Poison type=2）
				broadcastPlayerPoison(target, 2, s.deps)
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
		buff.DeltaMaxHP != 0 || buff.DeltaMaxMP != 0 || buff.DeltaAC != 0 ||
		buff.DeltaDmgMod != 0 || buff.DeltaHitMod != 0 {
		handler.SendPlayerStatus(target.Session, target)
	}

	s.sendBuffIcon(target, skill.SkillID, uint16(skill.BuffDuration))
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

	// 通知周圍玩家重新顯示此角色（下一 tick VisibilitySystem 也會處理，
	// 但主動 SendPutObject 讓解除更即時）
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		if viewer.CharID != player.CharID {
			handler.SendPutObject(viewer.Session, player)
		}
	}
}

// CancelInvisibility 匯出版本，供 combat/handler 呼叫。
func (s *SkillSystem) CancelInvisibility(player *world.PlayerInfo) {
	if player.Invisible {
		s.cancelInvisibility(player)
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
	213: true, 214: true, 216: true, 219: true, 228: true, 230: true,
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
	nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
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
	if buff.SetAbsoluteBarrier {
		target.AbsoluteBarrier = false
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

	// 追蹤需要發送的客戶端通知（迴圈結束後統一發送）
	needFreezeRemove := false
	needStunRemove := false
	needParalysisRemove := false
	needSleepRemove := false
	needInvisRemove := false

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
		}
		if buff.SetBraveSpeed > 0 {
			target.BraveSpeed = 0
			s.sendBraveToAll(target, 0, 0)
		}

		// 追蹤麻痺/凍結/暈眩類型
		if buff.SetParalyzed {
			switch skillID {
			case 87:
				needStunRemove = true
			case 157, 50, 80, 22, 30:
				needFreezeRemove = true
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
	// 隱身解除通知 + 周圍玩家重新顯示
	if needInvisRemove {
		handler.SendInvisible(target.Session, target.CharID, false)
		nearby := s.deps.World.GetNearbyPlayersAt(target.X, target.Y, target.MapID)
		for _, viewer := range nearby {
			if viewer.CharID != target.CharID {
				handler.SendPutObject(viewer.Session, target)
			}
		}
	}

	// 重新檢查是否仍有非 buff 來源的麻痺（毒麻痺/詛咒麻痺）
	if shouldStayParalyzed(target, false, false) {
		target.Paralyzed = true
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
				case 87:
					handler.SendParalysis(p.Session, handler.StunRemove)
				case 157, 50, 80, 22, 30:
					handler.SendParalysis(p.Session, handler.FreezeRemove)
					// 清除灰色色調
					broadcastPlayerPoison(p, 0, s.deps)
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

			if s.deps.Skills != nil {
				if sk := s.deps.Skills.Get(skillID); sk != nil && sk.SysMsgStop > 0 {
					handler.SendServerMessage(p.Session, uint16(sk.SysMsgStop))
				}
			}

			handler.SendPlayerStatus(p.Session, p)
		} else if buff.SetParalyzed && buff.TicksLeft%25 == 0 {
			// 3.80C 客戶端灰色色調會自動淡出，每 5 秒重發維持視覺
			switch skillID {
			case 157, 50, 80, 22, 30:
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
