package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestTrapSystemGmInvisibleDoesNotTriggerTrapLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID:   1,
		Session:     newSkillTestSession(t, 1),
		CharID:      1001,
		Name:        "gm-invis",
		X:           32767,
		Y:           32767,
		MapID:       4,
		HP:          100,
		MaxHP:       100,
		AccessLevel: 200,
		Invisible:   true,
	})
	trapMgr, trap := newTrapSystemTestDamageTrap(player.X, player.Y, player.MapID)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		Log:     zap.NewNop(),
		TrapMgr: trapMgr,
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.HP != 100 {
		t.Fatalf("yiwei C_MoveChar 以 isGmInvis 排除 WorldTrap.onPlayerMoved，HP=%d want=100", player.HP)
	}
	if !trap.Alive {
		t.Fatal("GM 隱身不應觸發陷阱，也不應停用陷阱")
	}
	if packets := drainSkillTestPackets(player.Session); len(packets) != 0 {
		t.Fatalf("GM 隱身不應收到陷阱效果或 HP 更新封包，got %d packets", len(packets))
	}
}

func TestTrapSystemNormalInvisibleStillTriggersTrapLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
		CharID:    1002,
		Name:      "normal-invis",
		X:         32768,
		Y:         32768,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
		Invisible: true,
	})
	trapMgr, trap := newTrapSystemTestDamageTrap(player.X, player.Y, player.MapID)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		Log:     zap.NewNop(),
		TrapMgr: trapMgr,
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.HP != 90 {
		t.Fatalf("一般隱身不是 Java isGmInvis，仍應觸發陷阱，HP=%d want=90", player.HP)
	}
	if trap.Alive {
		t.Fatal("一般隱身玩家踩到陷阱後應停用陷阱")
	}
}

func newTrapSystemTestDamageTrap(x, y int32, mapID int16) (*world.TrapManager, *world.TrapInstance) {
	trapData := &data.TrapData{
		Templates: map[int32]*data.TrapTemplate{
			1: {
				TrapID: 1,
				Type:   1,
				GfxID:  2231,
				Base:   10,
			},
		},
		Spawns: []data.TrapSpawn{
			{TrapID: 1, MapID: int32(mapID), X: x, Y: y, Count: 1},
		},
	}
	trapMgr := world.NewTrapManager(trapData, nil)
	return trapMgr, trapMgr.GetTrapsAt(x, y, mapID)[0]
}
