package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
	"go.uber.org/zap"
)

func TestSpawnFurnitureNpcUsesYiweiFacingTileAndSameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addNpcActionShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    9701,
		Name:      "owner",
		X:         100,
		Y:         100,
		MapID:     900,
		Heading:   0,
		ShowID:    77,
		SessionID: 1,
		Session:   newHandlerTestSession(t, 1),
	})
	sameShow := addNpcActionShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    9702,
		Name:      "same",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 2,
		Session:   newHandlerTestSession(t, 2),
	})
	otherShow := addNpcActionShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    9703,
		Name:      "other",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    88,
		SessionID: 3,
		Session:   newHandlerTestSession(t, 3),
	})
	deps := &Deps{
		World: ws,
		Npcs:  loadFurnitureShowIDTestNpcTable(t),
		Log:   zap.NewNop(),
	}

	spawnFurnitureNpc(player.Session, player, 7101, 80109, deps)

	npcObjID := ws.GetFurnitureNpc(7101)
	npc := ws.GetNpc(npcObjID)
	if npc == nil {
		t.Fatalf("家具應建立 NPC 並登記 item object 對應")
	}
	if npc.X != 100 || npc.Y != 99 {
		t.Fatalf("yiwei heading 0 家具應放在玩家北方一格，got (%d,%d)", npc.X, npc.Y)
	}
	if npc.Heading != 0 {
		t.Fatalf("yiwei 家具 heading 應固定 0，got %d", npc.Heading)
	}
	if npc.ShowID != player.ShowID {
		t.Fatalf("家具應繼承玩家 ShowID=%d，got %d", player.ShowID, npc.ShowID)
	}
	if !hasNpcActionShowIDPutObjectPacket(drainMovementShowIDPackets(sameShow.Session), npc.ID) {
		t.Fatalf("同 ShowID 玩家應收到家具 NPC 顯示封包")
	}
	if hasNpcActionShowIDPutObjectPacket(drainMovementShowIDPackets(otherShow.Session), npc.ID) {
		t.Fatalf("不同 ShowID 玩家不應收到家具 NPC 顯示封包")
	}
}

func TestRemoveFurnitureNpcBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	sameShow := addNpcActionShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    9712,
		Name:      "same",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 12,
		Session:   newHandlerTestSession(t, 12),
	})
	otherShow := addNpcActionShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    9713,
		Name:      "other",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    88,
		SessionID: 13,
		Session:   newHandlerTestSession(t, 13),
	})
	npc := &world.NpcInfo{
		ID:                 9811,
		NpcID:              80109,
		Impl:               "L1FurnitureInstance",
		X:                  100,
		Y:                  100,
		MapID:              900,
		ShowID:             77,
		FurnitureItemObjID: 7201,
	}
	ws.AddNpc(npc)
	ws.AddFurnitureNpc(7201, npc.ID)
	deps := &Deps{World: ws, Log: zap.NewNop()}

	removeFurnitureNpc(npc.ID, 7201, deps)

	if !hasNpcActionShowIDRemoveObjectPacket(drainMovementShowIDPackets(sameShow.Session), npc.ID) {
		t.Fatalf("同 ShowID 玩家應收到家具移除封包")
	}
	if hasNpcActionShowIDRemoveObjectPacket(drainMovementShowIDPackets(otherShow.Session), npc.ID) {
		t.Fatalf("yiwei furniture deleteMe/getRecognizePlayer 受 ShowID 約束，不同 ShowID 玩家不應收到家具移除封包")
	}
	if got := ws.GetNpc(npc.ID); got != nil {
		t.Fatalf("家具移除後 world 內不應保留 NPC")
	}
	if got := ws.GetFurnitureNpc(7201); got != 0 {
		t.Fatalf("家具移除後 item 對應應清空，got %d", got)
	}
}

func loadFurnitureShowIDTestNpcTable(t *testing.T) *data.NpcTable {
	t.Helper()
	path := filepath.Join(t.TempDir(), "npc_list.yaml")
	raw := []byte(`npcs:
  - npc_id: 80109
    name: furniture
    nameid: "$80109"
    impl: L1FurnitureInstance
    gfx_id: 1000
    level: 1
    hp: 10
    mp: 0
    ac: 10
    str: 1
    dex: 1
    con: 1
    wis: 1
    intel: 1
    mr: 0
    exp: 0
    lawful: 0
    size: small
    ranged: 1
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("寫入測試 NPC YAML 失敗: %v", err)
	}
	npcs, err := data.LoadNpcTable(path)
	if err != nil {
		t.Fatalf("載入測試 NPC YAML 失敗: %v", err)
	}
	return npcs
}
