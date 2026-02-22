-- +goose Up

CREATE TABLE accounts (
    name            VARCHAR(32) PRIMARY KEY,
    password_hash   VARCHAR(72) NOT NULL,
    access_level    SMALLINT NOT NULL DEFAULT 0,
    character_slot  SMALLINT NOT NULL DEFAULT 0,
    ip              VARCHAR(45),
    host            VARCHAR(256),
    banned          BOOLEAN NOT NULL DEFAULT FALSE,
    online          BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_active     TIMESTAMPTZ
);

CREATE TABLE characters (
    id              SERIAL PRIMARY KEY,
    account_name    VARCHAR(32) NOT NULL REFERENCES accounts(name),
    name            VARCHAR(32) NOT NULL UNIQUE,
    class_type      SMALLINT NOT NULL,
    sex             SMALLINT NOT NULL,
    class_id        INT NOT NULL,

    -- Stats
    str             SMALLINT NOT NULL,
    dex             SMALLINT NOT NULL,
    con             SMALLINT NOT NULL,
    wis             SMALLINT NOT NULL,
    cha             SMALLINT NOT NULL,
    intel           SMALLINT NOT NULL,

    -- Combat
    level           SMALLINT NOT NULL DEFAULT 1,
    exp             BIGINT NOT NULL DEFAULT 0,
    hp              SMALLINT NOT NULL,
    mp              SMALLINT NOT NULL,
    max_hp          SMALLINT NOT NULL,
    max_mp          SMALLINT NOT NULL,
    ac              SMALLINT NOT NULL DEFAULT 10,

    -- Position
    x               INT NOT NULL DEFAULT 32689,
    y               INT NOT NULL DEFAULT 32842,
    map_id          SMALLINT NOT NULL DEFAULT 2005,
    heading         SMALLINT NOT NULL DEFAULT 0,

    -- Social
    lawful          INT NOT NULL DEFAULT 0,
    title           VARCHAR(32) NOT NULL DEFAULT '',
    clan_id         INT NOT NULL DEFAULT 0,
    clan_name       VARCHAR(32) NOT NULL DEFAULT '',
    clan_rank       SMALLINT NOT NULL DEFAULT 0,

    -- Meta
    pk_count        INT NOT NULL DEFAULT 0,
    karma           INT NOT NULL DEFAULT 0,
    bonus_stats     SMALLINT NOT NULL DEFAULT 0,
    elixir_stats    SMALLINT NOT NULL DEFAULT 0,
    partner_id      INT NOT NULL DEFAULT 0,
    food            SMALLINT NOT NULL DEFAULT 40,
    high_level      SMALLINT NOT NULL DEFAULT 0,
    access_level    SMALLINT NOT NULL DEFAULT 0,

    -- Config (JSONB)
    bookmarks       JSONB DEFAULT '[]',
    config          JSONB DEFAULT '{}',

    -- Lifecycle
    birthday        INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    online_status   BOOLEAN NOT NULL DEFAULT FALSE,

    CONSTRAINT valid_class_type CHECK (class_type BETWEEN 0 AND 6),
    CONSTRAINT valid_sex CHECK (sex IN (0, 1))
);

CREATE INDEX idx_characters_account ON characters(account_name);
CREATE INDEX idx_characters_deleted ON characters(deleted_at) WHERE deleted_at IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS characters;
DROP TABLE IF EXISTS accounts;
