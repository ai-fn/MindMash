-- Analytics columnar mirror of the authoritative Postgres review_logs.
-- Fed by the stats-aggregator worker from the Kafka review.recorded stream.
--
-- ReplacingMergeTree dedups rows sharing the full sort key, which includes
-- review_id; combined with at-least-once Kafka delivery this makes ingestion
-- idempotent (a redelivered review collapses to one row on merge). Reads use
-- FINAL (see the rollup views) so duplicates are folded even before merge.
CREATE TABLE IF NOT EXISTS review_events
(
    review_id  String,
    user_id    String,
    profile_id String,
    card_id    String,
    deck_id    String,
    grade      Int8,
    prev_state Int16,
    new_state  Int16,
    ivl        Int32,
    time_ms    Int32,
    ts         DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (user_id, profile_id, ts, review_id);

-- Daily activity rollup: review count, correct count (grade > Again), and time
-- spent per tenant per day. Logical view over the deduped table.
CREATE VIEW IF NOT EXISTS reviews_daily AS
SELECT
    user_id,
    profile_id,
    deck_id,
    toDate(ts)                  AS day,
    count()                     AS reviews,
    countIf(grade > 1)          AS correct,
    sum(time_ms)                AS time_ms
FROM review_events FINAL
GROUP BY user_id, profile_id, deck_id, day;

-- Retention rollup: among cards that were in the review state when answered
-- (prev_state = 2), how many were recalled (grade > Again). Measured recall =
-- due_correct / due_reviews, compared by the API against target retention.
CREATE VIEW IF NOT EXISTS retention_daily AS
SELECT
    user_id,
    profile_id,
    deck_id,
    toDate(ts)                          AS day,
    countIf(prev_state = 2)             AS due_reviews,
    countIf(prev_state = 2 AND grade > 1) AS due_correct
FROM review_events FINAL
GROUP BY user_id, profile_id, deck_id, day;
