-- Citus distribution guard for scheduler tables.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it
-- distributes review_logs by user_id and co-locates it with cards so the
-- answer transaction (UPDATE cards + INSERT review_logs) stays single-shard.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down
-- migration is intentionally a no-op.
-- Idempotent: distribute only if not already distributed (create_distributed_table errors
-- on an already-distributed table), so a re-run or the dev auto-heal path is safe.
-- Guard-then-return: on plain Postgres the pg_dist_partition catalog does not exist, and a
-- single IF that references it (even behind an AND) fails at plan time. Return early before
-- any pg_dist_partition reference so the block is a true no-op without Citus.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    RETURN;  -- plain Postgres: nothing to distribute.
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = 'review_logs'::regclass) THEN
    PERFORM create_distributed_table('review_logs', 'user_id', colocate_with => 'note_types');
  END IF;
END $$;
