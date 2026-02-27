package system

import (
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/data"
	"github.com/l1jgo/server/internal/world"
)

// NpcRespawnSystem processes NPC delete timers and respawn timers each tick.
// Flow: NPC dies → DeleteTimer counts down → send S_RemoveObject →
// RespawnTimer counts down → respawn at spawn point. Phase 2 (Update).
type NpcRespawnSystem struct {
	world *world.State
	maps  *data.MapDataTable
}

func NewNpcRespawnSystem(ws *world.State, maps *data.MapDataTable) *NpcRespawnSystem {
	return &NpcRespawnSystem{world: ws, maps: maps}
}

func (s *NpcRespawnSystem) Phase() coresys.Phase { return coresys.PhaseUpdate }

func (s *NpcRespawnSystem) Update(_ time.Duration) {
	for _, npc := range s.world.NpcList() {
		if !npc.Dead {
			continue
		}

		// Phase 1: Delete timer — wait for death animation to finish before removing
		if npc.DeleteTimer > 0 {
			npc.DeleteTimer--
			if npc.DeleteTimer <= 0 {
				// Death animation done — remove NPC from client view
				nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
				for _, viewer := range nearby {
					sendRemoveObject(viewer.Session, npc.ID)
				}
			}
			continue // don't start respawn timer until delete phase is done
		}

		// Phase 2: Respawn timer
		if npc.RespawnTimer > 0 {
			npc.RespawnTimer--
			if npc.RespawnTimer <= 0 {
				s.respawnNpc(npc)
			}
		}
	}
}

func (s *NpcRespawnSystem) respawnNpc(npc *world.NpcInfo) {
	// Find unoccupied spawn tile
	spawnX, spawnY := npc.SpawnX, npc.SpawnY
	if s.world.IsOccupied(spawnX, spawnY, npc.SpawnMapID, npc.ID) {
		// Spiral search radius 1~3 for nearest empty tile
		found := false
		for r := int32(1); r <= 3 && !found; r++ {
			for dx := -r; dx <= r && !found; dx++ {
				for dy := -r; dy <= r && !found; dy++ {
					tx, ty := spawnX+dx, spawnY+dy
					if !s.world.IsOccupied(tx, ty, npc.SpawnMapID, npc.ID) {
						spawnX, spawnY = tx, ty
						found = true
					}
				}
			}
		}
	}

	npc.Dead = false
	npc.HP = npc.MaxHP
	npc.MP = npc.MaxMP
	npc.X = spawnX
	npc.Y = spawnY
	npc.MapID = npc.SpawnMapID
	npc.AggroTarget = 0
	npc.AttackTimer = 0
	npc.MoveTimer = 0
	npc.StuckTicks = 0

	// Set tile as blocked (map passability for NPC pathfinding)
	if s.maps != nil {
		s.maps.SetImpassable(npc.MapID, npc.X, npc.Y, true)
	}

	// Re-add to NPC AOI grid + entity grid
	s.world.NpcRespawn(npc)

	// 通知附近玩家：顯示 NPC + 封鎖格子
	nearby := s.world.GetNearbyPlayersAt(npc.X, npc.Y, npc.MapID)
	for _, viewer := range nearby {
		sendNpcPack(viewer.Session, npc)
	}
}
