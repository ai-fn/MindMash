-- Citus distribution guard.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it distributes
-- all collection tables by user_id and co-locates them with user_usn (migrations/platform) so
-- intra-shard FKs resolve locally.
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
  -- Anchor first: a table carrying a foreign key can only be distributed AFTER the
  -- table it references. note_types ← notes ← cards ; deck_configs ← decks ← cards.
  IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = 'note_types'::regclass) THEN
    PERFORM create_distributed_table('note_types', 'user_id', colocate_with => 'user_usn');
  END IF;
  FOREACH child IN ARRAY ARRAY[
    'deck_configs','decks','notes','cards','filtered_decks','tags','tombstones'
  ] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = child::regclass) THEN
      PERFORM create_distributed_table(child, 'user_id', colocate_with => 'note_types');
    END IF;
  END LOOP;
END $$;
