package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/net/packet"
	"github.com/l1jgo/server/internal/world"
)

// HandleRestart processes C_RESTART (opcode 177).
// When a dead player clicks restart, resurrect them at the nearest town.
func HandleRestart(sess *net.Session, _ *packet.Reader, deps *Deps) {
	player := deps.World.GetBySession(sess.ID)
	if player == nil {
		return
	}
	if !player.Dead {
		return
	}

	// Resurrect
	player.Dead = false
	player.HP = int16(player.Level) // Java: setCurrentHp(getLevel())
	if player.HP < 1 {
		player.HP = 1
	}
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	player.MP = int16(player.Level / 2)
	if player.MP > player.MaxMP {
		player.MP = player.MaxMP
	}
	player.Food = 40 // Java: C_Restart sets food = 40

	// Get respawn location based on current map (Lua: scripts/world/respawn.lua)
	rx, ry, rmap := getBackLocation(player.MapID, deps)

	// Update tile collision (set new position — old was cleared on death)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(rmap, rx, ry, true)
	}

	// Broadcast removal from old position
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		sendRemoveObject(other.Session, player.CharID)
	}

	// Move to respawn point
	deps.World.UpdatePosition(sess.ID, rx, ry, rmap, 0)

	// Send map ID (in case map changed)
	sendMapID(sess, uint16(rmap), false)

	// Send own char pack at new position
	sendPutObject(sess, player)

	// Send status update
	sendPlayerStatus(sess, player)

	// Send to nearby players at new location
	newNearby := deps.World.GetNearbyPlayers(rx, ry, rmap, sess.ID)
	for _, other := range newNearby {
		sendPutObject(other.Session, player)
		sendPutObject(sess, other)
	}

	// Send nearby NPCs
	nearbyNpcs := deps.World.GetNearbyNpcs(rx, ry, rmap)
	for _, npc := range nearbyNpcs {
		sendNpcPack(sess, npc)
	}

	// Send nearby ground items
	nearbyGnd := deps.World.GetNearbyGroundItems(rx, ry, rmap)
	for _, g := range nearbyGnd {
		sendDropItem(sess, g)
	}

	// Send weather
	sendWeather(sess, 0)

	deps.Log.Info(fmt.Sprintf("玩家重新開始  角色=%s  x=%d  y=%d  地圖=%d", player.Name, rx, ry, rmap))
}

// KillPlayer handles player death: set dead, broadcast animation, apply exp penalty.
func KillPlayer(player *world.PlayerInfo, deps *Deps) {
	if player.Dead {
		return
	}

	player.Dead = true
	player.HP = 0

	// Clear tile collision (dead player doesn't block movement)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, false)
	}

	// Broadcast death animation to self + nearby
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, player.CharID, 8) // ACTION_Die = 8
	}
	sendActionGfx(player.Session, player.CharID, 8)

	// Send HP update (0)
	sendHpUpdate(player.Session, player)

	// Exp penalty via Lua (scripts/core/levelup.lua)
	applyDeathExpPenalty(player, deps)
	sendExpUpdate(player.Session, player.Level, player.Exp)

	deps.Log.Info(fmt.Sprintf("玩家死亡  角色=%s  x=%d  y=%d", player.Name, player.X, player.Y))
}

// applyDeathExpPenalty subtracts exp on death via Lua (scripts/core/levelup.lua).
func applyDeathExpPenalty(player *world.PlayerInfo, deps *Deps) {
	penalty := deps.Scripting.CalcDeathExpPenalty(int(player.Level), int(player.Exp))
	if penalty > 0 {
		player.Exp -= int32(penalty)
	}
}

// getBackLocation returns respawn coordinates via Lua (scripts/world/respawn.lua).
func getBackLocation(mapID int16, deps *Deps) (int32, int32, int16) {
	loc := deps.Scripting.GetRespawnLocation(int(mapID))
	if loc != nil {
		return int32(loc.X), int32(loc.Y), int16(loc.Map)
	}
	return 33084, 33391, 4
}
