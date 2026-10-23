-- Hierarchical deck tree.  path (ltree) encodes ancestor chain for fast subtree queries.
-- config_id is nullable: a deck without an explicit config inherits from its parent or the default.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE IF NOT EXISTS decks (
    id         UUID          NOT NULL,
    user_id    UUID          NOT NULL,
    profile_id UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    parent_id  UUID,                            -- nullable for root decks
    name       TEXT          NOT NULL,
    path       LTREE         NOT NULL,          -- e.g. "root.parent.child"
    config_id  UUID,                            -- nullable; FK below
    usn        BIGINT        NOT NULL DEFAULT 0,
    mtime      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    -- Intra-shard FKs. RESTRICT, not SET NULL: that would null user_id too, and Citus rejects it
    -- on FKs over the distribution column. The service reparents children / resets configs first.
    FOREIGN KEY (user_id, parent_id) REFERENCES decks (user_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (user_id, config_id) REFERENCES deck_configs (user_id, id) ON DELETE RESTRICT
);
