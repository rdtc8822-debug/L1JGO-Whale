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

func TestDragonDoorSpawnKeeperUsesYiweiSpawnAndShowIDLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addDragonDoorTestPlayer(t, ws, 1, 1001, "player", 100, 77)
	sameShow := addDragonDoorTestPlayer(t, ws, 2, 1002, "same_show", 101, 77)
	otherShow := addDragonDoorTestPlayer(t, ws, 3, 1003, "other_show", 101, 88)
	sys := NewDragonDoorSystem(ws, &handler.Deps{
		World: ws,
		Npcs:  loadDragonDoorShowIDTestNpcTable(t),
		Log:   zap.NewNop(),
	})

	sys.SpawnKeeper(player.Session, player, 70932)

	npcs := ws.NpcList()
	if len(npcs) != 1 {
		t.Fatalf("應生成 1 隻龍門 NPC，got %d", len(npcs))
	}
	npc := npcs[0]
	if npc.X != player.X || npc.Y != player.Y {
		t.Fatalf("yiwei r=0 生成應使用玩家座標，got (%d,%d), want (%d,%d)", npc.X, npc.Y, player.X, player.Y)
	}
	if npc.Heading != 5 {
		t.Fatalf("yiwei L1SpawnUtil.spawnR 生成 heading 應固定 5，got %d", npc.Heading)
	}
	if npc.ShowID != player.ShowID {
		t.Fatalf("龍門 NPC 應繼承玩家 ShowID，got %d want %d", npc.ShowID, player.ShowID)
	}
	if !hasPutObjectPacket(drainSkillTestPackets(sameShow.Session), npc.ID) {
		t.Fatal("同 ShowID 玩家應收到龍門 NPC 生成封包")
	}
	if hasPutObjectPacket(drainSkillTestPackets(otherShow.Session), npc.ID) {
		t.Fatal("不同 ShowID 玩家不應收到龍門 NPC 生成封包")
	}
}

func TestDragonDoorRemoveKeeperBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	sameShow := addDragonDoorTestPlayer(t, ws, 1, 1001, "same_show", 100, 77)
	otherShow := addDragonDoorTestPlayer(t, ws, 2, 1002, "other_show", 100, 88)
	npc := &world.NpcInfo{
		ID:     2201,
		NpcID:  70932,
		Name:   "dragon_door_keeper",
		X:      100,
		Y:      100,
		MapID:  900,
		ShowID: 77,
		HP:     100,
		MaxHP:  100,
	}
	ws.AddNpc(npc)
	sys := NewDragonDoorSystem(ws, &handler.Deps{World: ws, Log: zap.NewNop()})

	sys.removeKeeper(npc)

	if !hasRemoveObjectPacket(drainSkillTestPackets(sameShow.Session), npc.ID) {
		t.Fatal("同 ShowID 玩家應收到龍門 NPC 移除封包")
	}
	if hasRemoveObjectPacket(drainSkillTestPackets(otherShow.Session), npc.ID) {
		t.Fatal("不同 ShowID 玩家不應收到龍門 NPC 移除封包")
	}
	if ws.GetNpc(npc.ID) != nil {
		t.Fatal("龍門 NPC 應從 World 移除")
	}
}

func TestDragonDoorKeeperArrivedBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	sameShow := addDragonDoorTestPlayer(t, ws, 1, 1001, "same_show", 100, 77)
	otherShow := addDragonDoorTestPlayer(t, ws, 2, 1002, "other_show", 100, 88)
	npc := &world.NpcInfo{
		ID:     2202,
		NpcID:  70932,
		Name:   "dragon_door_keeper",
		X:      100,
		Y:      100,
		MapID:  900,
		ShowID: 77,
		HP:     100,
		MaxHP:  100,
	}
	ws.AddNpc(npc)
	sys := NewDragonDoorSystem(ws, &handler.Deps{World: ws, Log: zap.NewNop()})
	keeper := &keeperEntry{npcObjID: npc.ID, npcID: npc.NpcID, profile: keeperProfile{heading: 2}}

	sys.keeperArrived(keeper, npc)

	samePackets := drainSkillTestPackets(sameShow.Session)
	if !hasActionGfxPacket(samePackets, npc.ID, 8) {
		t.Fatal("同 ShowID 玩家應收到龍門 NPC 抵達死亡動作封包")
	}
	if !hasRemoveObjectPacket(samePackets, npc.ID) {
		t.Fatal("同 ShowID 玩家應收到龍門 NPC 抵達後移除封包")
	}
	otherPackets := drainSkillTestPackets(otherShow.Session)
	if hasActionGfxPacket(otherPackets, npc.ID, 8) {
		t.Fatal("不同 ShowID 玩家不應收到龍門 NPC 抵達死亡動作封包")
	}
	if hasRemoveObjectPacket(otherPackets, npc.ID) {
		t.Fatal("不同 ShowID 玩家不應收到龍門 NPC 抵達後移除封包")
	}
}

func addDragonDoorTestPlayer(t *testing.T, ws *world.State, sessionID uint64, charID int32, name string, x int32, showID int32) *world.PlayerInfo {
	t.Helper()
	return addSkillTestPlayer(ws, &world.PlayerInfo{
		SessionID: sessionID,
		Session:   newSkillTestSession(t, sessionID),
		CharID:    charID,
		Name:      name,
		X:         x,
		Y:         100,
		MapID:     900,
		ShowID:    showID,
		HP:        100,
		MaxHP:     100,
	})
}

func loadDragonDoorShowIDTestNpcTable(t *testing.T) *data.NpcTable {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "npc_list.yaml")
	raw := []byte(`npcs:
  - npc_id: 70932
    name: dragon_door_keeper
    nameid: dragon_door_keeper
    impl: L1Monster
    gfx_id: 145
    level: 10
    hp: 50
    mp: 10
    str: 10
    dex: 10
    con: 10
    wis: 10
    intel: 10
    mr: 0
    exp: 1
    lawful: 0
    size: small
    ranged: 1
    atk_speed: 1000
    sub_magic_speed: 1000
    passive_speed: 1000
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("寫入龍門 NPC 測試資料失敗: %v", err)
	}
	npcs, err := data.LoadNpcTable(path)
	if err != nil {
		t.Fatalf("讀取龍門 NPC 測試資料失敗: %v", err)
	}
	return npcs
}
