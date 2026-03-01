package world

// FollowerInfo holds in-memory data for a quest follower NPC.
// Not DB-persisted — follower is session-only. When dismissed or lost,
// the original NPC respawns at SpawnX/SpawnY.
// Accessed only from the game loop goroutine — no locks needed.
type FollowerInfo struct {
	ID          int32  // NPC-range object ID (from NextNpcID)
	OwnerCharID int32  // CharID of the player this follower belongs to
	NpcID       int32  // NPC template ID (follower version)
	OrigNpcID   int32  // Original NPC template ID (for respawn on dismiss)
	GfxID       int32  // Sprite ID
	NameID      string // Client string table key
	Name        string // Display name
	Level       int16
	HP          int32
	MaxHP       int32

	X       int32
	Y       int32
	MapID   int16
	Heading int16

	// Original NPC spawn position (for respawn on follower dismissal)
	SpawnX     int32
	SpawnY     int32
	SpawnMapID int16

	MoveTimer int // ticks until next move toward master

	Dead bool
}

// --- State follower management ---

// AddFollower registers a follower in the world. Uses NPC AOI grid + entity grid.
func (s *State) AddFollower(f *FollowerInfo) {
	if s.followers == nil {
		s.followers = make(map[int32]*FollowerInfo)
	}
	s.followers[f.ID] = f
	s.npcAoi.Add(f.ID, f.X, f.Y, f.MapID)
	s.entity.Occupy(f.MapID, f.X, f.Y, f.ID)
}

// RemoveFollower removes a follower from the world and frees its tile.
func (s *State) RemoveFollower(followerID int32) *FollowerInfo {
	if s.followers == nil {
		return nil
	}
	f, ok := s.followers[followerID]
	if !ok {
		return nil
	}
	s.npcAoi.Remove(f.ID, f.X, f.Y, f.MapID)
	s.entity.Vacate(f.MapID, f.X, f.Y, f.ID)
	delete(s.followers, followerID)
	return f
}

// GetFollower returns a follower by its object ID, or nil if not found.
func (s *State) GetFollower(followerID int32) *FollowerInfo {
	if s.followers == nil {
		return nil
	}
	return s.followers[followerID]
}

// GetFollowerByOwner returns the follower belonging to a player (max one per player).
func (s *State) GetFollowerByOwner(charID int32) *FollowerInfo {
	for _, f := range s.followers {
		if f.OwnerCharID == charID {
			return f
		}
	}
	return nil
}

// UpdateFollowerPosition moves a follower and updates AOI + entity grids.
func (s *State) UpdateFollowerPosition(followerID int32, newX, newY int32, heading int16) {
	f := s.followers[followerID]
	if f == nil {
		return
	}
	oldX, oldY := f.X, f.Y
	f.X = newX
	f.Y = newY
	f.Heading = heading
	s.npcAoi.Move(followerID, oldX, oldY, f.MapID, newX, newY, f.MapID)
	s.entity.Move(f.MapID, oldX, oldY, newX, newY, followerID)
}

// TeleportFollower moves a follower to a new location (possibly different map).
// Updates AOI grid + entity grid for cross-map movement.
func (s *State) TeleportFollower(followerID int32, newX, newY int32, newMapID, heading int16) {
	f := s.followers[followerID]
	if f == nil {
		return
	}
	oldX, oldY, oldMap := f.X, f.Y, f.MapID
	s.npcAoi.Remove(followerID, oldX, oldY, oldMap)
	s.entity.Vacate(oldMap, oldX, oldY, followerID)
	f.X = newX
	f.Y = newY
	f.MapID = newMapID
	f.Heading = heading
	s.npcAoi.Add(followerID, newX, newY, newMapID)
	s.entity.Occupy(newMapID, newX, newY, followerID)
}

// AllFollowers iterates all in-world followers.
func (s *State) AllFollowers(fn func(*FollowerInfo)) {
	for _, f := range s.followers {
		fn(f)
	}
}

// FollowerCount returns total follower count in-world.
func (s *State) FollowerCount() int {
	return len(s.followers)
}

// GetNearbyFollowers returns all alive followers visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyFollowers(x, y int32, mapID int16) []*FollowerInfo {
	nearbyIDs := s.npcAoi.GetNearby(x, y, mapID)
	var result []*FollowerInfo
	for _, nid := range nearbyIDs {
		f := s.followers[nid]
		if f == nil || f.Dead {
			continue
		}
		dx := f.X - x
		dy := f.Y - y
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
			result = append(result, f)
		}
	}
	return result
}
