-- +goose Up

ALTER TABLE characters ADD COLUMN char_config BYTEA DEFAULT NULL;

-- +goose Down

ALTER TABLE characters DROP COLUMN IF EXISTS char_config;
