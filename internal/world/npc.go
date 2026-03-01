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
	PoisonAtk  byte  // 怪物施毒能力（從模板載入）: 0=無, 1=傷害毒, 2=沉默毒, 4=麻痺毒

	// Spawn data for respawning
	SpawnX       int32
	SpawnY       int32
	SpawnMapID   int16
	RespawnDelay int // seconds

	// State
	Dead         bool
	DeleteTimer  int // ticks until S_RemoveObject is sent (Java: NPC_DELETION_TIME, default 10s = 50 ticks)
	RespawnTimer int // ticks remaining until respawn

	// AI state — 仇恨系統
	AggroTarget  uint64           // SessionID of hate target (0 = no target)，由仇恨列表驅動
	HateList     map[uint64]int32 // 仇恨列表 — key=SessionID, value=累積傷害仇恨值
	AttackTimer  int    // ticks until next attack (cooldown)
	MoveTimer    int    // ticks until next move towards target
	StuckTicks   int    // consecutive ticks blocked by another entity (for stuck detection)

	// Idle wandering state (Java: _randomMoveDistance / _randomMoveDirection)
	WanderDist   int   // remaining tiles to walk in current wander direction
	WanderDir    int16 // current wander heading (0-7)
	WanderTimer  int   // ticks until next wander step

	// 負面狀態（debuff）
	Paralyzed     bool           // 麻痺/凍結/暈眩 — 跳過所有 AI 行為
	Sleeped       bool           // 睡眠 — 跳過所有 AI 行為，受傷時解除
	ActiveDebuffs map[int32]int  // skillID → 剩餘 ticks（NPC 不需 stat delta，只需計時）

	// 法術中毒系統（Java L1DamagePoison 對 NPC）
	PoisonDmgAmt      int32  // 每次扣血量（0=無毒）
	PoisonDmgTimer    int    // 距下次扣血的 tick 計數（每 15 tick 扣一次）
	PoisonAttackerSID uint64 // 施毒者 SessionID（仇恨歸屬用）
}

// HasDebuff 檢查 NPC 是否有指定 debuff。
func (n *NpcInfo) HasDebuff(skillID int32) bool {
	if n.ActiveDebuffs == nil {
		return false
	}
	_, ok := n.ActiveDebuffs[skillID]
	return ok
}

// AddDebuff 對 NPC 施加 debuff（skillID → ticks）。
func (n *NpcInfo) AddDebuff(skillID int32, ticks int) {
	if n.ActiveDebuffs == nil {
		n.ActiveDebuffs = make(map[int32]int)
	}
	n.ActiveDebuffs[skillID] = ticks
}

// RemoveDebuff 移除 NPC 的指定 debuff。
func (n *NpcInfo) RemoveDebuff(skillID int32) {
	if n.ActiveDebuffs != nil {
		delete(n.ActiveDebuffs, skillID)
	}
}
