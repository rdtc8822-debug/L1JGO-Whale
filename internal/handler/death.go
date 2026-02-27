package handler

import (
	"fmt"

	"github.com/l1jgo/server/internal/core/event"
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
	player.LastMoveTime = 0 // reset speed validation
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
	player.Food = int16(deps.Config.Gameplay.InitialFood)

	// Get respawn location based on current map (Lua: scripts/world/respawn.lua)
	rx, ry, rmap := getBackLocation(player.MapID, deps)

	// Clear old tile (for NPC pathfinding)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(player.MapID, player.X, player.Y, false)
	}

	// Broadcast removal from old position
	nearby := deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		SendRemoveObject(other.Session, player.CharID)
	}

	// Move to respawn point
	deps.World.UpdatePosition(sess.ID, rx, ry, rmap, 0)

	// Mark new tile as impassable (for NPC pathfinding)
	if deps.MapData != nil {
		deps.MapData.SetImpassable(rmap, rx, ry, true)
	}

	// Send map ID (in case map changed)
	sendMapID(sess, uint16(rmap), false)

	// Send own char pack at new position
	SendPutObject(sess, player)

	// Send status update
	sendPlayerStatus(sess, player)

	// 重置 Known 集合（重生 = 完全切換場景）
	if player.Known == nil {
		player.Known = world.NewKnownEntities()
	} else {
		player.Known.Reset()
	}

	// 發送附近玩家 + 填入 Known
	newNearby := deps.World.GetNearbyPlayers(rx, ry, rmap, sess.ID)
	for _, other := range newNearby {
		SendPutObject(other.Session, player)
		SendPutObject(sess, other)
		player.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}
	}

	// 發送附近 NPC + 填入 Known
	nearbyNpcs := deps.World.GetNearbyNpcs(rx, ry, rmap)
	for _, npc := range nearbyNpcs {
		SendNpcPack(sess, npc)
		player.Known.Npcs[npc.ID] = world.KnownPos{X: npc.X, Y: npc.Y}
	}

	// 發送附近地面物品 + 填入 Known
	nearbyGnd := deps.World.GetNearbyGroundItems(rx, ry, rmap)
	for _, g := range nearbyGnd {
		SendDropItem(sess, g)
		player.Known.GroundItems[g.ID] = world.KnownPos{X: g.X, Y: g.Y}
	}

	// 發送附近召喚獸 + 填入 Known
	nearbySums := deps.World.GetNearbySummons(rx, ry, rmap)
	for _, sum := range nearbySums {
		isOwner := sum.OwnerCharID == player.CharID
		masterName := ""
		if m := deps.World.GetByCharID(sum.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendSummonPack(sess, sum, isOwner, masterName)
		player.Known.Summons[sum.ID] = world.KnownPos{X: sum.X, Y: sum.Y}
	}

	// 發送附近魔法娃娃 + 填入 Known
	nearbyDolls := deps.World.GetNearbyDolls(rx, ry, rmap)
	for _, doll := range nearbyDolls {
		masterName := ""
		if m := deps.World.GetByCharID(doll.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendDollPack(sess, doll, masterName)
		player.Known.Dolls[doll.ID] = world.KnownPos{X: doll.X, Y: doll.Y}
	}

	// 發送附近隨從 + 填入 Known
	nearbyFollowers := deps.World.GetNearbyFollowers(rx, ry, rmap)
	for _, f := range nearbyFollowers {
		SendFollowerPack(sess, f)
		player.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
	}

	// 發送附近寵物 + 填入 Known
	nearbyPets := deps.World.GetNearbyPets(rx, ry, rmap)
	for _, pet := range nearbyPets {
		isOwner := pet.OwnerCharID == player.CharID
		masterName := ""
		if m := deps.World.GetByCharID(pet.OwnerCharID); m != nil {
			masterName = m.Name
		}
		SendPetPack(sess, pet, isOwner, masterName)
		player.Known.Pets[pet.ID] = world.KnownPos{X: pet.X, Y: pet.Y}
	}

	// 發送附近門 + 填入 Known
	nearbyDoors := deps.World.GetNearbyDoors(rx, ry, rmap)
	for _, d := range nearbyDoors {
		SendDoorPerceive(sess, d)
		player.Known.Doors[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
	}

	// Send weather (Java: sends current world weather, not hardcoded 0)
	sendWeather(sess, deps.World.Weather)

	deps.Log.Info(fmt.Sprintf("玩家重新開始  角色=%s  x=%d  y=%d  地圖=%d", player.Name, rx, ry, rmap))
}

// KillPlayer handles player death: set dead, broadcast animation, apply exp penalty.
func KillPlayer(player *world.PlayerInfo, deps *Deps) {
	if player.Dead {
		return
	}

	player.Dead = true
	player.HP = 0

	// Dead player no longer occupies the tile
	deps.World.VacateEntity(player.MapID, player.X, player.Y, player.CharID)

	// Broadcast death animation to self + nearby
	nearby := deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		sendActionGfx(viewer.Session, player.CharID, 8) // ACTION_Die = 8
	}
	sendActionGfx(player.Session, player.CharID, 8)

	// 死亡時清除所有毒和詛咒
	if player.PoisonType != 0 {
		CurePoison(player, deps)
	}
	if player.CurseType != 0 {
		CureCurseParalysis(player, deps)
	}

	// Clear ALL buffs on death (good and bad, no exceptions)
	clearAllBuffsOnDeath(player, deps)

	// Send HP update (0)
	sendHpUpdate(player.Session, player)

	// Exp penalty via Lua (scripts/core/levelup.lua): 5% of level exp range
	applyDeathExpPenalty(player, deps)
	sendExpUpdate(player.Session, player.Level, player.Exp)

	// Emit PlayerDied event (readable next tick by subscribers)
	if deps.Bus != nil {
		event.Emit(deps.Bus, event.PlayerDied{
			CharID: player.CharID,
			MapID:  player.MapID,
			X:      player.X,
			Y:      player.Y,
		})
	}

	deps.Log.Info(fmt.Sprintf("玩家死亡  角色=%s  x=%d  y=%d", player.Name, player.X, player.Y))
}

// applyDeathExpPenalty subtracts exp on death via Lua (scripts/core/levelup.lua).
func applyDeathExpPenalty(player *world.PlayerInfo, deps *Deps) {
	penalty := deps.Scripting.CalcDeathExpPenalty(int(player.Level), int(player.Exp))
	if penalty > 0 {
		player.Exp -= int32(penalty)
	}
}

// clearAllBuffsOnDeath removes ALL buffs (good and bad) unconditionally.
// Unlike cancelAllBuffs, this ignores the non-cancellable list.
func clearAllBuffsOnDeath(player *world.PlayerInfo, deps *Deps) {
	if player.ActiveBuffs == nil {
		return
	}
	for skillID, buff := range player.ActiveBuffs {
		revertBuffStats(player, buff)
		delete(player.ActiveBuffs, skillID)
		cancelBuffIcon(player, skillID, deps)

		if skillID == SkillShapeChange {
			UndoPoly(player, deps)
		}
		if buff.SetMoveSpeed > 0 {
			player.MoveSpeed = 0
			player.HasteTicks = 0
			sendSpeedToAll(player, deps, 0, 0)
		}
		if buff.SetBraveSpeed > 0 {
			player.BraveSpeed = 0
			sendBraveToAll(player, deps, 0, 0)
		}
	}
	sendPlayerStatus(player.Session, player)
}

// getBackLocation returns respawn coordinates via Lua (scripts/world/respawn.lua).
func getBackLocation(mapID int16, deps *Deps) (int32, int32, int16) {
	loc := deps.Scripting.GetRespawnLocation(int(mapID))
	if loc != nil {
		return int32(loc.X), int32(loc.Y), int16(loc.Map)
	}
	return 33084, 33391, 4
}
