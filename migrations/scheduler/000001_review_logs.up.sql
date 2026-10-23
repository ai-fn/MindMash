-- review_logs records every answer submitted by a user.
-- Intervals (prev_ivl, new_ivl) are stored in SECONDS to capture sub-day
-- learning-step durations (e.g. 10 minutes = 600 seconds).
CREATE TABLE IF NOT EXISTS review_logs (
    id         UUID         NOT NULL,
    user_id    UUID         NOT NULL,
    profile_id UUID         NOT NULL,
    card_id    UUID         NOT NULL,
    deck_id    UUID         NOT NULL,          -- deck at review time, so moving a card keeps today's counts
    grade      SMALLINT     NOT NULL CHECK (grade BETWEEN 1 AND 4),
    prev_state SMALLINT     NOT NULL,
    prev_ivl   BIGINT       NOT NULL DEFAULT 0,  -- the 36500-day FSRS cap overflows INT seconds
    new_ivl    BIGINT       NOT NULL,
    time_ms    INT          NOT NULL DEFAULT 0,
    ts         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    client_review_id UUID,                      -- Answer idempotency key
    PRIMARY KEY (user_id, id)
);

-- Supports per-card history lookups scoped by user.
CREATE INDEX IF NOT EXISTS rl_card ON review_logs (user_id, card_id, ts);
-- Daily-limit counts scan today's rows per user.
CREATE INDEX IF NOT EXISTS rl_day ON review_logs (user_id, ts);
CREATE UNIQUE INDEX IF NOT EXISTS rl_client_review ON review_logs (user_id, client_review_id) WHERE client_review_id IS NOT NULL;
