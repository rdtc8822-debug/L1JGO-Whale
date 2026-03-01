package system

import (
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
		if npc.Impl != "L1Monster" {
			continue
		}
		s.tickMonsterAI(npc)
	}
}

// ---------- Monster AI (Lua-driven) ----------

func (s *NpcAISystem) tickMonsterAI(npc *world.NpcInfo) {
	// NPC 法術中毒 tick（每 3 秒扣血）
	tickNpcPoison(npc, s.world, s.deps)

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
		if target == nil || target.Dead || target.MapID != npc.MapID {
			// 當前目標失效 → 從仇恨列表移除，嘗試回退到次高仇恨
			RemoveHateTarget(npc, npc.AggroTarget)
			npc.AggroTarget = 0
			target = nil
			// 嘗試仇恨列表中的下一個目標
			if nextSID := GetMaxHateTarget(npc); nextSID != 0 {
				if nextTarget := s.world.GetBySession(nextSID); nextTarget != nil &&
					!nextTarget.Dead && nextTarget.MapID == npc.MapID {
					npc.AggroTarget = nextSID
					target = nextTarget
				} else {
					RemoveHateTarget(npc, nextSID)
				}
			}
		}
		// 注意：不在此處檢查安全區域。被動仇恨（被攻擊）不受安全區域限制。
		// 安全區域只阻止主動索敵（agro scan），由下方處理。
		// Java 行為：隱藏之谷等新手區整張地圖都是安全區域，怪物被打一定會反擊。
	}

	// Agro mobs scan for new target if none
	var nearbyPlayers []*world.PlayerInfo
	if target == nil && npc.Agro {
		nearbyPlayers = s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		bestDist := int32(999)
		for _, p := range nearbyPlayers {
			if p.Dead {
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
			npc.MoveTimer = 0  // snap out of wander — react immediately
			npc.WanderDist = 0
		}
	}

	// 附近無玩家 → 跳過 Lua（複用 agro 掃描結果，避免重複 AOI 查詢）
	if target == nil {
		if nearbyPlayers == nil {
			nearbyPlayers = s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		}
		if len(nearbyPlayers) == 0 {
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

	// Convert mob skills to Lua entries
	var mobSkills []scripting.MobSkillEntry
	if skills := s.deps.MobSkills.Get(npc.NpcID); skills != nil {
		mobSkills = make([]scripting.MobSkillEntry, len(skills))
		for i, sk := range skills {
			mobSkills[i] = scripting.MobSkillEntry{
				SkillID:       sk.SkillID,
				MpConsume:     sk.MpConsume,
				TriggerRandom: sk.TriggerRandom,
				TriggerHP:     sk.TriggerHP,
				TriggerRange:  sk.TriggerRange,
				ActID:         sk.ActID,
				GfxID:         sk.GfxID,
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
			if target != nil {
				s.executeNpcSkill(npc, target, cmd.SkillID, cmd.ActID, cmd.GfxID)
				setNpcAtkCooldown(npc)
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
		nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		bestDist := int32(999)
		for _, p := range nearby {
			if p.Dead || p.Invisible {
				continue
			}
			if p.WantedTicks <= 0 && !p.PinkName {
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
					s.npcRangedAttack(npc, target)
				} else {
					s.npcMeleeAttack(npc, target)
				}
				setNpcAtkCooldown(npc)
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
	oldNearby := s.world.GetNearbyPlayersAt(oldX, oldY, npc.MapID)
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
	newNearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range newNearby {
		sendNpcPack(viewer.Session, npc)
	}
}

// ---------- NPC Combat ----------

func (s *NpcAISystem) npcMeleeAttack(npc *world.NpcInfo, target *world.PlayerInfo) {
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

	nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// 反擊屏障（skill 91）：近戰攻擊時機率觸發反彈
	// Java 參考: L1AttackNpc.calcDamage() — 檢查 target.hasSkillEffect(COUNTER_BARRIER)
	if damage > 0 && target.HasBuff(91) {
		// 機率判定：probability = probabilityValue(25) ，與 random(1~100) 比較
		prob := 25 // 基礎觸發率
		if world.RandInt(100)+1 <= prob {
			// 計算反彈傷害（Java: calcCounterBarrierDamage — NPC 版本：(STR + Level) << 1）
			cbDmg := int32((int(npc.STR) + int(npc.Level)) << 1)
			// 套用設定倍率（Java: ConfigSkill.COUNTER_BARRIER_DMG = 1.5）
			cbDmg = cbDmg * 3 / 2
			if cbDmg > 0 {
				// 反彈傷害施加在 NPC 身上
				npc.HP -= cbDmg
				if npc.HP < 0 {
					npc.HP = 0
				}
				// 播放反擊屏障觸發特效（GFX 10710）
				handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(target.CharID, 10710))
				// 原始攻擊傷害歸零
				damage = 0
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

	atkData := buildNpcAttack(npc.ID, target.CharID, damage, npc.Heading)
	handler.BroadcastToPlayers(nearby, atkData)

	if damage <= 0 {
		return
	}

	target.HP -= int16(damage)
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
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

	res := s.deps.Scripting.CalcNpcRanged(scripting.CombatContext{
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

	nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	rngData := buildNpcRangedAttack(npc.ID, target.CharID, damage, npc.Heading,
		npc.X, npc.Y, target.X, target.Y)
	handler.BroadcastToPlayers(nearby, rngData)

	if damage <= 0 {
		return
	}

	target.HP -= int16(damage)
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
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
func (s *NpcAISystem) executeNpcSkill(npc *world.NpcInfo, target *world.PlayerInfo, skillID, actID, gfxID int) {
	// 目標絕對屏障：免疫所有傷害和 debuff
	if target.AbsoluteBarrier {
		npc.AggroTarget = 0
		return
	}

	skill := s.deps.Skills.Get(int32(skillID))
	if skill == nil {
		s.npcMeleeAttack(npc, target)
		return
	}

	// Consume MP
	if skill.MpConsume > 0 {
		npc.MP -= int32(skill.MpConsume)
		if npc.MP < 0 {
			npc.MP = 0
		}
	}

	npc.Heading = calcNpcHeading(npc.X, npc.Y, target.X, target.Y)
	nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	// Spell visual effect: mob-specific gfx_id takes priority, fallback to skill's CastGfx
	gfx := skill.CastGfx
	if gfxID > 0 {
		gfx = int32(gfxID)
	}

	// Determine if this is a magic projectile (has dice/damage) or physical/buff skill
	isMagicProjectile := skill.DamageValue > 0 || skill.DamageDice > 0

	if isMagicProjectile {
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

		useType := byte(6) // ranged magic
		if skill.Area > 0 {
			useType = 8 // AoE magic
		}
		skillAtkData := buildNpcUseAttackSkill(npc.ID, target.CharID,
			int16(damage), npc.Heading, gfx, useType,
			npc.X, npc.Y, target.X, target.Y)
		handler.BroadcastToPlayers(nearby, skillAtkData)

		target.HP -= int16(damage)
		target.Dirty = true
		if target.HP <= 0 {
			target.HP = 0
			s.deps.Death.KillPlayer(target)
			npc.AggroTarget = 0
			return
		}
		sendHPUpdate(target.Session, target.HP, target.MaxHP)
	} else {
		// 非傷害技能（debuff）：發送特效 + 套用 debuff 狀態
		if gfx > 0 {
			effData := handler.BuildSkillEffect(target.CharID, gfx)
			handler.BroadcastToPlayers(nearby, effData)
		}
		// 透過 SkillManager 套用 buff/debuff 效果（麻痺、睡眠、減速等）
		if s.deps.Skill != nil {
			s.deps.Skill.ApplyNpcDebuff(target, skill)
		}
	}
}

// ---------- NPC Movement ----------

// npcMoveToward moves NPC 1 tile toward a target position.
// If the direct path is blocked, tries two alternate side-step directions.
func npcMoveToward(ws *world.State, npc *world.NpcInfo, tx, ty int32, maps *data.MapDataTable) {
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
	// All candidates blocked — last resort: pass through
	h := calcNpcHeading(npc.X, npc.Y, mx, my)
	if maps == nil || maps.IsPassableIgnoreOccupant(npc.MapID, npc.X, npc.Y, int(h)) {
		npcExecuteMove(ws, npc, mx, my, h, maps)
	}
}

// npcExecuteMove performs the actual NPC position update and broadcasts.
func npcExecuteMove(ws *world.State, npc *world.NpcInfo, moveX, moveY int32, heading int16, maps *data.MapDataTable) {
	oldX, oldY := npc.X, npc.Y

	if maps != nil {
		maps.SetImpassable(npc.MapID, oldX, oldY, false)
		maps.SetImpassable(npc.MapID, moveX, moveY, true)
	}

	ws.UpdateNpcPosition(npc.ID, moveX, moveY, heading)

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	data := buildNpcMove(npc.ID, oldX, oldY, npc.Heading)
	handler.BroadcastToPlayers(nearby, data)
}

// npcWander handles idle wandering. dir: 0-7=new direction, -1=continue, -2=toward spawn.
func npcWander(ws *world.State, npc *world.NpcInfo, dir int, maps *data.MapDataTable) {
	wanderTicks := calcNpcMoveTicks(npc)

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

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
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
	npc.AttackTimer = atkCooldown
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
	w.WriteC(0)
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

func sendHPUpdate(sess *gonet.Session, hp, maxHP int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteH(uint16(hp))
	w.WriteH(uint16(maxHP))
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
		nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
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
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
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
	case 29, 76, 152: // 緩速系列 — NPC debuff 到期
		// 速度恢復由 calcNpcMoveTicks 自動處理（不再有 slow debuff → 不翻倍）
	case 11: // 毒咒 — 清除傷害毒
		npc.PoisonDmgAmt = 0
		npc.PoisonDmgTimer = 0
		if npc.Paralyzed {
			// NPC 仍在凍結中 → 清除綠色後重發灰色色調
			handler.BroadcastToPlayers(nearby, handler.BuildPoison(npc.ID, 2))
		} else {
			handler.BroadcastToPlayers(nearby, clearPoison)
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
		nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
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
		npc.HP -= npc.PoisonDmgAmt
		// 毒傷害不可殺死 NPC — HP 最低 1
		if npc.HP <= 1 {
			npc.HP = 1
		}
		// 廣播 HP 條給所有附近玩家
		nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		handler.BroadcastToPlayers(nearby, handler.BuildHpMeter(npc.ID, hpRatio))
	}
}
