-- Per-user deck scheduling configuration (new/review limits, FSRS weights, etc.).
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE IF NOT EXISTS deck_configs (
    id                UUID          NOT NULL,
    user_id           UUID          NOT NULL,
    profile_id        UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    name              TEXT          NOT NULL,
    new_per_day       INT           NOT NULL DEFAULT 20,
    rev_per_day       INT           NOT NULL DEFAULT 200,
    desired_retention DOUBLE PRECISION   NOT NULL DEFAULT 0.9,
    fsrs_weights      DOUBLE PRECISION[] NOT NULL DEFAULT '{}',
    rollover_hour     SMALLINT      NOT NULL DEFAULT 4,
    timezone          TEXT          NOT NULL DEFAULT 'UTC',
    max_interval_days INT           NOT NULL DEFAULT 36500 CHECK (max_interval_days BETWEEN 1 AND 36500),
    usn               BIGINT        NOT NULL DEFAULT 0, -- update sequence number for sync
    mtime             TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);
