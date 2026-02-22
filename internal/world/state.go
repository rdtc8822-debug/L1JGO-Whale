package world

import (
	"time"

	"github.com/l1jgo/server/internal/net"
)

// PlayerInfo holds in-memory data for a player currently in-world.
// Accessed only from the game loop goroutine — no locks needed.
type PlayerInfo struct {
	SessionID uint64
	Session   *net.Session
	CharID    int32  // DB ID, used as object ID in packets
	Name      string
	X         int32
	Y         int32
	MapID     int16
	Heading   int16
	ClassID   int32 // GFX
	Level     int16
	Lawful    int32
	Title     string
	ClanID    int32
	ClanName  string
	ClanRank  int16
	ClassType int16 // 0=Prince, 1=Knight, 2=Elf, 3=Wizard, 4=DarkElf, 5=DragonKnight, 6=Illusionist
	HP        int16
	MaxHP     int16
	MP        int16
	MaxMP     int16
	Str       int16
	Dex       int16
	Con       int16
	Wis       int16
	Intel     int16
	Cha       int16
	Exp        int32 // cumulative total exp
	BonusStats int16 // number of bonus stat points already allocated (level 51+)
	Speed      byte  // 0=normal, 1=fast, etc.
	MoveSpeed  byte  // 0=normal, 1=hasted (green potion), 2=slowed
	BraveSpeed byte  // 0=none, 1=brave (attack speed), 3=elf brave
	HasteTicks  int   // remaining ticks for haste buff (0 = expired)
	BraveTicks  int   // remaining ticks for brave buff (0 = expired)
	WisdomTicks int   // remaining ticks for wisdom buff (0 = expired)
	WisdomSP    int16 // SP bonus from wisdom potion (removed when buff expires)
	AC         int16 // current AC (base 10 - equipment bonus; lower = better)
	MR         int16 // magic resistance
	HitMod     int16 // melee hit bonus from buffs
	DmgMod     int16 // melee damage bonus from buffs
	BowHitMod  int16 // bow hit bonus from buffs
	BowDmgMod  int16 // bow damage bonus from buffs
	SP         int16 // spell power bonus from buffs
	HPR        int16 // HP regen bonus from buffs (per regen tick)
	MPR        int16 // MP regen bonus from buffs (per regen tick)
	FireRes    int16 // fire resistance
	WaterRes   int16 // water resistance
	WindRes    int16 // wind resistance
	EarthRes   int16 // earth resistance
	Dodge      int16 // dodge bonus
	Food       int16 // satiety 0-225 (225=full); sent in S_STATUS
	PKCount    int32 // PK kill count
	PinkName      bool // temporary red name (180 seconds after attacking blue player)
	PinkNameTicks int  // remaining ticks for pink name timer
	RegenHPAcc int   // HP regen accumulator: counts 1-second ticks since last HP regen

	Dead       bool  // true when HP <= 0, waiting for restart
	Invisible  bool  // true when under Invisibility
	Paralyzed  bool  // true when frozen/stunned/bound
	Sleeped    bool  // true when under sleep effect

	Inv          *Inventory // in-memory inventory
	Equip        Equipment  // equipped items (value type, zero-initialized = all slots empty)
	EquipBonuses EquipStats // cached equipment stat contributions (for diff on equip/unequip)

	// Cached current weapon visual byte (for S_PUT_OBJECT / S_CHANGE_DESC)
	CurrentWeapon byte

	// Pending teleport destination (set by teleport scroll/spell, executed by C_TELEPORT)
	TeleportX       int32
	TeleportY       int32
	TeleportMapID   int16
	TeleportHeading int16
	HasTeleport     bool // true when a teleport is prepared and waiting for C_TELEPORT confirmation

	// Teleport bookmarks
	Bookmarks []Bookmark

	// Known spell IDs (skill_id values the player has learned)
	KnownSpells []int32

	// Global cast cooldown: cannot cast any spell before this time (Java: isSkillDelay)
	SkillDelayUntil time.Time

	// Active buffs: skillID → remaining ticks. Decremented each tick; removed at 0.
	ActiveBuffs map[int32]*ActiveBuff

	// Warehouse: temporary cache while warehouse UI is open
	WarehouseItems []*WarehouseCache // loaded from DB on open, nil when closed
	WarehouseType  int16             // 3=personal, 4=elf, 5=clan

	// Party
	PartyID     int32  // 0=not in party
	PartyLeader bool

	// Trade
	TradePartnerID  int32      // CharID of trade partner (0 = not trading)
	TradeWindowOpen bool       // true after target accepted trade (windows are open)
	TradeOk         bool       // true when this side has pressed confirm
	TradeItems      []*InvItem // items offered in trade
	TradeGold       int32      // gold offered in trade

	// Pending yes/no dialog (S_Message_YN response tracking)
	PendingYesNoType int16 // 0=none, 252=trade confirm, 953=party invite, etc.
	PendingYesNoData int32 // related charID (trade partner or party inviter)

	// Party invite context: what type of party the inviter wants to create
	// Java: pc.setPartyType(type) — 0=normal, 1=auto-share
	PartyInviteType byte

	// Party position refresh: Java L1PartyRefresh runs every 25 seconds.
	// Counter decrements each tick; at 0, sends position refresh and resets.
	PartyRefreshTicks int
}

// WarehouseCache maps a temporary objectID to a DB warehouse item.
type WarehouseCache struct {
	TempObjID  int32
	DbID       int32 // warehouse_items.id in DB
	ItemID     int32
	Count      int32
	EnchantLvl int16
	Bless      int16
	Stackable  bool
	Identified bool
	UseType    byte // 0=etcitem, 1=weapon, 2=armor
	Name       string
	InvGfx     int32
	Weight     int32
}

// ActiveBuff tracks a single active buff/debuff on a player.
type ActiveBuff struct {
	SkillID      int32
	TicksLeft    int   // remaining ticks (0 = permanent until cancelled)
	// Stat deltas applied when buff started (reversed on removal)
	DeltaAC      int16
	DeltaStr     int16
	DeltaDex     int16
	DeltaCon     int16
	DeltaWis     int16
	DeltaIntel   int16
	DeltaCha     int16
	DeltaMaxHP   int16
	DeltaMaxMP   int16
	DeltaHitMod  int16
	DeltaDmgMod  int16
	DeltaSP      int16
	DeltaMR      int16
	DeltaHPR     int16
	DeltaMPR     int16
	DeltaBowHit  int16
	DeltaBowDmg  int16
	DeltaFireRes  int16
	DeltaWaterRes int16
	DeltaWindRes  int16
	DeltaEarthRes int16
	DeltaDodge    int16
	// Special flags for non-stat effects
	SetMoveSpeed  byte // if > 0, the buff set MoveSpeed to this value
	SetBraveSpeed byte // if > 0, the buff set BraveSpeed to this value
	SetInvisible  bool // buff made player invisible
	SetParalyzed  bool // buff paralyzed/froze player
	SetSleeped    bool // buff put player to sleep
}

// HasBuff returns true if the player has the given skill effect active.
func (p *PlayerInfo) HasBuff(skillID int32) bool {
	if p.ActiveBuffs == nil {
		return false
	}
	_, ok := p.ActiveBuffs[skillID]
	return ok
}

// AddBuff adds or replaces a buff. Returns the old buff if replaced, for stat reversal.
func (p *PlayerInfo) AddBuff(buff *ActiveBuff) *ActiveBuff {
	if p.ActiveBuffs == nil {
		p.ActiveBuffs = make(map[int32]*ActiveBuff)
	}
	old := p.ActiveBuffs[buff.SkillID]
	p.ActiveBuffs[buff.SkillID] = buff
	return old
}

// RemoveBuff removes a buff and returns it for stat reversal, or nil if not found.
func (p *PlayerInfo) RemoveBuff(skillID int32) *ActiveBuff {
	if p.ActiveBuffs == nil {
		return nil
	}
	old := p.ActiveBuffs[skillID]
	delete(p.ActiveBuffs, skillID)
	return old
}

// Bookmark is a saved teleport location for a player.
type Bookmark struct {
	ID    int32  // unique bookmark ID (auto-increment from DB)
	Name  string // display name
	X     int32
	Y     int32
	MapID int16
}

// State tracks all players and NPCs currently in-world.
// Single-goroutine access only (game loop).
type State struct {
	bySession map[uint64]*PlayerInfo // SessionID → PlayerInfo
	byCharID  map[int32]*PlayerInfo  // CharID → PlayerInfo
	byName    map[string]*PlayerInfo // CharName → PlayerInfo
	aoi       *AOIGrid

	npcs    map[int32]*NpcInfo // NPC object ID → NpcInfo
	npcList []*NpcInfo         // all NPCs (for tick iteration)

	groundItems map[int32]*GroundItem // ground item object ID → GroundItem

	Parties     *PartyManager
	ChatParties *ChatPartyManager
}

func NewState() *State {
	return &State{
		bySession:   make(map[uint64]*PlayerInfo),
		byCharID:    make(map[int32]*PlayerInfo),
		byName:      make(map[string]*PlayerInfo),
		aoi:         NewAOIGrid(),
		Parties:     NewPartyManager(),
		ChatParties: NewChatPartyManager(),
		npcs:        make(map[int32]*NpcInfo),
		groundItems: make(map[int32]*GroundItem),
	}
}

// AddPlayer registers a player in the world. Returns the PlayerInfo.
func (s *State) AddPlayer(p *PlayerInfo) {
	s.bySession[p.SessionID] = p
	s.byCharID[p.CharID] = p
	s.byName[p.Name] = p
	s.aoi.Add(p.SessionID, p.X, p.Y, p.MapID)
}

// RemovePlayer removes a player from the world.
func (s *State) RemovePlayer(sessionID uint64) *PlayerInfo {
	p, ok := s.bySession[sessionID]
	if !ok {
		return nil
	}
	s.aoi.Remove(sessionID, p.X, p.Y, p.MapID)
	delete(s.bySession, sessionID)
	delete(s.byCharID, p.CharID)
	delete(s.byName, p.Name)
	return p
}

// GetBySession returns a player by session ID.
func (s *State) GetBySession(sessionID uint64) *PlayerInfo {
	return s.bySession[sessionID]
}

// GetByCharID returns a player by character DB ID.
func (s *State) GetByCharID(charID int32) *PlayerInfo {
	return s.byCharID[charID]
}

// GetByName returns a player by character name.
func (s *State) GetByName(name string) *PlayerInfo {
	return s.byName[name]
}

// UpdatePosition moves a player and updates AOI grid.
func (s *State) UpdatePosition(sessionID uint64, newX, newY int32, newMapID int16, heading int16) {
	p := s.bySession[sessionID]
	if p == nil {
		return
	}
	oldX, oldY, oldMap := p.X, p.Y, p.MapID
	p.X = newX
	p.Y = newY
	p.MapID = newMapID
	p.Heading = heading
	s.aoi.Move(sessionID, oldX, oldY, oldMap, newX, newY, newMapID)
}

// GetNearbyPlayers returns all players visible to the given position.
// Uses Chebyshev distance <= 20 (matching Java PC_RECOGNIZE_RANGE).
func (s *State) GetNearbyPlayers(x, y int32, mapID int16, excludeSession uint64) []*PlayerInfo {
	nearbyIDs := s.aoi.GetNearby(x, y, mapID)
	result := make([]*PlayerInfo, 0, len(nearbyIDs))
	for _, sid := range nearbyIDs {
		if sid == excludeSession {
			continue
		}
		p := s.bySession[sid]
		if p == nil {
			continue
		}
		// Chebyshev distance check
		dx := p.X - x
		dy := p.Y - y
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
			result = append(result, p)
		}
	}
	return result
}

// PlayerCount returns the number of players in-world.
func (s *State) PlayerCount() int {
	return len(s.bySession)
}

// AllPlayers iterates all in-world players.
func (s *State) AllPlayers(fn func(*PlayerInfo)) {
	for _, p := range s.bySession {
		fn(p)
	}
}

// --- NPC methods ---

// AddNpc registers an NPC in the world.
func (s *State) AddNpc(npc *NpcInfo) {
	s.npcs[npc.ID] = npc
	s.npcList = append(s.npcList, npc)
}

// GetNpc returns an NPC by its object ID.
func (s *State) GetNpc(id int32) *NpcInfo {
	return s.npcs[id]
}

// GetNearbyNpcs returns all alive NPCs visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyNpcs(x, y int32, mapID int16) []*NpcInfo {
	var result []*NpcInfo
	for _, npc := range s.npcs {
		if npc.Dead || npc.MapID != mapID {
			continue
		}
		dx := npc.X - x
		dy := npc.Y - y
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
			result = append(result, npc)
		}
	}
	return result
}

// NpcList returns the full NPC list for tick iteration (spawn/respawn system).
func (s *State) NpcList() []*NpcInfo {
	return s.npcList
}

// NpcCount returns total NPC count.
func (s *State) NpcCount() int {
	return len(s.npcs)
}

// GetNearbyPlayersAt returns all players near a position (for NPC broadcasting).
func (s *State) GetNearbyPlayersAt(x, y int32, mapID int16) []*PlayerInfo {
	return s.GetNearbyPlayers(x, y, mapID, 0) // 0 = no exclude
}

// --- Ground item methods ---

// AddGroundItem registers a ground item in the world.
func (s *State) AddGroundItem(item *GroundItem) {
	s.groundItems[item.ID] = item
}

// RemoveGroundItem removes a ground item from the world.
func (s *State) RemoveGroundItem(id int32) *GroundItem {
	item, ok := s.groundItems[id]
	if !ok {
		return nil
	}
	delete(s.groundItems, id)
	return item
}

// GetGroundItem returns a ground item by its object ID.
func (s *State) GetGroundItem(id int32) *GroundItem {
	return s.groundItems[id]
}

// GetNearbyGroundItems returns all ground items visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyGroundItems(x, y int32, mapID int16) []*GroundItem {
	var result []*GroundItem
	for _, item := range s.groundItems {
		if item.MapID != mapID {
			continue
		}
		dx := item.X - x
		dy := item.Y - y
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
			result = append(result, item)
		}
	}
	return result
}

// TickGroundItems decrements TTL on ground items and returns expired ones.
func (s *State) TickGroundItems() []*GroundItem {
	var expired []*GroundItem
	for id, item := range s.groundItems {
		if item.TTL > 0 {
			item.TTL--
			if item.TTL <= 0 {
				expired = append(expired, item)
				delete(s.groundItems, id)
			}
		}
	}
	return expired
}
