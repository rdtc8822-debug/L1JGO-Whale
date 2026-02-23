package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server    ServerConfig    `toml:"server"`
	Database  DatabaseConfig  `toml:"database"`
	Network   NetworkConfig   `toml:"network"`
	Rates     RatesConfig     `toml:"rates"`
	Enchant   EnchantConfig   `toml:"enchant"`
	Character CharacterConfig `toml:"character"`
	Logging   LoggingConfig   `toml:"logging"`
	RateLimit RateLimitConfig `toml:"rate_limit"`
}

type EnchantConfig struct {
	WeaponChance float64 `toml:"weapon_chance"` // success rate above safe enchant (0.0-1.0)
	ArmorChance  float64 `toml:"armor_chance"`  // success rate above safe enchant (0.0-1.0)
}

type ServerConfig struct {
	Name      string `toml:"name"`
	ID        int    `toml:"id"`
	Language  int    `toml:"language"` // 0=US, 3=Taiwan, 4=Japan, 5=China
	StartTime int64  // set at boot, not from config
}

type DatabaseConfig struct {
	DSN             string        `toml:"dsn"`
	MaxOpenConns    int           `toml:"max_open_conns"`
	MaxIdleConns    int           `toml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `toml:"conn_max_lifetime"`
}

type NetworkConfig struct {
	BindAddress       string        `toml:"bind_address"`
	TickRate          time.Duration `toml:"tick_rate"`
	InQueueSize       int           `toml:"in_queue_size"`
	OutQueueSize      int           `toml:"out_queue_size"`
	MaxPacketsPerTick int           `toml:"max_packets_per_tick"`
	WriteTimeout      time.Duration `toml:"write_timeout"`
	ReadTimeout       time.Duration `toml:"read_timeout"`
}

type RatesConfig struct {
	ExpRate    float64 `toml:"exp_rate"`
	DropRate   float64 `toml:"drop_rate"`
	GoldRate   float64 `toml:"gold_rate"`
	LawfulRate float64 `toml:"lawful_rate"`
}

type CharacterConfig struct {
	DefaultSlots         int    `toml:"default_slots"`
	AutoCreateAccounts   bool   `toml:"auto_create_accounts"`
	Delete7Days          bool   `toml:"delete_7_days"`
	Delete7DaysMinLevel  int    `toml:"delete_7_days_min_level"`
	ClientLanguageCode   string `toml:"client_language_code"`
	ChangeTitleByOneself bool   `toml:"change_title_by_oneself"`
}

type LoggingConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"` // "json" or "console"
}

type RateLimitConfig struct {
	Enabled                bool `toml:"enabled"`
	LoginAttemptsPerMinute int  `toml:"login_attempts_per_minute"`
	PacketsPerSecond       int  `toml:"packets_per_second"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := defaults()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Server.StartTime = time.Now().Unix()
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Name:     "L1JGO-Whale",
			ID:       1,
			Language: 3, // Taiwan
		},
		Database: DatabaseConfig{
			DSN:             "postgres://l1jgo:l1jgo@localhost:5432/l1jgo?sslmode=disable",
			MaxOpenConns:    20,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Network: NetworkConfig{
			BindAddress:       "0.0.0.0:7001",
			TickRate:          200 * time.Millisecond,
			InQueueSize:       128,
			OutQueueSize:      256,
			MaxPacketsPerTick: 32,
			WriteTimeout:      10 * time.Second,
			ReadTimeout:       60 * time.Second,
		},
		Rates: RatesConfig{
			ExpRate:    1.0,
			DropRate:   1.0,
			GoldRate:   1.0,
			LawfulRate: 1.0,
		},
		Enchant: EnchantConfig{
			WeaponChance: 0.5,
			ArmorChance:  1.0 / 3.0,
		},
		Character: CharacterConfig{
			DefaultSlots:         6,
			AutoCreateAccounts:   true,
			Delete7Days:          true,
			Delete7DaysMinLevel:  5,
			ClientLanguageCode:   "MS950",
			ChangeTitleByOneself: true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "console",
		},
		RateLimit: RateLimitConfig{
			Enabled:                true,
			LoginAttemptsPerMinute: 10,
			PacketsPerSecond:       60,
		},
	}
}
