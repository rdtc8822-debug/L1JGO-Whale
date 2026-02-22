package persist

import (
	"context"
	"fmt"
)

// WALEntry represents one economic write-ahead log entry.
type WALEntry struct {
	TxType     string // "trade", "shop", "auction"
	FromChar   int32
	ToChar     int32
	ItemID     int32
	Count      int32
	EnchantLvl int16
	GoldAmount int64
}

type WALRepo struct {
	db *DB
}

func NewWALRepo(db *DB) *WALRepo {
	return &WALRepo{db: db}
}

// WriteWAL atomically writes a batch of WAL entries in a single transaction.
// Returns nil on success. If it fails, the caller should cancel the operation.
func (r *WALRepo) WriteWAL(ctx context.Context, entries []WALEntry) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("wal begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, e := range entries {
		if _, err := tx.Exec(ctx,
			`INSERT INTO economic_wal (tx_type, from_char, to_char, item_id, count, enchant_lvl, gold_amount)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			e.TxType, e.FromChar, e.ToChar, e.ItemID, e.Count, e.EnchantLvl, e.GoldAmount,
		); err != nil {
			return fmt.Errorf("wal insert: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// MarkProcessed marks all WAL entries as processed (called during batch flush).
func (r *WALRepo) MarkProcessed(ctx context.Context) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE economic_wal SET processed = TRUE WHERE processed = FALSE`,
	)
	return err
}
