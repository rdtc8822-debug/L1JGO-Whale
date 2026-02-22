-- +goose Up

CREATE TABLE character_items (
    id          SERIAL PRIMARY KEY,
    char_id     INT NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    item_id     INT NOT NULL,
    count       INT NOT NULL DEFAULT 1,
    enchant_lvl SMALLINT NOT NULL DEFAULT 0,
    bless       SMALLINT NOT NULL DEFAULT 0,
    equipped    BOOLEAN NOT NULL DEFAULT FALSE,
    identified  BOOLEAN NOT NULL DEFAULT TRUE,
    equip_slot  SMALLINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_character_items_char ON character_items(char_id);

-- +goose Down

DROP TABLE IF EXISTS character_items;
