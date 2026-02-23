package persist

import (
	"context"

	"github.com/l1jgo/server/internal/world"
)

// ItemRow represents a persisted inventory item.
type ItemRow struct {
	ID         int32
	CharID     int32
	ItemID     int32
	Count      int32
	EnchantLvl int16
	Bless      int16
	Equipped   bool
	Identified bool
	EquipSlot  int16
	ObjID      int32 // persisted ObjectID for shortcut bar stability
}

type ItemRepo struct {
	db *DB
}

func NewItemRepo(db *DB) *ItemRepo {
	return &ItemRepo{db: db}
}

// LoadByCharID returns all items belonging to a character.
func (r *ItemRepo) LoadByCharID(ctx context.Context, charID int32) ([]ItemRow, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, char_id, item_id, count, enchant_lvl, bless, equipped, identified, equip_slot, obj_id
		 FROM character_items WHERE char_id = $1`, charID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ItemRow
	for rows.Next() {
		var it ItemRow
		if err := rows.Scan(
			&it.ID, &it.CharID, &it.ItemID, &it.Count,
			&it.EnchantLvl, &it.Bless, &it.Equipped, &it.Identified, &it.EquipSlot,
			&it.ObjID,
		); err != nil {
			return nil, err
		}
		result = append(result, it)
	}
	return result, rows.Err()
}

// MaxObjID returns the maximum obj_id across all character items.
// Used on startup to initialize the ObjectID counter above all persisted values.
func (r *ItemRepo) MaxObjID(ctx context.Context) (int32, error) {
	var maxID int32
	err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(obj_id), 0) FROM character_items`,
	).Scan(&maxID)
	return maxID, err
}

// SaveInventory replaces all items for a character (delete + bulk insert).
// Persists item.ObjectID as obj_id for shortcut bar reference stability.
func (r *ItemRepo) SaveInventory(ctx context.Context, charID int32, inv *world.Inventory, equip *world.Equipment) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Delete all existing items for this character
	if _, err := tx.Exec(ctx, `DELETE FROM character_items WHERE char_id = $1`, charID); err != nil {
		return err
	}

	// Insert current inventory with persisted ObjectID
	for _, item := range inv.Items {
		equipSlot := int16(0)
		if item.Equipped {
			// Find which slot this item is in
			for s := world.EquipSlot(1); s < world.SlotMax; s++ {
				if equip.Get(s) == item {
					equipSlot = int16(s)
					break
				}
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO character_items (char_id, item_id, count, enchant_lvl, bless, equipped, identified, equip_slot, obj_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			charID, item.ItemID, item.Count, int16(item.EnchantLvl), int16(item.Bless),
			item.Equipped, item.Identified, equipSlot, item.ObjectID,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
