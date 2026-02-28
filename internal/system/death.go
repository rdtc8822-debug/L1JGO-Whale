package system

import (
	"fmt"

	"github.com/l1jgo/server/internal/core/event"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/net"
	"github.com/l1jgo/server/internal/world"
)

// DeathSystem 處理玩家死亡與重生邏輯。
type DeathSystem struct {
	deps *handler.Deps
}

func NewDeathSystem(deps *handler.Deps) *DeathSystem {
	return &DeathSystem{deps: deps}
}

// ==================== 玩家死亡 ====================

// KillPlayer implements handler.DeathManager — 處理玩家死亡。
func (s *DeathSystem) KillPlayer(player *world.PlayerInfo) {
	if player.Dead {
		return
	}

	player.Dead = true
	player.HP = 0

	// 死亡玩家不再佔用格子
	s.deps.World.VacateEntity(player.MapID, player.X, player.Y, player.CharID)

	// 廣播死亡動畫
	nearby := s.deps.World.GetNearbyPlayersAt(player.X, player.Y, player.MapID)
	for _, viewer := range nearby {
		handler.SendActionGfx(viewer.Session, player.CharID, 8) // ACTION_Die = 8
	}
	handler.SendActionGfx(player.Session, player.CharID, 8)

	// 死亡時清除所有毒和詛咒
	if player.PoisonType != 0 {
		CurePoison(player, s.deps)
	}
	if player.CurseType != 0 {
		CureCurseParalysis(player, s.deps)
	}

	// 清除所有 buff（死亡時無例外全清，含不可取消的）
	if s.deps.Skill != nil {
		s.deps.Skill.ClearAllBuffsOnDeath(player)
	}

	// 發送 HP 更新（0）
	handler.SendHpUpdate(player.Session, player)

	// Lua 經驗懲罰（scripts/core/levelup.lua）：等級經驗範圍的 5%
	applyDeathExpPenalty(player, s.deps)
	handler.SendExpUpdate(player.Session, player.Level, player.Exp)

	// 發出 PlayerDied 事件（下一 tick 可讀取）
	if s.deps.Bus != nil {
		event.Emit(s.deps.Bus, event.PlayerDied{
			CharID: player.CharID,
			MapID:  player.MapID,
			X:      player.X,
			Y:      player.Y,
		})
	}

	s.deps.Log.Info(fmt.Sprintf("玩家死亡  角色=%s  x=%d  y=%d", player.Name, player.X, player.Y))
}

// ==================== 死亡重生 ====================

// ProcessRestart implements handler.DeathManager — 處理死亡後重生。
func (s *DeathSystem) ProcessRestart(sess *net.Session, player *world.PlayerInfo) {
	if !player.Dead {
		return
	}

	// 復活
	player.Dead = false
	player.LastMoveTime = 0 // 重置速度驗證
	player.HP = int16(player.Level)
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
	player.Food = int16(s.deps.Config.Gameplay.InitialFood)

	// 取得重生位置（Lua: scripts/world/respawn.lua）
	rx, ry, rmap := getBackLocation(player.MapID, s.deps)

	// 清除舊格子碰撞
	if s.deps.MapData != nil {
		s.deps.MapData.SetImpassable(player.MapID, player.X, player.Y, false)
	}

	// 廣播從舊位置移除
	nearby := s.deps.World.GetNearbyPlayers(player.X, player.Y, player.MapID, sess.ID)
	for _, other := range nearby {
		handler.SendRemoveObject(other.Session, player.CharID)
	}

	// 移動到重生點
	s.deps.World.UpdatePosition(sess.ID, rx, ry, rmap, 0)

	// 標記新格子
	if s.deps.MapData != nil {
		s.deps.MapData.SetImpassable(rmap, rx, ry, true)
	}

	// 發送地圖 ID
	handler.SendMapID(sess, uint16(rmap), false)

	// 發送自身角色封包
	handler.SendPutObject(sess, player)

	// 發送狀態更新
	handler.SendPlayerStatus(sess, player)

	// 重置 Known 集合
	if player.Known == nil {
		player.Known = world.NewKnownEntities()
	} else {
		player.Known.Reset()
	}

	// 發送附近玩家 + 填入 Known
	newNearby := s.deps.World.GetNearbyPlayers(rx, ry, rmap, sess.ID)
	for _, other := range newNearby {
		handler.SendPutObject(other.Session, player)
		handler.SendPutObject(sess, other)
		player.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}
	}

	// 發送附近 NPC + 填入 Known
	nearbyNpcs := s.deps.World.GetNearbyNpcs(rx, ry, rmap)
	for _, npc := range nearbyNpcs {
		handler.SendNpcPack(sess, npc)
		player.Known.Npcs[npc.ID] = world.KnownPos{X: npc.X, Y: npc.Y}
	}

	// 發送附近地面物品 + 填入 Known
	nearbyGnd := s.deps.World.GetNearbyGroundItems(rx, ry, rmap)
	for _, g := range nearbyGnd {
		handler.SendDropItem(sess, g)
		player.Known.GroundItems[g.ID] = world.KnownPos{X: g.X, Y: g.Y}
	}

	// 發送附近召喚獸 + 填入 Known
	nearbySums := s.deps.World.GetNearbySummons(rx, ry, rmap)
	for _, sum := range nearbySums {
		isOwner := sum.OwnerCharID == player.CharID
		masterName := ""
		if m := s.deps.World.GetByCharID(sum.OwnerCharID); m != nil {
			masterName = m.Name
		}
		handler.SendSummonPack(sess, sum, isOwner, masterName)
		player.Known.Summons[sum.ID] = world.KnownPos{X: sum.X, Y: sum.Y}
	}

	// 發送附近魔法娃娃 + 填入 Known
	nearbyDolls := s.deps.World.GetNearbyDolls(rx, ry, rmap)
	for _, doll := range nearbyDolls {
		masterName := ""
		if m := s.deps.World.GetByCharID(doll.OwnerCharID); m != nil {
			masterName = m.Name
		}
		handler.SendDollPack(sess, doll, masterName)
		player.Known.Dolls[doll.ID] = world.KnownPos{X: doll.X, Y: doll.Y}
	}

	// 發送附近隨從 + 填入 Known
	nearbyFollowers := s.deps.World.GetNearbyFollowers(rx, ry, rmap)
	for _, f := range nearbyFollowers {
		handler.SendFollowerPack(sess, f)
		player.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
	}

	// 發送附近寵物 + 填入 Known
	nearbyPets := s.deps.World.GetNearbyPets(rx, ry, rmap)
	for _, pet := range nearbyPets {
		isOwner := pet.OwnerCharID == player.CharID
		masterName := ""
		if m := s.deps.World.GetByCharID(pet.OwnerCharID); m != nil {
			masterName = m.Name
		}
		handler.SendPetPack(sess, pet, isOwner, masterName)
		player.Known.Pets[pet.ID] = world.KnownPos{X: pet.X, Y: pet.Y}
	}

	// 發送附近門 + 填入 Known
	nearbyDoors := s.deps.World.GetNearbyDoors(rx, ry, rmap)
	for _, d := range nearbyDoors {
		handler.SendDoorPerceive(sess, d)
		player.Known.Doors[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
	}

	// 發送天氣
	handler.SendWeather(sess, s.deps.World.Weather)

	s.deps.Log.Info(fmt.Sprintf("玩家重新開始  角色=%s  x=%d  y=%d  地圖=%d", player.Name, rx, ry, rmap))
}

// ==================== 內部輔助函式 ====================

// applyDeathExpPenalty 透過 Lua 扣除死亡經驗懲罰。
func applyDeathExpPenalty(player *world.PlayerInfo, deps *handler.Deps) {
	penalty := deps.Scripting.CalcDeathExpPenalty(int(player.Level), int(player.Exp))
	if penalty > 0 {
		player.Exp -= int32(penalty)
	}
}

// getBackLocation 透過 Lua 取得重生座標。
func getBackLocation(mapID int16, deps *handler.Deps) (int32, int32, int16) {
	loc := deps.Scripting.GetRespawnLocation(int(mapID))
	if loc != nil {
		return int32(loc.X), int32(loc.Y), int16(loc.Map)
	}
	return 33084, 33391, 4
}
