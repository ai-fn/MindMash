-- Citus distribution cannot be cleanly reversed without recreating the tables.
-- This down migration is intentionally a no-op.
-- To undo distribution, drop and recreate the schema from scratch.
SELECT 1;
