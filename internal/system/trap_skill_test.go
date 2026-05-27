package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
)

func TestTrapSkillUsesTrapSkillTimeLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 401,
		Session:   newSkillTestSession(t, 401),
		CharID:    4001,
		Name:      "trap-skill-time",
		X:         32770,
		Y:         32770,
		MapID:     4,
		Dex:       10,
	})
	skillSys := newSkillBuffTestSystem(t, ws)
	trapMgr, trap := newTrapSkillTestTrap(player.X, player.Y, player.MapID, 26, 7)
	skillSys.deps.Skill = skillSys
	skillSys.deps.TrapMgr = trapMgr
	sys := NewTrapSystem(skillSys.deps)

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	buff := player.ActiveBuffs[26]
	if buff == nil {
		t.Fatal("yiwei type 5 陷阱應以 TYPE_GMBUFF 套用指定技能 buff")
	}
	if buff.TicksLeft != 35 {
		t.Fatalf("yiwei L1Trap.onType5 會使用陷阱 skillTimeSeconds=7 秒，ticks=%d want=35", buff.TicksLeft)
	}
	if player.Dex != 15 {
		t.Fatalf("陷阱技能仍應走 Go buff 系統套用能力值，DEX=%d want=15", player.Dex)
	}
}

func newTrapSkillTestTrap(x, y int32, mapID int16, skillID, skillTime int32) (*world.TrapManager, *world.TrapInstance) {
	trapData := &data.TrapData{
		Templates: map[int32]*data.TrapTemplate{
			5: {
				TrapID:    5,
				Type:      5,
				GfxID:     2231,
				SkillID:   skillID,
				SkillTime: skillTime,
			},
		},
		Spawns: []data.TrapSpawn{
			{TrapID: 5, MapID: int32(mapID), X: x, Y: y, Count: 1},
		},
	}
	trapMgr := world.NewTrapManager(trapData, nil)
	return trapMgr, trapMgr.GetTrapsAt(x, y, mapID)[0]
}
