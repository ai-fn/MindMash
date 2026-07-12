-- Deletion log used by the sync protocol to propagate hard deletes to clients.
-- type identifies which entity was deleted (e.g. 'card', 'note', 'deck', 'note_type').
-- usn is stamped at deletion time from the user_usn counter.
-- Distributed by user_id (Citus shard key); composite PK (user_id, type, id).
CREATE TABLE IF NOT EXISTS tombstones (
    user_id    UUID          NOT NULL,
    type       TEXT          NOT NULL,           -- entity type: card | note | deck | note_type | tag
    id         UUID          NOT NULL,           -- entity id that was deleted
    usn        BIGINT        NOT NULL,           -- stamped from user_usn at deletion; no default
    deleted_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, type, id),
    -- usn is always assigned from the per-user counter (>= 1); 0 would mean "never synced".
    CHECK (usn > 0)
);
