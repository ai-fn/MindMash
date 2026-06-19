-- Citus distribution guard for scheduler tables.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it
-- distributes review_logs by user_id and co-locates it with cards so the
-- answer transaction (UPDATE cards + INSERT review_logs) stays single-shard.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down
-- migration is intentionally a no-op.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    PERFORM create_distributed_table('review_logs', 'user_id', colocate_with => 'note_types');
  END IF;
END $$;
