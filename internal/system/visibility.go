package system

import (
	"time"

	coresys "github.com/l1jgo/server/internal/core/system"
	"github.com/l1jgo/server/internal/handler"
	"github.com/l1jgo/server/internal/world"
)

// VisibilitySystem 處理所有實體的 AOI（Area of Interest）進出視野。
// 對應 Java 的 8 個 UpdateObject 定時線程（每 350ms 掃描一次可見物件）。
// Phase 3（PostUpdate），每 2 tick（400ms）執行一次。
//
// HandleMove 只負責廣播 S_MoveCharPacket + 格子封鎖（匹配 Java C_MoveChar）。
// 所有實體的出現/消失封包由本系統統一處理。
type VisibilitySystem struct {
	world *world.State
	deps  *handler.Deps
	ticks int
}

func NewVisibilitySystem(ws *world.State, deps *handler.Deps) *VisibilitySystem {
	return &VisibilitySystem{world: ws, deps: deps}
}

func (s *VisibilitySystem) Phase() coresys.Phase { return coresys.PhasePostUpdate }

func (s *VisibilitySystem) Update(_ time.Duration) {
	s.ticks++
	if s.ticks < 2 {
		return
	}
	s.ticks = 0

	s.world.AllPlayers(func(p *world.PlayerInfo) {
		if p.Known == nil {
			return
		}
		s.updatePlayerVisibility(p)
		s.updateNpcVisibility(p)
		s.updateSummonVisibility(p)
		s.updateDollVisibility(p)
		s.updateFollowerVisibility(p)
		s.updatePetVisibility(p)
		s.updateGroundItemVisibility(p)
		s.updateDoorVisibility(p)
	})
}

// --- 玩家 AOI ---

func (s *VisibilitySystem) updatePlayerVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyPlayers(p.X, p.Y, p.MapID, p.SessionID)

	// 建立當前可見集合
	currentSet := make(map[int32]struct{}, len(nearby))
	for _, other := range nearby {
		currentSet[other.CharID] = struct{}{}

		if _, known := p.Known.Players[other.CharID]; !known {
			// 新進入視野
			handler.SendPutObject(p.Session, other)
			p.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}

			// 同步毒/詛咒色調（讓新進入視野的玩家看到正確的中毒視覺）
			if other.PoisonType > 0 {
				if other.PoisonType == 4 {
					handler.SendPoison(p.Session, other.CharID, 2) // 灰色（麻痺毒已麻痺）
				} else {
					handler.SendPoison(p.Session, other.CharID, 1) // 綠色（傷害/沉默/麻痺延遲）
				}
			}
			if other.CurseType > 0 {
				handler.SendPoison(p.Session, other.CharID, 2) // 灰色（詛咒麻痺）
			}
		} else {
			// 持續在視野內：更新記錄的位置（可能已移動）
			p.Known.Players[other.CharID] = world.KnownPos{X: other.X, Y: other.Y}
		}
	}

	// 離開視野
	for charID := range p.Known.Players {
		if _, still := currentSet[charID]; !still {
			handler.SendRemoveObject(p.Session, charID)
			delete(p.Known.Players, charID)
		}
	}
}

// --- NPC AOI ---

func (s *VisibilitySystem) updateNpcVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyNpcsForVis(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, npc := range nearby {
		currentSet[npc.ID] = struct{}{}

		if _, known := p.Known.Npcs[npc.ID]; !known {
			// Java onPerceive: 死亡 NPC 以 status=8 發送，活 NPC 以 status=0 發送
			if npc.Dead {
				handler.SendNpcDeadPack(p.Session, npc) // 屍體姿態
			} else {
				handler.SendNpcPack(p.Session, npc)
			}
			p.Known.Npcs[npc.ID] = world.KnownPos{X: npc.X, Y: npc.Y}
		} else {
			p.Known.Npcs[npc.ID] = world.KnownPos{X: npc.X, Y: npc.Y}
		}
	}

	for id := range p.Known.Npcs {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.Npcs, id)
		}
	}
}

// --- 召喚獸 AOI ---

func (s *VisibilitySystem) updateSummonVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbySummons(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, sum := range nearby {
		currentSet[sum.ID] = struct{}{}

		if _, known := p.Known.Summons[sum.ID]; !known {
			isOwner := sum.OwnerCharID == p.CharID
			masterName := ""
			if m := s.world.GetByCharID(sum.OwnerCharID); m != nil {
				masterName = m.Name
			}
			handler.SendSummonPack(p.Session, sum, isOwner, masterName)
			p.Known.Summons[sum.ID] = world.KnownPos{X: sum.X, Y: sum.Y}
		} else {
			p.Known.Summons[sum.ID] = world.KnownPos{X: sum.X, Y: sum.Y}
		}
	}

	for id := range p.Known.Summons {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.Summons, id)
		}
	}
}

// --- 魔法娃娃 AOI ---

func (s *VisibilitySystem) updateDollVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyDolls(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, doll := range nearby {
		currentSet[doll.ID] = struct{}{}

		if _, known := p.Known.Dolls[doll.ID]; !known {
			masterName := ""
			if m := s.world.GetByCharID(doll.OwnerCharID); m != nil {
				masterName = m.Name
			}
			handler.SendDollPack(p.Session, doll, masterName)
			p.Known.Dolls[doll.ID] = world.KnownPos{X: doll.X, Y: doll.Y}
		} else {
			p.Known.Dolls[doll.ID] = world.KnownPos{X: doll.X, Y: doll.Y}
		}
	}

	for id := range p.Known.Dolls {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.Dolls, id)
		}
	}
}

// --- 隨從 AOI ---

func (s *VisibilitySystem) updateFollowerVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyFollowers(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, f := range nearby {
		currentSet[f.ID] = struct{}{}

		if _, known := p.Known.Followers[f.ID]; !known {
			handler.SendFollowerPack(p.Session, f)
			p.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
		} else {
			p.Known.Followers[f.ID] = world.KnownPos{X: f.X, Y: f.Y}
		}
	}

	for id := range p.Known.Followers {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.Followers, id)
		}
	}
}

// --- 寵物 AOI ---

func (s *VisibilitySystem) updatePetVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyPets(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, pet := range nearby {
		currentSet[pet.ID] = struct{}{}

		if _, known := p.Known.Pets[pet.ID]; !known {
			isOwner := pet.OwnerCharID == p.CharID
			masterName := ""
			if m := s.world.GetByCharID(pet.OwnerCharID); m != nil {
				masterName = m.Name
			}
			handler.SendPetPack(p.Session, pet, isOwner, masterName)
			p.Known.Pets[pet.ID] = world.KnownPos{X: pet.X, Y: pet.Y}
		} else {
			p.Known.Pets[pet.ID] = world.KnownPos{X: pet.X, Y: pet.Y}
		}
	}

	for id := range p.Known.Pets {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.Pets, id)
		}
	}
}

// --- 地面物品 AOI ---

func (s *VisibilitySystem) updateGroundItemVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyGroundItems(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, g := range nearby {
		currentSet[g.ID] = struct{}{}

		if _, known := p.Known.GroundItems[g.ID]; !known {
			handler.SendDropItem(p.Session, g)
			p.Known.GroundItems[g.ID] = world.KnownPos{X: g.X, Y: g.Y}
		}
		// 地面物品不需更新位置（不會移動）
	}

	for id := range p.Known.GroundItems {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.GroundItems, id)
		}
	}
}

// --- 門 AOI ---

func (s *VisibilitySystem) updateDoorVisibility(p *world.PlayerInfo) {
	nearby := s.world.GetNearbyDoors(p.X, p.Y, p.MapID)

	currentSet := make(map[int32]struct{}, len(nearby))
	for _, d := range nearby {
		currentSet[d.ID] = struct{}{}

		if _, known := p.Known.Doors[d.ID]; !known {
			handler.SendDoorPerceive(p.Session, d)
			p.Known.Doors[d.ID] = world.KnownPos{X: d.X, Y: d.Y}
		}
		// 門不需更新位置（不會移動）
	}

	for id := range p.Known.Doors {
		if _, still := currentSet[id]; !still {
			handler.SendRemoveObject(p.Session, id)
			delete(p.Known.Doors, id)
		}
	}
}
