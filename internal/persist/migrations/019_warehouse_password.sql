-- +goose Up
ALTER TABLE accounts ADD COLUMN warehouse_password INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE accounts DROP COLUMN IF EXISTS warehouse_password;
