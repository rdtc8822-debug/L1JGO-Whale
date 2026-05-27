package system

import (
	"testing"
	"time"

	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestGroundEffectFireWallNpcAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	owner := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 51,
		Session:   newSkillTestSession(t, 51),
		CharID:    5101,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
		Intel:     18,
	})
	npc := &world.NpcInfo{
		ID:    5102,
		NpcID: 45001,
		Name:  "barrier_npc",
		X:     102,
		Y:     100,
		MapID: 4,
		HP:    100,
		MaxHP: 100,
	}
	npc.AddDebuff(78, 100)
	ws.AddNpc(npc)
	ws.AddGroundEffect(&world.GroundEffect{
		ID:           world.NextGroundEffectID(),
		SkillID:      58,
		NpcID:        81157,
		GfxID:        168,
		Type:         world.GroundEffectFireWall,
		X:            101,
		Y:            100,
		MapID:        4,
		OwnerCharID:  owner.CharID,
		OwnerSession: owner.SessionID,
		OwnerName:    owner.Name,
		OwnerIntel:   owner.Intel,
	})
	sys := NewGroundEffectSystem(ws, &handler.Deps{World: ws, Log: zap.NewNop()})

	for i := 0; i < fireWallDamageIntervalTicks; i++ {
		sys.Update(200 * time.Millisecond)
	}

	if npc.HP != 100 {
		t.Fatalf("yiwei EffectFirewallTimer.dmg0/SKM0 會讓 ABSOLUTE_BARRIER NPC 火牢傷害為 0，HP=%d", npc.HP)
	}
}

func TestGroundEffectCubeBalanceNpcAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	owner := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 52,
		Session:   newSkillTestSession(t, 52),
		CharID:    5201,
		Name:      "caster",
		X:         100,
		Y:         100,
		MapID:     4,
	})
	npc := &world.NpcInfo{
		ID:    5202,
		NpcID: 45001,
		Name:  "barrier_npc",
		X:     101,
		Y:     100,
		MapID: 4,
		HP:    100,
		MaxHP: 100,
		MP:    10,
		MaxMP: 100,
	}
	npc.AddDebuff(78, 100)
	ws.AddNpc(npc)
	ws.AddGroundEffect(&world.GroundEffect{
		ID:           world.NextGroundEffectID(),
		SkillID:      220,
		NpcID:        80152,
		GfxID:        6724,
		Type:         world.GroundEffectCubeBalance,
		X:            100,
		Y:            100,
		MapID:        4,
		OwnerCharID:  owner.CharID,
		OwnerSession: owner.SessionID,
	})
	sys := NewGroundEffectSystem(ws, &handler.Deps{World: ws, Log: zap.NewNop()})

	for i := 0; i < cubeBalanceDamageIntervalTicks; i++ {
		sys.Update(200 * time.Millisecond)
	}

	if npc.HP != 100 {
		t.Fatalf("yiwei L1MonsterInstance.receiveDamage 會以 SKM0 擋下 ABSOLUTE_BARRIER NPC 立方傷害，HP=%d", npc.HP)
	}
	if npc.MP != 15 {
		t.Fatalf("ABSOLUTE_BARRIER 只阻擋傷害，不應阻擋和諧立方回 MP，MP=%d", npc.MP)
	}
}
