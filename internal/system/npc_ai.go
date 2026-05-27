package system

import (
	"math"
	"math/rand"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	gonet "github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/scripting"
	"github.com/l1jgo/server/internal/world"
)

// NpcAISystem processes NPC AI via Lua: Go handles target detection + command
// execution, Lua handles all decision logic. Guard NPCs use a simpler Go-only
// AI path. Phase 2 (Update).
type NpcAISystem struct {
	world *world.State
	deps  *handler.Deps
}

type npcShockStunApplier interface {
	ApplyNpcShockStun(caster *world.NpcInfo, target *world.PlayerInfo, skill *data.SkillInfo, leverage int)
}

type npcAreaShockStunApplier interface {
	ApplyNpcAreaShockStun(caster *world.NpcInfo, targets []*world.PlayerInfo)
}

const (
	mobSkillPotionTurnToDamage      int32 = 4011
	mobSkillPolluteWater            int32 = 4012
	mobSkillHealTurnToDamage        int32 = 4013
	mobSkillDecayPotion             int32 = 71
	mobSkillAreaWeaponBreakGfxID    int32 = 172
	mobSkillPotionTurnToDamageGfxID int32 = 7781
	mobSkillPolluteWaterGfxID       int32 = 7782
	mobSkillHealTurnToDamageGfxID   int32 = 7780
	mobSkillSpecialAreaStatusTicks        = 60
	mobSkillCurseParalyzeDelayTicks       = 25
	mobSkillTurnUndeadID                  = 18
	mobSkillKirtasBarrier4          int32 = 11057
	mobSkillKirtasBarrier3          int32 = 11058
	mobSkillKirtasBarrier2          int32 = 11059
	mobSkillKirtasBarrier1          int32 = 11060
	mobSkillLindviorSkySpiked       int32 = 11061
	mobSkillKirtasBarrierTicks            = 1 << 30
)

func NewNpcAISystem(ws *world.State, deps *handler.Deps) *NpcAISystem {
	return &NpcAISystem{world: ws, deps: deps}
}

func (s *NpcAISystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

func (s *NpcAISystem) Update(_ time.Duration) {
	for _, npc := range s.world.NpcList() {
		if npc.Dead {
			continue
		}
		// Guard AI: separate branch — simple Go logic, no Lua needed.
		if npc.Impl == "L1Guard" {
			s.tickGuardAI(npc)
			continue
		}
		if npc.Impl == "L1Monster" {
			s.tickMonsterAI(npc)
			continue
		}
		// 非戰鬥 NPC：有 passive_speed 的才隨機行走（鳥、村莊 NPC 等）
		if npc.MoveSpeed > 0 {
			s.tickNpcRandomWalk(npc)
		}
	}
}

// ---------- Non-combat NPC Random Walk ----------

// tickNpcRandomWalk 處理非戰鬥 NPC 的隨機行走（鳥、村莊 NPC 等）。
// Java 參考：L1NpcInstance.noTarget() — 方向 0-7 = 8 方位移動，8-39 = 暫停。
// 距離出生點超過 8 格時，強制走向出生點。
func (s *NpcAISystem) tickNpcRandomWalk(npc *world.NpcInfo) {
	if npc.MoveTimer > 0 {
		npc.MoveTimer--
		return
	}

	// 距離出生點超過 8 格 → 走回家
	if chebyshev32(npc.X, npc.Y, npc.SpawnX, npc.SpawnY) > 8 {
		npcWander(s.world, npc, -2, s.deps.MapData)
		return
	}

	// 還有剩餘步數 → 繼續同方向走
	if npc.WanderDist > 0 {
		npcWander(s.world, npc, -1, s.deps.MapData)
		return
	}

	// 隨機選擇新動作（Java: random 0-39）
	dir := rand.Intn(40)
	if dir < 8 {
		// 0-7: 向該方向移動
		npcWander(s.world, npc, dir, s.deps.MapData)
	} else {
		// 8-39: 暫停不動（機率 80%）
		npc.MoveTimer = calcNpcMoveTicks(npc)
	}
}

// ---------- Monster AI (Lua-driven) ----------

func (s *NpcAISystem) tickMonsterAI(npc *world.NpcInfo) {
	tickNpcKirtasBarrierLikeJava(npc)
	if npc.HiddenStatus != world.NpcHiddenNone {
		return
	}

	// NPC 法術中毒 tick（每 3 秒扣血）
	tickNpcPoison(npc, s.world, s.deps)
	tickNpcMobSkillDelaysLikeJava(npc)

	// 負面狀態：麻痺/暈眩/凍結/睡眠時跳過所有行動
	if npc.Paralyzed || npc.Sleeped {
		tickNpcDebuffs(npc, s.world, s.deps)
		return
	}
	// 即使沒被控也要遞減 debuff 計時器（如致盲等不影響行動的 debuff）
	tickNpcDebuffs(npc, s.world, s.deps)

	// Decrement timers
	if npc.AttackTimer > 0 {
		npc.AttackTimer--
	}
	if npc.MoveTimer > 0 {
		npc.MoveTimer--
	}

	// --- 目標檢測（含仇恨列表回退） ---
	var target *world.PlayerInfo
	if npc.AggroTarget != 0 {
		target = s.world.GetBySession(npc.AggroTarget)
		if !s.npcCanKeepTargetLikeJava(npc, target) {
			// 當前目標失效 → 從仇恨列表移除，嘗試回退到次高仇恨
			RemoveHateTarget(npc, npc.AggroTarget)
			npc.AggroTarget = 0
			target = nil
			// 嘗試仇恨列表中的下一個目標
			for {
				nextSID := GetMaxHateTarget(npc)
				if nextSID == 0 {
					break
				}
				if nextTarget := s.world.GetBySession(nextSID); s.npcCanKeepTargetLikeJava(npc, nextTarget) {
					npc.AggroTarget = nextSID
					target = nextTarget
					break
				}
				RemoveHateTarget(npc, nextSID)
			}
		}
		// 注意：不在此處檢查安全區域。被動仇恨（被攻擊）不受安全區域限制。
		// 安全區域只阻止主動索敵（agro scan），由下方處理。
		// Java 行為：隱藏之谷等新手區整張地圖都是安全區域，怪物被打一定會反擊。
	}

	// Agro mobs scan for new target if none
	var nearbyPlayers []*world.PlayerInfo
	if target == nil && npc.Agro {
		nearbyPlayers = s.npcNearbyPlayersLikeJava(npc)
		bestDist := int32(999)
		for _, p := range nearbyPlayers {
			if !s.npcCanAcquireTargetLikeJava(npc, p) {
				continue
			}
			// Skip players in safety zones (Java: getZoneType() == 1)
			if s.deps.MapData != nil &&
				s.deps.MapData.IsSafetyZone(p.MapID, p.X, p.Y) {
				continue
			}
			dist := chebyshev32(npc.X, npc.Y, p.X, p.Y)
			if dist <= 8 && dist < bestDist {
				bestDist = dist
				target = p
			}
		}
		if target != nil {
			npc.AggroTarget = target.SessionID
			npc.MoveTimer = 0 // snap out of wander — react immediately
			npc.WanderDist = 0
		}
	}

	// 附近無玩家 → 回家 + 跳過 Lua（複用 agro 掃描結果，避免重複 AOI 查詢）
	if target == nil {
		if nearbyPlayers == nil {
			nearbyPlayers = s.npcNearbyPlayersLikeJava(npc)
		}
		if len(nearbyPlayers) == 0 {
			// 無目標 + 無附近玩家 → 傳送回出生點
			if npc.X != npc.SpawnX || npc.Y != npc.SpawnY {
				s.npcTeleportHome(npc)
			}
			npc.AggroTarget = 0
			ClearHateList(npc)
			return
		}
	}

	// --- Build AIContext for Lua ---
	targetDist := int32(0)
	targetID, targetAC, targetLevel := 0, 0, 0
	targetX, targetY := int32(0), int32(0)
	if target != nil {
		targetDist = chebyshev32(npc.X, npc.Y, target.X, target.Y)
		targetID = int(target.CharID)
		targetAC = int(target.AC)
		targetLevel = int(target.Level)
		targetX = target.X
		targetY = target.Y
	}

	spawnDist := chebyshev32(npc.X, npc.Y, npc.SpawnX, npc.SpawnY)
	canUseMobSkill := s.npcCanUseMobSkillAgainstTargetLikeJava(npc, target)

	// Convert mob skills to Lua entries
	var mobSkills []scripting.MobSkillEntry
	if canUseMobSkill && s.deps.MobSkills != nil {
		if skills := s.deps.MobSkills.Get(npc.NpcID); skills != nil {
			mobSkills = make([]scripting.MobSkillEntry, 0, len(skills))
			for _, sk := range skills {
				if npcMobSkillReuseDelayActiveLikeJava(npc, sk) {
					continue
				}
				if npcMobSkillTypeDelayActiveLikeJava(npc, sk) {
					continue
				}
				if npcMobSkillTriggerCountReachedLikeJava(npc, sk) {
					continue
				}
				skillTargetDist := targetDist
				companionTargetID := int32(0)
				triggerCompanionHP := sk.TriggerCompanionHP
				if sk.TriggerCompanionHP > 0 {
					companion, hasCompanion := s.npcMobSkillCompanionTargetLikeJava(npc, sk.TriggerCompanionHP)
					if hasCompanion && companion == nil {
						continue
					}
					if companion != nil {
						skillTargetDist = chebyshev32(npc.X, npc.Y, companion.X, companion.Y)
						companionTargetID = companion.ID
					} else {
						triggerCompanionHP = 0
					}
				}
				mobSkills = append(mobSkills, scripting.MobSkillEntry{
					ActNo:              sk.ActNo,
					SkillID:            sk.SkillID,
					Type:               sk.Type,
					MpConsume:          sk.MpConsume,
					TriggerRandom:      sk.TriggerRandom,
					TriggerHP:          sk.TriggerHP,
					TriggerCompanionHP: triggerCompanionHP,
					TriggerRange:       sk.TriggerRange,
					TriggerCount:       sk.TriggerCount,
					ReuseDelay:         sk.ReuseDelay,
					TargetDist:         int(skillTargetDist),
					Range:              sk.Range,
					AreaWidth:          sk.AreaWidth,
					AreaHeight:         sk.AreaHeight,
					ActID:              sk.ActID,
					GfxID:              sk.GfxID,
					Leverage:           sk.Leverage,
					ChangeTarget:       sk.ChangeTarget,
					CompanionTargetID:  companionTargetID,
					SummonID:           sk.SummonID,
					SummonMin:          sk.SummonMin,
					SummonMax:          sk.SummonMax,
					PolyID:             sk.PolyID,
				})
			}
		}
	}

	ctx := scripting.AIContext{
		NpcID:       int(npc.NpcID),
		X:           int(npc.X),
		Y:           int(npc.Y),
		MapID:       int(npc.MapID),
		HP:          int(npc.HP),
		MaxHP:       int(npc.MaxHP),
		MP:          int(npc.MP),
		MaxMP:       int(npc.MaxMP),
		Level:       int(npc.Level),
		AtkDmg:      int(npc.AtkDmg),
		AtkSpeed:    int(npc.AtkSpeed),
		MoveSpeed:   int(npc.MoveSpeed),
		Ranged:      int(npc.Ranged),
		Agro:        npc.Agro,
		TargetID:    targetID,
		TargetX:     int(targetX),
		TargetY:     int(targetY),
		TargetDist:  int(targetDist),
		TargetAC:    targetAC,
		TargetLevel: targetLevel,
		CanAttack:   npc.AttackTimer <= 0,
		CanMove:     npc.MoveTimer <= 0,
		Skills:      mobSkills,
		WanderDist:  npc.WanderDist,
		SpawnDist:   int(spawnDist),
	}

	// --- Call Lua AI ---
	cmds := s.deps.Scripting.RunNpcAI(ctx)

	// --- Execute commands ---
	for _, cmd := range cmds {
		switch cmd.Type {
		case "attack":
			if target != nil {
				s.npcMeleeAttack(npc, target)
				setNpcAtkCooldown(npc)
			}
		case "ranged_attack":
			if target != nil {
				s.npcRangedAttack(npc, target)
				setNpcAtkCooldown(npc)
			}
		case "skill":
			if !canUseMobSkill {
				continue
			}
			used := false
			if cmd.CompanionTargetID != 0 {
				used = s.executeNpcCompanionSkillLikeJava(npc, s.world.GetNpc(cmd.CompanionTargetID), cmd.SkillID, cmd.GfxID, cmd.Leverage)
			} else if cmd.ChangeTarget == 2 {
				// 自我施法（治療/加速等）
				used = s.executeNpcSelfSkill(npc, cmd.SkillID, cmd.GfxID, cmd.Leverage)
			} else if target != nil {
				skillTarget := target
				if cmd.ChangeTarget == 3 {
					skillTarget = s.npcRandomMobSkillTargetLikeJava(npc, target, cmd.TriggerRange)
				}
				if cmd.SkillType == 1 {
					used = s.executeNpcPhysicalSkill(npc, skillTarget, cmd.ActID, cmd.GfxID, cmd.Leverage, cmd.Range, cmd.AreaWidth, cmd.AreaHeight)
				} else {
					used = s.executeNpcSkill(npc, skillTarget, cmd.SkillID, cmd.ActID, cmd.GfxID, cmd.Leverage)
				}
			}
			if used {
				recordNpcMobSkillUseLikeJava(npc, cmd.ActNo)
				recordNpcMobSkillReuseDelayLikeJava(npc, cmd.ActNo, cmd.SkillType, cmd.ReuseDelay)
				if cmd.SkillType == 2 {
					s.setNpcMobSkillTypeTwoCooldownLikeJava(npc, cmd.SkillID)
				} else {
					setNpcAtkCooldown(npc)
				}
			}
		case "summon":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcSummon(npc, cmd.SummonID, cmd.SummonMin, cmd.SummonMax, cmd.GfxID))
		case "poly":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcPolyLikeJava(npc, cmd.PolyID, cmd.ActID))
		case "area_shock_stun":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaShockStun(npc, cmd.ActID))
		case "area_silence":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaSilenceLikeJava(npc, cmd.ActID))
		case "area_cancellation":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaCancellationLikeJava(npc, cmd.ActID))
		case "area_weapon_break":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaWeaponBreakLikeJava(npc, cmd.ActID))
		case "area_potion_turn_to_damage":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaPotionTurnToDamageLikeJava(npc, cmd.ActID))
		case "area_pollute_water_wave":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaPolluteWaterWaveLikeJava(npc, cmd.ActID))
		case "area_heal_turn_to_damage":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaHealTurnToDamageLikeJava(npc, cmd.ActID))
		case "area_wind_shackle":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaWindShackleLikeJava(npc, cmd.ActID, cmd.GfxID))
		case "area_debuff":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaDebuffLikeJava(npc, cmd.SkillID, cmd.Leverage, cmd.ActID, cmd.GfxID))
		case "area_decay_potion":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaDecayPotionLikeJava(npc, cmd.ActID))
		case "area_poison":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaPoisonLikeJava(npc, cmd.SummonID, cmd.Leverage, cmd.ActID))
		case "spawn_effect":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcSpawnEffectLikeJava(npc, cmd.SummonID, cmd.Leverage, cmd.ActID))
		case "area_curse_paralyze":
			if !canUseMobSkill {
				continue
			}
			recordNpcSubMagicMobSkillSuccessLikeJava(npc, cmd, s.executeNpcAreaCurseParalyzeLikeJava(npc, cmd.ActID))
		case "flee":
			if target != nil {
				npcFleeFrom(s.world, npc, target.X, target.Y, s.deps.MapData)
				npc.MoveTimer = calcNpcMoveTicks(npc)
			}
		case "move_toward":
			if target != nil {
				npcMoveToward(s.world, npc, target.X, target.Y, s.deps.MapData)
				npc.MoveTimer = calcNpcMoveTicks(npc)
			}
		case "wander":
			npcWander(s.world, npc, cmd.Dir, s.deps.MapData)
		case "lose_aggro":
			npc.AggroTarget = 0
		}
	}
}

// ---------- Guard AI (Go-only) ----------

// tickGuardAI processes a single guard NPC's AI each tick.
// Guards hunt wanted players (isWanted), counter-attack when hit, and return home when idle.
func (s *NpcAISystem) tickGuardAI(npc *world.NpcInfo) {
	// NPC 法術中毒 tick（每 3 秒扣血）
	tickNpcPoison(npc, s.world, s.deps)

	// 負面狀態：麻痺/暈眩/凍結/睡眠時跳過所有行動
	if npc.Paralyzed || npc.Sleeped {
		tickNpcDebuffs(npc, s.world, s.deps)
		return
	}
	tickNpcDebuffs(npc, s.world, s.deps)

	// Decrement timers
	if npc.AttackTimer > 0 {
		npc.AttackTimer--
	}
	if npc.MoveTimer > 0 {
		npc.MoveTimer--
	}

	// --- Target validation ---
	var target *world.PlayerInfo
	if npc.AggroTarget != 0 {
		target = s.world.GetBySession(npc.AggroTarget)
		if target == nil || target.Dead || target.MapID != npc.MapID {
			npc.AggroTarget = 0
			target = nil
		}
		// Lose aggro if target is too far (Java: getTileLineDistance() > 30)
		if target != nil && chebyshev32(npc.X, npc.Y, target.X, target.Y) > 30 {
			npc.AggroTarget = 0
			target = nil
		}
	}

	// --- Target search: scan for wanted players ---
	if target == nil {
		nearby := s.nearbyNpcViewersAt(npc, npc.X, npc.Y, npc.MapID)
		bestDist := int32(999)
		for _, p := range nearby {
			if p.Dead || p.Invisible {
				continue
			}
			// Java L1GuardInstance.searchTarget(): 只追殺通緝犯（isWanted）
			// PinkName（桃紅名＝暫時攻擊他人）不觸發警衛追殺
			if p.WantedTicks <= 0 {
				continue
			}
			dist := chebyshev32(npc.X, npc.Y, p.X, p.Y)
			if dist <= 8 && dist < bestDist {
				bestDist = dist
				target = p
			}
		}
		if target != nil {
			npc.AggroTarget = target.SessionID
			npc.MoveTimer = 0
		}
	}

	// --- Has target: chase and attack ---
	if target != nil {
		dist := chebyshev32(npc.X, npc.Y, target.X, target.Y)
		atkRange := int32(npc.Ranged)
		if atkRange < 1 {
			atkRange = 1
		}

		if dist <= atkRange {
			if npc.AttackTimer <= 0 {
				if npc.Ranged > 1 {
					// 遠程攻擊需要視線
					if s.deps.MapData != nil && !s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, target.X, target.Y) {
						// LOS 失敗 → 嘗試靠近目標
						if npc.MoveTimer <= 0 {
							npcMoveToward(s.world, npc, target.X, target.Y, s.deps.MapData)
							npc.MoveTimer = calcNpcMoveTicks(npc)
						}
					} else {
						s.npcRangedAttack(npc, target)
						setNpcAtkCooldown(npc)
					}
				} else {
					s.npcMeleeAttack(npc, target)
					setNpcAtkCooldown(npc)
				}
			}
		} else {
			if npc.MoveTimer <= 0 {
				npcMoveToward(s.world, npc, target.X, target.Y, s.deps.MapData)
				moveTicks := calcNpcMoveTicks(npc)
				npc.MoveTimer = moveTicks
			}
		}
		return
	}

	// --- No target: return home ---
	if npc.X != npc.SpawnX || npc.Y != npc.SpawnY {
		homeDist := chebyshev32(npc.X, npc.Y, npc.SpawnX, npc.SpawnY)
		if homeDist > 30 {
			s.guardTeleportHome(npc)
			return
		}
		if npc.MoveTimer <= 0 {
			npcMoveToward(s.world, npc, npc.SpawnX, npc.SpawnY, s.deps.MapData)
			moveTicks := calcNpcMoveTicks(npc)
			npc.MoveTimer = moveTicks
		}
	}
}

// guardTeleportHome instantly moves a guard back to its spawn point.
func (s *NpcAISystem) guardTeleportHome(npc *world.NpcInfo) {
	oldX, oldY := npc.X, npc.Y

	// 通知舊位置附近玩家：移除 NPC + 解鎖格子
	oldNearby := s.nearbyNpcViewersAt(npc, oldX, oldY, npc.MapID)
	rmData := handler.BuildRemoveObject(npc.ID)
	handler.BroadcastToPlayers(oldNearby, rmData)

	// Update map passability
	if s.deps.MapData != nil {
		s.deps.MapData.SetImpassable(npc.MapID, oldX, oldY, false)
		s.deps.MapData.SetImpassable(npc.SpawnMapID, npc.SpawnX, npc.SpawnY, true)
	}

	// Update position (NPC AOI grid + entity grid)
	s.world.UpdateNpcPosition(npc.ID, npc.SpawnX, npc.SpawnY, 0)
	npc.MapID = npc.SpawnMapID

	// 通知新位置附近玩家：顯示 NPC + 封鎖格子
	newNearby := s.nearbyNpcViewersAt(npc, npc.X, npc.Y, npc.MapID)
	for _, viewer := range newNearby {
		sendNpcPack(viewer.Session, npc)
	}
}

// npcTeleportHome 將怪物瞬移回出生點（附近無玩家時觸發）。
func (s *NpcAISystem) npcTeleportHome(npc *world.NpcInfo) {
	oldX, oldY := npc.X, npc.Y

	// 通知舊位置附近玩家：移除 NPC + 解鎖格子
	oldNearby := s.nearbyNpcViewersAt(npc, oldX, oldY, npc.MapID)
	rmData := handler.BuildRemoveObject(npc.ID)
	handler.BroadcastToPlayers(oldNearby, rmData)

	if s.deps.MapData != nil {
		s.deps.MapData.SetImpassable(npc.MapID, oldX, oldY, false)
		s.deps.MapData.SetImpassable(npc.SpawnMapID, npc.SpawnX, npc.SpawnY, true)
	}

	s.world.UpdateNpcPosition(npc.ID, npc.SpawnX, npc.SpawnY, 0)
	npc.MapID = npc.SpawnMapID

	// 回家時回滿血（Java: NPC 回家 = 重置狀態）
	npc.HP = npc.MaxHP
	npc.MP = npc.MaxMP

	// 通知新位置附近玩家
	newNearby := s.nearbyNpcViewersAt(npc, npc.X, npc.Y, npc.MapID)
	for _, viewer := range newNearby {
		sendNpcPack(viewer.Session, npc)
	}
}

func (s *NpcAISystem) nearbyNpcViewersAt(npc *world.NpcInfo, x, y int32, mapID int16) []*world.PlayerInfo {
	return nearbyNpcViewersFromState(s.world, npc, x, y, mapID)
}

func nearbyNpcViewersFromState(ws *world.State, npc *world.NpcInfo, x, y int32, mapID int16) []*world.PlayerInfo {
	if ws == nil || npc == nil {
		return nil
	}
	return ws.GetNearbyPlayersInShow(x, y, mapID, 0, npc.ShowID)
}

// ---------- NPC Combat ----------

func (s *NpcAISystem) npcMeleeAttack(npc *world.NpcInfo, target *world.PlayerInfo) {
	if !s.npcCanAttackPosition(npc, target) {
		return
	}

	// 目標絕對屏障：免疫所有傷害（Java: L1AttackNpc.dmg0）
	if target.AbsoluteBarrier {
		npc.AggroTarget = 0 // NPC 無法攻擊屏障目標，清除仇恨
		return
	}

	// 被攻擊時解除睡眠
	if target.Sleeped {
		target.Sleeped = false
		target.RemoveBuff(62)
		target.RemoveBuff(66)
		target.RemoveBuff(103)
		handler.SendParalysis(target.Session, handler.SleepRemove)
	}

	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)

	res := s.deps.Scripting.CalcNpcMelee(scripting.CombatContext{
		AttackerLevel:  int(npc.Level),
		AttackerSTR:    int(npc.STR),
		AttackerDEX:    int(npc.DEX),
		AttackerWeapon: int(npc.AtkDmg),
		TargetAC:       int(target.AC),
		TargetLevel:    int(target.Level),
	})

	damage := int32(res.Damage)
	if !res.IsHit || damage < 0 {
		damage = 0
	}
	damage = applyNpcWeaponBreakDamage(npc, damage)
	damage = applyImmuneToHarmDamage(target, damage)
	damage = applyReductionArmorDamage(target, damage, false)

	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)

	// 反擊屏障（skill 91）：近戰攻擊時機率觸發反彈
	// Java 參考: L1AttackNpc.calcDamage() — 檢查 target.hasSkillEffect(COUNTER_BARRIER)
	// Java probability 公式（L1MagicPc.java:670-674 case COUNTER_BARRIER）：
	//   probability = l1skills.probabilityValue (SQL=25) + (target.Level - npc.Level) + COUNTER_BARRIER_ROM (33)
	// 即 58 + lvlDiff。yiwei 預設 `各職業技能相關設置.properties:13 COUNTER_BARRIER_ROM = 33`，
	// skill 91 SQL probability_value=25。
	if damage > 0 && target.HasBuff(91) {
		prob := 25 + int(target.Level) - int(npc.Level) + 33
		if world.RandInt(100)+1 <= prob {
			// 計算反彈傷害（Java: calcCounterBarrierDamage — NPC 版本：(STR + Level) << 1）
			cbDmg := int32((int(npc.STR) + int(npc.Level)) << 1)
			// 套用設定倍率（Java: ConfigSkill.COUNTER_BARRIER_DMG = 1.5）
			cbDmg = cbDmg * 3 / 2
			if cbDmg > 0 {
				// 播放反擊屏障觸發特效（GFX 10710）
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, 10710))
				// 原始攻擊傷害歸零
				damage = 0
				if !npcDamageBlockedBySkm0LikeJava(npc) {
					// 反彈傷害施加在 NPC 身上
					npc.HP -= cbDmg
					if npc.HP < 0 {
						npc.HP = 0
					}
					// 如果 NPC 被反彈殺死
					if npc.HP <= 0 {
						hpData := handler.BuildHpMeter(npc.ID, 0)
						handler.BroadcastToPlayers(nearby, hpData)
						handleNpcDeath(npc, target, nearby, s.deps)
						npc.AggroTarget = 0
						return
					}
					// 廣播 NPC HP 條
					hpRatio := int16(0)
					if npc.MaxHP > 0 {
						hpRatio = int16((npc.HP * 100) / npc.MaxHP)
					}
					handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
				}
			}
		}
	}

	// 致命身軀（skill 191）：NPC 近戰 23% 機率反彈 40 傷害給 NPC
	// 對齊 Java `L1PcInstance.java:2788-2796` else-if `attackNpc != null` 分支：
	// 在 CounterBarrier 後檢查，若觸發 → NPC 扣 40 HP，原始傷害歸零。
	if newDmg, reflected := mortalBodyReflectFromNpc(target, npc, damage, nearby); reflected {
		damage = newDmg
		if npc.HP <= 0 {
			hpData := handler.BuildHpMeter(npc.ID, 0)
			handler.BroadcastToPlayers(nearby, hpData)
			handleNpcDeath(npc, target, nearby, s.deps)
			npc.AggroTarget = 0
			return
		}
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	}

	atkData := buildNpcAttack(npc.ID, target.CharID, damage, npc.Heading)
	handler.BroadcastToPlayers(nearby, atkData)

	if damage <= 0 {
		return
	}

	target.HP -= int32(damage)
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		// 守衛擊殺：PK count -1 + 清除通緝（Java L1PcInstance:7393）
		if npc.Impl == "L1Guard" && target.PKCount > 0 {
			target.PKCount--
			target.WantedTicks = 0
		}
		s.deps.Death.KillPlayer(target)
		npc.AggroTarget = 0
		return
	}
	sendHPUpdate(target.Session, target.HP, target.MaxHP)

	// 怪物施毒判定（Java L1AttackNpc.addNpcPoisonAttack）
	if npc.PoisonAtk > 0 {
		ApplyNpcPoisonAttack(npc, target, s.world, s.deps)
	}
}

func (s *NpcAISystem) npcRangedAttack(npc *world.NpcInfo, target *world.PlayerInfo) {
	if !s.npcCanAttackPosition(npc, target) {
		return
	}

	// 目標絕對屏障：免疫所有傷害
	if target.AbsoluteBarrier {
		npc.AggroTarget = 0
		return
	}

	// 被攻擊時解除睡眠
	if target.Sleeped {
		target.Sleeped = false
		target.RemoveBuff(62)
		target.RemoveBuff(66)
		target.RemoveBuff(103)
		handler.SendParalysis(target.Session, handler.SleepRemove)
	}

	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)
	targetDodge := 0
	if npc.Ranged >= 10 && chebyshev32(npc.X, npc.Y, target.X, target.Y) >= 2 {
		targetDodge = int(calcPlayerErLikeYiwei(target))
	}

	res := s.deps.Scripting.CalcNpcRanged(scripting.CombatContext{
		AttackerLevel:  int(npc.Level),
		AttackerSTR:    int(npc.STR),
		AttackerDEX:    int(npc.DEX),
		AttackerWeapon: int(npc.AtkDmg),
		TargetAC:       int(target.AC),
		TargetLevel:    int(target.Level),
		TargetDodge:    targetDodge,
	})

	damage := int32(res.Damage)
	if !res.IsHit || damage < 0 {
		damage = 0
	}
	damage = applyNpcWeaponBreakDamage(npc, damage)
	damage = applyImmuneToHarmDamage(target, damage)
	damage = applyReductionArmorDamage(target, damage, false)

	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	rngData := buildNpcRangedAttack(npc.ID, target.CharID, damage, npc.Heading,
		npc.X, npc.Y, target.X, target.Y)
	handler.BroadcastToPlayers(nearby, rngData)

	if damage <= 0 {
		return
	}

	target.HP -= int32(damage)
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		// 守衛擊殺：PK count -1 + 清除通緝（Java L1PcInstance:7393）
		if npc.Impl == "L1Guard" && target.PKCount > 0 {
			target.PKCount--
			target.WantedTicks = 0
		}
		s.deps.Death.KillPlayer(target)
		npc.AggroTarget = 0
		return
	}
	sendHPUpdate(target.Session, target.HP, target.MaxHP)

	// 怪物施毒判定（Java L1AttackNpc.addNpcPoisonAttack）
	if npc.PoisonAtk > 0 {
		ApplyNpcPoisonAttack(npc, target, s.world, s.deps)
	}
}

// executeNpcSkill handles an NPC using a skill on a player.
// leverage > 0 表示 type 1 物理技能，傷害 = STR * leverage / 10。
func (s *NpcAISystem) executeNpcSkill(npc *world.NpcInfo, target *world.PlayerInfo, skillID, actID, gfxID, leverage int) bool {
	if npc == nil || target == nil {
		return false
	}
	skill := s.deps.Skills.Get(int32(skillID))
	if skill == nil {
		s.npcMeleeAttack(npc, target)
		return false
	}
	targetNone := skill.Target == "none"
	if !targetNone {
		if !npcSkillPlayerTargetAllowedLikeJava(npc, target) {
			return false
		}

		// Java L1SkillUse.isTarget(): most NPC target skills fail before runSkill on Absolute Barrier.
		if target.AbsoluteBarrier {
			return false
		}
	}

	if consumeNpcSilenceForMobSkillLikeJava(npc) {
		return false
	}

	// Java L1SkillUse.makeTargetList(): NPC target skills must still pass the skill table range.
	if !targetNone && !npcSkillTargetInRangeLikeJava(npc, target, skill.Ranged) {
		return false
	}

	// Consume MP
	if skill.MpConsume > 0 {
		npc.MP -= int32(skill.MpConsume)
		if npc.MP < 0 {
			npc.MP = 0
		}
	}

	effectTargetID := target.CharID
	effectTargetX, effectTargetY := target.X, target.Y
	if targetNone {
		effectTargetID = npc.ID
		effectTargetX, effectTargetY = npc.X, npc.Y
	}

	npc.Heading = calcNpcHeading(npc.X, npc.Y, effectTargetX, effectTargetY)
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	areaCenterX, areaCenterY := effectTargetX, effectTargetY

	// Spell visual effect: mob-specific gfx_id takes priority, fallback to skill's CastGfx
	gfx := skill.CastGfx
	if gfxID > 0 {
		gfx = int32(gfxID)
	}

	// Determine if this is a magic projectile (has dice/damage) or physical/buff skill
	isMagicProjectile := skill.DamageValue > 0 || skill.DamageDice > 0

	if isMagicProjectile {
		// LOS 檢查（Java: L1SkillUse — glanceCheck）
		if s.deps.MapData != nil && !s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, effectTargetX, effectTargetY) {
			return false // 視線被牆阻擋
		}
		if skill.Area > 0 {
			// AoE 技能：傷害範圍內所有玩家
			useType := byte(8)
			// 先對主目標發送技能動畫
			skillAtkData := buildNpcUseAttackSkill(npc.ID, effectTargetID,
				0, npc.Heading, gfx, useType,
				npc.X, npc.Y, effectTargetX, effectTargetY)
			handler.BroadcastToPlayers(nearby, skillAtkData)

			// 對範圍內每個玩家獨立計算傷害
			area := int32(skill.Area)
			for _, p := range nearby {
				if !npcSkillPlayerTargetAllowedLikeJava(npc, p) || p.AbsoluteBarrier {
					continue
				}
				if !skill.Through && s.deps.MapData != nil && !s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, p.X, p.Y) {
					continue
				}
				if chebyshev32(areaCenterX, areaCenterY, p.X, p.Y) > area {
					continue
				}
				if skill.SkillID == mobSkillLindviorSkySpiked {
					broadcastNpcLindviorSkySpikedEffectLikeJava(npc, p.X, p.Y, nearby)
				}
				sctx := scripting.SkillDamageContext{
					SkillID:         int(skill.SkillID),
					DamageValue:     skill.DamageValue,
					DamageDice:      skill.DamageDice,
					DamageDiceCount: skill.DamageDiceCount,
					SkillLevel:      skill.SkillLevel,
					Attr:            skill.Attr,
					AttackerLevel:   int(npc.Level),
					AttackerSTR:     int(npc.STR),
					AttackerDEX:     int(npc.DEX),
					TargetAC:        int(p.AC),
					TargetLevel:     int(p.Level),
					TargetMR:        int(p.MR),
				}
				res := s.deps.Scripting.CalcSkillDamage(sctx)
				dmg := int32(res.Damage)
				if dmg < 1 {
					dmg = 1
				}
				dmg = applyNpcMagicLeverageLikeJava(dmg, leverage)
				dmg = applyDragonEyeMagicDamageReductionLikeYiwei(p, dmg, world.RandInt(100), nearby)
				p.HP -= int32(dmg)
				p.Dirty = true
				if p.HP <= 0 {
					p.HP = 0
					s.deps.Death.KillPlayer(p)
					if p.SessionID == npc.AggroTarget {
						npc.AggroTarget = 0
					}
				} else {
					sendHPUpdate(p.Session, p.HP, p.MaxHP)
				}
			}
		} else {
			// 單目標魔法攻擊
			sctx := scripting.SkillDamageContext{
				SkillID:         int(skill.SkillID),
				DamageValue:     skill.DamageValue,
				DamageDice:      skill.DamageDice,
				DamageDiceCount: skill.DamageDiceCount,
				SkillLevel:      skill.SkillLevel,
				Attr:            skill.Attr,
				AttackerLevel:   int(npc.Level),
				AttackerSTR:     int(npc.STR),
				AttackerDEX:     int(npc.DEX),
				TargetAC:        int(target.AC),
				TargetLevel:     int(target.Level),
				TargetMR:        int(target.MR),
			}
			res := s.deps.Scripting.CalcSkillDamage(sctx)
			damage := int32(res.Damage)
			if damage < 1 {
				damage = 1
			}
			damage = applyNpcMagicLeverageLikeJava(damage, leverage)
			damage = applyImmuneToHarmDamage(target, damage)
			damage = applyReductionArmorDamage(target, damage, false)
			damage = applyDragonEyeMagicDamageReductionLikeYiwei(target, damage, world.RandInt(100), nearby)
			if skill.SkillID == mobSkillLindviorSkySpiked {
				broadcastNpcLindviorSkySpikedEffectLikeJava(npc, target.X, target.Y, nearby)
			}

			skillAtkData := buildNpcUseAttackSkill(npc.ID, target.CharID,
				int16(damage), npc.Heading, gfx, 6,
				npc.X, npc.Y, target.X, target.Y)
			handler.BroadcastToPlayers(nearby, skillAtkData)

			target.HP -= int32(damage)
			target.Dirty = true
			if target.HP <= 0 {
				target.HP = 0
				s.deps.Death.KillPlayer(target)
				npc.AggroTarget = 0
				return true
			}
			sendHPUpdate(target.Session, target.HP, target.MaxHP)
		}
	} else {
		// 非傷害技能（debuff）：發送特效 + 套用 debuff 狀態
		if skill.SkillID == 87 {
			if applier, ok := s.deps.Skill.(npcShockStunApplier); ok {
				shockSkill := *skill
				if actID > 0 {
					shockSkill.ActionID = actID
				}
				if gfxID > 0 {
					shockSkill.CastGfx = int32(gfxID)
				}
				applier.ApplyNpcShockStun(npc, target, &shockSkill, leverage)
				return true
			}
		}
		if gfx > 0 {
			effectID := target.CharID
			if targetNone {
				effectID = npc.ID
			}
			effData := handler.BuildSkillEffect(effectID, gfx)
			handler.BroadcastToPlayers(nearby, effData)
		}
		if targetNone && s.executeNpcKirtasBarrierLikeJava(npc, skill.SkillID, nearby) {
			return true
		}
		// 透過 SkillManager 套用 buff/debuff 效果（麻痺、睡眠、減速等）
		if s.deps.Skill != nil {
			s.deps.Skill.ApplyNpcDebuff(target, skill)
		}
	}
	return true
}

func broadcastNpcLindviorSkySpikedEffectLikeJava(npc *world.NpcInfo, targetX, targetY int32, nearby []*world.PlayerInfo) {
	gfx := npcLindviorSkySpikedEffectGfxLikeJava(calcNpcHeading(npc.X, npc.Y, targetX, targetY))
	if gfx == 0 {
		return
	}
	handler.BroadcastToPlayers(nearby, buildEffectLocation(targetX, targetY, gfx))
}

func npcLindviorSkySpikedEffectGfxLikeJava(dir int16) int32 {
	switch dir {
	case 3, 4:
		return 7987
	case 5:
		return 8050
	case 6, 7:
		return 8051
	default:
		return 0
	}
}

func (s *NpcAISystem) executeNpcKirtasBarrierLikeJava(npc *world.NpcInfo, skillID int32, nearby []*world.PlayerInfo) bool {
	if npc == nil {
		return false
	}
	actionStatus := byte(0)
	switch skillID {
	case mobSkillKirtasBarrier1:
		actionStatus = 20
		npc.AddDebuff(mobSkillKirtasBarrier1, mobSkillKirtasBarrierTicks)
	case mobSkillKirtasBarrier2:
		actionStatus = 40
		npc.AddDebuff(mobSkillKirtasBarrier2, mobSkillKirtasBarrierTicks)
	case mobSkillKirtasBarrier3:
		actionStatus = 4
		npc.AddDebuff(mobSkillKirtasBarrier3, mobSkillKirtasBarrierTicks)
		npc.AddDebuff(78, mobSkillKirtasBarrierTicks)
	case mobSkillKirtasBarrier4:
		actionStatus = 24
		npc.AddDebuff(mobSkillKirtasBarrier4, mobSkillKirtasBarrierTicks)
		npc.AddDebuff(mobSkillKirtasBarrier1, mobSkillKirtasBarrierTicks)
		npc.AddDebuff(mobSkillKirtasBarrier2, mobSkillKirtasBarrierTicks)
	default:
		return false
	}
	setNpcHidden(npc, world.NpcHiddenKirtas, actionStatus)
	npc.KirtasBarrierTicks = 0
	for _, viewer := range nearby {
		sendNpcPack(viewer.Session, npc)
	}
	return true
}

func tickNpcKirtasBarrierLikeJava(npc *world.NpcInfo) {
	if npc == nil || npc.NpcID != 81163 || npc.HiddenStatus != world.NpcHiddenKirtas {
		return
	}
	if npc.HasDebuff(mobSkillKirtasBarrier4) ||
		npc.HasDebuff(mobSkillKirtasBarrier3) ||
		npc.HasDebuff(mobSkillKirtasBarrier2) ||
		npc.HasDebuff(mobSkillKirtasBarrier1) {
		npc.KirtasBarrierTicks++
	}
}

func applyNpcMagicLeverageLikeJava(damage int32, leverage int) int32 {
	if leverage <= 0 {
		return damage
	}
	damage = damage * int32(leverage) / 10
	if damage < 1 {
		return 1
	}
	return damage
}

func calcNpcHealingLikeJava(npc *world.NpcInfo, skill *data.SkillInfo, leverage int) int32 {
	if npc == nil || skill == nil || skill.DamageDice <= 0 {
		return 0
	}
	magicBonus := calcMagicBonusLikeJava(int(npc.Intel))
	if magicBonus > 10 {
		magicBonus = 10
	}
	diceCount := skill.DamageValue + magicBonus
	if diceCount <= 0 {
		return 0
	}
	heal := int32(0)
	for i := 0; i < diceCount; i++ {
		heal += int32(rand.Intn(skill.DamageDice) + 1)
	}
	if npc.Lawful > 0 {
		heal = int32(float64(heal) * (1.0 + float64(npc.Lawful)/32768.0))
	}
	if leverage <= 0 {
		leverage = 10
	}
	heal = int32(float64(heal) * (float64(leverage) / 10.0))
	if heal < 0 {
		return 0
	}
	return heal
}

func applyNpcMagicHealingModifiersLikeJava(target *world.NpcInfo, heal int32) int32 {
	if target == nil || heal <= 0 {
		return heal
	}
	if target.HasDebuff(skillPolluteWater) {
		heal >>= 1
	}
	if target.HasDebuff(mobSkillPolluteWater) {
		heal >>= 1
	}
	if target.HasDebuff(mobSkillHealTurnToDamage) {
		heal *= -1
	}
	return heal
}

func (s *NpcAISystem) executeNpcAreaShockStun(npc *world.NpcInfo, actID int) bool {
	if npc == nil || npc.MapID == 93 {
		return false
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 1
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	if applier, ok := s.deps.Skill.(npcAreaShockStunApplier); ok {
		applier.ApplyNpcAreaShockStun(npc, nearby)
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaSilenceLikeJava(npc *world.NpcInfo, actID int) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.HasBuff(64) || p.HasBuff(161) {
			continue
		}
		s.broadcastNpcAreaTargetSkillEffectLikeJava(p, 10708)
		p.Silenced = true
		p.AddBuff(&world.ActiveBuff{
			SkillID:     64,
			TicksLeft:   80,
			SetSilenced: true,
		})
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaCancellationLikeJava(npc *world.NpcInfo, actID int) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	if s.deps != nil && s.deps.Skill != nil {
		for _, p := range nearby {
			if p != nil {
				s.deps.Skill.CancelAllBuffs(p)
			}
		}
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaWeaponBreakLikeJava(npc *world.NpcInfo, actID int) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.Session == nil {
			continue
		}
		weapon := p.Equip.Weapon()
		if weapon == nil {
			continue
		}
		s.broadcastNpcAreaTargetSkillEffectLikeJava(p, mobSkillAreaWeaponBreakGfxID)
		handler.SendServerMessageArgs(p.Session, 268, itemLogName(weapon))
		applyWeaponBreakDurability(weapon, s.calcNpcWeaponBreakDurabilityDamageLikeJava(npc))
		syncEquippedFlagFromSlots(p, weapon)
		if s.deps != nil && s.deps.Items != nil {
			if itemInfo := s.deps.Items.Get(weapon.ItemID); itemInfo != nil {
				handler.SendItemStatusUpdate(p.Session, weapon, itemInfo)
			}
		}
		p.Dirty = true
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) broadcastNpcAreaTargetSkillEffectLikeJava(target *world.PlayerInfo, gfxID int32) {
	if s == nil || s.world == nil || target == nil {
		return
	}
	viewers := s.world.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)
	handler.BroadcastToPlayers(viewers, handler.BuildSkillEffect(target.CharID, gfxID))
}

func (s *NpcAISystem) calcNpcWeaponBreakDurabilityDamageLikeJava(npc *world.NpcInfo) int8 {
	maxDamage := 1
	if s != nil && s.deps != nil && s.deps.Npcs != nil && npc != nil {
		if template := s.deps.Npcs.Get(npc.NpcID); template != nil {
			maxDamage = int(template.INT) / 3
		}
	}
	if maxDamage < 1 {
		maxDamage = 1
	}
	return int8(world.RandInt(maxDamage) + 1)
}

func (s *NpcAISystem) executeNpcAreaPotionTurnToDamageLikeJava(npc *world.NpcInfo, actID int) bool {
	return s.executeNpcSpecialAreaStatusLikeJava(npc, actID, mobSkillPotionTurnToDamage, mobSkillPotionTurnToDamageGfxID, true)
}

func (s *NpcAISystem) executeNpcAreaPolluteWaterWaveLikeJava(npc *world.NpcInfo, actID int) bool {
	return s.executeNpcSpecialAreaStatusLikeJava(npc, actID, mobSkillPolluteWater, mobSkillPolluteWaterGfxID, false)
}

func (s *NpcAISystem) executeNpcAreaHealTurnToDamageLikeJava(npc *world.NpcInfo, actID int) bool {
	return s.executeNpcSpecialAreaStatusLikeJava(npc, actID, mobSkillHealTurnToDamage, mobSkillHealTurnToDamageGfxID, false)
}

func (s *NpcAISystem) executeNpcSpecialAreaStatusLikeJava(npc *world.NpcInfo, actID int, skillID, gfxID int32, blockDecayPotion bool) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.HasBuff(mobSkillPotionTurnToDamage) || p.HasBuff(mobSkillPolluteWater) || p.HasBuff(mobSkillHealTurnToDamage) {
			continue
		}
		if blockDecayPotion && p.HasBuff(mobSkillDecayPotion) {
			continue
		}
		s.broadcastNpcAreaTargetSkillEffectLikeJava(p, gfxID)
		p.AddBuff(&world.ActiveBuff{
			SkillID:   skillID,
			TicksLeft: mobSkillSpecialAreaStatusTicks,
		})
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaWindShackleLikeJava(npc *world.NpcInfo, actID, gfxID int) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.HasBuff(167) {
			continue
		}
		s.broadcastNpcAreaTargetSkillEffectLikeJava(p, int32(gfxID))
		if p.Session != nil {
			handler.SendWindShackle(p.Session, p.CharID, 16)
		}
		p.AddBuff(&world.ActiveBuff{
			SkillID:   167,
			TicksLeft: 80,
		})
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaDebuffLikeJava(npc *world.NpcInfo, skillID, durationSec, actID, gfxID int) bool {
	if npc == nil || npc.MapID == 93 || skillID <= 0 || s == nil || s.world == nil || s.deps == nil || s.deps.Skills == nil || s.deps.Skill == nil {
		return false
	}
	skill := s.deps.Skills.Get(int32(skillID))
	if skill == nil {
		return false
	}
	effectiveSkill := *skill
	if durationSec > 0 {
		effectiveSkill.BuffDuration = durationSec
	}
	effectGfx := int32(gfxID)
	if effectGfx <= 0 {
		effectGfx = skill.CastGfx
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.HasBuff(effectiveSkill.SkillID) {
			continue
		}
		if effectiveSkill.SkillID == mobSkillDecayPotion && p.HasBuff(mobSkillPotionTurnToDamage) {
			continue
		}
		if effectGfx > 0 {
			s.broadcastNpcAreaTargetSkillEffectLikeJava(p, effectGfx)
		}
		s.deps.Skill.ApplyNpcDebuff(p, &effectiveSkill)
	}
	actionID := actID
	if actionID <= 0 {
		actionID = skill.ActionID
	}
	if actionID > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	}
	return true
}

func (s *NpcAISystem) executeNpcAreaDecayPotionLikeJava(npc *world.NpcInfo, actID int) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil || s.deps == nil || s.deps.Skills == nil || s.deps.Skill == nil {
		return false
	}
	skill := s.deps.Skills.Get(71)
	if skill == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.HasBuff(71) {
			continue
		}
		s.deps.Skill.ApplyNpcDebuff(p, skill)
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaPoisonLikeJava(npc *world.NpcInfo, summonID int32, durationSec, actID int) bool {
	if npc == nil || npc.MapID == 93 || summonID == 0 || s == nil || s.world == nil || s.deps == nil || s.deps.Npcs == nil {
		return false
	}
	if s.deps.Npcs.Get(summonID) == nil {
		return false
	}
	if durationSec <= 0 {
		durationSec = 10
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || isGMInvisible(p) {
			continue
		}
		spawnNpcGroundEffectLikeJava(s.world, s.deps.Npcs, npc, 0, summonID, world.GroundEffectPoisonCloud, p.X, p.Y, durationSec*groundEffectTickSec)
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 1
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcSpawnEffectLikeJava(npc *world.NpcInfo, summonID int32, durationSec, actID int) bool {
	if npc == nil || npc.MapID == 93 || summonID == 0 || s == nil || s.world == nil || s.deps == nil || s.deps.Npcs == nil {
		return false
	}
	if durationSec <= 0 {
		durationSec = 10
	}
	if _, ok := spawnNpcGroundEffectLikeJava(s.world, s.deps.Npcs, npc, 0, summonID, world.GroundEffectNpcEffect, npc.X, npc.Y, durationSec*groundEffectTickSec); !ok {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	actionID := actID
	if actionID <= 0 {
		actionID = 1
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) executeNpcAreaCurseParalyzeLikeJava(npc *world.NpcInfo, actID int) bool {
	if npc == nil || npc.MapID == 93 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	for _, p := range nearby {
		if p == nil || p.Session == nil || p.Paralyzed || p.CurseType != 0 || p.HasBuff(157) || p.HasBuff(50) || p.HasBuff(80) {
			continue
		}
		p.CurseType = 1
		p.CurseTicksLeft = mobSkillCurseParalyzeDelayTicks
		s.broadcastNpcAreaTargetPoisonLikeJava(p, 2)
		handler.SendServerMessage(p.Session, 212)
	}
	actionID := actID
	if actionID <= 0 {
		actionID = 19
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, byte(actionID)))
	return true
}

func (s *NpcAISystem) broadcastNpcAreaTargetPoisonLikeJava(target *world.PlayerInfo, poisonType byte) {
	if s == nil || s.world == nil || target == nil {
		return
	}
	viewers := s.world.GetNearbyPlayersInShow(target.X, target.Y, target.MapID, 0, target.ShowID)
	handler.BroadcastToPlayers(viewers, handler.BuildPoison(target.CharID, poisonType))
}

// executeNpcPhysicalSkill 處理 NPC type 1 物理技能（leverage 倍率傷害）。
// Java 參考：L1MobSkillUse — type == 1 時 damage = STR * leverage / 10，
// 播放 actID 動作動畫（非魔法 GFX），走物理命中判定。
func (s *NpcAISystem) executeNpcPhysicalSkill(npc *world.NpcInfo, target *world.PlayerInfo, actID, gfxID, leverage, skillRange, areaWidth, areaHeight int) bool {
	if npc == nil || target == nil {
		return false
	}
	if skillRange > 0 && chebyshev32(npc.X, npc.Y, target.X, target.Y) > int32(skillRange) {
		return false
	}
	if !s.npcHasTargetSightLikeJava(npc, target) {
		return false
	}

	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)
	targets := []*world.PlayerInfo{target}
	if areaHeight > 0 {
		targets = s.npcPhysicalSkillAreaTargetsLikeJava(npc, target, areaWidth, areaHeight)
		if len(targets) == 0 {
			return false
		}
	}
	nearby := s.nearbyNpcViewersAt(npc, npc.X, npc.Y, npc.MapID)

	if gfxID > 0 {
		effData := handler.BuildSkillEffect(npc.ID, int32(gfxID))
		handler.BroadcastToPlayers(nearby, effData)
	}

	for _, hitTarget := range targets {
		if hitTarget == nil {
			continue
		}
		res := s.deps.Scripting.CalcNpcMelee(scripting.CombatContext{
			AttackerLevel:  int(npc.Level),
			AttackerSTR:    int(npc.STR),
			AttackerDEX:    int(npc.DEX),
			AttackerWeapon: int(npc.AtkDmg),
			TargetAC:       int(hitTarget.AC),
			TargetLevel:    int(hitTarget.Level),
		})

		if !res.IsHit {
			if hitTarget.CharID == target.CharID {
				handler.BroadcastToPlayers(nearby, buildNpcAttackWithAction(npc.ID, target.CharID, 0, npc.Heading, actID))
			}
			continue
		}

		damage := int32(res.Damage)
		if leverage > 0 {
			// yiwei mobskill type 1 可用 leverage 覆寫物理技能倍率。
			damage = int32(npc.STR) * int32(leverage) / 10
		}
		if damage < 1 {
			damage = 1
		}
		damage = applyImmuneToHarmDamage(hitTarget, damage)
		damage = applyReductionArmorDamage(hitTarget, damage, false)

		if hitTarget.CharID == target.CharID {
			handler.BroadcastToPlayers(nearby, buildNpcAttackWithAction(npc.ID, target.CharID, damage, npc.Heading, actID))
		}

		if hitTarget.Sleeped {
			hitTarget.Sleeped = false
			hitTarget.RemoveBuff(62)
			hitTarget.RemoveBuff(66)
			hitTarget.RemoveBuff(103)
			handler.SendParalysis(hitTarget.Session, handler.SleepRemove)
		}

		hitTarget.HP -= int32(damage)
		hitTarget.Dirty = true
		if hitTarget.HP <= 0 {
			hitTarget.HP = 0
			if s.deps != nil && s.deps.Death != nil {
				s.deps.Death.KillPlayer(hitTarget)
			}
			if hitTarget.SessionID == npc.AggroTarget {
				npc.AggroTarget = 0
			}
			continue
		}
		sendHPUpdate(hitTarget.Session, hitTarget.HP, hitTarget.MaxHP)
	}
	return true
}

func (s *NpcAISystem) npcPhysicalSkillAreaTargetsLikeJava(npc *world.NpcInfo, target *world.PlayerInfo, areaWidth, areaHeight int) []*world.PlayerInfo {
	if s == nil || s.world == nil || npc == nil || target == nil || areaHeight <= 0 {
		return nil
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	result := make([]*world.PlayerInfo, 0, len(nearby))
	for _, p := range nearby {
		if p == nil || p.Dead || isGMInvisible(p) {
			continue
		}
		if !npcPhysicalSkillBoxContainsLikeJava(npc, p, npc.Heading, areaWidth, areaHeight) {
			continue
		}
		if s.deps != nil && s.deps.MapData != nil && !s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, p.X, p.Y) {
			continue
		}
		result = append(result, p)
	}
	return result
}

func npcPhysicalSkillBoxContainsLikeJava(npc *world.NpcInfo, target *world.PlayerInfo, heading int16, width, height int) bool {
	if npc == nil || target == nil || target.MapID != npc.MapID || height <= 0 {
		return false
	}
	if npc.X == target.X && npc.Y == target.Y {
		return true
	}
	if heading < 0 || heading > 7 {
		heading = 0
	}
	headingRotate := [8]int{6, 7, 0, 1, 2, 3, 4, 5}
	theta := float64(headingRotate[heading]) * math.Pi / 4
	cosTheta := math.Cos(theta)
	sinTheta := math.Sin(theta)
	x1 := target.X - npc.X
	y1 := target.Y - npc.Y
	rotX := int(math.Round(float64(x1)*cosTheta + float64(y1)*sinTheta))
	rotY := int(math.Round(-float64(x1)*sinTheta + float64(y1)*cosTheta))
	distance := int(chebyshev32(npc.X, npc.Y, target.X, target.Y))
	return rotX > 0 && distance <= height && rotY >= -width && rotY <= width
}

func (s *NpcAISystem) npcCanAttackPosition(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || target.Dead || target.MapID != npc.MapID || target.ShowID != npc.ShowID {
		return false
	}
	attackRange := int(npc.Ranged)
	if attackRange <= 0 {
		attackRange = 1
	}
	if chebyshev32(npc.X, npc.Y, target.X, target.Y) > int32(attackRange) {
		return false
	}
	return s.npcHasTargetSightLikeJava(npc, target)
}

func (s *NpcAISystem) npcNearbyPlayersLikeJava(npc *world.NpcInfo) []*world.PlayerInfo {
	if s == nil || s.world == nil || npc == nil {
		return nil
	}
	return s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
}

func (s *NpcAISystem) npcCanKeepTargetLikeJava(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || target.HP <= 0 || target.Dead {
		return false
	}
	if target.MapID != npc.MapID || target.ShowID != npc.ShowID {
		return false
	}
	if chebyshev32(npc.X, npc.Y, target.X, target.Y) > 30 {
		return false
	}
	inHate := npcHasHateTarget(npc, target)
	if target.Invisible && !inHate {
		return false
	}
	if target.AccessLevel >= 200 && !inHate {
		return false
	}
	if !inHate {
		if !s.npcHasMoveDirectionToTargetLikeJava(npc, target) {
			return false
		}
		if !s.npcHasTargetSightLikeJava(npc, target) {
			return false
		}
	}
	return true
}

func (s *NpcAISystem) npcCanAcquireTargetLikeJava(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || target.HP <= 0 || target.Dead {
		return false
	}
	if target.MapID != npc.MapID || target.ShowID != npc.ShowID {
		return false
	}
	if target.AccessLevel >= 200 || target.Invisible {
		return false
	}
	return s.npcHasMoveDirectionToTargetLikeJava(npc, target) && s.npcHasTargetSightLikeJava(npc, target)
}

func npcHasHateTarget(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || npc.HateList == nil {
		return false
	}
	_, ok := npc.HateList[target.SessionID]
	return ok
}

func (s *NpcAISystem) npcHasMoveDirectionToTargetLikeJava(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil {
		return false
	}
	if s == nil || s.deps == nil || s.deps.MapData == nil || s.world == nil {
		return true
	}
	if npc.X == target.X && npc.Y == target.Y {
		return true
	}
	for _, c := range npcStepCandidatesToward(npc, target.X, target.Y) {
		if c.x == npc.X && c.y == npc.Y {
			continue
		}
		if !s.deps.MapData.IsInMap(npc.MapID, c.x, c.y) {
			continue
		}
		h := calcNpcHeading(npc.X, npc.Y, c.x, c.y)
		if !s.deps.MapData.IsPassable(npc.MapID, npc.X, npc.Y, int(h)) {
			continue
		}
		occupant := s.world.OccupantAt(c.x, c.y, npc.MapID)
		if occupant > 0 && occupant != target.CharID && occupant < 200_000_000 {
			continue
		}
		return true
	}
	return false
}

func (s *NpcAISystem) npcHasTargetSightLikeJava(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || target.MapID != npc.MapID {
		return false
	}
	if s == nil || s.deps == nil || s.deps.MapData == nil {
		return true
	}
	return s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, target.X, target.Y)
}

func (s *NpcAISystem) npcCanUseMobSkillAgainstTargetLikeJava(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || target.HP <= 0 || target.Dead || target.MapID != npc.MapID || target.ShowID != npc.ShowID {
		return false
	}
	// Java L1NpcInstance.attack(): _mobSkill.skillUse(target) 只會在攻擊位置成立，
	// 或至少 glanceCheck(target.x, target.y) 成立時被呼叫。
	if s.deps != nil && s.deps.MapData != nil {
		return s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, target.X, target.Y)
	}
	return true
}

func (s *NpcAISystem) npcRandomMobSkillTargetLikeJava(npc *world.NpcInfo, fallback *world.PlayerInfo, triggerRange int) *world.PlayerInfo {
	candidates := s.npcNearbyPlayersLikeJava(npc)
	valid := make([]*world.PlayerInfo, 0, len(candidates))
	for _, p := range candidates {
		if p == nil || p.HP <= 0 || p.Dead || p.MapID != npc.MapID || p.ShowID != npc.ShowID || isGMInvisible(p) {
			continue
		}
		if !npcHasHateTarget(npc, p) {
			continue
		}
		distance := chebyshev32(npc.X, npc.Y, p.X, p.Y)
		if !mobSkillTriggerDistanceLikeJava(triggerRange, distance) {
			continue
		}
		if s != nil && s.deps != nil && s.deps.MapData != nil && !s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, p.X, p.Y) {
			continue
		}
		valid = append(valid, p)
	}
	if len(valid) == 0 {
		return fallback
	}
	return valid[rand.Intn(len(valid))]
}

func (s *NpcAISystem) npcMobSkillCompanionTargetLikeJava(npc *world.NpcInfo, triggerCompanionHP int) (*world.NpcInfo, bool) {
	if s == nil || s.world == nil || npc == nil || npc.Family == "" || triggerCompanionHP <= 0 {
		return nil, false
	}
	var selected *world.NpcInfo
	selectedRatio := int32(101)
	hasCompanion := false
	for _, other := range s.world.GetNearbyNpcsInShow(npc.X, npc.Y, npc.MapID, npc.ShowID) {
		if other == nil || other.ID == npc.ID || other.Dead || other.MaxHP <= 0 || other.Family != npc.Family {
			continue
		}
		hasCompanion = true
		ratio := (other.HP * 100) / other.MaxHP
		if ratio <= int32(triggerCompanionHP) && ratio < selectedRatio {
			selected = other
			selectedRatio = ratio
		}
	}
	return selected, hasCompanion
}

func mobSkillTriggerDistanceLikeJava(triggerRange int, distance int32) bool {
	if triggerRange < 0 {
		return distance <= int32(-triggerRange)
	}
	if triggerRange > 0 {
		return distance >= int32(triggerRange)
	}
	return false
}

func npcSkillTargetInRangeLikeJava(npc *world.NpcInfo, target *world.PlayerInfo, skillRange int) bool {
	if npc == nil || target == nil {
		return false
	}
	if skillRange != -1 {
		return chebyshev32(npc.X, npc.Y, target.X, target.Y) <= int32(skillRange)
	}
	return npcTargetInScreenLikeJava(npc.X, npc.Y, target.X, target.Y)
}

func npcSkillPlayerTargetAllowedLikeJava(npc *world.NpcInfo, target *world.PlayerInfo) bool {
	if npc == nil || target == nil || target.HP <= 0 || target.Dead {
		return false
	}
	if target.MapID != npc.MapID || target.ShowID != npc.ShowID {
		return false
	}
	if isGMInvisible(target) {
		return false
	}
	return true
}

func npcTargetInScreenLikeJava(x, y, targetX, targetY int32) bool {
	dist := abs32(targetX-x) + abs32(targetY-y)
	if dist > 19 {
		return false
	}
	if dist <= 18 {
		return true
	}
	dist2 := abs32(targetX-(x-18)) + abs32(targetY-(y-18))
	return dist2 >= 19 && dist2 <= 52
}

func npcMobSkillTriggerCountReachedLikeJava(npc *world.NpcInfo, sk data.MobSkill) bool {
	if npc == nil || sk.TriggerCount <= 0 {
		return false
	}
	return npc.MobSkillUseCounts[sk.ActNo] >= sk.TriggerCount
}

func npcMobSkillReuseDelayActiveLikeJava(npc *world.NpcInfo, sk data.MobSkill) bool {
	if npc == nil || sk.ReuseDelay <= 0 || npc.MobSkillDelayTicks == nil {
		return false
	}
	return npc.MobSkillDelayTicks[sk.ActNo] > 0
}

func npcMobSkillTypeDelayActiveLikeJava(npc *world.NpcInfo, sk data.MobSkill) bool {
	if npc == nil || npc.MobSkillTypeDelayTicks == nil {
		return false
	}
	return npc.MobSkillTypeDelayTicks[sk.Type] > 0
}

func tickNpcMobSkillDelaysLikeJava(npc *world.NpcInfo) {
	if npc == nil {
		return
	}
	for actNo, ticks := range npc.MobSkillDelayTicks {
		if ticks <= 1 {
			delete(npc.MobSkillDelayTicks, actNo)
			continue
		}
		npc.MobSkillDelayTicks[actNo] = ticks - 1
	}
	for skillType, ticks := range npc.MobSkillTypeDelayTicks {
		if ticks <= 1 {
			delete(npc.MobSkillTypeDelayTicks, skillType)
			continue
		}
		npc.MobSkillTypeDelayTicks[skillType] = ticks - 1
	}
}

func recordNpcMobSkillUseLikeJava(npc *world.NpcInfo, actNo int) {
	if npc == nil {
		return
	}
	if npc.MobSkillUseCounts == nil {
		npc.MobSkillUseCounts = make(map[int]int)
	}
	npc.MobSkillUseCounts[actNo]++
}

func recordNpcMobSkillReuseDelayLikeJava(npc *world.NpcInfo, actNo, skillType, reuseDelayMillis int) {
	ticks := npcMobSkillReuseDelayTicksLikeJava(reuseDelayMillis)
	if npc == nil || ticks <= 0 {
		return
	}
	if npc.MobSkillDelayTicks == nil {
		npc.MobSkillDelayTicks = make(map[int]int)
	}
	npc.MobSkillDelayTicks[actNo] = ticks
	if npcMobSkillUsesTypeDelayLikeJava(skillType) {
		if npc.MobSkillTypeDelayTicks == nil {
			npc.MobSkillTypeDelayTicks = make(map[int]int)
		}
		npc.MobSkillTypeDelayTicks[skillType] = ticks
	}
}

func recordNpcSubMagicMobSkillSuccessLikeJava(npc *world.NpcInfo, cmd scripting.AICommand, used bool) {
	if !used {
		return
	}
	recordNpcMobSkillUseLikeJava(npc, cmd.ActNo)
	recordNpcMobSkillReuseDelayLikeJava(npc, cmd.ActNo, cmd.SkillType, cmd.ReuseDelay)
	setNpcSubMagicCooldown(npc)
}

func npcMobSkillUsesTypeDelayLikeJava(skillType int) bool {
	return skillType == 3 || skillType == 4
}

func consumeNpcSilenceForMobSkillLikeJava(npc *world.NpcInfo) bool {
	if npc == nil || !npc.HasDebuff(64) {
		return false
	}
	// Java L1SkillUse.checkUseSkill(): NPC 持有 SILENCE 時移除狀態並讓該次施法失敗。
	npc.RemoveDebuff(64)
	return true
}

func npcMobSkillReuseDelayTicksLikeJava(reuseDelayMillis int) int {
	if reuseDelayMillis <= 0 {
		return 0
	}
	const tickMillis = 200
	ticks := (reuseDelayMillis + tickMillis - 1) / tickMillis
	if ticks < 1 {
		return 1
	}
	return ticks
}

func (s *NpcAISystem) executeNpcCompanionSkillLikeJava(caster, target *world.NpcInfo, skillID, gfxID, leverage int) bool {
	if caster == nil || target == nil || target.Dead || target.MapID != caster.MapID || target.ShowID != caster.ShowID {
		return false
	}
	skill := s.deps.Skills.Get(int32(skillID))
	if skill == nil {
		return false
	}
	if consumeNpcSilenceForMobSkillLikeJava(caster) {
		return false
	}
	if skill.MpConsume > 0 {
		caster.MP -= int32(skill.MpConsume)
		if caster.MP < 0 {
			caster.MP = 0
		}
	}

	nearby := s.world.GetNearbyPlayersInShow(caster.X, caster.Y, caster.MapID, 0, caster.ShowID)
	gfx := skill.CastGfx
	if gfxID > 0 {
		gfx = int32(gfxID)
	}
	if gfx > 0 {
		effData := handler.BuildSkillEffect(target.ID, gfx)
		handler.BroadcastToPlayers(nearby, effData)
	}

	if skill.DamageValue > 0 || skill.DamageDice > 0 {
		heal := calcNpcHealingLikeJava(caster, skill, leverage)
		heal = applyNpcMagicHealingModifiersLikeJava(target, heal)
		target.HP += heal
		if target.HP > target.MaxHP {
			target.HP = target.MaxHP
		}
		hpRatio := int16(0)
		if target.MaxHP > 0 {
			hpRatio = int16((target.HP * 100) / target.MaxHP)
		}
		handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(target.ID, hpRatio))
	}
	if skill.BuffDuration > 0 {
		target.AddDebuff(int32(skillID), int(skill.BuffDuration)*5)
	}
	return true
}

func (s *NpcAISystem) executeNpcPolyLikeJava(npc *world.NpcInfo, polyID int32, actID int) bool {
	if npc == nil || npc.MapID == 93 || polyID <= 0 || s == nil || s.world == nil {
		return false
	}
	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	usePoly := false
	for _, p := range nearby {
		if p == nil || p.Dead || isGMInvisible(p) {
			continue
		}
		if s.deps != nil && s.deps.MapData != nil && !s.deps.MapData.HasLineOfSight(npc.MapID, npc.X, npc.Y, p.X, p.Y) {
			continue
		}
		if hasPolymorphControlRing(p) {
			p.SummonSelectionMode = false
			p.PendingPolySkill = true
			if p.Session != nil {
				handler.SendShowPolyList(p.Session, p.CharID)
				handler.SendServerMessage(p.Session, 966)
			}
			usePoly = true
			continue
		}
		if s.deps == nil || s.deps.Polymorph == nil || s.deps.Polys == nil {
			continue
		}
		poly := s.deps.Polys.GetByID(polyID)
		if poly == nil || !poly.IsMatchCause(data.PolyCauseNPC) {
			continue
		}
		s.deps.Polymorph.DoPoly(p, polyID, 1800, data.PolyCauseNPC)
		if p.PolyID == polyID || p.TempCharGfx == polyID {
			usePoly = true
		}
	}
	if !usePoly {
		return false
	}

	if len(nearby) > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(nearby[0].CharID, 230))
	}
	actionID := byte(19)
	if actID > 0 {
		actionID = byte(actID)
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, actionID))
	return true
}

// executeNpcSelfSkill 處理 NPC 自我施法（change_target == 2）。
// 主要用途：自我治療（恢復 HP）、自我加速（haste）、自我 buff。
// Java 參考：L1MobSkillUse.changeMobTarget() 中 changeTarget == 2 分支。
func (s *NpcAISystem) executeNpcSelfSkill(npc *world.NpcInfo, skillID, gfxID, leverage int) bool {
	if npc == nil {
		return false
	}
	skill := s.deps.Skills.Get(int32(skillID))
	if skill == nil {
		return false
	}
	if consumeNpcSilenceForMobSkillLikeJava(npc) {
		return false
	}

	// 消耗 MP
	if skill.MpConsume > 0 {
		npc.MP -= int32(skill.MpConsume)
		if npc.MP < 0 {
			npc.MP = 0
		}
	}

	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)

	// 播放特效
	gfx := skill.CastGfx
	if gfxID > 0 {
		gfx = int32(gfxID)
	}
	if gfx > 0 {
		effData := handler.BuildSkillEffect(npc.ID, gfx)
		handler.BroadcastToPlayers(nearby, effData)
	}

	// 自我治療：yiwei 以 DamageValue + NPC INT magicBonus 作為擲骰次數。
	if skill.DamageValue > 0 || skill.DamageDice > 0 {
		heal := calcNpcHealingLikeJava(npc, skill, leverage)
		heal = applyNpcMagicHealingModifiersLikeJava(npc, heal)
		npc.HP += heal
		if npc.HP > npc.MaxHP {
			npc.HP = npc.MaxHP
		}
		// 廣播 NPC HP 條
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	}

	// 自我加速：Haste 系列技能（Java: NPC 使用 speed-up 技能時設定移動倍率）
	// 透過 debuff 機制暫存加速效果（正面效果也用 debuff 計時器管理）
	if skill.BuffDuration > 0 {
		npc.AddDebuff(int32(skillID), int(skill.BuffDuration)*5) // 秒 → ticks
	}
	return true
}

// executeNpcSummon 處理 NPC 召喚小怪（type 3 技能）。
// Java 參考：L1MobSkillUseSpawn — 在施法者附近隨機位置生成指定 NPC。
// 被召喚的怪物不掉落物品（Java: _storeDroped = true），但這裡簡化為普通怪物。
func (s *NpcAISystem) executeNpcSummon(npc *world.NpcInfo, summonID int32, summonMin, summonMax, gfxID int) bool {
	if npc != nil && npc.MapID == 93 {
		return false
	}
	if summonID <= 0 {
		return false
	}

	if s.deps == nil || s.deps.Npcs == nil {
		return false
	}
	tmpl := s.deps.Npcs.Get(summonID)
	if tmpl == nil {
		return false
	}

	// 計算召喚數量
	count := summonMin
	if summonMax > summonMin {
		count = summonMin + rand.Intn(summonMax-summonMin+1)
	}
	if count <= 0 {
		count = 1
	}
	if count > 8 {
		count = 8 // 上限保護
	}

	nearby := s.world.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)

	// 播放召喚特效
	if gfxID > 0 {
		effData := handler.BuildSkillEffect(npc.ID, int32(gfxID))
		handler.BroadcastToPlayers(nearby, effData)
	}

	// 在 NPC 周圍生成小怪
	summoned := 0
	for i := 0; i < count; i++ {
		// 在 NPC 附近 3 格內隨機找可走位置
		sx, sy := npc.X, npc.Y
		for try := 0; try < 10; try++ {
			tx := npc.X + int32(rand.Intn(7)) - 3
			ty := npc.Y + int32(rand.Intn(7)) - 3
			if s.deps.MapData != nil && s.deps.MapData.IsPassablePoint(npc.MapID, tx, ty) {
				if !s.world.IsOccupied(tx, ty, npc.MapID, 0) {
					sx, sy = tx, ty
					break
				}
			}
		}

		summonNpc := newRuntimeNpcFromTemplate(tmpl, sx, sy, npc.MapID, 0, npc.ShowID, 0, s.deps.SprTable)

		s.world.AddNpc(summonNpc)
		summoned++
		if s.deps.MapData != nil {
			s.deps.MapData.SetImpassable(npc.MapID, sx, sy, true)
		}

		// 通知附近玩家顯示新 NPC
		for _, viewer := range nearby {
			sendNpcPack(viewer.Session, summonNpc)
		}
	}
	return summoned > 0
}

// ---------- NPC Movement ----------

type npcStepCandidate struct{ x, y int32 }

func npcStepCandidatesToward(npc *world.NpcInfo, tx, ty int32) []npcStepCandidate {
	if npc == nil {
		return nil
	}
	dx := tx - npc.X
	dy := ty - npc.Y
	candidates := make([]npcStepCandidate, 0, 3)

	mx, my := npc.X, npc.Y
	if dx > 0 {
		mx++
	} else if dx < 0 {
		mx--
	}
	if dy > 0 {
		my++
	} else if dy < 0 {
		my--
	}
	candidates = append(candidates, npcStepCandidate{mx, my})

	if dx != 0 && dy != 0 {
		candidates = append(candidates, npcStepCandidate{mx, npc.Y})
		candidates = append(candidates, npcStepCandidate{npc.X, my})
	} else if dx != 0 {
		candidates = append(candidates, npcStepCandidate{mx, npc.Y + 1})
		candidates = append(candidates, npcStepCandidate{mx, npc.Y - 1})
	} else if dy != 0 {
		candidates = append(candidates, npcStepCandidate{npc.X + 1, my})
		candidates = append(candidates, npcStepCandidate{npc.X - 1, my})
	}
	return candidates
}

// npcMoveToward moves NPC 1 tile toward a target position.
// If the direct path is blocked, tries two alternate side-step directions.
func npcMoveToward(ws *world.State, npc *world.NpcInfo, tx, ty int32, maps *data.MapDataTable) {
	if maps == nil {
		return
	}
	dx := tx - npc.X
	dy := ty - npc.Y

	type candidate struct{ x, y int32 }
	candidates := make([]candidate, 0, 3)

	// Primary: direct toward target
	mx, my := npc.X, npc.Y
	if dx > 0 {
		mx++
	} else if dx < 0 {
		mx--
	}
	if dy > 0 {
		my++
	} else if dy < 0 {
		my--
	}
	candidates = append(candidates, candidate{mx, my})

	// Side-steps
	if dx != 0 && dy != 0 {
		candidates = append(candidates, candidate{mx, npc.Y})
		candidates = append(candidates, candidate{npc.X, my})
	} else if dx != 0 {
		candidates = append(candidates, candidate{mx, npc.Y + 1})
		candidates = append(candidates, candidate{mx, npc.Y - 1})
	} else if dy != 0 {
		candidates = append(candidates, candidate{npc.X + 1, my})
		candidates = append(candidates, candidate{npc.X - 1, my})
	}

	for _, c := range candidates {
		if c.x == npc.X && c.y == npc.Y {
			continue
		}
		h := calcNpcHeading(npc.X, npc.Y, c.x, c.y)

		if !maps.IsPassable(npc.MapID, npc.X, npc.Y, int(h)) {
			continue
		}
		occupant := ws.OccupantAt(c.x, c.y, npc.MapID)
		if occupant > 0 && occupant < 200_000_000 {
			continue
		}

		npcExecuteMove(ws, npc, c.x, c.y, h, maps)
		return
	}
	// All candidates blocked — last resort: pass through
	h := calcNpcHeading(npc.X, npc.Y, mx, my)
	if maps.IsPassableIgnoreOccupant(npc.MapID, npc.X, npc.Y, int(h)) {
		npcExecuteMove(ws, npc, mx, my, h, maps)
	}
}

// npcExecuteMove performs the actual NPC position update and broadcasts.
func npcExecuteMove(ws *world.State, npc *world.NpcInfo, moveX, moveY int32, heading int16, maps *data.MapDataTable) {
	oldX, oldY := npc.X, npc.Y

	if maps == nil || !maps.IsPassableIgnoreOccupant(npc.MapID, oldX, oldY, int(heading)) {
		return
	}
	maps.SetImpassable(npc.MapID, oldX, oldY, false)
	maps.SetImpassable(npc.MapID, moveX, moveY, true)

	ws.UpdateNpcPosition(npc.ID, moveX, moveY, heading)

	nearby := ws.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	data := buildNpcMove(npc.ID, oldX, oldY, npc.Heading)
	handler.BroadcastToPlayers(nearby, data)
}

// npcFleeFrom 讓 NPC 遠離目標移動 1 格。
// Java 參考：L1NpcInstance.setHomeDir() — 計算反向方向。
func npcFleeFrom(ws *world.State, npc *world.NpcInfo, tx, ty int32, maps *data.MapDataTable) {
	// 計算反向（從目標 → 自己的方向 = 遠離方向）
	dx := npc.X - tx
	dy := npc.Y - ty

	type candidate struct{ x, y int32 }
	candidates := make([]candidate, 0, 3)

	// 主要方向：直接遠離目標
	mx, my := npc.X, npc.Y
	if dx > 0 {
		mx++
	} else if dx < 0 {
		mx--
	}
	if dy > 0 {
		my++
	} else if dy < 0 {
		my--
	}
	candidates = append(candidates, candidate{mx, my})

	// 側向備選
	if dx != 0 && dy != 0 {
		candidates = append(candidates, candidate{mx, npc.Y})
		candidates = append(candidates, candidate{npc.X, my})
	} else if dx != 0 {
		candidates = append(candidates, candidate{mx, npc.Y + 1})
		candidates = append(candidates, candidate{mx, npc.Y - 1})
	} else if dy != 0 {
		candidates = append(candidates, candidate{npc.X + 1, my})
		candidates = append(candidates, candidate{npc.X - 1, my})
	}

	for _, c := range candidates {
		if c.x == npc.X && c.y == npc.Y {
			continue
		}
		h := calcNpcHeading(npc.X, npc.Y, c.x, c.y)
		if maps != nil && !maps.IsPassable(npc.MapID, npc.X, npc.Y, int(h)) {
			continue
		}
		occupant := ws.OccupantAt(c.x, c.y, npc.MapID)
		if occupant > 0 && occupant < 200_000_000 {
			continue
		}
		npcExecuteMove(ws, npc, c.x, c.y, h, maps)
		return
	}
}

// npcWander handles idle wandering. dir: 0-7=new direction, -1=continue, -2=toward spawn.
func npcWander(ws *world.State, npc *world.NpcInfo, dir int, maps *data.MapDataTable) {
	wanderTicks := calcNpcMoveTicks(npc)

	if dir >= 8 {
		npc.WanderDist = 0
		npc.MoveTimer = wanderTicks
		return
	}

	if dir == -1 {
		// Continue current direction
	} else if dir == -2 {
		npc.WanderDir = calcNpcHeading(npc.X, npc.Y, npc.SpawnX, npc.SpawnY)
		npc.WanderDist = rand.Intn(5) + 2
	} else {
		npc.WanderDir = int16(dir)
		npc.WanderDist = rand.Intn(5) + 2
	}

	if npc.WanderDist <= 0 {
		return
	}

	if maps != nil && !maps.IsPassable(npc.MapID, npc.X, npc.Y, int(npc.WanderDir)) {
		npc.WanderDist = 0
		return
	}

	moveX := npc.X + npcHeadingDX[npc.WanderDir]
	moveY := npc.Y + npcHeadingDY[npc.WanderDir]
	npc.WanderDist--
	npc.MoveTimer = wanderTicks

	oldX, oldY := npc.X, npc.Y

	if maps != nil {
		maps.SetImpassable(npc.MapID, oldX, oldY, false)
		maps.SetImpassable(npc.MapID, moveX, moveY, true)
	}

	ws.UpdateNpcPosition(npc.ID, moveX, moveY, npc.WanderDir)

	nearby := ws.GetNearbyPlayersInShow(npc.X, npc.Y, npc.MapID, 0, npc.ShowID)
	data := buildNpcMove(npc.ID, oldX, oldY, npc.Heading)
	handler.BroadcastToPlayers(nearby, data)
}

// ---------- Shared utilities ----------

func setNpcAtkCooldown(npc *world.NpcInfo) {
	atkCooldown := 10
	if npc.AtkSpeed > 0 {
		atkCooldown = int(npc.AtkSpeed) / 200
		if atkCooldown < 3 {
			atkCooldown = 3
		}
	}
	// Java L1NpcInstance.java:2629-2633：WIND_SHACKLE(167) 對 NPC ATTACK_SPEED/MAGIC_SPEED +25% sleepTime。
	if npc.HasDebuff(167) {
		atkCooldown += atkCooldown / 4
	}
	setNpcActionCooldown(npc, atkCooldown)
}

func setNpcAtkMagicCooldown(npc *world.NpcInfo) {
	if npc.AtkMagicSpeed <= 0 {
		setNpcAtkCooldown(npc)
		return
	}
	cooldown := int(npc.AtkMagicSpeed) / 200
	if cooldown < 3 {
		cooldown = 3
	}
	if npc.HasDebuff(167) {
		cooldown += cooldown / 4
	}
	setNpcActionCooldown(npc, cooldown)
}

func setNpcSubMagicCooldown(npc *world.NpcInfo) {
	if npc.SubMagicSpeed <= 0 {
		setNpcAtkCooldown(npc)
		return
	}
	cooldown := int(npc.SubMagicSpeed) / 200
	if cooldown < 3 {
		cooldown = 3
	}
	if npc.HasDebuff(167) {
		cooldown += cooldown / 4
	}
	setNpcActionCooldown(npc, cooldown)
}

func setNpcActionCooldown(npc *world.NpcInfo, cooldown int) {
	npc.AttackTimer = cooldown
	if npc.MoveTimer < cooldown {
		npc.MoveTimer = cooldown
	}
}

func (s *NpcAISystem) setNpcMobSkillTypeTwoCooldownLikeJava(npc *world.NpcInfo, skillID int) {
	if s != nil && s.deps != nil && s.deps.Skills != nil {
		if skill := s.deps.Skills.Get(int32(skillID)); skill != nil &&
			skill.Target == "attack" && skillID != mobSkillTurnUndeadID {
			setNpcAtkMagicCooldown(npc)
			return
		}
	}
	setNpcSubMagicCooldown(npc)
}

func chebyshev32(x1, y1, x2, y2 int32) int32 {
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

var npcHeadingDX = [8]int32{0, 1, 1, 1, 0, -1, -1, -1}
var npcHeadingDY = [8]int32{-1, -1, 0, 1, 1, 1, 0, -1}

func calcNpcHeading(sx, sy, tx, ty int32) int16 {
	ddx := tx - sx
	ddy := ty - sy
	if ddx > 0 {
		ddx = 1
	} else if ddx < 0 {
		ddx = -1
	}
	if ddy > 0 {
		ddy = 1
	} else if ddy < 0 {
		ddy = -1
	}
	for i := int16(0); i < 8; i++ {
		if npcHeadingDX[i] == ddx && npcHeadingDY[i] == ddy {
			return i
		}
	}
	return 0
}

// ---------- Packet helpers ----------
// These are local to the system package to avoid circular imports.

// npcArrowSeqNum is a sequential counter for NPC ranged attack packets.
var npcArrowSeqNum int32

// buildNpcMove 建構 NPC 移動封包位元組（不發送）。
// Java S_MoveNpcPacket: [C op][D id][H locX][H locY][C heading][C 0x80]
// 與玩家版不同：NPC 版尾部有 0x80 旗標。
func buildNpcMove(npcID int32, prevX, prevY int32, heading int16) []byte {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MOVE_OBJECT)
	w.WriteD(npcID)
	w.WriteH(uint16(prevX))
	w.WriteH(uint16(prevY))
	w.WriteC(byte(heading))
	w.WriteC(0x80) // NPC 移動旗標（Java S_MoveNpcPacket 第 30 行）
	return w.Bytes()
}

// buildNpcAttack 建構 NPC 近戰攻擊封包位元組（不發送）。
func buildNpcAttack(attackerID, targetID, damage int32, heading int16) []byte {
	return handler.BuildAttackPacket(attackerID, targetID, damage, heading)
}

func buildNpcAttackWithAction(attackerID, targetID, damage int32, heading int16, actID int) []byte {
	actionID := byte(1)
	if actID > 1 && actID <= 255 {
		actionID = byte(actID)
	}
	return handler.BuildAttackPacketWithAction(attackerID, targetID, damage, heading, actionID)
}

// buildNpcRangedAttack 建構 NPC 遠程攻擊封包位元組（不發送）。
func buildNpcRangedAttack(attackerID, targetID, damage int32, heading int16, ax, ay, tx, ty int32) []byte {
	npcArrowSeqNum++
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(1)
	w.WriteD(attackerID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(npcArrowSeqNum)
	w.WriteH(66)
	w.WriteC(0)
	w.WriteH(uint16(ax))
	w.WriteH(uint16(ay))
	w.WriteH(uint16(tx))
	w.WriteH(uint16(ty))
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	return w.Bytes()
}

func sendNpcPack(sess *gonet.Session, npc *world.NpcInfo) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_PUT_OBJECT)
	w.WriteH(uint16(npc.X))
	w.WriteH(uint16(npc.Y))
	w.WriteD(npc.ID)
	w.WriteH(uint16(npc.GfxID))
	w.WriteC(world.NpcActionStatus(npc))
	w.WriteC(byte(npc.Heading))
	w.WriteC(0)
	w.WriteC(0)
	w.WriteD(npc.Exp)
	w.WriteH(0)
	w.WriteS(npc.NameID)
	w.WriteS("")
	w.WriteC(0x00)
	w.WriteD(0)
	w.WriteS("")
	w.WriteS("")
	w.WriteC(0x00)
	w.WriteC(0xFF)
	w.WriteC(0x00)
	w.WriteC(byte(npc.Level))
	w.WriteC(0xFF)
	w.WriteC(0xFF)
	w.WriteC(0x00)
	sess.Send(w.Bytes())
}

// buildNpcUseAttackSkill 建構 NPC 技能攻擊封包位元組（不發送）。
func buildNpcUseAttackSkill(casterID, targetID int32, damage int16, heading int16, gfxID int32, useType byte, cx, cy, tx, ty int32) []byte {
	npcArrowSeqNum++
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(18)
	w.WriteD(casterID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(npcArrowSeqNum)
	w.WriteH(uint16(gfxID))
	w.WriteC(useType)
	w.WriteH(uint16(cx))
	w.WriteH(uint16(cy))
	w.WriteH(uint16(tx))
	w.WriteH(uint16(ty))
	w.WriteC(0)
	w.WriteC(0)
	w.WriteC(0)
	return w.Bytes()
}

func sendHPUpdate(sess *gonet.Session, hp, maxHP int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteD(hp)
	w.WriteD(maxHP)
	sess.Send(w.Bytes())
}

// ---------- NPC Debuff 計時 ----------

// tickNpcDebuffs 遞減 NPC 的所有 debuff 計時器。到期時清除狀態並廣播解除封包。
func tickNpcDebuffs(npc *world.NpcInfo, ws *world.State, deps *handler.Deps) {
	if len(npc.ActiveDebuffs) == 0 {
		return
	}
	refreshGrey := false // 凍結類 debuff 是否需要定期重發灰色色調
	for skillID, ticksLeft := range npc.ActiveDebuffs {
		ticksLeft--
		if ticksLeft <= 0 {
			delete(npc.ActiveDebuffs, skillID)
			removeNpcDebuffEffect(npc, skillID, ws)
		} else {
			npc.ActiveDebuffs[skillID] = ticksLeft
			// 3.80C 客戶端的 S_Poison 灰色色調會自動淡出，需定期重發維持視覺
			if !refreshGrey && isFreezeDebuff(skillID) && ticksLeft%25 == 0 {
				refreshGrey = true
			}
		}
	}
	if refreshGrey && npc.Paralyzed {
		nearby := nearbyNpcViewersFromState(ws, npc, npc.X, npc.Y, npc.MapID)
		handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
	}
}

// isFreezeDebuff 判斷是否為凍結類 debuff（需要維持灰色色調的技能）。
func isFreezeDebuff(skillID int32) bool {
	switch skillID {
	case 22, 30, 50, 80, 157:
		return true
	}
	return false
}

// removeNpcDebuffEffect 清除 NPC 的 debuff 狀態旗標，並廣播視覺解除封包。
func removeNpcDebuffEffect(npc *world.NpcInfo, skillID int32, ws *world.State) {
	nearby := nearbyNpcViewersFromState(ws, npc, npc.X, npc.Y, npc.MapID)
	clearPoison := handler.BuildPoison(npc.ID, 0) // 預建清除色調封包

	switch skillID {
	case 87: // 衝擊之暈 — 解除暈眩
		npc.Paralyzed = false
	case 157: // 大地屏障 — 解除凍結 + 灰色色調
		npc.Paralyzed = false
		handler.BroadcastToPlayers(nearby, clearPoison)
	case 33: // 木乃伊詛咒 階段一到期 → 進入階段二（真正麻痺 4 秒）
		npc.Paralyzed = true
		npc.AddDebuff(4001, 20) // 4 秒 = 20 ticks
	case 4001: // 木乃伊詛咒 階段二到期 — 解除麻痺
		npc.Paralyzed = false
		handler.BroadcastToPlayers(nearby, clearPoison)
	case 62, 66: // 沉睡之霧 — 解除睡眠
		npc.Sleeped = false
	case 103: // 暗黑盲咒 — 解除睡眠（Java 用 skill 66 的效果）
		npc.Sleeped = false
	case 20, 40: // 闇盲咒術 — 致盲（NPC 無視覺，僅計時）
		// NPC 致盲不影響行動旗標
	case handler.SkillShapeChange: // 變形術 — 還原 NPC 原始圖像
		removeShapeChangeFromNpc(npc)
		for _, viewer := range nearby {
			handler.SendChangeShape(viewer.Session, npc.ID, npc.GfxID, 0)
		}
	case 29, 76, 152: // 緩速系列 — NPC debuff 到期
		// 速度恢復由 calcNpcMoveTicks 自動處理（不再有 slow debuff → 不翻倍）
	case cubeStatusQuakeEnemy: // 立方：地裂 — 解除短暫束縛
		npc.Paralyzed = false
	case 11: // 毒咒 — 清除傷害毒
		npc.PoisonDmgAmt = 0
		npc.PoisonDmgTimer = 0
		poisonNearby := nearbyNpcPoisonX8Viewers(ws, npc)
		if npc.Paralyzed {
			// NPC 仍在凍結中 → 清除綠色後重發灰色色調
			handler.BroadcastToPlayers(poisonNearby, handler.BuildPoison(npc.ID, 2))
		} else {
			handler.BroadcastToPlayers(poisonNearby, clearPoison)
		}
	case 80: // 冰雪颶風 — 解除凍結
		npc.Paralyzed = false
		handler.BroadcastToPlayers(nearby, clearPoison)
	case 22: // 寒冰氣息 — 解除凍結 + 灰色色調
		npc.Paralyzed = false
		handler.BroadcastToPlayers(nearby, clearPoison)
	case 30: // 岩牢 — 解除凍結 + 灰色色調
		npc.Paralyzed = false
		handler.BroadcastToPlayers(nearby, clearPoison)
	case 50: // 冰矛 — 解除凍結 + 灰色色調
		npc.Paralyzed = false
		handler.BroadcastToPlayers(nearby, clearPoison)
	case 47: // 弱化術 — 僅計時自動清除
	case skillElementalFallDown: // 弱化屬性 — 還原被降低的單一屬性抗性
		removeElementalFallDownFromNpc(npc)
	case 56: // 疾病術 — 僅計時自動清除
	}
}

// calcNpcMoveTicks 計算 NPC 移動間隔 tick 數。
// 緩速 debuff（29/76/152）時移動間隔翻倍。
func calcNpcMoveTicks(npc *world.NpcInfo) int {
	moveTicks := 4
	if npc.MoveSpeed > 0 {
		moveTicks = int(npc.MoveSpeed) / 200
		if moveTicks < 2 {
			moveTicks = 2
		}
	}
	// 緩速術效果：移動間隔翻倍（Java: moveSpeed 設為 2 = slow）
	if npc.HasDebuff(29) || npc.HasDebuff(76) || npc.HasDebuff(152) {
		moveTicks *= 2
	}
	return moveTicks
}

// tickNpcPoison 處理 NPC 的法術中毒效果（Java L1DamagePoison 對 NPC）。
// 每 15 tick（3 秒）造成 PoisonDmgAmt 傷害。毒傷害不會殺死 NPC（HP 最低 1）。
func tickNpcPoison(npc *world.NpcInfo, ws *world.State, deps *handler.Deps) {
	if npc.PoisonDmgAmt <= 0 || npc.Dead {
		return
	}

	// 計時（與 debuff 11 綁定）
	if !npc.HasDebuff(11) {
		// debuff 到期 → 清除中毒
		npc.PoisonDmgAmt = 0
		npc.PoisonDmgTimer = 0
		npc.PoisonAttackerSID = 0
		nearby := nearbyNpcPoisonX8Viewers(ws, npc)
		if npc.Paralyzed {
			// NPC 仍在凍結中 → 維持灰色色調
			handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		} else {
			handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 0))
		}
		return
	}

	// 仇恨歸屬：毒傷害累加仇恨（Java: NPC 會追擊施毒者）
	if npc.PoisonAttackerSID != 0 {
		AddHate(npc, npc.PoisonAttackerSID, npc.PoisonDmgAmt)
	}

	npc.PoisonDmgTimer++
	if npc.PoisonDmgTimer >= 15 {
		npc.PoisonDmgTimer = 0
		if npcDamageBlockedBySkm0LikeJava(npc) {
			return
		}
		npc.HP -= npc.PoisonDmgAmt
		// 毒傷害不可殺死 NPC — HP 最低 1
		if npc.HP <= 1 {
			npc.HP = 1
		}
		// 廣播 HP 條給所有附近玩家
		nearby := nearbyNpcViewersFromState(ws, npc, npc.X, npc.Y, npc.MapID)
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	}
}
