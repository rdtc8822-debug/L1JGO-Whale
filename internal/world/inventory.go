package world

import (
	"math"
	"math/rand"
	"sync/atomic"
)

// RandInt returns a random int in [0, n). Safe to call from game loop goroutine.
func RandInt(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.Intn(n)
}

const (
	MaxInventorySize = 180
	AdenaItemID      = 40308
)

// itemObjIDCounter generates unique item object IDs.
// Starts at 500_000_000 to avoid collision with char IDs and NPC IDs.
var itemObjIDCounter atomic.Int32

func init() {
	itemObjIDCounter.Store(500_000_000)
}

// NextItemObjID returns a unique object ID for an item instance.
func NextItemObjID() int32 {
	return itemObjIDCounter.Add(1)
}

// InvItem represents a single item instance in a player's inventory.
type InvItem struct {
	ObjectID   int32  // unique per instance
	ItemID     int32  // template ID
	Name       string // display name
	InvGfx     int32  // inventory graphic ID
	Count      int32  // stack count (1 for non-stackable)
	Identified bool
	EnchantLvl byte
	Bless      byte   // 0=normal, 1=blessed, 2=cursed, >=128=sealed
	Stackable  bool
	Weight     int32  // per-unit weight
	UseType    byte
	Equipped   bool   // true if currently worn/wielded
}

// Inventory holds a player's in-memory item list.
// Accessed only from the game loop goroutine.
type Inventory struct {
	Items []*InvItem
}

// NewInventory creates an empty inventory.
func NewInventory() *Inventory {
	return &Inventory{
		Items: make([]*InvItem, 0, 16),
	}
}

// FindByItemID returns the first item matching the template ID (for stackable items).
func (inv *Inventory) FindByItemID(itemID int32) *InvItem {
	for _, it := range inv.Items {
		if it.ItemID == itemID {
			return it
		}
	}
	return nil
}

// FindByObjectID returns the item with the given object ID.
func (inv *Inventory) FindByObjectID(objectID int32) *InvItem {
	for _, it := range inv.Items {
		if it.ObjectID == objectID {
			return it
		}
	}
	return nil
}

// Size returns the number of item slots used.
func (inv *Inventory) Size() int {
	return len(inv.Items)
}

// IsFull returns true if inventory is at max capacity.
func (inv *Inventory) IsFull() bool {
	return len(inv.Items) >= MaxInventorySize
}

// AddItem adds or stacks an item. Returns the affected item (new or existing).
// Does NOT send packets — caller is responsible.
func (inv *Inventory) AddItem(itemID int32, count int32, name string, invGfx int32, weight int32, stackable bool, bless byte) *InvItem {
	if stackable {
		existing := inv.FindByItemID(itemID)
		if existing != nil {
			existing.Count += count
			return existing
		}
	}

	item := &InvItem{
		ObjectID:   NextItemObjID(),
		ItemID:     itemID,
		Name:       name,
		InvGfx:     invGfx,
		Count:      count,
		Identified: true,
		Stackable:  stackable,
		Weight:     weight,
		Bless:      bless,
	}
	inv.Items = append(inv.Items, item)
	return item
}

// RemoveItem removes count from a stackable item or removes the item entirely.
// Returns true if the item was fully removed (slot freed), false if just decremented.
func (inv *Inventory) RemoveItem(objectID int32, count int32) (removed bool) {
	for i, it := range inv.Items {
		if it.ObjectID == objectID {
			if it.Stackable && it.Count > count {
				it.Count -= count
				return false
			}
			// Remove slot entirely
			inv.Items = append(inv.Items[:i], inv.Items[i+1:]...)
			return true
		}
	}
	return false
}

// GetAdena returns the current adena count.
func (inv *Inventory) GetAdena() int32 {
	item := inv.FindByItemID(AdenaItemID)
	if item == nil {
		return 0
	}
	return item.Count
}

// TotalWeight returns the total weight of all items (in 1/1000 units).
// TotalWeight returns the total carried weight in display units.
// Java: each item weight = max(count * templateWeight / 1000, 1); sum all.
func (inv *Inventory) TotalWeight() int32 {
	var total int32
	for _, it := range inv.Items {
		if it.Weight == 0 {
			continue
		}
		w := it.Count * it.Weight / 1000
		if w < 1 {
			w = 1
		}
		total += w
	}
	return total
}

// MaxWeight calculates max carrying capacity from STR/CON.
// Java: 150 * floor(0.6*STR + 0.4*CON + 1)
// Equipment/buff weight reduction bonuses can be applied by caller.
func MaxWeight(str, con int16) int32 {
	return int32(150 * math.Floor(0.6*float64(str)+0.4*float64(con)+1))
}

// Weight242 returns weight as a 0-242 scale value for the client UI bar.
// Java: round((currentWeight / maxWeight) * 242), clamped to 0-242.
func (inv *Inventory) Weight242(maxWeight int32) byte {
	if maxWeight <= 0 {
		return 0
	}
	total := inv.TotalWeight()
	if total <= 0 {
		return 0
	}
	if total >= maxWeight {
		return 242
	}
	w := float64(total) * 242.0 / float64(maxWeight)
	v := int(math.Round(w))
	if v > 242 {
		v = 242
	}
	return byte(v)
}

// IsOverWeight returns true if adding the given raw template weight would exceed capacity.
func (inv *Inventory) IsOverWeight(addWeight int32, maxWeight int32) bool {
	extra := addWeight / 1000
	if extra < 1 && addWeight > 0 {
		extra = 1
	}
	return inv.TotalWeight()+extra >= maxWeight
}

// EffectiveBless returns the bless byte for inventory packets.
// Unidentified items are displayed as bless=3 (dark/gray name) by the client.
func EffectiveBless(item *InvItem) byte {
	if !item.Identified {
		if item.Bless >= 128 {
			return item.Bless // preserve sealed state
		}
		return 3 // unidentified
	}
	return item.Bless
}
