package system

import (
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestTrapPoisonDamageUsesPoisonTimerLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 301,
		Session:   newSkillTestSession(t, 301),
		CharID:    3001,
		Name:      "trap-damage-poison",
		X:         32767,
		Y:         32767,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
	})
	trapMgr, trap := newTrapPoisonTestTrap(player.X, player.Y, player.MapID, 1, 0, 5000, 10)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		TrapMgr: trapMgr,
		Log:     zap.NewNop(),
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.HP != 100 {
		t.Fatalf("yiwei L1DamagePoison 會先套毒，等待 damageSpan 後才扣血，HP=%d want=100", player.HP)
	}
	if player.PoisonType != 1 || player.PoisonDmgAmount != 10 {
		t.Fatalf("type 4 一般毒應套用傷害毒狀態，type=%d dmg=%d", player.PoisonType, player.PoisonDmgAmount)
	}
	if player.PoisonDmgIntervalTicks != 25 {
		t.Fatalf("poisonTime=5000ms 應成為 25 ticks 傷害間隔，got=%d", player.PoisonDmgIntervalTicks)
	}
	for i := 0; i < 24; i++ {
		TickPlayerPoison(player, sys.deps)
	}
	if player.HP != 100 {
		t.Fatalf("未滿 damageSpan 前不應扣血，HP=%d want=100", player.HP)
	}
	TickPlayerPoison(player, sys.deps)
	if player.HP != 90 {
		t.Fatalf("滿 poisonTime 後應扣 poisonDamage，HP=%d want=90", player.HP)
	}
	if !hasPoisonPacket(drainSkillTestPackets(player.Session), player.CharID, 1) {
		t.Fatal("一般毒應送綠色色調 S_Poison")
	}
}

func TestTrapPoisonSilenceUsesPoisonSystemLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 302,
		Session:   newSkillTestSession(t, 302),
		CharID:    3002,
		Name:      "trap-silence-poison",
		X:         32768,
		Y:         32768,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
	})
	trapMgr, trap := newTrapPoisonTestTrap(player.X, player.Y, player.MapID, 2, 0, 0, 0)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		TrapMgr: trapMgr,
		Log:     zap.NewNop(),
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.PoisonType != 2 || !player.Silenced {
		t.Fatalf("yiwei L1SilencePoison 應套沉默毒並禁止施法，type=%d silenced=%v", player.PoisonType, player.Silenced)
	}
	packets := drainSkillTestPackets(player.Session)
	if !hasPoisonPacket(packets, player.CharID, 1) {
		t.Fatal("沉默毒應送綠色色調 S_Poison")
	}
	if !hasServerMessage(packets, 310) {
		t.Fatal("沉默毒應送 Java 訊息 310")
	}
}

func TestTrapPoisonParalysisUsesTrapDelayAndTimeLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 303,
		Session:   newSkillTestSession(t, 303),
		CharID:    3003,
		Name:      "trap-paralysis-poison",
		X:         32769,
		Y:         32769,
		MapID:     4,
		HP:        100,
		MaxHP:     100,
	})
	trapMgr, trap := newTrapPoisonTestTrap(player.X, player.Y, player.MapID, 3, 5000, 5000, 0)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		TrapMgr: trapMgr,
		Log:     zap.NewNop(),
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	if player.PoisonType != 3 || player.PoisonTicksLeft != 25 || player.Paralyzed {
		t.Fatalf("麻痺毒初期應為 25 ticks 延遲且尚未麻痺，type=%d ticks=%d paralyzed=%v", player.PoisonType, player.PoisonTicksLeft, player.Paralyzed)
	}
	for i := 0; i < 24; i++ {
		TickPlayerPoison(player, sys.deps)
	}
	if player.Paralyzed || player.PoisonType != 3 {
		t.Fatalf("未滿 poisonDelay 前不應進入麻痺階段，type=%d paralyzed=%v", player.PoisonType, player.Paralyzed)
	}
	TickPlayerPoison(player, sys.deps)
	if player.PoisonType != 4 || !player.Paralyzed || player.PoisonTicksLeft != 25 {
		t.Fatalf("滿 poisonDelay 後應進入 25 ticks 麻痺階段，type=%d ticks=%d paralyzed=%v", player.PoisonType, player.PoisonTicksLeft, player.Paralyzed)
	}
	packets := drainSkillTestPackets(player.Session)
	if !hasServerMessage(packets, 212) {
		t.Fatal("麻痺毒應送 Java 訊息 212")
	}
	if !hasParalysisSubtype(packets, handler.ParalysisApply) {
		t.Fatal("麻痺毒延遲到期應送 S_Paralysis(TYPE_PARALYSIS,true)")
	}
}

func newTrapPoisonTestTrap(x, y int32, mapID int16, poisonType, delay, poisonTime, damage int32) (*world.TrapManager, *world.TrapInstance) {
	trapData := &data.TrapData{
		Templates: map[int32]*data.TrapTemplate{
			4: {
				TrapID:       4,
				Type:         4,
				GfxID:        2231,
				PoisonType:   poisonType,
				PoisonDelay:  delay,
				PoisonTime:   poisonTime,
				PoisonDamage: damage,
			},
		},
		Spawns: []data.TrapSpawn{
			{TrapID: 4, MapID: int32(mapID), X: x, Y: y, Count: 1},
		},
	}
	trapMgr := world.NewTrapManager(trapData, nil)
	return trapMgr, trapMgr.GetTrapsAt(x, y, mapID)[0]
}
