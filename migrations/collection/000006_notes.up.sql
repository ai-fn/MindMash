-- A note holds the raw content (fields) for one or more cards derived from a note type.
-- tags is denormalized here for fast filtering; also normalized in the tags table for autocomplete.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE notes (
    id           UUID          NOT NULL,
    user_id      UUID          NOT NULL,
    profile_id   UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    note_type_id UUID          NOT NULL,
    fields       JSONB         NOT NULL DEFAULT '{}',
    tags         TEXT[]        NOT NULL DEFAULT '{}',
    flags        SMALLINT      NOT NULL DEFAULT 0,
    usn          BIGINT        NOT NULL DEFAULT 0,
    mtime        TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    -- fields maps field name -> value; reject scalars/arrays at the DB boundary.
    CHECK (jsonb_typeof(fields) = 'object'),
    -- Intra-shard FK: note type must belong to the same user.
    -- ON DELETE RESTRICT: a note type may only be deleted once no notes use it;
    -- the service enforces this and reports a typed conflict error.
    FOREIGN KEY (user_id, note_type_id) REFERENCES note_types (user_id, id) ON DELETE RESTRICT
);
