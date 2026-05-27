package system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestTrapMonsterTrapSpawnsAndLinksTargetLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 101,
		Session:   newSkillTestSession(t, 101),
		CharID:    1001,
		Name:      "trap-trigger",
		X:         32767,
		Y:         32767,
		MapID:     4,
		ShowID:    12,
		HP:        100,
		MaxHP:     100,
	})
	trapMgr, trap := newTrapMonsterTestTrap(player.X, player.Y, player.MapID, 45244, 2)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		Npcs:    loadTrapMonsterTestNpcTable(t),
		TrapMgr: trapMgr,
		Log:     zap.NewNop(),
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	summoned := trapMonsterTestSummonedNpcs(ws, player, 45244)
	if len(summoned) != 2 {
		t.Fatalf("yiwei L1Trap.onType3 會依 count 召喚怪物，got=%d want=2", len(summoned))
	}
	for _, npc := range summoned {
		if chebyshev32(player.X, player.Y, npc.X, npc.Y) > 5 {
			t.Fatalf("yiwei randomLocation(5,false) 應在觸發玩家周圍 5 格內召怪，npc=(%d,%d) player=(%d,%d)", npc.X, npc.Y, player.X, player.Y)
		}
		if npc.AggroTarget != player.SessionID {
			t.Fatalf("yiwei newNpc.setLink(trodFrom) 應讓召喚怪鎖定觸發玩家，AggroTarget=%d want=%d", npc.AggroTarget, player.SessionID)
		}
		if npc.HateList[player.SessionID] != 0 {
			t.Fatalf("yiwei setLink 是 0 hate 鎖定，不應累加傷害仇恨，hate=%v", npc.HateList)
		}
	}
	if trap.Alive {
		t.Fatal("type 3 陷阱觸發後應停用並進入重生流程")
	}
}

func TestTrapMonsterTrapInheritsShowAndBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 201,
		Session:   newSkillTestSession(t, 201),
		CharID:    2001,
		Name:      "trap-trigger",
		X:         32800,
		Y:         32800,
		MapID:     4,
		ShowID:    77,
		HP:        100,
		MaxHP:     100,
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 202,
		Session:   newSkillTestSession(t, 202),
		CharID:    2002,
		Name:      "same-show",
		X:         player.X + 1,
		Y:         player.Y,
		MapID:     player.MapID,
		ShowID:    player.ShowID,
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: 203,
		Session:   newSkillTestSession(t, 203),
		CharID:    2003,
		Name:      "other-show",
		X:         player.X + 2,
		Y:         player.Y,
		MapID:     player.MapID,
		ShowID:    88,
	})
	trapMgr, trap := newTrapMonsterTestTrap(player.X, player.Y, player.MapID, 45244, 1)
	sys := NewTrapSystem(&handler.Deps{
		World:   ws,
		Npcs:    loadTrapMonsterTestNpcTable(t),
		TrapMgr: trapMgr,
		Log:     zap.NewNop(),
	})

	sys.TriggerTraps(player.Session, player, []*world.TrapInstance{trap})

	summoned := trapMonsterTestSummonedNpcs(ws, player, 45244)
	if len(summoned) != 1 {
		t.Fatalf("應召喚 1 隻測試怪物，got=%d", len(summoned))
	}
	npc := summoned[0]
	if npc.ShowID != player.ShowID {
		t.Fatalf("Go 副本隔離下 type 3 陷阱召喚怪應繼承觸發玩家 ShowID，got=%d want=%d", npc.ShowID, player.ShowID)
	}
	if !hasPutObjectPacket(drainSkillTestPackets(sameShow.Session), npc.ID) {
		t.Fatal("同 ShowID 玩家應收到 type 3 陷阱召喚怪顯示封包")
	}
	if hasPutObjectPacket(drainSkillTestPackets(otherShow.Session), npc.ID) {
		t.Fatal("不同 ShowID 玩家不應收到 type 3 陷阱召喚怪顯示封包")
	}
}

func newTrapMonsterTestTrap(x, y int32, mapID int16, npcID, count int32) (*world.TrapManager, *world.TrapInstance) {
	trapData := &data.TrapData{
		Templates: map[int32]*data.TrapTemplate{
			3: {
				TrapID:       3,
				Type:         3,
				GfxID:        2231,
				MonsterNpcID: npcID,
				MonsterCount: count,
			},
		},
		Spawns: []data.TrapSpawn{
			{TrapID: 3, MapID: int32(mapID), X: x, Y: y, Count: 1},
		},
	}
	trapMgr := world.NewTrapManager(trapData, nil)
	return trapMgr, trapMgr.GetTrapsAt(x, y, mapID)[0]
}

func loadTrapMonsterTestNpcTable(t *testing.T) *data.NpcTable {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "npc_list.yaml")
	if err := os.WriteFile(path, []byte(`npcs:
  - npc_id: 45244
    name: summoned-trap-monster
    nameid: summoned-trap-monster
    impl: L1Monster
    gfx_id: 1
    level: 5
    hp: 30
    mp: 0
    ac: 10
    str: 12
    dex: 10
    intel: 8
    mr: 0
    exp: 20
    lawful: 0
    size: small
    ranged: 1
    atk_speed: 1000
    passive_speed: 1000
`), 0o644); err != nil {
		t.Fatalf("寫入測試 NPC YAML 失敗: %v", err)
	}
	table, err := data.LoadNpcTable(path)
	if err != nil {
		t.Fatalf("載入測試 NPC YAML 失敗: %v", err)
	}
	return table
}

func trapMonsterTestSummonedNpcs(ws *world.State, player *world.PlayerInfo, npcID int32) []*world.NpcInfo {
	var out []*world.NpcInfo
	for _, npc := range ws.GetNearbyNpcs(player.X, player.Y, player.MapID) {
		if npc.NpcID == npcID {
			out = append(out, npc)
		}
	}
	return out
}
