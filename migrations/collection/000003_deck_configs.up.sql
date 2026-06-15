-- Per-user deck scheduling configuration (new/review limits, FSRS weights, etc.).
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE deck_configs (
    id                UUID          NOT NULL,
    user_id           UUID          NOT NULL,
    profile_id        UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    name              TEXT          NOT NULL,
    new_per_day       INT           NOT NULL DEFAULT 20,
    rev_per_day       INT           NOT NULL DEFAULT 200,
    desired_retention DOUBLE PRECISION   NOT NULL DEFAULT 0.9,
    fsrs_weights      DOUBLE PRECISION[] NOT NULL DEFAULT '{}',
    learning_steps    INT[]              NOT NULL DEFAULT '{1,10}', -- minutes
    rollover_hour     SMALLINT      NOT NULL DEFAULT 4,
    timezone          TEXT          NOT NULL DEFAULT 'UTC',
    usn               BIGINT        NOT NULL DEFAULT 0, -- update sequence number for sync
    mtime             TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);
