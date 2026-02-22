package world

import "sync/atomic"

// groundItemIDCounter generates unique object IDs for ground items.
// Starts at 700_000_000 to avoid collision with char/NPC/inventory IDs.
var groundItemIDCounter atomic.Int32

func init() {
	groundItemIDCounter.Store(700_000_000)
}

// NextGroundItemID returns a unique object ID for a ground item.
func NextGroundItemID() int32 {
	return groundItemIDCounter.Add(1)
}

// GroundItem represents an item on the ground that players can see and pick up.
// Not persisted to DB — exists only in memory.
type GroundItem struct {
	ID         int32  // unique ground object ID
	ItemID     int32  // template ID
	Count      int32  // stack count
	EnchantLvl byte   // enchant level
	Name       string // display name
	GrdGfx     int32  // ground visual GFX ID
	X          int32
	Y          int32
	MapID      int16
	OwnerID    int32 // CharID of dropper (0 = anyone can pick up)
	TTL        int   // ticks remaining until auto-delete (0 = permanent)
}
