package world

// SummonStatus represents the AI behavior mode of a summon.
type SummonStatus int

const (
	SummonAggressive SummonStatus = 1 // 攻擊態勢 — attack master's target
	SummonDefensive  SummonStatus = 2 // 防禦態勢 — counterattack aggressors
	SummonRest       SummonStatus = 3 // 休憩 — stay in place, no combat
	SummonExtend     SummonStatus = 4 // 散開 — move away from master
	SummonAlert      SummonStatus = 5 // 警戒 — guard position, attack nearby
	SummonDismiss    SummonStatus = 6 // 解散 — return to nature (despawn)
)

// SummonInfo holds in-memory data for a summoned creature.
// Not DB-persisted — deleted on logout or expiry.
// Accessed only from the game loop goroutine — no locks needed.
type SummonInfo struct {
	ID          int32  // NPC-range object ID (from NextNpcID)
	OwnerCharID int32  // CharID of the player who summoned this
	NpcID       int32  // NPC template ID (determines sprite/stats)
	GfxID       int32  // Sprite ID
	NameID      string // Client string table key (e.g. "$936")
	Name        string // Display name
	Level       int16
	HP          int32
	MaxHP       int32
	MP          int32
	MaxMP       int32
	AC          int16
	STR         int16
	DEX         int16
	MR          int16
	AtkDmg      int32
	AtkSpeed    int16 // attack animation speed (ms, 0 = default)
	MoveSpd     int16 // passive/move speed (ms, 0 = default)
	Ranged      int16 // attack range (1 = melee, >1 = ranged)
	Lawful      int32
	Size        string // "small" or "large"

	X       int32
	Y       int32
	MapID   int16
	Heading int16

	Status     SummonStatus // current AI behavior mode
	Tamed      bool         // true if tamed monster (vs pure summon skill)
	PetCost    int          // CHA cost for this summon (Java: petcost); default 6
	TimerTicks int          // remaining ticks alive (3600s * 5 = 18000 ticks)

	// AI state
	AggroTarget   int32 // NPC object ID of hate target (0 = no target)
	AggroPlayerID int32 // CharID of player target (for PvP summon attack; 0 = none)
	AttackTimer   int   // ticks until next attack (cooldown)
	MoveTimer     int   // ticks until next move towards target
	StuckTicks    int   // consecutive ticks blocked by another entity
	HomeX         int32 // alert mode anchor position
	HomeY         int32

	Dead bool
}

// --- State summon management ---

// AddSummon registers a summon in the world. Uses NPC AOI grid + entity grid.
func (s *State) AddSummon(sum *SummonInfo) {
	if s.summons == nil {
		s.summons = make(map[int32]*SummonInfo)
	}
	s.summons[sum.ID] = sum
	s.npcAoi.Add(sum.ID, sum.X, sum.Y, sum.MapID)
	s.entity.Occupy(sum.MapID, sum.X, sum.Y, sum.ID)
}

// RemoveSummon removes a summon from the world and frees its tile.
func (s *State) RemoveSummon(summonID int32) *SummonInfo {
	if s.summons == nil {
		return nil
	}
	sum, ok := s.summons[summonID]
	if !ok {
		return nil
	}
	s.npcAoi.Remove(sum.ID, sum.X, sum.Y, sum.MapID)
	s.entity.Vacate(sum.MapID, sum.X, sum.Y, sum.ID)
	delete(s.summons, summonID)
	return sum
}

// GetSummon returns a summon by its object ID, or nil if not found.
func (s *State) GetSummon(summonID int32) *SummonInfo {
	if s.summons == nil {
		return nil
	}
	return s.summons[summonID]
}

// GetSummonsByOwner returns all summons belonging to a player.
func (s *State) GetSummonsByOwner(charID int32) []*SummonInfo {
	var result []*SummonInfo
	for _, sum := range s.summons {
		if sum.OwnerCharID == charID {
			result = append(result, sum)
		}
	}
	return result
}

// UpdateSummonPosition moves a summon and updates AOI + entity grids.
func (s *State) UpdateSummonPosition(summonID int32, newX, newY int32, heading int16) {
	sum := s.summons[summonID]
	if sum == nil {
		return
	}
	oldX, oldY := sum.X, sum.Y
	sum.X = newX
	sum.Y = newY
	sum.Heading = heading
	s.npcAoi.Move(summonID, oldX, oldY, sum.MapID, newX, newY, sum.MapID)
	s.entity.Move(sum.MapID, oldX, oldY, newX, newY, summonID)
}

// TeleportSummon moves a summon to a new location (possibly different map).
// Updates AOI grid + entity grid for cross-map movement.
func (s *State) TeleportSummon(summonID int32, newX, newY int32, newMapID, heading int16) {
	sum := s.summons[summonID]
	if sum == nil {
		return
	}
	oldX, oldY, oldMap := sum.X, sum.Y, sum.MapID
	s.npcAoi.Remove(summonID, oldX, oldY, oldMap)
	s.entity.Vacate(oldMap, oldX, oldY, summonID)
	sum.X = newX
	sum.Y = newY
	sum.MapID = newMapID
	sum.Heading = heading
	s.npcAoi.Add(summonID, newX, newY, newMapID)
	s.entity.Occupy(newMapID, newX, newY, summonID)
}

// AllSummons iterates all in-world summons.
func (s *State) AllSummons(fn func(*SummonInfo)) {
	for _, sum := range s.summons {
		fn(sum)
	}
}

// SummonCount returns total summon count in-world.
func (s *State) SummonCount() int {
	return len(s.summons)
}

// GetNearbySummons returns all alive summons visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbySummons(x, y int32, mapID int16) []*SummonInfo {
	nearbyIDs := s.npcAoi.GetNearby(x, y, mapID)
	var result []*SummonInfo
	for _, nid := range nearbyIDs {
		sum := s.summons[nid]
		if sum == nil || sum.Dead {
			continue
		}
		dx := sum.X - x
		dy := sum.Y - y
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		dist := dx
		if dy > dist {
			dist = dy
		}
		if dist <= 20 {
			result = append(result, sum)
		}
	}
	return result
}
