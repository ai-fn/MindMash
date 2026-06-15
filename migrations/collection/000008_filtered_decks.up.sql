-- Filtered (dynamic/cram) decks built from a search query.
-- Cards are temporarily moved into a filtered deck for cram sessions.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE filtered_decks (
    id         UUID          NOT NULL,
    user_id    UUID          NOT NULL,
    profile_id UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    name       TEXT          NOT NULL,
    query      TEXT          NOT NULL,          -- search expression used to populate the deck
    usn        BIGINT        NOT NULL DEFAULT 0,
    mtime      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);
