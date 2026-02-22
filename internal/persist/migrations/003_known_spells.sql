-- +goose Up

ALTER TABLE characters ADD COLUMN known_spells JSONB DEFAULT '[]';

-- +goose Down

ALTER TABLE characters DROP COLUMN IF EXISTS known_spells;
