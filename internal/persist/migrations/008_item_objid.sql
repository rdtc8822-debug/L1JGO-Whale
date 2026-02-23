-- +goose Up
ALTER TABLE character_items ADD COLUMN obj_id INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE character_items DROP COLUMN obj_id;
