-- Citus distribution guard.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it distributes
-- all media tables by user_id and co-locates them with user_usn (migrations/platform).
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down migration is a no-op.
-- Idempotent: each table is distributed only if it isn't already (create_distributed_table
-- errors on an already-distributed table), so a partial re-run or the dev auto-heal path is safe.
DO $$
DECLARE
  child TEXT;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    RETURN;  -- plain Postgres: nothing to distribute.
  END IF;
  FOREACH child IN ARRAY ARRAY['media','media_variants'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = child::regclass) THEN
      PERFORM create_distributed_table(child, 'user_id', colocate_with => 'user_usn');
    END IF;
  END LOOP;
END $$;
