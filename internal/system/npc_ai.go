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

	// --- Target detection (Go engine responsibility) ---
	var target *world.PlayerInfo
	if npc.AggroTarget != 0 {
		target = s.world.GetBySession(npc.AggroTarget)
		if target == nil || target.Dead || target.MapID != npc.MapID {
			npc.AggroTarget = 0
			target = nil
		}
		// Drop aggro if target entered a safety zone (Java: getZoneType() == 1)
		if target != nil && s.deps.MapData != nil &&
			s.deps.MapData.IsSafetyZone(target.MapID, target.X, target.Y) {
			npc.AggroTarget = 0
			target = nil
		}
	}

	// Agro mobs scan for new target if none
	if target == nil && npc.Agro {
		nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		bestDist := int32(999)
		for _, p := range nearby {
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

	// Skip Lua call if no players nearby (optimization)
	if target == nil {
		nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
		if len(nearby) == 0 {
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
				npc.MoveTimer = 3
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
				moveTicks := 4
				if npc.MoveSpeed > 0 {
					moveTicks = int(npc.MoveSpeed) / 200
					if moveTicks < 2 {
						moveTicks = 2
					}
				}
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
			moveTicks := 4
			if npc.MoveSpeed > 0 {
				moveTicks = int(npc.MoveSpeed) / 200
				if moveTicks < 2 {
					moveTicks = 2
				}
			}
			npc.MoveTimer = moveTicks
		}
	}
}

// guardTeleportHome instantly moves a guard back to its spawn point.
func (s *NpcAISystem) guardTeleportHome(npc *world.NpcInfo) {
	oldX, oldY := npc.X, npc.Y

	// 通知舊位置附近玩家：移除 NPC + 解鎖格子
	oldNearby := s.world.GetNearbyPlayersAt(oldX, oldY, npc.MapID)
	for _, viewer := range oldNearby {
		sendRemoveObject(viewer.Session, npc.ID)
	}

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
	for _, viewer := range nearby {
		sendNpcAttack(viewer.Session, npc.ID, target.CharID, damage, npc.Heading)
	}

	if damage <= 0 {
		return
	}

	target.HP -= int16(damage)
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		handler.KillPlayer(target, s.deps)
		npc.AggroTarget = 0
		return
	}
	sendHPUpdate(target.Session, target.HP, target.MaxHP)

	// 怪物施毒判定（Java L1AttackNpc.addNpcPoisonAttack）
	if npc.PoisonAtk > 0 {
		handler.ApplyNpcPoisonAttack(npc, target, s.world, s.deps)
	}
}

func (s *NpcAISystem) npcRangedAttack(npc *world.NpcInfo, target *world.PlayerInfo) {
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
	for _, viewer := range nearby {
		sendNpcRangedAttack(viewer.Session, npc.ID, target.CharID, damage, npc.Heading,
			npc.X, npc.Y, target.X, target.Y)
	}

	if damage <= 0 {
		return
	}

	target.HP -= int16(damage)
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		handler.KillPlayer(target, s.deps)
		npc.AggroTarget = 0
		return
	}
	sendHPUpdate(target.Session, target.HP, target.MaxHP)

	// 怪物施毒判定（Java L1AttackNpc.addNpcPoisonAttack）
	if npc.PoisonAtk > 0 {
		handler.ApplyNpcPoisonAttack(npc, target, s.world, s.deps)
	}
}

// executeNpcSkill handles an NPC using a skill on a player.
func (s *NpcAISystem) executeNpcSkill(npc *world.NpcInfo, target *world.PlayerInfo, skillID, actID, gfxID int) {
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
		for _, viewer := range nearby {
			sendNpcUseAttackSkill(viewer.Session, npc.ID, target.CharID,
				int16(damage), npc.Heading, gfx, useType,
				npc.X, npc.Y, target.X, target.Y)
		}

		target.HP -= int16(damage)
		target.Dirty = true
		if target.HP <= 0 {
			target.HP = 0
			handler.KillPlayer(target, s.deps)
			npc.AggroTarget = 0
			return
		}
		sendHPUpdate(target.Session, target.HP, target.MaxHP)
	} else {
		// Non-damage skill (buff/debuff): use S_EFFECT on target
		if gfx > 0 {
			for _, viewer := range nearby {
				sendSkillEffect(viewer.Session, target.CharID, gfx)
			}
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
	for _, viewer := range nearby {
		sendNpcMove(viewer.Session, npc.ID, oldX, oldY, npc.Heading)
	}
}

// npcWander handles idle wandering. dir: 0-7=new direction, -1=continue, -2=toward spawn.
func npcWander(ws *world.State, npc *world.NpcInfo, dir int, maps *data.MapDataTable) {
	wanderTicks := 4
	if npc.MoveSpeed > 0 {
		wanderTicks = int(npc.MoveSpeed) / 200
		if wanderTicks < 2 {
			wanderTicks = 2
		}
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

	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcMove(viewer.Session, npc.ID, oldX, oldY, npc.Heading)
	}
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

// sendNpcMove sends S_MOVE_OBJECT (opcode 10) to animate NPC movement.
// Java S_MoveCharPacket constructor 2 (AI): [C op][D id][H locX][H locY][C heading]
// No trailing bytes — differs from PC constructor which has writeH(0).
func sendNpcMove(sess *gonet.Session, npcID int32, prevX, prevY int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MOVE_OBJECT)
	w.WriteD(npcID)
	w.WriteH(uint16(prevX))
	w.WriteH(uint16(prevY))
	w.WriteC(byte(heading))
	sess.Send(w.Bytes())
}

func sendNpcAttack(sess *gonet.Session, attackerID, targetID, damage int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_ATTACK)
	w.WriteC(1)
	w.WriteD(attackerID)
	w.WriteD(targetID)
	w.WriteH(uint16(damage))
	w.WriteC(byte(heading))
	w.WriteD(0)
	w.WriteC(0)
	sess.Send(w.Bytes())
}

func sendNpcRangedAttack(sess *gonet.Session, attackerID, targetID, damage int32, heading int16, ax, ay, tx, ty int32) {
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
	sess.Send(w.Bytes())
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

func sendNpcUseAttackSkill(sess *gonet.Session, casterID, targetID int32, damage int16, heading int16, gfxID int32, useType byte, cx, cy, tx, ty int32) {
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
	sess.Send(w.Bytes())
}

func sendRemoveObject(sess *gonet.Session, objectID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_OBJECT)
	w.WriteD(objectID)
	sess.Send(w.Bytes())
}

func sendHPUpdate(sess *gonet.Session, hp, maxHP int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HIT_POINT)
	w.WriteH(uint16(hp))
	w.WriteH(uint16(maxHP))
	sess.Send(w.Bytes())
}

func sendSkillEffect(sess *gonet.Session, objectID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(objectID)
	w.WriteH(uint16(gfxID))
	sess.Send(w.Bytes())
}

// ---------- NPC Debuff 計時 ----------

// tickNpcDebuffs 遞減 NPC 的所有 debuff 計時器。到期時清除狀態並廣播解除封包。
func tickNpcDebuffs(npc *world.NpcInfo, ws *world.State, deps *handler.Deps) {
	if len(npc.ActiveDebuffs) == 0 {
		return
	}
	for skillID, ticksLeft := range npc.ActiveDebuffs {
		ticksLeft--
		if ticksLeft <= 0 {
			delete(npc.ActiveDebuffs, skillID)
			removeNpcDebuffEffect(npc, skillID, ws)
		} else {
			npc.ActiveDebuffs[skillID] = ticksLeft
		}
	}
}

// removeNpcDebuffEffect 清除 NPC 的 debuff 狀態旗標，並廣播視覺解除封包。
func removeNpcDebuffEffect(npc *world.NpcInfo, skillID int32, ws *world.State) {
	nearby := ws.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)

	switch skillID {
	case 87: // 衝擊之暈 — 解除暈眩
		npc.Paralyzed = false
	case 157: // 大地屏障 — 解除凍結 + 灰色色調
		npc.Paralyzed = false
		for _, viewer := range nearby {
			sendNpcPoison(viewer.Session, npc.ID, 0) // 清除灰色色調
		}
	case 33: // 木乃伊詛咒 階段一到期 → 進入階段二（真正麻痺 4 秒）
		npc.Paralyzed = true
		npc.AddDebuff(4001, 20) // 4 秒 = 20 ticks
	case 4001: // 木乃伊詛咒 階段二到期 — 解除麻痺
		npc.Paralyzed = false
		for _, viewer := range nearby {
			sendNpcPoison(viewer.Session, npc.ID, 0) // 清除灰色色調
		}
	case 62, 66: // 沉睡之霧 — 解除睡眠
		npc.Sleeped = false
	case 103: // 暗黑盲咒 — 解除睡眠（Java 用 skill 66 的效果）
		npc.Sleeped = false
	case 20, 40: // 闇盲咒術 — 致盲（NPC 無視覺，僅計時）
		// NPC 致盲不影響行動旗標
	case 29, 76, 152: // 緩速系列 — NPC debuff 到期
		// NPC 速度恢復（無需特殊處理，NPC 無獨立速度欄位）
	}
}

// sendNpcPoison 發送 S_Poison 到觀察者（NPC 版本，避免 handler 套件循環引用）。
func sendNpcPoison(sess *gonet.Session, objectID int32, poisonType byte) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_POISON)
	w.WriteD(objectID)
	switch poisonType {
	case 1: // 綠色
		w.WriteC(0x01)
		w.WriteC(0x00)
	case 2: // 灰色
		w.WriteC(0x00)
		w.WriteC(0x01)
	default: // 治癒
		w.WriteC(0x00)
		w.WriteC(0x00)
	}
	sess.Send(w.Bytes())
}
