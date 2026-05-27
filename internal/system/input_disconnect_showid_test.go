package system

import (
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestInputCleanupCompanionsBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	owner := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9101,
		Name:      "owner",
		X:         100,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 1,
		Session:   newSkillTestSession(t, 1),
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9102,
		Name:      "same",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 2,
		Session:   newSkillTestSession(t, 2),
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9103,
		Name:      "other",
		X:         102,
		Y:         100,
		MapID:     900,
		ShowID:    88,
		SessionID: 3,
		Session:   newSkillTestSession(t, 3),
	})

	ids := []int32{9201, 9202, 9203, 9204, 9205}
	ws.AddSummon(&world.SummonInfo{
		ID:          ids[0],
		OwnerCharID: owner.CharID,
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      owner.ShowID,
	})
	ws.AddDoll(&world.DollInfo{
		ID:          ids[1],
		OwnerCharID: owner.CharID,
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      owner.ShowID,
	})
	ws.AddHierarch(&world.HierarchInfo{
		ID:          ids[2],
		OwnerCharID: owner.CharID,
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      owner.ShowID,
	})
	ws.AddPet(&world.PetInfo{
		ID:          ids[3],
		OwnerCharID: owner.CharID,
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      owner.ShowID,
	})
	ws.AddFollower(&world.FollowerInfo{
		ID:          ids[4],
		OwnerCharID: owner.CharID,
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      owner.ShowID,
	})

	sys := &InputSystem{worldState: ws}
	sys.cleanupCompanions(owner)

	samePackets := drainSkillTestPackets(sameShow.Session)
	otherPackets := drainSkillTestPackets(otherShow.Session)
	for _, id := range ids {
		if !hasRemoveObjectPacket(samePackets, id) {
			t.Fatalf("同 ShowID 玩家應收到斷線 companion %d 移除封包", id)
		}
		if hasRemoveObjectPacket(otherPackets, id) {
			t.Fatalf("yiwei companion deleteMe/broadcastPacketAll 受 ShowID 約束，不同 ShowID 玩家不應收到斷線 companion %d 移除封包", id)
		}
	}
}

func TestInputCleanupFollowerRespawnsOriginalNpcOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	owner := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9301,
		Name:      "owner",
		X:         100,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 11,
		Session:   newSkillTestSession(t, 11),
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9302,
		Name:      "same",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 12,
		Session:   newSkillTestSession(t, 12),
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9303,
		Name:      "other",
		X:         102,
		Y:         100,
		MapID:     900,
		ShowID:    88,
		SessionID: 13,
		Session:   newSkillTestSession(t, 13),
	})

	followerID := int32(9401)
	ws.AddFollower(&world.FollowerInfo{
		ID:          followerID,
		OwnerCharID: owner.CharID,
		OrigNpcID:   45001,
		GfxID:       3906,
		Name:        "follower",
		NameID:      "$45001",
		Level:       10,
		MaxHP:       100,
		X:           100,
		Y:           100,
		MapID:       900,
		ShowID:      owner.ShowID,
		SpawnX:      100,
		SpawnY:      100,
		SpawnMapID:  900,
	})

	sys := &InputSystem{worldState: ws, mapData: newSkillLOSTestMap(t)}
	sys.cleanupCompanions(owner)

	samePackets := drainSkillTestPackets(sameShow.Session)
	otherPackets := drainSkillTestPackets(otherShow.Session)
	if !hasRemoveObjectPacket(samePackets, followerID) {
		t.Fatalf("同 ShowID 玩家應收到斷線 follower 移除封包")
	}
	if hasRemoveObjectPacket(otherPackets, followerID) {
		t.Fatalf("不同 ShowID 玩家不應收到斷線 follower 移除封包")
	}
	respawned := ws.NpcList()
	if len(respawned) != 1 {
		t.Fatalf("斷線 follower 應還原 1 隻原 NPC，實際 %d", len(respawned))
	}
	if respawned[0].ShowID != owner.ShowID {
		t.Fatalf("follower 還原原 NPC 應繼承 ShowID=%d，實際 %d", owner.ShowID, respawned[0].ShowID)
	}
	if !hasPutObjectPacket(samePackets, respawned[0].ID) {
		t.Fatalf("同 ShowID 玩家應收到 follower 還原原 NPC 顯示封包")
	}
	if hasPutObjectPacket(otherPackets, respawned[0].ID) {
		t.Fatalf("yiwei follower deleteMe/respawn 可見性受 ShowID 約束，不同 ShowID 玩家不應收到還原原 NPC 顯示封包")
	}
}

func TestInputDisconnectRemoveObjectBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9501,
		Name:      "leaver",
		X:         100,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 21,
		Session:   newSkillTestSession(t, 21),
	})
	sameShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9502,
		Name:      "same",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 22,
		Session:   newSkillTestSession(t, 22),
	})
	otherShow := addSkillTestPlayer(ws, &world.PlayerInfo{
		CharID:    9503,
		Name:      "other",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    88,
		SessionID: 23,
		Session:   newSkillTestSession(t, 23),
	})

	broadcastDisconnectRemoveObject(player, ws, player.SessionID)

	if !hasRemoveObjectPacket(drainSkillTestPackets(sameShow.Session), player.CharID) {
		t.Fatalf("同 ShowID 玩家應收到斷線玩家移除封包")
	}
	if hasRemoveObjectPacket(drainSkillTestPackets(otherShow.Session), player.CharID) {
		t.Fatalf("yiwei logout/getRecognizePlayer 受 ShowID 約束，不同 ShowID 玩家不應收到斷線玩家移除封包")
	}
}
