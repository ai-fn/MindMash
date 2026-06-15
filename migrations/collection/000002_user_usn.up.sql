-- Per-user monotonic sync counter.
-- The application increments this with:
--   INSERT INTO user_usn (user_id, usn) VALUES ($1, 1)
--   ON CONFLICT (user_id) DO UPDATE SET usn = user_usn.usn + 1 RETURNING usn
CREATE TABLE user_usn (
    user_id UUID    PRIMARY KEY,
    usn     BIGINT  NOT NULL DEFAULT 0
);
