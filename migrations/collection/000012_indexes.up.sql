-- Performance indexes from spec 10 §14.

-- Due-card queue: fetch cards ready for review in a deck, excluding suspended/buried.
CREATE INDEX cards_due ON cards (user_id, deck_id, due) WHERE queue >= 0 AND state <> 0;

-- New-card queue: fetch unseen cards in insertion order within a deck.
CREATE INDEX cards_new ON cards (user_id, deck_id, id) WHERE state = 0;

-- Note→cards lookups: card generation (ListCardsByNote on every note edit), the
-- bulk tag/flag subqueries, and the ON DELETE RESTRICT FK check on note delete.
-- Postgres does not auto-index the child side of the (user_id, note_id) FK.
CREATE INDEX cards_note ON cards (user_id, note_id);

-- Tag search on notes (GIN over TEXT[]).
CREATE INDEX notes_tags ON notes USING GIN (tags);

-- Full-field search on notes (GIN with jsonb_path_ops for @@ queries).
CREATE INDEX notes_fields ON notes USING GIN (fields jsonb_path_ops);

-- Hierarchical path queries on decks (GIST for ltree operators @>, <@, etc.).
CREATE INDEX decks_path ON decks USING GIST (path);

-- Sync protocol: fetch changed cards/notes since a given USN.
CREATE INDEX cards_usn ON cards (user_id, usn);
CREATE INDEX notes_usn ON notes (user_id, usn);
