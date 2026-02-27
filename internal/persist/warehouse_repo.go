package persist

import (
	"context"
)

// WarehouseItem represents a single item stored in the warehouse.
type WarehouseItem struct {
	ID          int32
	AccountName string
	CharName    string
	WhType      int16 // 3=personal, 4=elf, 5=clan, 6=character
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

// LoadByCharName 載入角色專屬倉庫（wh_type=6），以 char_name 為主鍵。
// Java 角色倉庫以 character ID 為鍵，每個角色獨立。
func (r *WarehouseRepo) LoadByCharName(ctx context.Context, charName string, whType int16) ([]WarehouseItem, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, account_name, char_name, wh_type, item_id, count, enchant_lvl, bless, identified
		 FROM warehouse_items WHERE char_name = $1 AND wh_type = $2`, charName, whType,
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

// InsertClanWarehouseHistory 寫入血盟倉庫操作歷史。
// Java: L1DwarfForClanInventory.writeHistory()
// histType: 0=存入, 1=領出
func (r *WarehouseRepo) InsertClanWarehouseHistory(ctx context.Context, clanID int32, charName string, histType int, itemName string, itemCount int32) error {
	_, err := r.db.Pool.Exec(ctx,
		`INSERT INTO clan_warehouse_history (clan_id, char_name, type, item_name, item_count)
		 VALUES ($1, $2, $3, $4, $5)`,
		clanID, charName, histType, itemName, itemCount,
	)
	return err
}

// ClanWarehouseHistoryEntry 是血盟倉庫歷史記錄。
type ClanWarehouseHistoryEntry struct {
	CharName   string
	Type       int    // 0=存入, 1=領出
	ItemName   string
	ItemCount  int32
	MinutesAgo int32 // 距今多少分鐘
}

// LoadClanWarehouseHistory 載入血盟倉庫歷史（最近 3 天），並清理過期記錄。
// Java: S_PledgeWarehouseHistory — 自動刪除超過 3 天（259200000ms）的記錄。
func (r *WarehouseRepo) LoadClanWarehouseHistory(ctx context.Context, clanID int32) ([]ClanWarehouseHistoryEntry, error) {
	// 先刪除超過 3 天的記錄
	_, err := r.db.Pool.Exec(ctx,
		`DELETE FROM clan_warehouse_history WHERE clan_id = $1 AND record_time < NOW() - INTERVAL '3 days'`,
		clanID,
	)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT char_name, type, item_name, item_count,
		        EXTRACT(EPOCH FROM (NOW() - record_time))::int / 60 AS minutes_ago
		 FROM clan_warehouse_history WHERE clan_id = $1 ORDER BY id DESC`,
		clanID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ClanWarehouseHistoryEntry
	for rows.Next() {
		var e ClanWarehouseHistoryEntry
		if err := rows.Scan(&e.CharName, &e.Type, &e.ItemName, &e.ItemCount, &e.MinutesAgo); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
