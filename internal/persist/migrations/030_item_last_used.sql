-- +goose Up

ALTER TABLE character_items
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ NULL;

ALTER TABLE warehouse_items
    ADD COLUMN IF NOT EXISTS last_used_at BIGINT NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE warehouse_items
    DROP COLUMN IF EXISTS last_used_at;

ALTER TABLE character_items
    DROP COLUMN IF EXISTS last_used_at;
