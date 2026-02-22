package world

import "sync/atomic"

// npcIDCounter generates unique NPC object IDs.
// Starts at 200_000_000 to avoid collision with character DB IDs.
var npcIDCounter atomic.Int32

func init() {
	npcIDCounter.Store(200_000_000)
}

// NextNpcID returns a unique object ID for an NPC instance.
func NextNpcID() int32 {
	return npcIDCounter.Add(1)
}

// NpcInfo holds runtime data for an NPC currently in-world.
// Accessed only from the game loop goroutine — no locks.
type NpcInfo struct {
	ID      int32 // unique object ID (from NextNpcID)
	NpcID   int32 // template ID
	Impl    string // L1Monster, L1Merchant, L1Guard, etc.
	GfxID   int32
	Name    string
	NameID  string // client string table key (e.g. "$936")
	Level   int16
	X       int32
	Y       int32
	MapID   int16
	Heading int16
	HP      int32
	MaxHP   int32
	MP      int32
	MaxMP   int32
	AC      int16
	STR     int16
	DEX     int16
	Exp     int32  // exp reward on kill
	Lawful  int32
	Size    string // "small" or "large"
	MR      int16
	Undead  bool
	Agro    bool   // true = aggressive, attacks players on sight
	AtkDmg  int32  // damage per attack (simplified: Level + STR/3)
	Ranged  int16  // attack range (1 = melee, >1 = ranged attacker)
	AtkSpeed   int16 // attack animation speed (ms, 0 = default)
	MoveSpeed  int16 // passive/move speed (ms, 0 = default)

	// Spawn data for respawning
	SpawnX       int32
	SpawnY       int32
	SpawnMapID   int16
	RespawnDelay int // seconds

	// State
	Dead         bool
	RespawnTimer int // ticks remaining until respawn

	// AI state
	AggroTarget  uint64 // SessionID of hate target (0 = no target)
	AttackTimer  int    // ticks until next attack (cooldown)
	MoveTimer    int    // ticks until next move towards target

	// Idle wandering state (Java: _randomMoveDistance / _randomMoveDirection)
	WanderDist   int   // remaining tiles to walk in current wander direction
	WanderDir    int16 // current wander heading (0-7)
	WanderTimer  int   // ticks until next wander step
}
