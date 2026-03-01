package system

import (
	"context"
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	gonet "github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/persist"
	"github.com/l1jgo/server/internal/world"
)

// CompanionAISystem processes AI ticks for summons, dolls, followers, and pets.
// Runs in Phase 2 (Update), after NpcAISystem.
type CompanionAISystem struct {
	world *world.State
	deps  *handler.Deps
}

func NewCompanionAISystem(ws *world.State, deps *handler.Deps) *CompanionAISystem {
	return &CompanionAISystem{world: ws, deps: deps}
}

func (s *CompanionAISystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

func (s *CompanionAISystem) Update(_ time.Duration) {
	s.tickSummons()
	s.tickDolls()
	s.tickFollowers()
	s.tickPets()
}

// ========================================================================
//  Summon AI
// ========================================================================

func (s *CompanionAISystem) tickSummons() {
	ws := s.world
	// Collect IDs to avoid modifying map during iteration
	var toRemove []int32
	ws.AllSummons(func(sum *world.SummonInfo) {
		if sum.Dead {
			return
		}

		master := ws.GetByCharID(sum.OwnerCharID)
		if master == nil {
			// Master offline — remove summon
			toRemove = append(toRemove, sum.ID)
			return
		}

		// Timer expiry (non-zero = has duration)
		if sum.TimerTicks > 0 {
			sum.TimerTicks--
			if sum.TimerTicks <= 0 {
				toRemove = append(toRemove, sum.ID)
				return
			}
		}

		// Decrement cooldowns
		if sum.AttackTimer > 0 {
			sum.AttackTimer--
		}
		if sum.MoveTimer > 0 {
			sum.MoveTimer--
		}

		// Check if on same map as master
		if sum.MapID != master.MapID {
			// Teleport to master
			s.companionTeleportToMaster(sum.ID, master, func(id int32, x, y int32, h int16) {
				ws.UpdateSummonPosition(id, x, y, h)
			})
			return
		}

		dist := chebyshev32(sum.X, sum.Y, master.X, master.Y)

		switch sum.Status {
		case world.SummonRest:
			// Stay in place, clear aggro
			sum.AggroTarget = 0
			sum.AggroPlayerID = 0

		case world.SummonAggressive:
			// Priority 1: Leash — if too far from master (>5 tiles), drop target and follow
			if dist > 5 {
				sum.AggroTarget = 0
				if sum.MoveTimer <= 0 {
					s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, master.X, master.Y,
						func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
					sum.MoveTimer = 2
				}
				break
			}
			// Priority 2: Follow master if > 2 tiles and no target
			if dist > 2 && sum.AggroTarget == 0 && sum.MoveTimer <= 0 {
				s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, master.X, master.Y,
					func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
				sum.MoveTimer = 2
			}
			// Priority 3: Scan for target if none
			if sum.AggroTarget == 0 {
				s.summonScanForTarget(sum)
			}
			// Priority 4: Attack target
			if sum.AggroTarget != 0 && sum.AttackTimer <= 0 {
				s.summonAttackTarget(sum)
			}

		case world.SummonDefensive:
			// Leash — if too far from master (>5 tiles), drop target and follow
			if dist > 5 {
				sum.AggroTarget = 0
				if sum.MoveTimer <= 0 {
					s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, master.X, master.Y,
						func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
					sum.MoveTimer = 2
				}
				break
			}
			// Only counterattack (aggro set by being attacked)
			if sum.AggroTarget != 0 && sum.AttackTimer <= 0 {
				s.summonAttackTarget(sum)
			}
			// Follow master
			if dist > 2 && sum.MoveTimer <= 0 {
				s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, master.X, master.Y,
					func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
				sum.MoveTimer = 2
			}

		case world.SummonExtend:
			// Move away from master if too close
			if dist < 5 && sum.MoveTimer <= 0 {
				awayX := sum.X + (sum.X - master.X)
				awayY := sum.Y + (sum.Y - master.Y)
				s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, awayX, awayY,
					func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
				sum.MoveTimer = 3
			}

		case world.SummonAlert:
			// Leash — if too far from master (>5 tiles), drop target and follow
			if dist > 5 {
				sum.AggroTarget = 0
				if sum.MoveTimer <= 0 {
					s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, master.X, master.Y,
						func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
					sum.MoveTimer = 2
				}
				break
			}
			// Guard position: attack nearby, return to home if drifted
			homeDist := chebyshev32(sum.X, sum.Y, sum.HomeX, sum.HomeY)
			if homeDist > 1 && sum.AggroTarget == 0 && sum.MoveTimer <= 0 {
				s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, sum.HomeX, sum.HomeY,
					func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
				sum.MoveTimer = 2
			}
			if sum.AggroTarget == 0 {
				s.summonScanForTarget(sum)
			}
			if sum.AggroTarget != 0 && sum.AttackTimer <= 0 {
				s.summonAttackTarget(sum)
			}
		}
	})

	// Remove expired/orphaned summons
	for _, id := range toRemove {
		sum := ws.RemoveSummon(id)
		if sum == nil {
			continue
		}
		nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
		for _, viewer := range nearby {
			sendCompanionSoundEffect(viewer.Session, sum.ID, 169) // death sound
			sendCompanionRemove(viewer.Session, sum.ID)
		}
	}
}

// summonScanForTarget finds the closest alive monster within range 3.
// Java: summons only aggro nearby enemies; range 3 prevents chasing distant mobs.
// 只攻擊怪物（L1Monster），不攻擊商店、守衛等友好 NPC。
func (s *CompanionAISystem) summonScanForTarget(sum *world.SummonInfo) {
	nearby := s.world.GetNearbyNpcs(sum.X, sum.Y, sum.MapID)
	var bestDist int32 = 999
	for _, npc := range nearby {
		if npc.Dead || npc.Impl != "L1Monster" {
			continue
		}
		d := chebyshev32(sum.X, sum.Y, npc.X, npc.Y)
		if d <= 3 && d < bestDist {
			bestDist = d
			sum.AggroTarget = npc.ID
		}
	}
}

// summonAttackTarget makes the summon attack its current target NPC.
func (s *CompanionAISystem) summonAttackTarget(sum *world.SummonInfo) {
	ws := s.world
	targetNpc := ws.GetNpc(sum.AggroTarget)
	if targetNpc == nil || targetNpc.Dead {
		sum.AggroTarget = 0
		return
	}

	dist := chebyshev32(sum.X, sum.Y, targetNpc.X, targetNpc.Y)
	if dist > int32(sum.Ranged) || dist > 1 && sum.Ranged <= 1 {
		// Move toward target instead of attacking
		if sum.MoveTimer <= 0 {
			s.companionMoveToward(sum.ID, sum.X, sum.Y, sum.MapID, targetNpc.X, targetNpc.Y,
				func(id int32, x, y int32, h int16) { ws.UpdateSummonPosition(id, x, y, h) })
			sum.MoveTimer = 2
		}
		return
	}

	// Calculate damage (simplified: AtkDmg ± random variance)
	dmg := sum.AtkDmg
	if dmg <= 0 {
		dmg = int32(sum.Level)/2 + 1
	}
	variance := int32(world.RandInt(int(dmg/4+1))) - dmg/8
	dmg += variance
	if dmg < 1 {
		dmg = 1
	}

	// 扣血
	targetNpc.HP -= dmg
	heading := calcNpcHeading(sum.X, sum.Y, targetNpc.X, targetNpc.Y)

	// 廣播攻擊動畫
	nearby := ws.GetNearbyPlayersAt(sum.X, sum.Y, sum.MapID)
	atkData := buildNpcAttack(sum.ID, targetNpc.ID, dmg, heading)
	handler.BroadcastToPlayers(nearby, atkData)

	// 傷害歸屬主人（仇恨累加到主人 SessionID）
	master := ws.GetByCharID(sum.OwnerCharID)
	if master != nil {
		AddHate(targetNpc, master.SessionID, dmg)
		sendCompanionHpMeter(master.Session, sum.ID, sum.HP, sum.MaxHP)
	}

	// 攻擊冷卻
	atkCooldown := 10
	if sum.AtkSpeed > 0 {
		atkCooldown = int(sum.AtkSpeed) / 200
		if atkCooldown < 3 {
			atkCooldown = 3
		}
	}
	sum.AttackTimer = atkCooldown

	// NPC 死亡 → 統一走 handleNpcDeath（含經驗分配、掉落、善惡）
	if targetNpc.HP <= 0 {
		targetNpc.HP = 0
		sum.AggroTarget = 0
		if master != nil {
			handleNpcDeath(targetNpc, master, nearby, s.deps)
		} else {
			// 主人離線：僅做基礎死亡處理（無經驗/掉落）
			targetNpc.Dead = true
			ws.NpcDied(targetNpc)
			targetNpc.DeleteTimer = 50
			if targetNpc.RespawnDelay > 0 {
				targetNpc.RespawnTimer = targetNpc.RespawnDelay * 5
			}
			ClearHateList(targetNpc)
			for _, viewer := range nearby {
				handler.SendActionGfx(viewer.Session, targetNpc.ID, 8)
				handler.SendNpcDeadPack(viewer.Session, targetNpc)
			}
		}
	}
}

// ========================================================================
//  Doll AI
// ========================================================================

func (s *CompanionAISystem) tickDolls() {
	ws := s.world
	var toRemove []int32

	ws.AllDolls(func(doll *world.DollInfo) {
		master := ws.GetByCharID(doll.OwnerCharID)
		if master == nil || master.Dead || master.Invisible {
			toRemove = append(toRemove, doll.ID)
			return
		}

		// Timer countdown
		if doll.TimerTicks > 0 {
			doll.TimerTicks--
			if doll.TimerTicks <= 0 {
				toRemove = append(toRemove, doll.ID)
				return
			}
		}

		// Decrement move cooldown
		if doll.MoveTimer > 0 {
			doll.MoveTimer--
		}

		// Check same map
		if doll.MapID != master.MapID {
			s.companionTeleportToMaster(doll.ID, master, func(id int32, x, y int32, h int16) {
				ws.UpdateDollPosition(id, x, y, h)
			})
			return
		}

		// Follow master (with move cooldown like summons)
		dist := chebyshev32(doll.X, doll.Y, master.X, master.Y)
		if dist > 2 && doll.MoveTimer <= 0 {
			s.companionMoveToward(doll.ID, doll.X, doll.Y, doll.MapID, master.X, master.Y,
				func(id int32, x, y int32, h int16) { ws.UpdateDollPosition(id, x, y, h) })
			doll.MoveTimer = 2 // 2 ticks = 400ms between moves
		}
	})

	// Remove expired dolls (bonuses must be removed by the calling handler)
	for _, id := range toRemove {
		doll := ws.RemoveDoll(id)
		if doll == nil {
			continue
		}
		// Revert bonuses on the master
		master := ws.GetByCharID(doll.OwnerCharID)
		if master != nil {
			if s.deps.DollMgr != nil {
				s.deps.DollMgr.RemoveDollBonuses(master, doll)
			}
			sendCompanionSoundEffect(master.Session, doll.ID, 5936) // dismiss sound
			sendDollTimerClear(master.Session)
			handler.SendPlayerStatus(master.Session, master)
		}
		nearby := ws.GetNearbyPlayersAt(doll.X, doll.Y, doll.MapID)
		for _, viewer := range nearby {
			sendCompanionRemove(viewer.Session, doll.ID)
		}
	}
}

// ========================================================================
//  Follower AI
// ========================================================================

func (s *CompanionAISystem) tickFollowers() {
	ws := s.world
	var toRemove []int32

	ws.AllFollowers(func(f *world.FollowerInfo) {
		if f.Dead {
			return
		}

		master := ws.GetByCharID(f.OwnerCharID)
		if master == nil || master.Dead {
			toRemove = append(toRemove, f.ID)
			return
		}

		// Different map — dismiss
		if f.MapID != master.MapID {
			toRemove = append(toRemove, f.ID)
			return
		}

		// Too far from master (>13 tiles) — dismiss
		dist := chebyshev32(f.X, f.Y, master.X, master.Y)
		if dist > 13 {
			toRemove = append(toRemove, f.ID)
			return
		}

		// Follow master
		if f.MoveTimer > 0 {
			f.MoveTimer--
		}
		if dist > 2 && f.MoveTimer <= 0 {
			s.companionMoveToward(f.ID, f.X, f.Y, f.MapID, master.X, master.Y,
				func(id int32, x, y int32, h int16) { ws.UpdateFollowerPosition(id, x, y, h) })
			f.MoveTimer = 2
		}
	})

	// Remove dismissed followers — respawn original NPC
	for _, id := range toRemove {
		f := ws.RemoveFollower(id)
		if f == nil {
			continue
		}
		nearby := ws.GetNearbyPlayersAt(f.X, f.Y, f.MapID)
		for _, viewer := range nearby {
			sendCompanionRemove(viewer.Session, f.ID)
		}
		// Respawn the original NPC at spawn position
		s.respawnOriginalNpc(f)
	}
}

// respawnOriginalNpc recreates the original NPC from a dismissed follower.
func (s *CompanionAISystem) respawnOriginalNpc(f *world.FollowerInfo) {
	if f.OrigNpcID == 0 {
		return
	}
	tmpl := s.deps.Npcs.Get(f.OrigNpcID)
	if tmpl == nil {
		return
	}
	npc := &world.NpcInfo{
		ID:      world.NextNpcID(),
		NpcID:   f.OrigNpcID,
		Impl:    tmpl.Impl,
		GfxID:   tmpl.GfxID,
		Name:    tmpl.Name,
		NameID:  tmpl.NameID,
		Level:   tmpl.Level,
		HP:      tmpl.HP,
		MaxHP:   tmpl.HP,
		MP:      tmpl.MP,
		MaxMP:   tmpl.MP,
		AC:      tmpl.AC,
		STR:     tmpl.STR,
		DEX:     tmpl.DEX,
		Exp:     tmpl.Exp,
		Lawful:  tmpl.Lawful,
		Size:    tmpl.Size,
		MR:        tmpl.MR,
		PoisonAtk: tmpl.PoisonAtk,
		X:         f.SpawnX,
		Y:         f.SpawnY,
		MapID:     f.SpawnMapID,
		SpawnX:    f.SpawnX,
		SpawnY:  f.SpawnY,
		SpawnMapID: f.SpawnMapID,
	}
	s.world.AddNpc(npc)
	nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcPack(viewer.Session, npc)
	}
}

// ========================================================================
//  Pet AI
// ========================================================================

func (s *CompanionAISystem) tickPets() {
	ws := s.world
	var toRemove []int32

	ws.AllPets(func(pet *world.PetInfo) {
		if pet.Dead {
			return
		}

		master := ws.GetByCharID(pet.OwnerCharID)
		if master == nil {
			// Master offline — save and remove
			toRemove = append(toRemove, pet.ID)
			return
		}

		// Decrement cooldowns
		if pet.AttackTimer > 0 {
			pet.AttackTimer--
		}
		if pet.MoveTimer > 0 {
			pet.MoveTimer--
		}

		// Check if on same map as master
		if pet.MapID != master.MapID {
			s.companionTeleportToMaster(pet.ID, master, func(id int32, x, y int32, h int16) {
				ws.UpdatePetPosition(id, x, y, h)
			})
			pet.MapID = master.MapID
			return
		}

		dist := chebyshev32(pet.X, pet.Y, master.X, master.Y)

		// Java: auto-teleport if > 5 tiles from master (for non-rest/extend/alert)
		if dist > 5 && pet.Status != world.PetStatusRest &&
			pet.Status != world.PetStatusExtend && pet.Status != world.PetStatusAlert {
			s.companionTeleportToMaster(pet.ID, master, func(id int32, x, y int32, h int16) {
				ws.UpdatePetPosition(id, x, y, h)
			})
			return
		}

		switch pet.Status {
		case world.PetStatusRest:
			// Stay in place, clear aggro
			pet.AggroTarget = 0
			pet.AggroPlayerID = 0

		case world.PetStatusAggressive:
			// Attack nearest NPC if no target
			if pet.AggroTarget == 0 {
				s.petScanForTarget(pet)
			}
			if pet.AggroTarget != 0 && pet.AttackTimer <= 0 {
				s.petAttackTarget(pet)
			}
			if dist > 2 && pet.MoveTimer <= 0 {
				s.companionMoveToward(pet.ID, pet.X, pet.Y, pet.MapID, master.X, master.Y,
					func(id int32, x, y int32, h int16) { ws.UpdatePetPosition(id, x, y, h) })
				pet.MoveTimer = 2
			}

		case world.PetStatusDefensive:
			// Only counterattack (aggro set by being attacked)
			if pet.AggroTarget != 0 && pet.AttackTimer <= 0 {
				s.petAttackTarget(pet)
			}
			if dist > 2 && pet.MoveTimer <= 0 {
				s.companionMoveToward(pet.ID, pet.X, pet.Y, pet.MapID, master.X, master.Y,
					func(id int32, x, y int32, h int16) { ws.UpdatePetPosition(id, x, y, h) })
				pet.MoveTimer = 2
			}

		case world.PetStatusExtend:
			// Move away from master if too close
			if dist < 5 && pet.MoveTimer <= 0 {
				awayX := pet.X + (pet.X - master.X)
				awayY := pet.Y + (pet.Y - master.Y)
				s.companionMoveToward(pet.ID, pet.X, pet.Y, pet.MapID, awayX, awayY,
					func(id int32, x, y int32, h int16) { ws.UpdatePetPosition(id, x, y, h) })
				pet.MoveTimer = 3
			}

		case world.PetStatusAlert:
			// Guard position: attack nearby, return to home if drifted
			homeDist := chebyshev32(pet.X, pet.Y, pet.HomeX, pet.HomeY)
			if homeDist > 1 && pet.AggroTarget == 0 && pet.MoveTimer <= 0 {
				s.companionMoveToward(pet.ID, pet.X, pet.Y, pet.MapID, pet.HomeX, pet.HomeY,
					func(id int32, x, y int32, h int16) { ws.UpdatePetPosition(id, x, y, h) })
				pet.MoveTimer = 2
			}
			if pet.AggroTarget == 0 {
				s.petScanForTarget(pet)
			}
			if pet.AggroTarget != 0 && pet.AttackTimer <= 0 {
				s.petAttackTarget(pet)
			}

		case world.PetStatusWhistle:
			// Move toward master, switch to rest when close
			if dist <= 2 {
				pet.Status = world.PetStatusRest
				pet.AggroTarget = 0
			} else if pet.MoveTimer <= 0 {
				s.companionMoveToward(pet.ID, pet.X, pet.Y, pet.MapID, master.X, master.Y,
					func(id int32, x, y int32, h int16) { ws.UpdatePetPosition(id, x, y, h) })
				pet.MoveTimer = 1
			}
		}
	})

	// Remove orphaned pets (master offline) — save to DB
	for _, id := range toRemove {
		pet := ws.RemovePet(id)
		if pet == nil {
			continue
		}
		// Save to DB before removing
		s.savePetToDB(pet)
		nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
		for _, viewer := range nearby {
			sendCompanionRemove(viewer.Session, pet.ID)
		}
	}
}

// petScanForTarget finds the closest alive monster within range 8 for a pet.
// 只攻擊怪物（L1Monster），不攻擊商店、守衛等友好 NPC。
func (s *CompanionAISystem) petScanForTarget(pet *world.PetInfo) {
	nearby := s.world.GetNearbyNpcs(pet.X, pet.Y, pet.MapID)
	var bestDist int32 = 999
	for _, npc := range nearby {
		if npc.Dead || npc.Impl != "L1Monster" {
			continue
		}
		d := chebyshev32(pet.X, pet.Y, npc.X, npc.Y)
		if d <= 8 && d < bestDist {
			bestDist = d
			pet.AggroTarget = npc.ID
		}
	}
}

// petAttackTarget makes the pet attack its current target NPC.
func (s *CompanionAISystem) petAttackTarget(pet *world.PetInfo) {
	ws := s.world
	targetNpc := ws.GetNpc(pet.AggroTarget)
	if targetNpc == nil || targetNpc.Dead {
		pet.AggroTarget = 0
		return
	}

	dist := chebyshev32(pet.X, pet.Y, targetNpc.X, targetNpc.Y)
	if dist > int32(pet.Ranged) || (dist > 1 && pet.Ranged <= 1) {
		// Move toward target
		if pet.MoveTimer <= 0 {
			s.companionMoveToward(pet.ID, pet.X, pet.Y, pet.MapID, targetNpc.X, targetNpc.Y,
				func(id int32, x, y int32, h int16) { ws.UpdatePetPosition(id, x, y, h) })
			pet.MoveTimer = 2
		}
		return
	}

	// Calculate damage (simplified)
	dmg := pet.AtkDmg + int32(pet.DamageByWeapon)
	if dmg <= 0 {
		dmg = int32(pet.Level)/2 + 1
	}
	variance := int32(world.RandInt(int(dmg/4+1))) - dmg/8
	dmg += variance
	if dmg < 1 {
		dmg = 1
	}

	// 扣血
	targetNpc.HP -= dmg
	heading := calcNpcHeading(pet.X, pet.Y, targetNpc.X, targetNpc.Y)

	// 廣播攻擊動畫
	nearby := ws.GetNearbyPlayersAt(pet.X, pet.Y, pet.MapID)
	petAtkData := buildNpcAttack(pet.ID, targetNpc.ID, dmg, heading)
	handler.BroadcastToPlayers(nearby, petAtkData)

	// 傷害歸屬主人（仇恨累加到主人 SessionID）
	master := ws.GetByCharID(pet.OwnerCharID)
	if master != nil {
		AddHate(targetNpc, master.SessionID, dmg)
	}

	// NPC 反擊 — 簡化傷害計算
	if targetNpc.HP > 0 {
		retalDmg := int32(targetNpc.Level) / 2
		if retalDmg < 1 {
			retalDmg = 1
		}
		retalDmg += int32(world.RandInt(int(retalDmg/2 + 1)))
		// 寵物 AC 減傷
		acReduction := int32(-pet.AC) / 3
		retalDmg -= acReduction
		if retalDmg < 1 {
			retalDmg = 1
		}
		pet.HP -= retalDmg
		if pet.HP <= 0 {
			if s.deps.PetLife != nil {
				s.deps.PetLife.PetDie(pet)
			}
			if master != nil {
				sendCompanionHpMeter(master.Session, pet.ID, 0, pet.MaxHP)
			}
			return
		}
	}

	// 更新主人的寵物血量
	if master != nil {
		sendCompanionHpMeter(master.Session, pet.ID, pet.HP, pet.MaxHP)
	}

	// 攻擊冷卻
	atkCooldown := 10
	if pet.AtkSpeed > 0 {
		atkCooldown = int(pet.AtkSpeed) / 200
		if atkCooldown < 3 {
			atkCooldown = 3
		}
	}
	pet.AttackTimer = atkCooldown

	// NPC 死亡 → 統一走 handleNpcDeath（含經驗分配、掉落、善惡）+ 寵物自身經驗
	if targetNpc.HP <= 0 {
		targetNpc.HP = 0
		pet.AggroTarget = 0

		if master != nil {
			handleNpcDeath(targetNpc, master, nearby, s.deps)
		} else {
			// 主人離線：僅做基礎死亡處理
			targetNpc.Dead = true
			ws.NpcDied(targetNpc)
			targetNpc.DeleteTimer = 50
			if targetNpc.RespawnDelay > 0 {
				targetNpc.RespawnTimer = targetNpc.RespawnDelay * 5
			}
			ClearHateList(targetNpc)
			for _, viewer := range nearby {
				handler.SendActionGfx(viewer.Session, targetNpc.ID, 8)
				handler.SendNpcDeadPack(viewer.Session, targetNpc)
			}
		}

		// 寵物自身經驗（獨立於玩家經驗分配）
		petExp := targetNpc.Exp
		if s.deps.Config.Rates.PetExpRate > 0 {
			petExp = int32(float64(petExp) * s.deps.Config.Rates.PetExpRate)
		}
		if petExp > 0 && s.deps.PetLife != nil {
			s.deps.PetLife.AddPetExp(pet, petExp)
			if master != nil {
				sendCompanionHpMeter(master.Session, pet.ID, pet.HP, pet.MaxHP)
			}
		}
	}
}

// savePetToDB saves pet state to database (fire-and-forget with short timeout).
func (s *CompanionAISystem) savePetToDB(pet *world.PetInfo) {
	if s.deps.PetRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.deps.PetRepo.Save(ctx, &persist.PetRow{
		ItemObjID: pet.ItemObjID,
		ObjID:     pet.ID,
		NpcID:     pet.NpcID,
		Name:      pet.Name,
		Level:     pet.Level,
		HP:        pet.HP,
		MaxHP:     pet.MaxHP,
		MP:        pet.MP,
		MaxMP:     pet.MaxMP,
		Exp:       pet.Exp,
		Lawful:    pet.Lawful,
	})
}

// ========================================================================
//  Shared companion movement
// ========================================================================

type updatePosFunc func(id int32, x, y int32, heading int16)

// companionMoveToward moves a companion one step toward the target position.
// Uses the same pathfinding logic as npcMoveToward.
func (s *CompanionAISystem) companionMoveToward(objID int32, curX, curY int32, mapID int16, tx, ty int32, updatePos updatePosFunc) {
	dx := tx - curX
	dy := ty - curY
	if dx == 0 && dy == 0 {
		return
	}

	type candidate struct{ x, y int32 }
	candidates := make([]candidate, 0, 3)

	mx, my := curX, curY
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

	if dx != 0 && dy != 0 {
		candidates = append(candidates, candidate{mx, curY})
		candidates = append(candidates, candidate{curX, my})
	} else if dx != 0 {
		candidates = append(candidates, candidate{mx, curY + 1})
		candidates = append(candidates, candidate{mx, curY - 1})
	} else if dy != 0 {
		candidates = append(candidates, candidate{curX + 1, my})
		candidates = append(candidates, candidate{curX - 1, my})
	}

	maps := s.deps.MapData
	ws := s.world

	for _, c := range candidates {
		if c.x == curX && c.y == curY {
			continue
		}
		h := calcNpcHeading(curX, curY, c.x, c.y)
		if maps != nil && !maps.IsPassable(mapID, curX, curY, int(h)) {
			continue
		}
		// Allow companions to pass through other NPCs (only block on players)
		occupant := ws.OccupantAt(c.x, c.y, mapID)
		if occupant > 0 && occupant < 200_000_000 {
			continue // player blocking
		}

		oldX, oldY := curX, curY
		updatePos(objID, c.x, c.y, h)

		// Broadcast movement
		nearby := ws.GetNearbyPlayersAt(c.x, c.y, mapID)
		for _, viewer := range nearby {
			sendCompanionMovePacket(viewer.Session, objID, oldX, oldY, h)
		}
		return
	}
	// All blocked — try pass-through as last resort
	h := calcNpcHeading(curX, curY, mx, my)
	if maps == nil || maps.IsPassableIgnoreOccupant(mapID, curX, curY, int(h)) {
		oldX, oldY := curX, curY
		updatePos(objID, mx, my, h)
		nearby := ws.GetNearbyPlayersAt(mx, my, mapID)
		for _, viewer := range nearby {
			sendCompanionMovePacket(viewer.Session, objID, oldX, oldY, h)
		}
	}
}

// companionTeleportToMaster moves a companion directly to the master (cross-map or long distance).
func (s *CompanionAISystem) companionTeleportToMaster(objID int32, master *world.PlayerInfo, updatePos updatePosFunc) {
	newX := master.X + int32(world.RandInt(3)) - 1
	newY := master.Y + int32(world.RandInt(3)) - 1
	updatePos(objID, newX, newY, master.Heading)
}

// ========================================================================
//  Packet helpers (system-local to avoid circular imports)
// ========================================================================

func sendCompanionMovePacket(sess *gonet.Session, objID int32, prevX, prevY int32, heading int16) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_MOVE_OBJECT)
	w.WriteD(objID)
	w.WriteH(uint16(prevX))
	w.WriteH(uint16(prevY))
	w.WriteC(byte(heading))
	sess.Send(w.Bytes())
}

func sendCompanionRemove(sess *gonet.Session, objID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_REMOVE_OBJECT)
	w.WriteD(objID)
	sess.Send(w.Bytes())
}

func sendCompanionSoundEffect(sess *gonet.Session, objID int32, gfxID int32) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EFFECT)
	w.WriteD(objID)
	w.WriteH(uint16(gfxID))
	sess.Send(w.Bytes())
}

func sendCompanionHpMeter(sess *gonet.Session, objID int32, hp, maxHP int32) {
	ratio := byte(0xFF)
	if maxHP > 0 {
		r := hp * 100 / maxHP
		if r > 100 {
			r = 100
		}
		ratio = byte(r)
	}
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_HP_METER)
	w.WriteD(objID)
	w.WriteC(ratio)
	sess.Send(w.Bytes())
}

func sendDollTimerClear(sess *gonet.Session) {
	w := packet.NewWriterWithOpcode(packet.S_OPCODE_EVENT)
	w.WriteH(56)
	w.WriteD(0) // clear timer
	sess.Send(w.Bytes())
}
