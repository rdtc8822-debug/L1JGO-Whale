package world

import (
	"math/rand"
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
	PKCount       int32 // PK kill count
	PinkName      bool  // temporary red name (180 seconds after attacking blue player)
	PinkNameTicks int   // remaining ticks for pink name timer
	WantedTicks   int   // >0 = wanted by guards (24h = 432000 ticks at 200ms/tick)
	FightId       int32 // 0=無決鬥, >0=決鬥對手角色 ID（Java: L1PcInstance.fightId）
	RegenHPAcc int   // HP regen accumulator: counts 1-second ticks since last HP regen

	Dead             bool // true when HP <= 0, waiting for restart
	Invisible        bool // true when under Invisibility
	Paralyzed        bool // true when frozen/stunned/bound
	Sleeped          bool // true when under sleep effect
	Silenced         bool // 沉默狀態（沉默毒 / silence 技能）— 禁止施法
	AbsoluteBarrier  bool // 絕對屏障（skill 78）— 免疫所有傷害，攻擊/施法/使用道具時解除

	LastMoveTime int64 // time.Now().UnixNano() of last accepted move (0 = no throttle)

	TempCharGfx int32 // 0=use ClassID; >0=current polymorph GFX sprite
	PolyID      int32 // current polymorph poly_id (for equip/skill checks; 0=not polymorphed)
	ActiveSetID int   // armor set ID currently active (0=none); cleared when set is incomplete

	// Summon selection mode: true when "summonlist" dialog is open, waiting for player to pick a summon.
	// Set by executeSummonMonster when ring equipped; cleared by HandleNpcAction on numeric response.
	SummonSelectionMode bool

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

	// --- 中毒系統（Java L1Poison）---
	// PoisonType: 0=無, 1=傷害毒, 2=沉默毒, 3=麻痺毒延遲中, 4=麻痺毒已麻痺
	PoisonType      byte
	PoisonTicksLeft int    // 毒剩餘 ticks（傷害毒:150, 麻痺延遲:100, 麻痺:80）
	PoisonDmgTimer  int    // 傷害毒：距下次扣血的 tick 計數（每 15 tick 扣一次）
	PoisonDmgAmount int16  // 傷害毒每次扣血量（NPC攻擊:20, 毒咒:5）
	PoisonAttacker  uint64 // 施毒者 SessionID（傷害毒歸屬用）

	// --- 詛咒麻痺系統（Java L1CurseParalysis，與毒系統獨立）---
	// CurseType: 0=無, 1=詛咒延遲中, 2=詛咒已麻痺
	CurseType      byte
	CurseTicksLeft int // 詛咒剩餘 ticks（延遲:25, 麻痺:20）

	// AOI 可見性追蹤（VisibilitySystem 使用）
	Known *KnownEntities

	// Pending yes/no dialog (S_Message_YN response tracking)
	PendingYesNoType int16 // 0=none, 252=trade confirm, 953=party invite, etc.
	PendingYesNoData int32 // related charID (trade partner or party inviter)

	// Party invite context: what type of party the inviter wants to create
	// Java: pc.setPartyType(type) — 0=normal, 1=auto-share
	PartyInviteType byte

	// Party position refresh: Java L1PartyRefresh runs every 25 seconds.
	// Counter decrements each tick; at 0, sends position refresh and resets.
	PartyRefreshTicks int

	// Crafting: set when S_InputAmount is sent, cleared on C_Amount response.
	// Non-empty value means the next opcode 11 (C_HYPERTEXT_INPUT_RESULT) should be
	// interpreted as C_Amount (crafting batch response) instead of monlist (polymorph).
	PendingCraftAction string

	// Paginated teleport (Npc_Teleport): current browsing state
	TelePage     int    // current page (0-based)
	TeleCategory string // current category key (e.g., "A", "B", "H01")
	TeleNpcObjID int32  // NPC object ID for the teleport dialog

	// Buddy/friend list (persisted to DB, loaded on enter world)
	Buddies []BuddyEntry

	// Exclude/block list (session-only, max 16 entries, NOT persisted)
	ExcludeList []string

	// 物品使用延遲（runtime-only，不持久化）
	// key=DelayID (如 502=道具共用), value=到期時間
	ItemDelays map[int]time.Time

	// Dirty flag for batch persistence. Set to true when any persisted state
	// changes (position, HP/MP, exp, inventory, buffs). PersistenceSystem only
	// saves dirty players and resets this flag after each successful save.
	Dirty bool
}

// BuddyEntry represents a single buddy in the player's friend list.
type BuddyEntry struct {
	CharID int32
	Name   string
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
	SetInvisible        bool // buff made player invisible
	SetParalyzed        bool // buff paralyzed/froze player
	SetSleeped          bool // buff put player to sleep
	SetAbsoluteBarrier  bool // buff 設定了絕對屏障（到期/移除時清 flag）
}

// HasBuff returns true if the player has the given skill effect active.
func (p *PlayerInfo) HasBuff(skillID int32) bool {
	if p.ActiveBuffs == nil {
		return false
	}
	_, ok := p.ActiveBuffs[skillID]
	return ok
}

// GetBuff returns the active buff for a skillID, or nil if not found.
func (p *PlayerInfo) GetBuff(skillID int32) *ActiveBuff {
	if p.ActiveBuffs == nil {
		return nil
	}
	return p.ActiveBuffs[skillID]
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

// KnownPos 記錄已知實體的最後位置（用於離開視野時解鎖格子）。
type KnownPos struct{ X, Y int32 }

// KnownEntities 追蹤玩家目前視野中的已知實體（類似 Java knownObjects）。
// VisibilitySystem 每 2 tick 掃描一次，與此集合做 diff。
type KnownEntities struct {
	Players     map[int32]KnownPos // CharID → 位置
	Npcs        map[int32]KnownPos // NPC 實例 ID → 位置
	Summons     map[int32]KnownPos // 召喚獸 ID → 位置
	Dolls       map[int32]KnownPos // 魔法娃娃 ID → 位置
	Followers   map[int32]KnownPos // 隨從 ID → 位置
	Pets        map[int32]KnownPos // 寵物 ID → 位置
	GroundItems map[int32]KnownPos // 地面物品 ID → 位置
	Doors       map[int32]KnownPos // 門 ID → 位置
}

// NewKnownEntities 建立空白的已知實體集合。
func NewKnownEntities() *KnownEntities {
	return &KnownEntities{
		Players:     make(map[int32]KnownPos),
		Npcs:        make(map[int32]KnownPos),
		Summons:     make(map[int32]KnownPos),
		Dolls:       make(map[int32]KnownPos),
		Followers:   make(map[int32]KnownPos),
		Pets:        make(map[int32]KnownPos),
		GroundItems: make(map[int32]KnownPos),
		Doors:       make(map[int32]KnownPos),
	}
}

// Reset 清空所有已知實體（用於傳送、rejectMove 等場景）。
func (k *KnownEntities) Reset() {
	clear(k.Players)
	clear(k.Npcs)
	clear(k.Summons)
	clear(k.Dolls)
	clear(k.Followers)
	clear(k.Pets)
	clear(k.GroundItems)
	clear(k.Doors)
}

// Bookmark is a saved teleport location for a player.
type Bookmark struct {
	ID    int32  // unique bookmark ID (auto-increment from DB)
	Name  string // display name
	X     int32
	Y     int32
	MapID int16
}

// tileKey uniquely identifies a tile in the world (map + coordinates).
type tileKey struct {
	MapID int16
	X, Y  int32
}

// EntityGrid is a tile occupancy map for O(1) collision checks.
// Supports multiple occupants per tile (for monster stuck-crossing scenarios).
// Player CharIDs < 100,000; NPC IDs start at 200,000,000 — no overlap.
type EntityGrid struct {
	tiles map[tileKey]map[int32]struct{}
}

func newEntityGrid() *EntityGrid {
	return &EntityGrid{tiles: make(map[tileKey]map[int32]struct{})}
}

// Occupy marks an entity as occupying a tile.
func (g *EntityGrid) Occupy(mapID int16, x, y int32, entityID int32) {
	k := tileKey{MapID: mapID, X: x, Y: y}
	cell := g.tiles[k]
	if cell == nil {
		cell = make(map[int32]struct{}, 1)
		g.tiles[k] = cell
	}
	cell[entityID] = struct{}{}
}

// Vacate removes an entity from a tile.
func (g *EntityGrid) Vacate(mapID int16, x, y int32, entityID int32) {
	k := tileKey{MapID: mapID, X: x, Y: y}
	cell := g.tiles[k]
	if cell != nil {
		delete(cell, entityID)
		if len(cell) == 0 {
			delete(g.tiles, k)
		}
	}
}

// Move atomically vacates old tile and occupies new tile.
func (g *EntityGrid) Move(mapID int16, oldX, oldY, newX, newY int32, entityID int32) {
	if oldX == newX && oldY == newY {
		return
	}
	g.Vacate(mapID, oldX, oldY, entityID)
	g.Occupy(mapID, newX, newY, entityID)
}

// IsOccupied returns true if any entity other than excludeID occupies the tile.
func (g *EntityGrid) IsOccupied(mapID int16, x, y int32, excludeID int32) bool {
	k := tileKey{MapID: mapID, X: x, Y: y}
	cell := g.tiles[k]
	if len(cell) == 0 {
		return false
	}
	for id := range cell {
		if id != excludeID {
			return true
		}
	}
	return false
}

// OccupantAt returns the first occupant ID at the tile, or 0 if empty.
func (g *EntityGrid) OccupantAt(mapID int16, x, y int32) int32 {
	k := tileKey{MapID: mapID, X: x, Y: y}
	for id := range g.tiles[k] {
		return id
	}
	return 0
}

// State tracks all players and NPCs currently in-world.
// Single-goroutine access only (game loop).
type State struct {
	bySession map[uint64]*PlayerInfo // SessionID → PlayerInfo
	byCharID  map[int32]*PlayerInfo  // CharID → PlayerInfo
	byName    map[string]*PlayerInfo // CharName → PlayerInfo
	aoi       *AOIGrid
	npcAoi    *NpcAOIGrid
	entity    *EntityGrid

	npcs    map[int32]*NpcInfo // NPC object ID → NpcInfo
	npcList []*NpcInfo         // all NPCs (for tick iteration)

	doors    map[int32]*DoorInfo // door object ID → DoorInfo
	doorList []*DoorInfo         // all doors (for tick iteration)

	pets      map[int32]*PetInfo      // pet object ID → PetInfo
	summons   map[int32]*SummonInfo   // summon object ID → SummonInfo
	dolls     map[int32]*DollInfo     // doll object ID → DollInfo
	followers map[int32]*FollowerInfo // follower object ID → FollowerInfo

	groundItems map[int32]*GroundItem // ground item object ID → GroundItem

	Parties     *PartyManager
	ChatParties *ChatPartyManager
	Clans       *ClanManager

	// Weather & game time (accessed from game loop only)
	Weather  byte // current weather type (0=clear, 1-3=snow, 17-19=rain)
	LastHour int  // last game hour for hour-change detection (-1 = uninitialized)

	// 可重用 AOI 查詢 buffer（遊戲迴圈單線程，無需鎖）
	aoiBuf    []uint64
	npcAoiBuf []int32
}

// RandomizeWeather picks a random weather with weighted distribution.
// Java defaults weather to 4 (clear) and never auto-changes. In Go, we add
// some variety but keep clear weather dominant (~60%) to avoid constant rain/snow.
// Valid values: 0=clear, 1-3=snow, 17-19=rain (Java confirms 17-19, not 16).
func (s *State) RandomizeWeather() {
	roll := rand.Intn(10) // 0-9
	switch {
	case roll < 6: // 60% clear
		s.Weather = 0
	case roll < 8: // 20% snow (light)
		s.Weather = byte(1 + rand.Intn(3)) // 1, 2, or 3
	default: // 20% rain (light)
		s.Weather = byte(17 + rand.Intn(3)) // 17, 18, or 19
	}
}

func NewState() *State {
	return &State{
		bySession:   make(map[uint64]*PlayerInfo),
		byCharID:    make(map[int32]*PlayerInfo),
		byName:      make(map[string]*PlayerInfo),
		aoi:         NewAOIGrid(),
		npcAoi:      NewNpcAOIGrid(),
		entity:      newEntityGrid(),
		Parties:     NewPartyManager(),
		ChatParties: NewChatPartyManager(),
		Clans:       NewClanManager(),
		npcs:        make(map[int32]*NpcInfo),
		doors:       make(map[int32]*DoorInfo),
		pets:        make(map[int32]*PetInfo),
		summons:     make(map[int32]*SummonInfo),
		dolls:       make(map[int32]*DollInfo),
		followers:   make(map[int32]*FollowerInfo),
		groundItems: make(map[int32]*GroundItem),
		LastHour:    -1,
	}
}

// AddPlayer registers a player in the world.
func (s *State) AddPlayer(p *PlayerInfo) {
	s.bySession[p.SessionID] = p
	s.byCharID[p.CharID] = p
	s.byName[p.Name] = p
	s.aoi.Add(p.SessionID, p.X, p.Y, p.MapID)
	s.entity.Occupy(p.MapID, p.X, p.Y, p.CharID)
}

// RemovePlayer removes a player from the world.
func (s *State) RemovePlayer(sessionID uint64) *PlayerInfo {
	p, ok := s.bySession[sessionID]
	if !ok {
		return nil
	}
	s.aoi.Remove(sessionID, p.X, p.Y, p.MapID)
	s.entity.Vacate(p.MapID, p.X, p.Y, p.CharID)
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

// UpdatePosition moves a player and updates AOI grid + entity grid.
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
	s.entity.Move(oldMap, oldX, oldY, newX, newY, p.CharID)
}

// GetNearbyPlayers returns all players visible to the given position.
// Uses Chebyshev distance <= 20 (matching Java PC_RECOGNIZE_RANGE).
func (s *State) GetNearbyPlayers(x, y int32, mapID int16, excludeSession uint64) []*PlayerInfo {
	s.aoiBuf = s.aoi.GetNearbyInto(x, y, mapID, s.aoiBuf)
	nearbyIDs := s.aoiBuf
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
	s.npcAoi.Add(npc.ID, npc.X, npc.Y, npc.MapID)
	s.entity.Occupy(npc.MapID, npc.X, npc.Y, npc.ID)
}

// GetNpc returns an NPC by its object ID.
func (s *State) GetNpc(id int32) *NpcInfo {
	return s.npcs[id]
}

// GetNearbyNpcs returns all alive NPCs visible from the given position (Chebyshev <= 20).
// Uses NPC AOI grid for O(cells) lookup instead of O(N) full scan.
func (s *State) GetNearbyNpcs(x, y int32, mapID int16) []*NpcInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	result := make([]*NpcInfo, 0, len(nearbyIDs))
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil || npc.Dead {
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

// GetNearbyNpcsForVis 回傳附近可見 NPC（含屍體）供 VisibilitySystem 使用。
// 活著的 NPC + 死亡但 DeleteTimer > 0 的 NPC（屍體仍需顯示在客戶端）。
func (s *State) GetNearbyNpcsForVis(x, y int32, mapID int16) []*NpcInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	result := make([]*NpcInfo, 0, len(nearbyIDs))
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc == nil {
			continue
		}
		// 跳過已完成刪除階段的死亡 NPC（DeleteTimer 已歸零）
		if npc.Dead && npc.DeleteTimer <= 0 {
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

// UpdateNpcPosition moves an NPC and updates NPC AOI grid + entity grid.
// All NPC position changes MUST go through this method to keep indices consistent.
func (s *State) UpdateNpcPosition(npcID int32, newX, newY int32, heading int16) {
	npc := s.npcs[npcID]
	if npc == nil {
		return
	}
	oldX, oldY := npc.X, npc.Y
	npc.X = newX
	npc.Y = newY
	npc.Heading = heading
	s.npcAoi.Move(npcID, oldX, oldY, npc.MapID, newX, newY, npc.MapID)
	s.entity.Move(npc.MapID, oldX, oldY, newX, newY, npcID)
}

// RemoveNpc permanently removes an NPC from the world (used for taming, conversion).
// Unlike NpcDied, this also deletes the NPC from the internal maps.
func (s *State) RemoveNpc(npcID int32) *NpcInfo {
	npc, ok := s.npcs[npcID]
	if !ok {
		return nil
	}
	if !npc.Dead {
		s.npcAoi.Remove(npc.ID, npc.X, npc.Y, npc.MapID)
		s.entity.Vacate(npc.MapID, npc.X, npc.Y, npc.ID)
	}
	delete(s.npcs, npcID)
	// Remove from npcList (swap-delete for O(1))
	for i, n := range s.npcList {
		if n.ID == npcID {
			s.npcList[i] = s.npcList[len(s.npcList)-1]
			s.npcList = s.npcList[:len(s.npcList)-1]
			break
		}
	}
	return npc
}

// NpcDied 處理 NPC 死亡：釋放格子碰撞但保留 AOI（屍體需持續可見）。
// AOI 移除由 NpcCorpseCleanup 在 DeleteTimer 歸零後處理。
func (s *State) NpcDied(npc *NpcInfo) {
	s.entity.Vacate(npc.MapID, npc.X, npc.Y, npc.ID)
}

// NpcCorpseCleanup 從 NPC AOI 網格移除死亡 NPC（屍體消失階段）。
// 由 NpcRespawnSystem 在 DeleteTimer 歸零後呼叫。
func (s *State) NpcCorpseCleanup(npc *NpcInfo) {
	s.npcAoi.Remove(npc.ID, npc.X, npc.Y, npc.MapID)
}

// NpcRespawn re-adds a respawned NPC to the NPC AOI grid and entity grid.
// Call this after resetting the NPC's position and clearing Dead flag.
func (s *State) NpcRespawn(npc *NpcInfo) {
	s.npcAoi.Add(npc.ID, npc.X, npc.Y, npc.MapID)
	s.entity.Occupy(npc.MapID, npc.X, npc.Y, npc.ID)
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

// IsPlayerAt returns true if any alive player occupies the exact tile (excluding excludeSession).
func (s *State) IsPlayerAt(x, y int32, mapID int16, excludeSession uint64) bool {
	s.aoiBuf = s.aoi.GetNearbyInto(x, y, mapID, s.aoiBuf)
	nearbyIDs := s.aoiBuf
	for _, sid := range nearbyIDs {
		if sid == excludeSession {
			continue
		}
		p := s.bySession[sid]
		if p != nil && p.X == x && p.Y == y && p.MapID == mapID && !p.Dead {
			return true
		}
	}
	return false
}

// IsNpcAt returns true if any alive NPC occupies the exact tile.
// Uses NPC AOI grid for O(cells) lookup instead of O(N) full scan.
func (s *State) IsNpcAt(x, y int32, mapID int16) bool {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	for _, nid := range nearbyIDs {
		npc := s.npcs[nid]
		if npc != nil && npc.X == x && npc.Y == y && !npc.Dead {
			return true
		}
	}
	return false
}

// IsOccupied returns true if any alive entity (player or NPC) occupies the tile,
// excluding the given entity ID. O(1) lookup via EntityGrid.
func (s *State) IsOccupied(x, y int32, mapID int16, excludeID int32) bool {
	return s.entity.IsOccupied(mapID, x, y, excludeID)
}

// OccupantAt returns the first occupant entity ID at the tile, or 0 if empty.
func (s *State) OccupantAt(x, y int32, mapID int16) int32 {
	return s.entity.OccupantAt(mapID, x, y)
}

// VacateEntity removes an entity from the entity grid (for death, disconnect, etc.)
func (s *State) VacateEntity(mapID int16, x, y int32, entityID int32) {
	s.entity.Vacate(mapID, x, y, entityID)
}

// OccupyEntity adds an entity to the entity grid (for respawn, login, etc.)
func (s *State) OccupyEntity(mapID int16, x, y int32, entityID int32) {
	s.entity.Occupy(mapID, x, y, entityID)
}

// --- Door methods ---

// AddDoor registers a door in the world.
func (s *State) AddDoor(door *DoorInfo) {
	s.doors[door.ID] = door
	s.doorList = append(s.doorList, door)
}

// GetDoor returns a door by its object ID.
func (s *State) GetDoor(id int32) *DoorInfo {
	return s.doors[id]
}

// GetNearbyDoors returns all doors visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyDoors(x, y int32, mapID int16) []*DoorInfo {
	var result []*DoorInfo
	for _, door := range s.doors {
		if door.MapID != mapID {
			continue
		}
		dx := door.X - x
		dy := door.Y - y
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
			result = append(result, door)
		}
	}
	return result
}

// RemoveDoor removes a door by its object ID.
func (s *State) RemoveDoor(id int32) {
	if _, ok := s.doors[id]; !ok {
		return
	}
	delete(s.doors, id)
	for i, d := range s.doorList {
		if d.ID == id {
			s.doorList = append(s.doorList[:i], s.doorList[i+1:]...)
			break
		}
	}
}

// DoorCount returns total door count.
func (s *State) DoorCount() int {
	return len(s.doors)
}

// GetNearbyPets returns all alive pets visible from the given position (Chebyshev <= 20).
func (s *State) GetNearbyPets(x, y int32, mapID int16) []*PetInfo {
	s.npcAoiBuf = s.npcAoi.GetNearbyInto(x, y, mapID, s.npcAoiBuf)
	nearbyIDs := s.npcAoiBuf
	var result []*PetInfo
	for _, nid := range nearbyIDs {
		pet := s.pets[nid]
		if pet == nil || pet.Dead {
			continue
		}
		dx := pet.X - x
		dy := pet.Y - y
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
			result = append(result, pet)
		}
	}
	return result
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
