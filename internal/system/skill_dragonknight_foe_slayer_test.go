package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
)

const testSkillCopyShockStun = int32(508)

func TestSkillDragonKnightFoeSlayerFoeSlayerHitsPlayerThreeTimesAndCanCopyShockStun(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		Level:     70,
		Str:       35,
		DmgMod:    100,
		HP:        100,
		MaxHP:     100,
	})
	target := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "target",
		X:         101,
		Y:         100,
		MapID:     4,
		Level:     1,
		HP:        1000,
		MaxHP:     1000,
	})
	s := newSkillTestSystem(t, ws)
	skill := &data.SkillInfo{
		SkillID:          187,
		Target:           "attack",
		Type:             64,
		Ranged:           2,
		DamageDice:       1,
		ProbabilityValue: 100,
		ActionID:         18,
	}

	s.executeAttackSkillOnPlayer(caster.Session, caster, skill, target)

	if target.HP >= 1000 {
		t.Fatalf("屠宰者應造成三段近戰型傷害，HP=%d", target.HP)
	}
	if !target.Paralyzed || !target.HasBuff(testSkillCopyShockStun) {
		t.Fatalf("屠宰者應可套用 COPY_SHOCK_STUN，Paralyzed=%v buff=%v", target.Paralyzed, target.GetBuff(testSkillCopyShockStun))
	}
}

func TestSkillDragonKnightFoeSlayerFoeSlayerHitsNpcThreeTimesAndCanCopyShockStun(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		Level:     70,
		Str:       35,
		DmgMod:    100,
		HP:        100,
		MaxHP:     100,
	})
	npc := &world.NpcInfo{
		ID:    2001,
		NpcID: 45001,
		Name:  "target",
		X:     101,
		Y:     100,
		MapID: 4,
		Level: 1,
		HP:    1000,
		MaxHP: 1000,
		Impl:  "L1Monster",
	}
	ws.AddNpc(npc)
	s := newSkillTestSystem(t, ws)
	skill := &data.SkillInfo{
		SkillID:          187,
		Target:           "attack",
		Type:             64,
		Ranged:           2,
		DamageDice:       1,
		ProbabilityValue: 100,
		ActionID:         18,
	}

	s.executeAttackSkill(caster.Session, caster, skill, npc.ID)

	if npc.HP >= 1000 {
		t.Fatalf("屠宰者應對 NPC 造成三段近戰型傷害，HP=%d", npc.HP)
	}
	if !npc.Paralyzed || !npc.HasDebuff(testSkillCopyShockStun) {
		t.Fatalf("屠宰者應可對 NPC 套用 COPY_SHOCK_STUN，Paralyzed=%v debuff=%v", npc.Paralyzed, npc.HasDebuff(testSkillCopyShockStun))
	}
}

func TestSkillDragonKnightFoeSlayerNpcAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	caster := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1002,
		Name:      "caster_barrier",
		X:         100,
		Y:         100,
		MapID:     4,
		Level:     70,
		Str:       35,
		DmgMod:    100,
		HP:        100,
		MaxHP:     100,
	})
	npc := &world.NpcInfo{
		ID:    2002,
		NpcID: 45001,
		Name:  "barrier_target",
		X:     101,
		Y:     100,
		MapID: 4,
		Level: 1,
		HP:    1000,
		MaxHP: 1000,
		Impl:  "L1Monster",
	}
	npc.AddDebuff(78, 100)
	ws.AddNpc(npc)
	s := newSkillTestSystem(t, ws)
	skill := &data.SkillInfo{
		SkillID:    187,
		Target:     "attack",
		Type:       64,
		Ranged:     2,
		DamageDice: 1,
		ActionID:   18,
	}

	s.executeAttackSkill(caster.Session, caster, skill, npc.ID)

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei FOE_SLAYER 三段 cha.onAction 會進入 L1AttackPc.dmg0，ABSOLUTE_BARRIER NPC 不應扣血，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
}
