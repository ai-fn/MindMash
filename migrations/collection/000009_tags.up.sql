-- Normalized tag registry for autocomplete.
-- Tags are also denormalized on notes.tags TEXT[] for fast per-note lookups.
-- name is CITEXT for case-insensitive uniqueness within a user scope.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE tags (
    id         UUID          NOT NULL,
    user_id    UUID          NOT NULL,
    profile_id UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    name       CITEXT        NOT NULL,
    usn        BIGINT        NOT NULL DEFAULT 0,
    mtime      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, name)                      -- one entry per tag name per user
);
