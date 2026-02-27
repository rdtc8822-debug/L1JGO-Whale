-- +goose Up

-- 角色專屬倉庫：按 char_name + wh_type 查詢（wh_type=6）
CREATE INDEX idx_warehouse_char ON warehouse_items(char_name, wh_type);

-- 血盟倉庫歷史記錄（Java: clan_warehouse_history）
CREATE TABLE clan_warehouse_history (
    id            SERIAL PRIMARY KEY,
    clan_id       INT NOT NULL,
    char_name     VARCHAR(16) NOT NULL,
    type          SMALLINT NOT NULL DEFAULT 0,   -- 0=存入, 1=領出
    item_name     VARCHAR(64) NOT NULL DEFAULT '',
    item_count    INT NOT NULL DEFAULT 1,
    record_time   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_clan_wh_history ON clan_warehouse_history(clan_id, record_time DESC);

-- +goose Down

DROP TABLE IF EXISTS clan_warehouse_history;
DROP INDEX IF EXISTS idx_warehouse_char;
