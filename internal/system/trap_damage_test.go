package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"

	"github.com/l1jgo/server/internal/handler"
)

func TestTrapDamagePlayerAbsoluteBarrierBlocksDamageLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:       501,
		Session:         newSkillTestSession(t, 501),
		CharID:          5001,
		Name:            "trap-absolute-barrier",
		X:               32771,
		Y:               32771,
		MapID:           4,
		HP:              100,
		MaxHP:           100,
		AbsoluteBarrier: true,
	})
	player.AddBuff(&world.ActiveBuff{SkillID: 78, TicksLeft: 25, SetAbsoluteBarrier: true})
	trapMgr, trap := newTrapSystemTestDamageTrap(player.X, player.Y, player.MapID)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		Log:     zap.NewNop(),
		TrapMgr: trapMgr,
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.HP != 100 {
		t.Fatalf("yiwei receiveDamage 會以 SKM0 擋下 ABSOLUTE_BARRIER 目標陷阱傷害，HP=%d want=100", player.HP)
	}
	if !player.AbsoluteBarrier || !player.HasBuff(78) {
		t.Fatal("陷阱傷害被擋下時不應解除目標絕對屏障")
	}
	if trap.Alive {
		t.Fatal("陷阱觸發後仍應停用，傷害是否被擋下不影響陷阱消耗")
	}
}

func TestTrapDamageBaseZeroDoesNotSendEffectLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 502,
		Session:   newSkillTestSession(t, 502),
		CharID:    5002,
		Name:      "trap-base-zero",
		X:         32772,
		Y:         32772,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
	})
	trapMgr, trap := newTrapDamageTestTrap(player.X, player.Y, player.MapID, 0)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		Log:     zap.NewNop(),
		TrapMgr: trapMgr,
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.HP != 100 {
		t.Fatalf("base<=0 的傷害陷阱不應造成傷害，HP=%d want=100", player.HP)
	}
	if hasEffectLocationPacket(drainSkillTestPackets(player.Session), trap.X, trap.Y, trap.Template.GfxID) {
		t.Fatal("yiwei L1Trap.onType1 在 _base<=0 時會 return，不應送 S_EffectLocation")
	}
	if trap.Alive {
		t.Fatal("無效傷害陷阱被踩後仍應依 WorldTrap 流程停用")
	}
}

func newTrapDamageTestTrap(x, y int32, mapID int16, base int32) (*world.TrapManager, *world.TrapInstance) {
	trapData := &data.TrapData{
		Templates: map[int32]*data.TrapTemplate{
			1: {
				TrapID: 1,
				Type:   1,
				GfxID:  2231,
				Base:   base,
			},
		},
		Spawns: []data.TrapSpawn{
			{TrapID: 1, MapID: int32(mapID), X: x, Y: y, Count: 1},
		},
	}
	trapMgr := world.NewTrapManager(trapData, nil)
	return trapMgr, trapMgr.GetTrapsAt(x, y, mapID)[0]
}
