-- Citus distribution guard.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it distributes
-- all collection tables by user_id and co-locates them so intra-shard FKs resolve locally.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down migration is a no-op.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    -- Order matters: a table carrying a foreign key can only be distributed
    -- AFTER the table it references is already distributed. Parents first:
    --   deck_configs ← decks ← cards ; note_types ← notes ← cards.
    PERFORM create_distributed_table('note_types',     'user_id');
    PERFORM create_distributed_table('deck_configs',   'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('decks',          'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('notes',          'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('cards',          'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('filtered_decks', 'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('tags',           'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('tombstones',     'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('outbox',         'user_id', colocate_with => 'note_types');
    PERFORM create_distributed_table('user_usn',       'user_id', colocate_with => 'note_types');
  END IF;
END $$;
