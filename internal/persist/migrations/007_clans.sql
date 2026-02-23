-- +goose Up

-- 血盟主表
CREATE TABLE clans (
    clan_id       SERIAL PRIMARY KEY,
    clan_name     VARCHAR(32) NOT NULL UNIQUE,
    leader_id     INT NOT NULL REFERENCES characters(id),
    leader_name   VARCHAR(32) NOT NULL,
    found_date    INT NOT NULL DEFAULT 0,
    has_castle    INT NOT NULL DEFAULT 0,
    has_house     INT NOT NULL DEFAULT 0,
    announcement  BYTEA NOT NULL DEFAULT ''::BYTEA,
    emblem_id     INT NOT NULL DEFAULT 0,
    emblem_status SMALLINT NOT NULL DEFAULT 0
);

-- 血盟成員表
CREATE TABLE clan_members (
    clan_id    INT NOT NULL REFERENCES clans(clan_id) ON DELETE CASCADE,
    char_id    INT NOT NULL REFERENCES characters(id),
    char_name  VARCHAR(32) NOT NULL,
    rank       SMALLINT NOT NULL DEFAULT 7,
    notes      BYTEA NOT NULL DEFAULT ''::BYTEA,
    PRIMARY KEY (clan_id, char_id)
);

CREATE INDEX idx_clan_members_char ON clan_members(char_id);

-- +goose Down
DROP TABLE IF EXISTS clan_members;
DROP TABLE IF EXISTS clans;
