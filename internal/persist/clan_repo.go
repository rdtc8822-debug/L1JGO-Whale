package persist

import (
	"context"
	"errors"
)

// ClanRow represents a row from the clans table.
type ClanRow struct {
	ClanID       int32
	ClanName     string
	LeaderID     int32
	LeaderName   string
	FoundDate    int32
	HasCastle    int32
	HasHouse     int32
	Announcement []byte
	EmblemID     int32
	EmblemStatus int16
}

// ClanMemberRow represents a row from the clan_members table.
type ClanMemberRow struct {
	ClanID   int32
	CharID   int32
	CharName string
	Rank     int16
	Notes    []byte
}

// ClanRepo handles all clan-related database operations.
type ClanRepo struct {
	db *DB
}

func NewClanRepo(db *DB) *ClanRepo {
	return &ClanRepo{db: db}
}

// LoadAll loads all clans and their members. Called at server startup.
func (r *ClanRepo) LoadAll(ctx context.Context) ([]ClanRow, []ClanMemberRow, error) {
	// Load clans
	clanRows, err := r.db.Pool.Query(ctx,
		`SELECT clan_id, clan_name, leader_id, leader_name, found_date,
		        has_castle, has_house, announcement, emblem_id, emblem_status
		 FROM clans ORDER BY clan_id`)
	if err != nil {
		return nil, nil, err
	}
	defer clanRows.Close()

	var clans []ClanRow
	for clanRows.Next() {
		var c ClanRow
		if err := clanRows.Scan(
			&c.ClanID, &c.ClanName, &c.LeaderID, &c.LeaderName, &c.FoundDate,
			&c.HasCastle, &c.HasHouse, &c.Announcement, &c.EmblemID, &c.EmblemStatus,
		); err != nil {
			return nil, nil, err
		}
		clans = append(clans, c)
	}
	if err := clanRows.Err(); err != nil {
		return nil, nil, err
	}

	// Load members
	memberRows, err := r.db.Pool.Query(ctx,
		`SELECT clan_id, char_id, char_name, rank, notes
		 FROM clan_members ORDER BY clan_id, char_id`)
	if err != nil {
		return nil, nil, err
	}
	defer memberRows.Close()

	var members []ClanMemberRow
	for memberRows.Next() {
		var m ClanMemberRow
		if err := memberRows.Scan(&m.ClanID, &m.CharID, &m.CharName, &m.Rank, &m.Notes); err != nil {
			return nil, nil, err
		}
		members = append(members, m)
	}
	if err := memberRows.Err(); err != nil {
		return nil, nil, err
	}

	return clans, members, nil
}

// CreateClan creates a new clan and deducts adena from the leader in a single transaction.
// Returns the new clan ID. WAL-safe: DB first, memory second.
func (r *ClanRepo) CreateClan(ctx context.Context, leaderCharID int32, leaderName, clanName string, foundDate int32, adenaCost int32) (int32, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Deduct adena from leader's inventory (gold item_id = 40308)
	tag, err := tx.Exec(ctx,
		`UPDATE items SET count = count - $1
		 WHERE owner_id = $2 AND item_id = 40308 AND count >= $1`,
		adenaCost, leaderCharID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrInsufficientGold
	}

	// Insert clan
	var clanID int32
	err = tx.QueryRow(ctx,
		`INSERT INTO clans (clan_name, leader_id, leader_name, found_date)
		 VALUES ($1, $2, $3, $4) RETURNING clan_id`,
		clanName, leaderCharID, leaderName, foundDate,
	).Scan(&clanID)
	if err != nil {
		return 0, err
	}

	// Insert leader as member with rank 10 (prince)
	_, err = tx.Exec(ctx,
		`INSERT INTO clan_members (clan_id, char_id, char_name, rank)
		 VALUES ($1, $2, $3, 10)`,
		clanID, leaderCharID, leaderName)
	if err != nil {
		return 0, err
	}

	// Update leader's character record
	_, err = tx.Exec(ctx,
		`UPDATE characters SET clan_id = $1, clan_name = $2, clan_rank = 10
		 WHERE id = $3`,
		clanID, clanName, leaderCharID)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return clanID, nil
}

// AddMember adds a new member to an existing clan in a single transaction.
func (r *ClanRepo) AddMember(ctx context.Context, clanID int32, clanName string, charID int32, charName string, rank int16) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO clan_members (clan_id, char_id, char_name, rank)
		 VALUES ($1, $2, $3, $4)`,
		clanID, charID, charName, rank)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`UPDATE characters SET clan_id = $1, clan_name = $2, clan_rank = $3
		 WHERE id = $4`,
		clanID, clanName, rank, charID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// RemoveMember removes a member from a clan in a single transaction.
func (r *ClanRepo) RemoveMember(ctx context.Context, clanID, charID int32) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`DELETE FROM clan_members WHERE clan_id = $1 AND char_id = $2`,
		clanID, charID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`UPDATE characters SET clan_id = 0, clan_name = '', clan_rank = 0
		 WHERE id = $1`, charID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// DissolveClan removes a clan and all its members in a single transaction.
func (r *ClanRepo) DissolveClan(ctx context.Context, clanID int32) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Clear all member character records
	_, err = tx.Exec(ctx,
		`UPDATE characters SET clan_id = 0, clan_name = '', clan_rank = 0
		 WHERE id IN (SELECT char_id FROM clan_members WHERE clan_id = $1)`,
		clanID)
	if err != nil {
		return err
	}

	// Delete members (CASCADE would handle this, but explicit is clearer)
	_, err = tx.Exec(ctx,
		`DELETE FROM clan_members WHERE clan_id = $1`, clanID)
	if err != nil {
		return err
	}

	// Delete clan
	_, err = tx.Exec(ctx,
		`DELETE FROM clans WHERE clan_id = $1`, clanID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// UpdateAnnouncement updates a clan's announcement.
func (r *ClanRepo) UpdateAnnouncement(ctx context.Context, clanID int32, announcement []byte) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE clans SET announcement = $1 WHERE clan_id = $2`,
		announcement, clanID)
	return err
}

// UpdateMemberNotes updates a member's personal notes.
func (r *ClanRepo) UpdateMemberNotes(ctx context.Context, clanID, charID int32, notes []byte) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE clan_members SET notes = $1 WHERE clan_id = $2 AND char_id = $3`,
		notes, clanID, charID)
	return err
}

// UpdateMemberRank updates a member's rank.
func (r *ClanRepo) UpdateMemberRank(ctx context.Context, clanID, charID int32, rank int16) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE clan_members SET rank = $1 WHERE clan_id = $2 AND char_id = $3`,
		rank, clanID, charID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`UPDATE characters SET clan_rank = $1 WHERE id = $2`,
		rank, charID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// LoadOfflineCharClan loads clan info for an offline character by name.
// Returns charID, clanID, clanName, clanRank, or error. pgx.ErrNoRows if not found.
func (r *ClanRepo) LoadOfflineCharClan(ctx context.Context, charName string) (int32, int32, string, int16, error) {
	var charID, clanID int32
	var clanName string
	var clanRank int16
	err := r.db.Pool.QueryRow(ctx,
		`SELECT id, clan_id, clan_name, clan_rank FROM characters
		 WHERE name = $1 AND deleted_at IS NULL`, charName,
	).Scan(&charID, &clanID, &clanName, &clanRank)
	if err != nil {
		return 0, 0, "", 0, err
	}
	return charID, clanID, clanName, clanRank, nil
}

// ErrInsufficientGold is returned when the player doesn't have enough gold.
var ErrInsufficientGold = errors.New("insufficient gold")
