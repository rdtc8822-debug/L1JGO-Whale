-- +goose Up

ALTER TABLE characters ADD COLUMN map_times JSONB DEFAULT '{}';

-- +goose Down

ALTER TABLE characters DROP COLUMN IF EXISTS map_times;
