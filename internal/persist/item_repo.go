package persist

import (
	"context"
	"time"

	"github.com/l1jgo/server/internal/world"
)

// ItemRow represents a persisted inventory item.
type ItemRow struct {
	ID               int32
	CharID           int32
	ItemID           int32
	Count            int32
	EnchantLvl       int16
	Bless            int16
	Equipped         bool
	Identified       bool
	EquipSlot        int16
	ObjID            int32 // persisted ObjectID for shortcut bar stability
	Durability       int16 // weapon durability (0=perfect, higher=more damaged, range 0-127)
	AttrEnchantKind  int16 // 元素屬性種類 (0=無, 1=地, 2=火, 4=水, 8=風)
	AttrEnchantLevel int16 // 元素屬性強化階段 (0-5)
	InnKeyID         int32 // 旅館鑰匙 ID（0=非鑰匙）
	InnNpcID         int32 // 旅館 NPC 模板 ID
	InnHall          bool  // 是否為會議室鑰匙
	InnDueTime       int64 // 租約到期時間（Unix 秒）
	ChargeCount      int16 // 魔杖充能次數
	LastUsedAt       int64 // delay_effect 上次使用時間（Unix 秒）
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
		`SELECT id, char_id, item_id, count, enchant_lvl, bless, equipped, identified, equip_slot, obj_id,
		        COALESCE(durability, 0),
		        COALESCE(attr_enchant_kind, 0), COALESCE(attr_enchant_level, 0),
		        COALESCE(inn_key_id, 0), COALESCE(inn_npc_id, 0),
		        COALESCE(inn_hall, FALSE), COALESCE(EXTRACT(EPOCH FROM inn_due_time)::BIGINT, 0),
		        COALESCE(charge_count, 0), COALESCE(EXTRACT(EPOCH FROM last_used_at)::BIGINT, 0)
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
			&it.ObjID, &it.Durability,
			&it.AttrEnchantKind, &it.AttrEnchantLevel,
			&it.InnKeyID, &it.InnNpcID, &it.InnHall, &it.InnDueTime,
			&it.ChargeCount, &it.LastUsedAt,
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
		// 旅館鑰匙到期時間：0 → NULL，非零 → 轉換為 timestamptz
		var innDueTime interface{}
		if item.InnDueTime != 0 {
			innDueTime = time.Unix(item.InnDueTime, 0)
		}
		var lastUsedAt interface{}
		if item.LastUsedAt != 0 {
			lastUsedAt = time.Unix(item.LastUsedAt, 0)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO character_items (char_id, item_id, count, enchant_lvl, bless, equipped, identified, equip_slot, obj_id, durability, attr_enchant_kind, attr_enchant_level, inn_key_id, inn_npc_id, inn_hall, inn_due_time, charge_count, last_used_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			charID, item.ItemID, item.Count, int16(item.EnchantLvl), int16(item.Bless),
			item.Equipped, item.Identified, equipSlot, item.ObjectID, int16(item.Durability),
			int16(item.AttrEnchantKind), int16(item.AttrEnchantLevel),
			item.InnKeyID, item.InnNpcID, item.InnHall, innDueTime,
			item.ChargeCount, lastUsedAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
