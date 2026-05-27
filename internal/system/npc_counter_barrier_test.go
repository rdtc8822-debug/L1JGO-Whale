package system

import (
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestNpcMeleeCounterBarrierNpcAbsoluteBarrierBlocksReflectDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	target := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
		CharID:    1301,
		Name:      "counter_barrier_target",
		X:         101,
		Y:         100,
		MapID:     900,
		Level:     100,
		HP:        500,
		MaxHP:     500,
	})
	target.AddBuff(&world.ActiveBuff{SkillID: 91, TicksLeft: 100})
	npc := &world.NpcInfo{
		ID:     2301,
		NpcID:  45161,
		Impl:   "L1Monster",
		Name:   "barrier_attacker",
		X:      100,
		Y:      100,
		MapID:  900,
		HP:     1000,
		MaxHP:  1000,
		Level:  1,
		STR:    30,
		DEX:    30,
		AtkDmg: 20,
	}
	npc.AddDebuff(78, 100)
	ws.AddNpc(npc)
	s := newNpcAILOSTestSystem(t, ws)

	s.npcMeleeAttack(npc, target)

	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei NPC→PC COUNTER_BARRIER 反傷會走 _npc.receiveDamage，ABSOLUTE_BARRIER NPC 不應扣血，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
	if target.HP != target.MaxHP {
		t.Fatalf("COUNTER_BARRIER 成功時原始 NPC 近戰傷害仍應歸零，HP=%d want=%d", target.HP, target.MaxHP)
	}
}
