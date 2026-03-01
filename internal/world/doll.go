package world

// DollInfo holds in-memory data for a magic doll companion.
// Not DB-persisted as instance — the item exists in the player's inventory.
// Accessed only from the game loop goroutine — no locks needed.
type DollInfo struct {
	ID          int32  // NPC-range object ID (from NextNpcID)
	OwnerCharID int32  // CharID of the player who summoned this doll
	ItemObjID   int32  // Inventory item objectID that spawned this doll
	DollTypeID  int32  // Doll template ID (from dolls.yaml)
	GfxID       int32  // Sprite ID
	NameID      string // Client string table key
	Name        string // Display name

	X       int32
	Y       int32
	MapID   int16
	Heading int16

	TimerTicks int // remaining ticks alive
	MoveTimer  int // movement cooldown (decremented each tick; move only when 0)

	// Stat bonuses applied to master on summon (reversed on dismiss).
	// Follows the delta pattern used by ActiveBuff.
	BonusAC       int16
	BonusDmg      int16
	BonusHit      int16
	BonusBowDmg   int16
	BonusBowHit   int16
	BonusSP       int16
	BonusMR       int16
	BonusHP       int16
	BonusMP       int16
	BonusHPR      int16
	BonusMPR      int16
	BonusFireRes  int16
	BonusWaterRes int16
	BonusWindRes  int16
	BonusEarthRes int16
	BonusDodge    int16
	BonusSTR      int16
	BonusDEX      int16
	BonusCON      int16
	BonusWIS      int16
	BonusINT      int16
	BonusCHA      int16
	BonusStunRes  int16
	BonusFreezeRes int16

	// Doll active skill (optional — some dolls can cast skills).
	SkillID     int32 // 0 = no skill
	SkillChance int   // probability percentage per tick
}

// --- State doll management ---

// AddDoll registers a doll in the world. Uses NPC AOI grid + entity grid.
func (s *State) AddDoll(doll *DollInfo) {
	if s.dolls == nil {
		s.dolls = make(map[int32]*DollInfo)
	}
	s.dolls[doll.ID] = doll
	s.npcAoi.Add(doll.ID, doll.X, doll.Y, doll.MapID)
	s.entity.Occupy(doll.MapID, doll.X, doll.Y, doll.ID)
}

// RemoveDoll removes a doll from the world and frees its tile.
func (s *State) RemoveDoll(dollID int32) *DollInfo {
	if s.dolls == nil {
		return nil
	}
	doll, ok := s.dolls[dollID]
	if !ok {
		return nil
	}
	s.npcAoi.Remove(doll.ID, doll.X, doll.Y, doll.MapID)
	s.entity.Vacate(doll.MapID, doll.X, doll.Y, doll.ID)
	delete(s.dolls, dollID)
	return doll
}

// GetDoll returns a doll by its object ID, or nil if not found.
func (s *State) GetDoll(dollID int32) *DollInfo {
	if s.dolls == nil {
		return nil
	}
	return s.dolls[dollID]
}

// GetDollsByOwner returns all dolls belonging to a player.
func (s *State) GetDollsByOwner(charID int32) []*DollInfo {
	var result []*DollInfo
	for _, doll := range s.dolls {
		if doll.OwnerCharID == charID {
			result = append(result, doll)
		}
	}
	return result
}

// UpdateDollPosition moves a doll and updates AOI + entity grids.
func (s *State) UpdateDollPosition(dollID int32, newX, newY int32, heading int16) {
	doll := s.dolls[dollID]
	if doll == nil {
		return
	}
	oldX, oldY := doll.X, doll.Y
	doll.X = newX
	doll.Y = newY
	doll.Heading = heading
	s.npcAoi.Move(dollID, oldX, oldY, doll.MapID, newX, newY, doll.MapID)
	s.entity.Move(doll.MapID, oldX, oldY, newX, newY, dollID)
}

// TeleportDoll moves a doll to a new location (possibly different map).
// Updates AOI grid + entity grid for cross-map movement.
func (s *State) TeleportDoll(dollID int32, newX, newY int32, newMapID, heading int16) {
	doll := s.dolls[dollID]
	if doll == nil {
		return
	}
	oldX, oldY, oldMap := doll.X, doll.Y, doll.MapID
	s.npcAoi.Remove(dollID, oldX, oldY, oldMap)
	s.entity.Vacate(oldMap, oldX, oldY, dollID)
	doll.X = newX
	doll.Y = newY
	doll.MapID = newMapID
	doll.Heading = heading
	s.npcAoi.Add(dollID, newX, newY, newMapID)
	s.entity.Occupy(newMapID, newX, newY, dollID)
}

// AllDolls iterates all in-world dolls.
func (s *State) AllDolls(fn func(*DollInfo)) {
	for _, doll := range s.dolls {
		fn(doll)
	}
}

// DollCount returns total doll count in-world.
func (s *State) DollCount() int {
	return len(s.dolls)
}

// GetNearbyDolls returns all dolls visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyDolls(x, y int32, mapID int16) []*DollInfo {
	nearbyIDs := s.npcAoi.GetNearby(x, y, mapID)
	var result []*DollInfo
	for _, nid := range nearbyIDs {
		doll := s.dolls[nid]
		if doll == nil {
			continue
		}
		dx := doll.X - x
		dy := doll.Y - y
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
			result = append(result, doll)
		}
	}
	return result
}
