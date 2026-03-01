package persist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type CharacterRow struct {
	ID          int32
	AccountName string
	Name        string
	ClassType   int16
	Sex         int16
	ClassID     int32
	Str         int16
	Dex         int16
	Con         int16
	Wis         int16
	Cha         int16
	Intel       int16
	Level       int16
	Exp         int64
	HP          int16
	MP          int16
	MaxHP       int16
	MaxMP       int16
	AC          int16
	X           int32
	Y           int32
	MapID       int16
	Heading     int16
	Lawful      int32
	Title       string
	ClanID      int32
	ClanName    string
	ClanRank    int16
	PKCount     int32
	Karma       int32
	BonusStats  int16
	ElixirStats int16
	PartnerID   int32
	Food        int16
	HighLevel   int16
	AccessLevel int16
	Birthday    int32
	DeletedAt   *time.Time
}

type CharacterRepo struct {
	db *DB
}

func NewCharacterRepo(db *DB) *CharacterRepo {
	return &CharacterRepo{db: db}
}

func (r *CharacterRepo) LoadByAccount(ctx context.Context, accountName string) ([]CharacterRow, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, account_name, name, class_type, sex, class_id,
		        str, dex, con, wis, cha, intel,
		        level, exp, hp, mp, max_hp, max_mp, ac,
		        x, y, map_id, heading,
		        lawful, title, clan_id, clan_name, clan_rank,
		        pk_count, karma, bonus_stats, elixir_stats, partner_id,
		        food, high_level, access_level, birthday, deleted_at
		 FROM characters
		 WHERE account_name = $1 AND deleted_at IS NULL
		 ORDER BY id`, accountName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CharacterRow
	for rows.Next() {
		var c CharacterRow
		if err := rows.Scan(
			&c.ID, &c.AccountName, &c.Name, &c.ClassType, &c.Sex, &c.ClassID,
			&c.Str, &c.Dex, &c.Con, &c.Wis, &c.Cha, &c.Intel,
			&c.Level, &c.Exp, &c.HP, &c.MP, &c.MaxHP, &c.MaxMP, &c.AC,
			&c.X, &c.Y, &c.MapID, &c.Heading,
			&c.Lawful, &c.Title, &c.ClanID, &c.ClanName, &c.ClanRank,
			&c.PKCount, &c.Karma, &c.BonusStats, &c.ElixirStats, &c.PartnerID,
			&c.Food, &c.HighLevel, &c.AccessLevel, &c.Birthday, &c.DeletedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (r *CharacterRepo) Create(ctx context.Context, c *CharacterRow) error {
	return r.db.Pool.QueryRow(ctx,
		`INSERT INTO characters (
			account_name, name, class_type, sex, class_id,
			str, dex, con, wis, cha, intel,
			level, exp, hp, mp, max_hp, max_mp, ac,
			x, y, map_id, heading,
			lawful, title, clan_id, clan_name, clan_rank,
			pk_count, karma, bonus_stats, elixir_stats, partner_id,
			food, high_level, access_level, birthday
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
			$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,
			$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36
		) RETURNING id`,
		c.AccountName, c.Name, c.ClassType, c.Sex, c.ClassID,
		c.Str, c.Dex, c.Con, c.Wis, c.Cha, c.Intel,
		c.Level, c.Exp, c.HP, c.MP, c.MaxHP, c.MaxMP, c.AC,
		c.X, c.Y, c.MapID, c.Heading,
		c.Lawful, c.Title, c.ClanID, c.ClanName, c.ClanRank,
		c.PKCount, c.Karma, c.BonusStats, c.ElixirStats, c.PartnerID,
		c.Food, c.HighLevel, c.AccessLevel, c.Birthday,
	).Scan(&c.ID)
}

func (r *CharacterRepo) NameExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := r.db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM characters WHERE name = $1)`, name,
	).Scan(&exists)
	return exists, err
}

func (r *CharacterRepo) CountByAccount(ctx context.Context, accountName string) (int, error) {
	var count int
	err := r.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM characters WHERE account_name = $1 AND deleted_at IS NULL`,
		accountName,
	).Scan(&count)
	return count, err
}

func (r *CharacterRepo) SoftDelete(ctx context.Context, name string) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE characters SET deleted_at = NOW() + INTERVAL '7 days' WHERE name = $1 AND deleted_at IS NULL`,
		name,
	)
	return err
}

func (r *CharacterRepo) HardDelete(ctx context.Context, name string) error {
	_, err := r.db.Pool.Exec(ctx,
		`DELETE FROM characters WHERE name = $1`, name,
	)
	return err
}

func (r *CharacterRepo) CleanExpiredDeletions(ctx context.Context, accountName string) (int64, error) {
	tag, err := r.db.Pool.Exec(ctx,
		`DELETE FROM characters WHERE account_name = $1 AND deleted_at IS NOT NULL AND deleted_at <= NOW()`,
		accountName,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SavePosition updates the character's position in the database.
func (r *CharacterRepo) SavePosition(ctx context.Context, name string, x, y int32, mapID, heading int16) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE characters SET x = $1, y = $2, map_id = $3, heading = $4 WHERE name = $5`,
		x, y, mapID, heading, name,
	)
	return err
}

// SaveCharacter updates all mutable character fields (position, stats, combat, clan).
func (r *CharacterRepo) SaveCharacter(ctx context.Context, c *CharacterRow) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE characters SET
			level = $1, exp = $2, hp = $3, mp = $4, max_hp = $5, max_mp = $6,
			x = $7, y = $8, map_id = $9, heading = $10,
			lawful = $11, str = $12, dex = $13, con = $14, wis = $15, cha = $16, intel = $17,
			bonus_stats = $18, elixir_stats = $19,
			clan_id = $20, clan_name = $21, clan_rank = $22,
			title = $23, karma = $24, pk_count = $25
		WHERE name = $26`,
		c.Level, c.Exp, c.HP, c.MP, c.MaxHP, c.MaxMP,
		c.X, c.Y, c.MapID, c.Heading,
		c.Lawful, c.Str, c.Dex, c.Con, c.Wis, c.Cha, c.Intel,
		c.BonusStats, c.ElixirStats,
		c.ClanID, c.ClanName, c.ClanRank,
		c.Title, c.Karma, c.PKCount,
		c.Name,
	)
	return err
}

// BookmarkRow represents a single bookmark in the JSONB bookmarks column.
type BookmarkRow struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	X     int32  `json:"x"`
	Y     int32  `json:"y"`
	MapID int16  `json:"map_id"`
}

// LoadBookmarks loads the bookmarks JSONB column for a character.
func (r *CharacterRepo) LoadBookmarks(ctx context.Context, name string) ([]BookmarkRow, error) {
	var raw []byte
	err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(bookmarks, '[]'::jsonb) FROM characters WHERE name = $1 AND deleted_at IS NULL`, name,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var bookmarks []BookmarkRow
	if err := json.Unmarshal(raw, &bookmarks); err != nil {
		return nil, err
	}
	return bookmarks, nil
}

// SaveBookmarks saves the bookmarks JSONB column for a character.
func (r *CharacterRepo) SaveBookmarks(ctx context.Context, name string, bookmarks []BookmarkRow) error {
	data, err := json.Marshal(bookmarks)
	if err != nil {
		return err
	}
	_, err = r.db.Pool.Exec(ctx,
		`UPDATE characters SET bookmarks = $1 WHERE name = $2`,
		data, name,
	)
	return err
}

// LoadKnownSpells loads the known_spells JSONB column for a character.
func (r *CharacterRepo) LoadKnownSpells(ctx context.Context, name string) ([]int32, error) {
	var raw []byte
	err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(known_spells, '[]'::jsonb) FROM characters WHERE name = $1 AND deleted_at IS NULL`, name,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var spells []int32
	if err := json.Unmarshal(raw, &spells); err != nil {
		return nil, err
	}
	return spells, nil
}

// SaveKnownSpells saves the known_spells JSONB column for a character.
func (r *CharacterRepo) SaveKnownSpells(ctx context.Context, name string, spells []int32) error {
	if spells == nil {
		spells = []int32{}
	}
	data, err := json.Marshal(spells)
	if err != nil {
		return err
	}
	_, err = r.db.Pool.Exec(ctx,
		`UPDATE characters SET known_spells = $1 WHERE name = $2`,
		data, name,
	)
	return err
}

// LoadCharConfig loads the raw character config blob (hotkeys, UI positions).
func (r *CharacterRepo) LoadCharConfig(ctx context.Context, charID int32) ([]byte, error) {
	var data []byte
	err := r.db.Pool.QueryRow(ctx,
		`SELECT char_config FROM characters WHERE id = $1 AND deleted_at IS NULL`, charID,
	).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// SaveCharConfig saves the raw character config blob (hotkeys, UI positions).
func (r *CharacterRepo) SaveCharConfig(ctx context.Context, charID int32, data []byte) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE characters SET char_config = $1 WHERE id = $2`,
		data, charID,
	)
	return err
}

// LoadMapTimes 載入角色的限時地圖已使用時間（JSONB）。
// key = 組別 OrderID, value = 已使用秒數。
func (r *CharacterRepo) LoadMapTimes(ctx context.Context, name string) (map[int]int, error) {
	var raw []byte
	err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(map_times, '{}'::jsonb) FROM characters WHERE name = $1 AND deleted_at IS NULL`, name,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	// JSON key 只能是字串，轉為 int key
	result := make(map[int]int, len(m))
	for k, v := range m {
		var oid int
		if _, err := fmt.Sscanf(k, "%d", &oid); err == nil {
			result[oid] = v
		}
	}
	return result, nil
}

// SaveMapTimes 儲存角色的限時地圖已使用時間（JSONB）。
func (r *CharacterRepo) SaveMapTimes(ctx context.Context, name string, mapTimes map[int]int) error {
	// 轉為 string key（JSON 要求）
	m := make(map[string]int, len(mapTimes))
	for k, v := range mapTimes {
		m[fmt.Sprintf("%d", k)] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = r.db.Pool.Exec(ctx,
		`UPDATE characters SET map_times = $1 WHERE name = $2`,
		data, name,
	)
	return err
}

// ResetAllMapTimes 每日重置所有角色的限時地圖時間。
// Java: ServerResetMapTimer.ResetTimingMap() 的 DB 部分。
func (r *CharacterRepo) ResetAllMapTimes(ctx context.Context) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE characters SET map_times = '{}' WHERE deleted_at IS NULL AND map_times != '{}'`)
	return err
}

func (r *CharacterRepo) LoadByName(ctx context.Context, name string) (*CharacterRow, error) {
	c := &CharacterRow{}
	err := r.db.Pool.QueryRow(ctx,
		`SELECT id, account_name, name, class_type, sex, class_id,
		        str, dex, con, wis, cha, intel,
		        level, exp, hp, mp, max_hp, max_mp, ac,
		        x, y, map_id, heading,
		        lawful, title, clan_id, clan_name, clan_rank,
		        pk_count, karma, bonus_stats, elixir_stats, partner_id,
		        food, high_level, access_level, birthday, deleted_at
		 FROM characters WHERE name = $1 AND deleted_at IS NULL`, name,
	).Scan(
		&c.ID, &c.AccountName, &c.Name, &c.ClassType, &c.Sex, &c.ClassID,
		&c.Str, &c.Dex, &c.Con, &c.Wis, &c.Cha, &c.Intel,
		&c.Level, &c.Exp, &c.HP, &c.MP, &c.MaxHP, &c.MaxMP, &c.AC,
		&c.X, &c.Y, &c.MapID, &c.Heading,
		&c.Lawful, &c.Title, &c.ClanID, &c.ClanName, &c.ClanRank,
		&c.PKCount, &c.Karma, &c.BonusStats, &c.ElixirStats, &c.PartnerID,
		&c.Food, &c.HighLevel, &c.AccessLevel, &c.Birthday, &c.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}
