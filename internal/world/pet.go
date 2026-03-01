package world

// PetStatus represents the AI behavior mode of a pet.
// Values must match Java L1PetInstance exactly (3.80C client expects these).
type PetStatus int

const (
	PetStatusAggressive PetStatus = 1 // 攻擊態勢 — actively attack enemies
	PetStatusDefensive  PetStatus = 2 // 防禦態勢 — counterattack only
	PetStatusRest       PetStatus = 3 // 休憩 — stay in place, no combat
	PetStatusExtend     PetStatus = 4 // 散開 — move away from master
	PetStatusAlert      PetStatus = 5 // 警戒 — guard position, attack nearby
	PetStatusDismiss    PetStatus = 6 // 解散 — liberate (convert to wild NPC)
	PetStatusWhistle    PetStatus = 7 // 召回 — move to master, then rest
)

// PetInvItem represents an equipment item held by a pet.
type PetInvItem struct {
	ItemID   int32
	ObjectID int32  // same as InvItem.ObjectID (for packet reference)
	Name     string
	GfxID    int32
	Count    int32
	Equipped bool
	IsWeapon bool // true = tooth/weapon, false = armor
	Bless    byte
}

// PetInfo holds in-memory data for a pet currently spawned in-world.
// Accessed only from the game loop goroutine — no locks needed.
type PetInfo struct {
	ID          int32  // NPC-range object ID for packets (allocated from NPC ID space)
	OwnerCharID int32  // CharID of the player who owns this pet
	ItemObjID   int32  // Amulet item ObjectID — primary key linking to DB
	NpcID       int32  // Current NPC template ID (changes on evolution)
	Name        string
	Level       int16
	HP          int32
	MaxHP       int32
	MP          int32
	MaxMP       int32
	Exp         int32
	Lawful      int32

	// Appearance (from NPC template)
	GfxID     int32
	NameID    string // client string table key (e.g. "$1554")
	MoveSpeed int16  // move animation speed (0 = default)

	X       int32
	Y       int32
	MapID   int16
	Heading int16

	Status PetStatus // current AI behavior mode

	// Combat stats (base from NPC template)
	AC       int16
	STR      int16
	DEX      int16
	CON      int16
	MR       int16
	AtkDmg   int32
	AtkSpeed int16 // attack animation speed (ms, 0 = default)
	Ranged   int16 // attack range (1 = melee, >1 = ranged)

	// Equipment bonuses (added on top of base stats)
	HitByWeapon    int // hit modifier from equipped weapon
	DamageByWeapon int // damage modifier from equipped weapon
	AddStr         int
	AddCon         int
	AddDex         int
	AddInt         int
	AddWis         int
	AddHP          int // max HP bonus from equipment
	AddMP          int // max MP bonus from equipment
	AddSP          int
	MDef           int

	// Pet inventory (equipment items given by master; max ~2)
	Items []*PetInvItem

	// Equipped pet items (object IDs; 0 = none)
	WeaponObjID int32
	ArmorObjID  int32

	// AI state (same pattern as SummonInfo)
	AggroTarget   int32 // NPC object ID of hate target (0 = no target)
	AggroPlayerID int32 // CharID of player target (for PvP; 0 = none)
	AttackTimer   int   // ticks until next attack (cooldown)
	MoveTimer     int   // ticks until next move towards target
	HomeX         int32 // alert mode anchor position
	HomeY         int32

	Dead  bool
	Dirty bool // needs persistence
}

// --- State pet management ---

// AddPet registers a pet in the world. It occupies a tile and is visible via NPC AOI.
func (s *State) AddPet(pet *PetInfo) {
	if s.pets == nil {
		s.pets = make(map[int32]*PetInfo)
	}
	s.pets[pet.ID] = pet
	s.npcAoi.Add(pet.ID, pet.X, pet.Y, pet.MapID)
	s.entity.Occupy(pet.MapID, pet.X, pet.Y, pet.ID)
}

// RemovePet removes a pet from the world and frees its tile.
func (s *State) RemovePet(petID int32) *PetInfo {
	if s.pets == nil {
		return nil
	}
	pet, ok := s.pets[petID]
	if !ok {
		return nil
	}
	s.npcAoi.Remove(pet.ID, pet.X, pet.Y, pet.MapID)
	s.entity.Vacate(pet.MapID, pet.X, pet.Y, pet.ID)
	delete(s.pets, petID)
	return pet
}

// GetPet returns a pet by its object ID, or nil if not found.
func (s *State) GetPet(petID int32) *PetInfo {
	if s.pets == nil {
		return nil
	}
	return s.pets[petID]
}

// GetPetsByOwner returns all pets belonging to a player.
func (s *State) GetPetsByOwner(charID int32) []*PetInfo {
	var result []*PetInfo
	for _, pet := range s.pets {
		if pet.OwnerCharID == charID {
			result = append(result, pet)
		}
	}
	return result
}

// GetPetByItemObjID returns a pet by its amulet item object ID, or nil if not spawned.
func (s *State) GetPetByItemObjID(itemObjID int32) *PetInfo {
	for _, pet := range s.pets {
		if pet.ItemObjID == itemObjID {
			return pet
		}
	}
	return nil
}

// UpdatePetPosition moves a pet and updates AOI + entity grids.
func (s *State) UpdatePetPosition(petID int32, newX, newY int32, heading int16) {
	pet := s.pets[petID]
	if pet == nil {
		return
	}
	oldX, oldY := pet.X, pet.Y
	pet.X = newX
	pet.Y = newY
	pet.Heading = heading
	s.npcAoi.Move(petID, oldX, oldY, pet.MapID, newX, newY, pet.MapID)
	s.entity.Move(pet.MapID, oldX, oldY, newX, newY, petID)
}

// TeleportPet moves a pet to a new location (possibly different map).
// Updates AOI grid + entity grid for cross-map movement.
func (s *State) TeleportPet(petID int32, newX, newY int32, newMapID, heading int16) {
	pet := s.pets[petID]
	if pet == nil {
		return
	}
	oldX, oldY, oldMap := pet.X, pet.Y, pet.MapID
	s.npcAoi.Remove(petID, oldX, oldY, oldMap)
	s.entity.Vacate(oldMap, oldX, oldY, petID)
	pet.X = newX
	pet.Y = newY
	pet.MapID = newMapID
	pet.Heading = heading
	s.npcAoi.Add(petID, newX, newY, newMapID)
	s.entity.Occupy(newMapID, newX, newY, petID)
}

// PetDied releases the tile occupied by a dead pet (keeps pet in world for collection).
func (s *State) PetDied(pet *PetInfo) {
	s.entity.Vacate(pet.MapID, pet.X, pet.Y, pet.ID)
}

// PetCount returns total pet count in-world.
func (s *State) PetCount() int {
	return len(s.pets)
}

// AllPets iterates all in-world pets.
func (s *State) AllPets(fn func(*PetInfo)) {
	for _, pet := range s.pets {
		fn(pet)
	}
}
