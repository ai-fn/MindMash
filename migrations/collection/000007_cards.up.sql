-- A card is the schedulable unit, derived from a note + template_ord.
-- state: 0=new, 1=learning, 2=review, 3=relearning.
-- queue: negative values = suspended (<0) or buried (-1, -2).
-- stability/difficulty are FSRS SM-18 parameters.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE IF NOT EXISTS cards (
    id           UUID          NOT NULL,
    user_id      UUID          NOT NULL,
    profile_id   UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    note_id      UUID          NOT NULL,
    deck_id      UUID          NOT NULL,
    template_ord SMALLINT      NOT NULL DEFAULT 0, -- which template within the note type
    state        SMALLINT      NOT NULL DEFAULT 0, -- 0 new | 1 learning | 2 review | 3 relearning
    queue        SMALLINT      NOT NULL DEFAULT 0, -- negative = suspended/buried
    due          TIMESTAMPTZ,                      -- next review time; NULL for unseen new cards
    stability    DOUBLE PRECISION,                 -- FSRS stability (days)
    difficulty   DOUBLE PRECISION,                 -- FSRS difficulty [0,1]
    reps         INT           NOT NULL DEFAULT 0,
    lapses       INT           NOT NULL DEFAULT 0,
    last_review  TIMESTAMPTZ,
    usn          BIGINT        NOT NULL DEFAULT 0,
    mtime        TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    -- Intra-shard FK: note must belong to the same user.
    -- ON DELETE RESTRICT: the service deletes a note's cards and writes their
    -- tombstones in the same tx before removing the note, so a DB-level cascade
    -- would silently skip USN bumps + tombstones and break sync. RESTRICT makes
    -- an out-of-order delete fail loudly instead.
    FOREIGN KEY (user_id, note_id) REFERENCES notes (user_id, id) ON DELETE RESTRICT,
    -- Intra-shard FK: deck must belong to the same user.
    -- ON DELETE RESTRICT: a deck may only be deleted once it holds no cards; the
    -- service moves cards out (or deletes them with tombstones) first.
    FOREIGN KEY (user_id, deck_id) REFERENCES decks (user_id, id) ON DELETE RESTRICT
);
