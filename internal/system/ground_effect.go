package system

import (
	"math/rand"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

const (
	fireWallDamageIntervalTicks    = 8
	cubeEffectIntervalTicks        = 20
	cubeBalanceDamageIntervalTicks = 25
	poisonCloudIntervalTicks       = 5
	poisonCloudDamage              = 100
	cubeStatusTicks                = 40

	cubeStatusIgnitionAlly  int32 = 1018
	cubeStatusIgnitionEnemy int32 = 1019
	cubeStatusQuakeAlly     int32 = 1020
	cubeStatusQuakeEnemy    int32 = 1021
	cubeStatusShockAlly     int32 = 1022
	cubeStatusShockEnemy    int32 = 1023
	cubeStatusShockMR       int32 = 1024
	cubeStatusBalance       int32 = 1025
)

type GroundEffectSystem struct {
	world *world.State
	deps  *handler.Deps
}

func NewGroundEffectSystem(ws *world.State, deps *handler.Deps) *GroundEffectSystem {
	return &GroundEffectSystem{world: ws, deps: deps}
}

func (s *GroundEffectSystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *GroundEffectSystem) Update(_ time.Duration) {
	for _, effect := range s.world.GroundEffectList() {
		if effect.Type == world.GroundEffectFireWall {
			effect.DamageTickAcc++
			if effect.DamageTickAcc >= fireWallDamageIntervalTicks {
				effect.DamageTickAcc = 0
				s.applyFireWallDamage(effect)
			}
			continue
		}
		if isCubeGroundEffect(effect.Type) {
			effect.DamageTickAcc++
			s.applyCubePulse(effect)
			continue
		}
		if effect.Type == world.GroundEffectPoisonCloud {
			effect.DamageTickAcc++
			if effect.DamageTickAcc >= poisonCloudIntervalTicks {
				effect.DamageTickAcc = 0
				s.applyPoisonCloudDamage(effect)
			}
		}
	}

	expired := s.world.TickGroundEffects()
	for _, effect := range expired {
		if effect.Type == world.GroundEffectTomb {
			if owner := s.world.GetByCharID(effect.OwnerCharID); owner != nil && owner.TombEffectID == effect.ID {
				owner.TombEffectID = 0
			}
		}
		s.broadcastRemove(effect)
	}
}

func isCubeGroundEffect(typ world.GroundEffectType) bool {
	switch typ {
	case world.GroundEffectCubeIgnition, world.GroundEffectCubeQuake, world.GroundEffectCubeShock, world.GroundEffectCubeBalance:
		return true
	default:
		return false
	}
}

// playerCubeIgnitionImmune 對齊 Java `L1Cube.giveEffect:79-93`：玩家持有
// STATUS_FREEZE(4000) / ABSOLUTE_BARRIER(78) / ICE_LANCE(50) / EARTH_BIND(157) /
// FREEZING_BLIZZARD(80) 任一時，CUBE_IGNITION 對玩家的週期傷害跳過。
func playerCubeIgnitionImmune(target *world.PlayerInfo) bool {
	if target == nil {
		return false
	}
	if target.AbsoluteBarrier {
		return true
	}
	return target.HasBuff(4000) || target.HasBuff(50) || target.HasBuff(157) || target.HasBuff(80)
}

// npcCubeIgnitionImmune 對齊 Java `L1Cube.giveEffect:79-93`：NPC 透過 hasDebuff
// 持有對應 status 時，CUBE_IGNITION 對 NPC 的週期傷害跳過。
func npcCubeIgnitionImmune(npc *world.NpcInfo) bool {
	if npc == nil {
		return false
	}
	return npc.HasDebuff(4000) || npc.HasDebuff(78) || npc.HasDebuff(50) || npc.HasDebuff(157) || npc.HasDebuff(80)
}

// playerCubeQuakeImmune 對齊 Java `L1Cube.giveEffect:110-121 case STATUS_CUBE_QUAKE_TO_ENEMY`：
// 玩家持有 STATUS_FREEZE(4000) / ABSOLUTE_BARRIER(78) / ICE_LANCE(50) / EARTH_BIND(157)
// 任一時，CUBE_QUAKE 對玩家的週期 paralysis 套用跳過。注意 Java QUAKE 的免疫清單
// **不包含** FREEZING_BLIZZARD(80)，與 CUBE_IGNITION 不同。
func playerCubeQuakeImmune(target *world.PlayerInfo) bool {
	if target == nil {
		return false
	}
	if target.AbsoluteBarrier {
		return true
	}
	return target.HasBuff(4000) || target.HasBuff(50) || target.HasBuff(157)
}

// npcCubeQuakeImmune 對齊 Java `L1Cube.giveEffect:110-121`：NPC 持有對應 status 時跳過。
func npcCubeQuakeImmune(npc *world.NpcInfo) bool {
	if npc == nil {
		return false
	}
	return npc.HasDebuff(4000) || npc.HasDebuff(78) || npc.HasDebuff(50) || npc.HasDebuff(157)
}

func (s *GroundEffectSystem) applyCubePulse(effect *world.GroundEffect) {
	nearby := s.world.GetNearbyPlayersInShow(effect.X, effect.Y, effect.MapID, 0, effect.ShowID)
	for _, target := range nearby {
		if target == nil || target.Dead {
			continue
		}
		if chebyshevDist(effect.X, effect.Y, target.X, target.Y) > 3 {
			continue
		}
		if s.isCubeAlly(effect, target) {
			s.applyCubeAlly(effect, target, nearby)
			continue
		}
		if s.deps != nil && s.deps.MapData != nil && s.deps.MapData.IsSafetyZone(target.MapID, target.X, target.Y) {
			continue
		}
		s.applyCubeEnemy(effect, target, nearby)
	}

	for _, npc := range s.world.GetNearbyNpcsInShow(effect.X, effect.Y, effect.MapID, effect.ShowID) {
		if npc == nil || npc.Dead || npc.Impl == "L1Effect" {
			continue
		}
		if chebyshevDist(effect.X, effect.Y, npc.X, npc.Y) > 3 {
			continue
		}
		s.applyCubeEnemyNpc(effect, npc, nearby)
	}
}

func (s *GroundEffectSystem) isCubeAlly(effect *world.GroundEffect, target *world.PlayerInfo) bool {
	if target.CharID == effect.OwnerCharID {
		return true
	}
	if effect.OwnerClanID != 0 && target.ClanID == effect.OwnerClanID {
		return true
	}
	owner := s.world.GetByCharID(effect.OwnerCharID)
	return owner != nil && owner.PartyID != 0 && owner.PartyID == target.PartyID
}

func (s *GroundEffectSystem) applyCubeAlly(effect *world.GroundEffect, target *world.PlayerInfo, nearby []*world.PlayerInfo) {
	switch effect.Type {
	case world.GroundEffectCubeIgnition:
		if s.addPlayerCubeBuff(target, cubeStatusIgnitionAlly, cubeStatusTicks, 30, 0, 0, 0) {
			s.broadcastCubeGfx(effect, target.CharID, effect.SkillID, false, nearby)
		}
	case world.GroundEffectCubeQuake:
		if s.addPlayerCubeBuff(target, cubeStatusQuakeAlly, cubeStatusTicks, 0, 0, 0, 30) {
			s.broadcastCubeGfx(effect, target.CharID, effect.SkillID, false, nearby)
		}
	case world.GroundEffectCubeShock:
		if s.addPlayerCubeBuff(target, cubeStatusShockAlly, cubeStatusTicks, 0, 0, 30, 0) {
			s.broadcastCubeGfx(effect, target.CharID, effect.SkillID, false, nearby)
		}
	case world.GroundEffectCubeBalance:
		s.addPlayerCubeBuff(target, cubeStatusBalance, cubeStatusTicks, 0, 0, 0, 0)
		s.applyCubeBalance(target, effect, nearby)
	}
}

func (s *GroundEffectSystem) applyCubeEnemy(effect *world.GroundEffect, target *world.PlayerInfo, nearby []*world.PlayerInfo) {
	switch effect.Type {
	case world.GroundEffectCubeIgnition:
		if s.addPlayerCubeBuff(target, cubeStatusIgnitionEnemy, cubeStatusTicks, 0, 0, 0, 0) {
			s.broadcastCubeGfx(effect, target.CharID, effect.SkillID, true, nearby)
		}
		// Java `L1Cube.giveEffect:79-93 case STATUS_CUBE_IGNITION_TO_ENEMY`：
		// STATUS_FREEZE(4000) / ABSOLUTE_BARRIER(78) / ICE_LANCE(50) /
		// EARTH_BIND(157) / FREEZING_BLIZZARD(80) 任一持有時跳過傷害。
		if effect.DamageTickAcc%cubeEffectIntervalTicks == 0 && !playerCubeIgnitionImmune(target) {
			s.damagePlayerByCube(effect, target, 10, nearby)
		}
	case world.GroundEffectCubeQuake:
		if s.addPlayerCubeBuff(target, cubeStatusQuakeEnemy, cubeStatusTicks, 0, 0, 0, 0) {
			s.broadcastCubeGfx(effect, target.CharID, effect.SkillID, true, nearby)
		}
		// Java `L1Cube.giveEffect:110-121`：四項 immune buff 任一持有則跳過 paralysis。
		if effect.DamageTickAcc%cubeEffectIntervalTicks == 0 && !playerCubeQuakeImmune(target) {
			target.Paralyzed = true
			target.AddBuff(&world.ActiveBuff{
				SkillID:      cubeStatusQuakeEnemy,
				TicksLeft:    5,
				SetParalyzed: true,
			})
			handler.SendParalysis(target.Session, handler.BindApply)
		}
	case world.GroundEffectCubeShock:
		if s.addPlayerCubeBuff(target, cubeStatusShockEnemy, cubeStatusTicks, 0, 0, 0, 0) {
			s.broadcastCubeGfx(effect, target.CharID, effect.SkillID, true, nearby)
		}
		s.addPlayerCubeBuff(target, cubeStatusShockMR, 20, 0, 0, 0, 0)
	case world.GroundEffectCubeBalance:
		s.addPlayerCubeBuff(target, cubeStatusBalance, cubeStatusTicks, 0, 0, 0, 0)
		s.applyCubeBalance(target, effect, nearby)
	}
}

func (s *GroundEffectSystem) applyCubeEnemyNpc(effect *world.GroundEffect, npc *world.NpcInfo, nearby []*world.PlayerInfo) {
	switch effect.Type {
	case world.GroundEffectCubeIgnition:
		// Java `L1Cube.giveEffect:79-93 case STATUS_CUBE_IGNITION_TO_ENEMY`：對 NPC 同樣套用
		// STATUS_FREEZE(4000) / ABSOLUTE_BARRIER(78) / ICE_LANCE(50) / EARTH_BIND(157) /
		// FREEZING_BLIZZARD(80) 免疫檢查（Java `hasSkillEffect` 對 cha 不分 PC/NPC）。
		if effect.DamageTickAcc%cubeEffectIntervalTicks == 0 && !npcCubeIgnitionImmune(npc) {
			handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, 2))
			npc.HP -= 10
			s.finishNpcCubeDamage(effect, npc, nearby)
		}
	case world.GroundEffectCubeQuake:
		// Java `L1Cube.giveEffect:110-121` 對 NPC 同樣套用四項 immune buff 檢查。
		if effect.DamageTickAcc%cubeEffectIntervalTicks == 0 && !npcCubeQuakeImmune(npc) {
			npc.Paralyzed = true
			npc.AddDebuff(cubeStatusQuakeEnemy, 5)
		}
	case world.GroundEffectCubeShock:
		// Java `L1Cube.giveEffect:135-145 case STATUS_CUBE_SHOCK_TO_ENEMY` 對 cha 套
		// `setSkillEffect(STATUS_MR_REDUCTION_BY_CUBE_SHOCK=1024, 4000)`，每 tick refresh
		// 維持 4 秒；Go `npc.AddDebuff` 使用 map[skillID]=ticks 寫入即覆蓋，自然完成 refresh。
		// 原本用 cubeStatusShockEnemy(1023) + 40 ticks 與 Java 不符，改為 1024 + 20 ticks (4 秒)。
		npc.AddDebuff(cubeStatusShockMR, 20)
	case world.GroundEffectCubeBalance:
		if effect.DamageTickAcc%cubeEffectIntervalTicks == 0 {
			npc.MP += 5
			if npc.MaxMP > 0 && npc.MP > npc.MaxMP {
				npc.MP = npc.MaxMP
			}
		}
		if effect.DamageTickAcc%cubeBalanceDamageIntervalTicks == 0 && !npcDamageBlockedBySkm0LikeJava(npc) {
			npc.HP -= 25
			s.finishNpcCubeDamage(effect, npc, nearby)
		}
	}
}

func (s *GroundEffectSystem) addPlayerCubeBuff(target *world.PlayerInfo, skillID int32, ticks int, fire, water, wind, earth int16) bool {
	if target.HasBuff(skillID) {
		return false
	}
	buff := &world.ActiveBuff{
		SkillID:       skillID,
		TicksLeft:     ticks,
		DeltaFireRes:  fire,
		DeltaWaterRes: water,
		DeltaWindRes:  wind,
		DeltaEarthRes: earth,
	}
	target.FireRes += fire
	target.WaterRes += water
	target.WindRes += wind
	target.EarthRes += earth
	target.AddBuff(buff)
	if fire != 0 || water != 0 || wind != 0 || earth != 0 {
		handler.SendPlayerStatus(target.Session, target)
	}
	return true
}

func (s *GroundEffectSystem) applyCubeBalance(target *world.PlayerInfo, effect *world.GroundEffect, nearby []*world.PlayerInfo) {
	if effect.DamageTickAcc%cubeEffectIntervalTicks == 0 {
		target.MP += 5
		if target.MP > target.MaxMP {
			target.MP = target.MaxMP
		}
		sendMpUpdate(target.Session, target)
	}
	if effect.DamageTickAcc%cubeBalanceDamageIntervalTicks == 0 {
		s.damagePlayerByCube(effect, target, 25, nearby)
	}
}

func (s *GroundEffectSystem) damagePlayerByCube(effect *world.GroundEffect, target *world.PlayerInfo, damage int32, nearby []*world.PlayerInfo) {
	if damage <= 0 || target.AbsoluteBarrier {
		return
	}
	damage = applyImmuneToHarmDamage(target, damage)
	if damage <= 0 {
		return
	}
	handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(target.CharID, 2))
	if target.Sleeped {
		breakPlayerSleepBySkill(target)
	}
	target.HP -= damage
	target.Dirty = true
	if target.HP <= 0 {
		target.HP = 0
		if s.deps != nil && s.deps.Death != nil {
			s.deps.Death.KillPlayer(target)
		}
		return
	}
	sendHpUpdate(target.Session, target)
}

func (s *GroundEffectSystem) finishNpcCubeDamage(effect *world.GroundEffect, npc *world.NpcInfo, nearby []*world.PlayerInfo) {
	if npc.HP < 0 {
		npc.HP = 0
	}
	hpRatio := int16(0)
	if npc.MaxHP > 0 {
		hpRatio = int16((npc.HP * 100) / npc.MaxHP)
	}
	for _, viewer := range nearby {
		handler.SendHpMeter(viewer.Session, npc.ID, hpRatio)
	}
	if npc.HP <= 0 {
		if s.deps != nil && s.deps.Combat != nil {
			owner := s.world.GetByCharID(effect.OwnerCharID)
			s.deps.Combat.HandleNpcDeath(npc, owner, nearby)
		} else {
			npc.Dead = true
			s.world.NpcDied(npc)
		}
	}
}

func (s *GroundEffectSystem) broadcastCubeGfx(effect *world.GroundEffect, targetID int32, skillID int32, enemy bool, nearby []*world.PlayerInfo) {
	if s.deps == nil || s.deps.Skills == nil {
		return
	}
	skill := s.deps.Skills.Get(skillID)
	if skill == nil {
		return
	}
	gfx := skill.CastGfx
	if enemy && skill.CastGfx2 > 0 {
		gfx = skill.CastGfx2
	}
	if gfx > 0 {
		handler.BroadcastToPlayers(nearby, handler.BuildSkillEffect(targetID, gfx))
	}
}

func (s *GroundEffectSystem) broadcastRemove(effect *world.GroundEffect) {
	nearby := s.world.GetNearbyPlayersInShow(effect.X, effect.Y, effect.MapID, 0, effect.ShowID)
	data := handler.BuildRemoveObject(effect.ID)
	for _, viewer := range nearby {
		viewer.Session.Send(data)
		if viewer.Known != nil {
			delete(viewer.Known.GroundEffects, effect.ID)
		}
	}
}

func (s *GroundEffectSystem) applyFireWallDamage(effect *world.GroundEffect) {
	nearby := s.world.GetNearbyPlayersInShow(effect.X, effect.Y, effect.MapID, 0, effect.ShowID)
	for _, target := range nearby {
		if target == nil || target.Dead || target.CharID == effect.OwnerCharID {
			continue
		}
		if chebyshevDist(effect.X, effect.Y, target.X, target.Y) > 1 {
			continue
		}
		if effect.OwnerClanID != 0 && target.ClanID == effect.OwnerClanID {
			continue
		}
		if s.deps != nil && s.deps.MapData != nil && s.deps.MapData.IsSafetyZone(target.MapID, target.X, target.Y) {
			continue
		}
		damage := calcFireWallDamage(effect.OwnerIntel, target.FireRes)
		if target.AbsoluteBarrier {
			damage = 0
		}
		damage = applyImmuneToHarmDamage(target, damage)
		if damage <= 0 {
			continue
		}
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(target.CharID, 2))
		if target.Sleeped {
			breakPlayerSleepBySkill(target)
		}
		target.HP -= damage
		target.Dirty = true
		if target.HP <= 0 {
			target.HP = 0
			if s.deps != nil && s.deps.Death != nil {
				s.deps.Death.KillPlayer(target)
			}
			continue
		}
		sendHpUpdate(target.Session, target)
	}

	for _, npc := range s.world.GetNearbyNpcsInShow(effect.X, effect.Y, effect.MapID, effect.ShowID) {
		if npc == nil || npc.Dead || npc.Impl == "L1Effect" {
			continue
		}
		if chebyshevDist(effect.X, effect.Y, npc.X, npc.Y) > 1 {
			continue
		}
		damage := calcFireWallDamage(effect.OwnerIntel, npc.FireRes)
		if npcDamageBlockedBySkm0LikeJava(npc) {
			damage = 0
		}
		if damage <= 0 {
			continue
		}
		handler.BroadcastToPlayers(nearby, handler.BuildActionGfx(npc.ID, 2))
		npc.HP -= damage
		if npc.HP < 0 {
			npc.HP = 0
		}
		hpRatio := int16(0)
		if npc.MaxHP > 0 {
			hpRatio = int16((npc.HP * 100) / npc.MaxHP)
		}
		for _, viewer := range nearby {
			handler.SendHpMeter(viewer.Session, npc.ID, hpRatio)
		}
		if npc.HP <= 0 {
			if s.deps != nil && s.deps.Combat != nil {
				owner := s.world.GetByCharID(effect.OwnerCharID)
				s.deps.Combat.HandleNpcDeath(npc, owner, nearby)
			} else {
				npc.Dead = true
				s.world.NpcDied(npc)
			}
		}
	}
}

func (s *GroundEffectSystem) applyPoisonCloudDamage(effect *world.GroundEffect) {
	nearby := s.world.GetNearbyPlayersInShow(effect.X, effect.Y, effect.MapID, 0, effect.ShowID)
	for _, target := range nearby {
		if target == nil || target.Dead || isGMInvisible(target) {
			continue
		}
		if target.X != effect.X || target.Y != effect.Y {
			continue
		}
		applyDamagePoisonToPlayer(target, 0, poisonCloudDamage, s.deps)
	}
}

func calcFireWallDamage(ownerInt int16, fireRes int16) int32 {
	randomBase := int(ownerInt) / 2
	if randomBase < 1 {
		randomBase = 1
	}
	src := int32(19 + rand.Intn(randomBase))
	return applyFireResistance(src, fireRes)
}

func applyFireResistance(damage int32, fireRes int16) int32 {
	resistFloor := int32(0.16 * float64(absInt16(fireRes)))
	if fireRes < 0 {
		resistFloor = -resistFloor
	}
	reduced := damage - (damage*resistFloor)/32
	if reduced < 0 {
		return 0
	}
	return reduced
}

func absInt16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}
