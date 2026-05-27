package handler

import (
	"testing"

	"github.com/l1jgo/server/internal/world"
)

func TestChangeCharRemoveObjectBroadcastsOnlySameShowLikeJava(t *testing.T) {
	ws := world.NewState()
	player := addMovementShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    6301,
		Name:      "changer",
		X:         100,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 1,
		Session:   newMovementShowIDTestSession(t, 1),
	})
	sameShow := addMovementShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    6302,
		Name:      "same",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    77,
		SessionID: 2,
		Session:   newMovementShowIDTestSession(t, 2),
	})
	otherShow := addMovementShowIDPlayer(ws, &world.PlayerInfo{
		CharID:    6303,
		Name:      "other",
		X:         101,
		Y:         100,
		MapID:     900,
		ShowID:    88,
		SessionID: 3,
		Session:   newMovementShowIDTestSession(t, 3),
	})

	broadcastChangeCharRemoveObject(player, ws, player.SessionID)

	if !hasNpcActionShowIDRemoveObjectPacket(drainMovementShowIDPackets(sameShow.Session), player.CharID) {
		t.Fatalf("same ShowID viewer should receive S_RemoveObject when player returns to character select")
	}
	if hasNpcActionShowIDRemoveObjectPacket(drainMovementShowIDPackets(otherShow.Session), player.CharID) {
		t.Fatalf("different ShowID viewer must not receive S_RemoveObject when player returns to character select")
	}
}
