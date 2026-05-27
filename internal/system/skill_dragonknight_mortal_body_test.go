package system

import (
	"math/rand"
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestMortalBodyNpcAbsoluteBarrierBlocksReflectDamageLikeJava(t *testing.T) {
	rand.Seed(1)
	target := &world.PlayerInfo{
		CharID: 1003,
		Name:   "mortal_body_target",
		HP:     100,
		MaxHP:  100,
	}
	target.AddBuff(&world.ActiveBuff{SkillID: skillMortalBody, TicksLeft: 100})
	npc := &world.NpcInfo{
		ID:    2003,
		NpcID: 45001,
		Name:  "barrier_attacker",
		HP:    1000,
		MaxHP: 1000,
	}
	npc.AddDebuff(78, 100)

	reflected := false
	for i := 0; i < 200; i++ {
		npc.HP = npc.MaxHP
		_, reflected = mortalBodyReflectFromNpc(target, npc, 10, nil)
		if reflected {
			break
		}
	}

	if !reflected {
		t.Fatal("測試資料應讓致命身軀反彈至少觸發一次")
	}
	if npc.HP != npc.MaxHP {
		t.Fatalf("yiwei MORTAL_BODY 對 NPC 反彈會走 attackNpc.receiveDamage，ABSOLUTE_BARRIER NPC 不應扣血，HP=%d want=%d", npc.HP, npc.MaxHP)
	}
}
