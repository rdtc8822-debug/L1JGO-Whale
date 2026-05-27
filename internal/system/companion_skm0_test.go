package system

import (
	"testing"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

func TestCompanionSummonAttackNpcIceLanceBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	master := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1001,
		Name:      "summon_master",
		X:         100,
		Y:         100,
		MapID:     900,
		ShowID:    77,
	})
	npc := &world.NpcInfo{
		ID:     2001,
		NpcID:  45001,
		Impl:   "L1Monster",
		Name:   "summon_ice_lance_target",
		X:      101,
		Y:      100,
		MapID:  900,
		ShowID: master.ShowID,
		HP:     1000,
		MaxHP:  1000,
	}
	npc.AddDebuff(50, 120)
	ws.AddNpc(npc)
	sum := &world.SummonInfo{
		ID:          3001,
		OwnerCharID: master.CharID,
		Name:        "summon",
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      master.ShowID,
		HP:          500,
		MaxHP:       500,
		Level:       50,
		AtkDmg:      80,
		Ranged:      1,
		AggroTarget: npc.ID,
	}
	ws.AddSummon(sum)
	ai := NewCompanionAISystem(ws, &handler.Deps{})

	ai.summonAttackTarget(sum)

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei L1AttackNpc.calcNpcDamage 會以 SKM0 擋下召喚物對 ICE_LANCE NPC 傷害，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
}

func TestCompanionPetAttackNpcIceLanceBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	master := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1002,
		Name:      "pet_master",
		X:         100,
		Y:         100,
		MapID:     900,
		ShowID:    77,
	})
	npc := &world.NpcInfo{
		ID:     2002,
		NpcID:  45001,
		Impl:   "L1Monster",
		Name:   "pet_ice_lance_target",
		X:      101,
		Y:      100,
		MapID:  900,
		ShowID: master.ShowID,
		HP:     1000,
		MaxHP:  1000,
		Level:  1,
	}
	npc.AddDebuff(50, 120)
	ws.AddNpc(npc)
	pet := &world.PetInfo{
		ID:          3002,
		OwnerCharID: master.CharID,
		Name:        "pet",
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      master.ShowID,
		HP:          500,
		MaxHP:       500,
		Level:       50,
		AtkDmg:      80,
		Ranged:      1,
		AggroTarget: npc.ID,
	}
	ws.AddPet(pet)
	ai := NewCompanionAISystem(ws, &handler.Deps{})

	ai.petAttackTarget(pet)

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei L1AttackNpc.calcNpcDamage 會以 SKM0 擋下寵物對 ICE_LANCE NPC 傷害，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
}
