package persist

import (
	"context"
)

// WarehouseItem represents a single item stored in the warehouse.
type WarehouseItem struct {
	ID          int32
	AccountName string
	CharName    string
	WhType      int16 // 3=personal, 4=elf, 5=clan
	ItemID      int32
	Count       int32
	EnchantLvl  int16
	Bless       int16
	Identified  bool
}

type WarehouseRepo struct {
	db *DB
}

func NewWarehouseRepo(db *DB) *WarehouseRepo {
	return &WarehouseRepo{db: db}
}

// Load returns all warehouse items for an account + warehouse type.
func (r *WarehouseRepo) Load(ctx context.Context, accountName string, whType int16) ([]WarehouseItem, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, account_name, char_name, wh_type, item_id, count, enchant_lvl, bless, identified
		 FROM warehouse_items WHERE account_name = $1 AND wh_type = $2`, accountName, whType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WarehouseItem
	for rows.Next() {
		var it WarehouseItem
		if err := rows.Scan(
			&it.ID, &it.AccountName, &it.CharName, &it.WhType,
			&it.ItemID, &it.Count, &it.EnchantLvl, &it.Bless, &it.Identified,
		); err != nil {
			return nil, err
		}
		result = append(result, it)
	}
	return result, rows.Err()
}

// Deposit inserts a new item into the warehouse.
func (r *WarehouseRepo) Deposit(ctx context.Context, item WarehouseItem) (int32, error) {
	var id int32
	err := r.db.Pool.QueryRow(ctx,
		`INSERT INTO warehouse_items (account_name, char_name, wh_type, item_id, count, enchant_lvl, bless, identified)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		item.AccountName, item.CharName, item.WhType, item.ItemID, item.Count,
		item.EnchantLvl, item.Bless, item.Identified,
	).Scan(&id)
	return id, err
}

// AddToStack increases the count of a stackable warehouse item.
func (r *WarehouseRepo) AddToStack(ctx context.Context, whItemID int32, addCount int32) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE warehouse_items SET count = count + $1 WHERE id = $2`,
		addCount, whItemID,
	)
	return err
}

// Withdraw removes a warehouse item or decrements count for stackable.
// Returns true if fully removed.
func (r *WarehouseRepo) Withdraw(ctx context.Context, whItemID int32, count int32) (bool, error) {
	var remaining int32
	err := r.db.Pool.QueryRow(ctx,
		`UPDATE warehouse_items SET count = count - $1 WHERE id = $2 RETURNING count`,
		count, whItemID,
	).Scan(&remaining)
	if err != nil {
		return false, err
	}

	if remaining <= 0 {
		_, err = r.db.Pool.Exec(ctx, `DELETE FROM warehouse_items WHERE id = $1`, whItemID)
		return true, err
	}
	return false, nil
}
