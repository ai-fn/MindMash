-- Performance indexes from spec 10 §14.

-- Due-card queue: fetch cards ready for review in a deck, excluding suspended/buried.
CREATE INDEX IF NOT EXISTS cards_due ON cards (user_id, deck_id, due) WHERE queue >= 0 AND state <> 0;

-- New-card queue: fetch unseen cards in insertion order within a deck.
CREATE INDEX IF NOT EXISTS cards_new ON cards (user_id, deck_id, id) WHERE state = 0;

-- Note→cards lookups: card generation (ListCardsByNote on every note edit), the
-- bulk tag/flag subqueries, and the ON DELETE RESTRICT FK check on note delete.
-- Postgres does not auto-index the child side of the (user_id, note_id) FK.
CREATE INDEX IF NOT EXISTS cards_note ON cards (user_id, note_id);

-- Tag search on notes (GIN over TEXT[]).
CREATE INDEX IF NOT EXISTS notes_tags ON notes USING GIN (tags);

-- Full-field search on notes (GIN with jsonb_path_ops for @@ queries).
CREATE INDEX IF NOT EXISTS notes_fields ON notes USING GIN (fields jsonb_path_ops);

-- Hierarchical path queries on decks (GIST for ltree operators @>, <@, etc.).
CREATE INDEX IF NOT EXISTS decks_path ON decks USING GIST (path);

-- Child side of the decks RESTRICT FKs, plus the reset-by-config lookup on config delete.
CREATE INDEX IF NOT EXISTS decks_parent ON decks (user_id, parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS decks_config ON decks (user_id, config_id) WHERE config_id IS NOT NULL;

-- Sync protocol: fetch changed cards/notes since a given USN.
CREATE INDEX IF NOT EXISTS cards_usn ON cards (user_id, usn);
CREATE INDEX IF NOT EXISTS notes_usn ON notes (user_id, usn);
