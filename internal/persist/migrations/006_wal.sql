-- +goose Up

CREATE TABLE economic_wal (
    id          BIGSERIAL PRIMARY KEY,
    tx_type     VARCHAR(16) NOT NULL,    -- 'trade', 'shop', 'auction'
    from_char   INT NOT NULL,
    to_char     INT NOT NULL,
    item_id     INT NOT NULL,
    count       INT NOT NULL DEFAULT 1,
    enchant_lvl SMALLINT NOT NULL DEFAULT 0,
    gold_amount BIGINT NOT NULL DEFAULT 0,
    processed   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wal_unprocessed ON economic_wal(processed) WHERE processed = FALSE;

-- +goose Down

DROP TABLE IF EXISTS economic_wal;
