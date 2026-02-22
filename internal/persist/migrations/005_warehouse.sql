-- +goose Up

CREATE TABLE warehouse_items (
    id            SERIAL PRIMARY KEY,
    account_name  VARCHAR(32) NOT NULL,
    char_name     VARCHAR(16) NOT NULL,
    wh_type       SMALLINT NOT NULL DEFAULT 3,  -- 3=personal, 4=elf, 5=clan
    item_id       INT NOT NULL,
    count         INT NOT NULL DEFAULT 1,
    enchant_lvl   SMALLINT NOT NULL DEFAULT 0,
    bless         SMALLINT NOT NULL DEFAULT 0,
    identified    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_warehouse_account ON warehouse_items(account_name, wh_type);

-- +goose Down

DROP TABLE IF EXISTS warehouse_items;
