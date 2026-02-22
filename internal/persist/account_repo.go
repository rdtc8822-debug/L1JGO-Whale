package persist

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type AccountRow struct {
	Name          string
	PasswordHash  string
	AccessLevel   int16
	CharacterSlot int16
	IP            string
	Host          string
	Banned        bool
	Online        bool
	CreatedAt     time.Time
	LastActive    *time.Time
}

type AccountRepo struct {
	db *DB
}

func NewAccountRepo(db *DB) *AccountRepo {
	return &AccountRepo{db: db}
}

func (r *AccountRepo) Load(ctx context.Context, name string) (*AccountRow, error) {
	row := &AccountRow{}
	err := r.db.Pool.QueryRow(ctx,
		`SELECT name, password_hash, access_level, character_slot,
		        COALESCE(ip,''), COALESCE(host,''), banned, online, created_at, last_active
		 FROM accounts WHERE name = $1`, name,
	).Scan(
		&row.Name, &row.PasswordHash, &row.AccessLevel, &row.CharacterSlot,
		&row.IP, &row.Host, &row.Banned, &row.Online, &row.CreatedAt, &row.LastActive,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *AccountRepo) Create(ctx context.Context, name, rawPassword, ip, host string) (*AccountRow, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	row := &AccountRow{
		Name:         name,
		PasswordHash: string(hash),
		IP:           ip,
		Host:         host,
		CreatedAt:    now,
		LastActive:   &now,
	}
	_, err = r.db.Pool.Exec(ctx,
		`INSERT INTO accounts (name, password_hash, ip, host, last_active)
		 VALUES ($1, $2, $3, $4, $5)`,
		row.Name, row.PasswordHash, row.IP, row.Host, row.LastActive,
	)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *AccountRepo) ValidatePassword(hash string, rawPassword string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(rawPassword)) == nil
}

func (r *AccountRepo) UpdateLastActive(ctx context.Context, name, ip string) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE accounts SET last_active = NOW(), ip = $2 WHERE name = $1`,
		name, ip,
	)
	return err
}

func (r *AccountRepo) SetOnline(ctx context.Context, name string, online bool) error {
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE accounts SET online = $2 WHERE name = $1`,
		name, online,
	)
	return err
}
