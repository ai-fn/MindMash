package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// Pagination bounds and the keyset sentinel. An empty page token means "from the
// start"; we translate it to the all-zero UUID so the `id > $token` predicate is
// uniform whether or not a cursor was supplied.
const (
	defaultPageSize = 50
	maxPageSize     = 200
	zeroUUID        = "00000000-0000-0000-0000-000000000000"
)

// Column lists kept next to the scans that consume them so the order stays in
// sync. Decks read/write path as text (ltree has no direct pgx codec).
const (
	noteTypeColumns     = "id, user_id, profile_id, name, fields, card_templates, css, usn, mtime"
	noteColumns         = "id, user_id, profile_id, note_type_id, fields, tags, flags, usn, mtime"
	cardColumns         = "id, user_id, profile_id, note_id, deck_id, template_ord, state, queue, due, stability, difficulty, reps, lapses, last_review, usn, mtime"
	deckColumns         = "id, user_id, profile_id, parent_id, name, path::text, config_id, usn, mtime"
	deckConfigColumns   = "id, user_id, profile_id, name, new_per_day, rev_per_day, desired_retention, fsrs_weights, learning_steps, rollover_hour, timezone, usn, mtime"
	filteredDeckColumns = "id, user_id, profile_id, name, query, usn, mtime"
)

// dbtx is satisfied by both *pgxpool.Pool and pgx.Tx, allowing the same query
// methods to work inside or outside a transaction without duplication.
type dbtx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// rowScanner is the common Scan surface of pgx.Row and pgx.Rows, letting one
// scan helper serve both single-row reads and row iteration.
type rowScanner interface {
	Scan(dest ...any) error
}

// PostgresRepository implements Repository over a pgxpool.Pool.
type PostgresRepository struct {
	pool *pgxpool.Pool
	db   dbtx
}

// NewPostgresRepository returns a repository that uses the given pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, db: pool}
}

// withDB returns a shallow copy of the repository using the given dbtx, so a
// tx-scoped repo shares all method logic.
func (r *PostgresRepository) withDB(db dbtx) *PostgresRepository {
	return &PostgresRepository{pool: r.pool, db: db}
}

// WithTx runs fn inside a database transaction. The Repository passed to fn uses
// the transaction connection; the outer repository is unchanged.
func (r *PostgresRepository) WithTx(ctx context.Context, fn func(Repository) error) error {
	return platformdb.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return fn(r.withDB(tx))
	})
}

// ─── Error + value helpers ───────────────────────────────────────────────────

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key
// constraint violation, raised when an ON DELETE RESTRICT child still
// references the row being deleted.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// 23503 = foreign_key_violation (INSERT/UPDATE referencing a missing row);
	// 23001 = restrict_violation (DELETE/UPDATE blocked by ON DELETE RESTRICT).
	return pgErr.Code == "23503" || pgErr.Code == "23001"
}

// mapNoRows maps pgx.ErrNoRows to a platform NotFound error; other errors pass through.
func mapNoRows(err error, msg string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platformerrors.NotFound(msg)
	}
	return err
}

// pageBounds resolves a Page into a keyset cursor and a clamped limit.
func pageBounds(p Page) (token string, limit int) {
	token = p.Token
	if token == "" {
		token = zeroUUID
	}
	limit = p.Size
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	return token, limit
}

// jsonArrayBytes marshals v for a JSONB column constrained to an array, mapping a
// nil slice ("null") to an empty array so the DB CHECK is satisfied.
func jsonArrayBytes(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return []byte("[]"), nil
	}
	return b, nil
}

// jsonObjectBytes marshals v for a JSONB column constrained to an object, mapping
// a nil map ("null") to an empty object so the DB CHECK is satisfied.
func jsonObjectBytes(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return []byte("{}"), nil
	}
	return b, nil
}

// ─── NextUSN ─────────────────────────────────────────────────────────────────

func (r *PostgresRepository) NextUSN(ctx context.Context, userID string) (int64, error) {
	var usn int64
	err := r.db.QueryRow(ctx, `
		INSERT INTO user_usn (user_id, usn) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET usn = user_usn.usn + 1
		RETURNING usn`, userID).Scan(&usn)
	if err != nil {
		return 0, fmt.Errorf("collection: next usn: %w", err)
	}
	return usn, nil
}

// ─── NoteType ────────────────────────────────────────────────────────────────

func scanNoteType(s rowScanner) (*NoteType, error) {
	nt := &NoteType{}
	var fields, templates []byte
	if err := s.Scan(&nt.ID, &nt.UserID, &nt.ProfileID, &nt.Name, &fields, &templates, &nt.CSS, &nt.USN, &nt.Mtime); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(fields, &nt.Fields); err != nil {
		return nil, fmt.Errorf("collection: decode note type fields: %w", err)
	}
	if err := json.Unmarshal(templates, &nt.CardTemplates); err != nil {
		return nil, fmt.Errorf("collection: decode note type templates: %w", err)
	}
	return nt, nil
}

func (r *PostgresRepository) CreateNoteType(ctx context.Context, nt *NoteType) error {
	fields, err := jsonArrayBytes(nt.Fields)
	if err != nil {
		return fmt.Errorf("collection: encode note type fields: %w", err)
	}
	templates, err := jsonArrayBytes(nt.CardTemplates)
	if err != nil {
		return fmt.Errorf("collection: encode note type templates: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO note_types (id, user_id, profile_id, name, fields, card_templates, css, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		nt.ID, nt.UserID, nt.ProfileID, nt.Name, fields, templates, nt.CSS, nt.USN, nt.Mtime)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("note type already exists")
		}
		return fmt.Errorf("collection: create note type: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetNoteType(ctx context.Context, userID, id string) (*NoteType, error) {
	nt, err := scanNoteType(r.db.QueryRow(ctx,
		`SELECT `+noteTypeColumns+` FROM note_types WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("collection: get note type: %w", mapNoRows(err, "note type not found"))
	}
	return nt, nil
}

func (r *PostgresRepository) ListNoteTypes(ctx context.Context, userID string, page Page) ([]NoteType, string, error) {
	token, limit := pageBounds(page)
	rows, err := r.db.Query(ctx,
		`SELECT `+noteTypeColumns+` FROM note_types WHERE user_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3`,
		userID, token, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("collection: list note types: %w", err)
	}
	defer rows.Close()

	var items []NoteType
	for rows.Next() {
		nt, err := scanNoteType(rows)
		if err != nil {
			return nil, "", fmt.Errorf("collection: list note types scan: %w", err)
		}
		items = append(items, *nt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("collection: list note types rows: %w", err)
	}
	return paginate(items, limit, func(nt NoteType) string { return nt.ID })
}

func (r *PostgresRepository) UpdateNoteType(ctx context.Context, nt *NoteType) error {
	fields, err := jsonArrayBytes(nt.Fields)
	if err != nil {
		return fmt.Errorf("collection: encode note type fields: %w", err)
	}
	templates, err := jsonArrayBytes(nt.CardTemplates)
	if err != nil {
		return fmt.Errorf("collection: encode note type templates: %w", err)
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE note_types SET name = $3, fields = $4, card_templates = $5, css = $6, usn = $7, mtime = $8
		WHERE user_id = $1 AND id = $2`,
		nt.UserID, nt.ID, nt.Name, fields, templates, nt.CSS, nt.USN, nt.Mtime)
	if err != nil {
		return fmt.Errorf("collection: update note type: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("note type not found")
	}
	return nil
}

func (r *PostgresRepository) DeleteNoteType(ctx context.Context, userID, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM note_types WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("note type is still in use by existing notes")
		}
		return fmt.Errorf("collection: delete note type: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("note type not found")
	}
	return nil
}

// ─── Note ────────────────────────────────────────────────────────────────────

func scanNote(s rowScanner) (*Note, error) {
	n := &Note{}
	var fields []byte
	var flags int16
	if err := s.Scan(&n.ID, &n.UserID, &n.ProfileID, &n.NoteTypeID, &fields, &n.Tags, &flags, &n.USN, &n.Mtime); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(fields, &n.Fields); err != nil {
		return nil, fmt.Errorf("collection: decode note fields: %w", err)
	}
	n.Flags = int32(flags)
	return n, nil
}

func (r *PostgresRepository) CreateNote(ctx context.Context, n *Note) error {
	fields, err := jsonObjectBytes(n.Fields)
	if err != nil {
		return fmt.Errorf("collection: encode note fields: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO notes (id, user_id, profile_id, note_type_id, fields, tags, flags, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		n.ID, n.UserID, n.ProfileID, n.NoteTypeID, fields, normSlice(n.Tags), int16(n.Flags), n.USN, n.Mtime)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("note already exists")
		}
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("note type does not exist")
		}
		return fmt.Errorf("collection: create note: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetNote(ctx context.Context, userID, id string) (*Note, error) {
	n, err := scanNote(r.db.QueryRow(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("collection: get note: %w", mapNoRows(err, "note not found"))
	}
	return n, nil
}

func (r *PostgresRepository) UpdateNote(ctx context.Context, n *Note) error {
	fields, err := jsonObjectBytes(n.Fields)
	if err != nil {
		return fmt.Errorf("collection: encode note fields: %w", err)
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE notes SET note_type_id = $3, fields = $4, tags = $5, flags = $6, usn = $7, mtime = $8
		WHERE user_id = $1 AND id = $2`,
		n.UserID, n.ID, n.NoteTypeID, fields, normSlice(n.Tags), int16(n.Flags), n.USN, n.Mtime)
	if err != nil {
		return fmt.Errorf("collection: update note: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("note not found")
	}
	return nil
}

func (r *PostgresRepository) DeleteNote(ctx context.Context, userID, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM notes WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("note still has cards; delete its cards first")
		}
		return fmt.Errorf("collection: delete note: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("note not found")
	}
	return nil
}

// ─── Card ────────────────────────────────────────────────────────────────────

func scanCard(s rowScanner) (*Card, error) {
	c := &Card{}
	var ord, state, queue int16
	if err := s.Scan(&c.ID, &c.UserID, &c.ProfileID, &c.NoteID, &c.DeckID,
		&ord, &state, &queue, &c.Due, &c.Stability, &c.Difficulty,
		&c.Reps, &c.Lapses, &c.LastReview, &c.USN, &c.Mtime); err != nil {
		return nil, err
	}
	c.TemplateOrd = int(ord)
	c.State = CardState(state)
	c.Queue = int(queue)
	return c, nil
}

func (r *PostgresRepository) CreateCard(ctx context.Context, c *Card) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO cards (id, user_id, profile_id, note_id, deck_id, template_ord, state, queue, due, stability, difficulty, reps, lapses, last_review, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		c.ID, c.UserID, c.ProfileID, c.NoteID, c.DeckID, int16(c.TemplateOrd), int16(c.State), int16(c.Queue),
		c.Due, c.Stability, c.Difficulty, c.Reps, c.Lapses, c.LastReview, c.USN, c.Mtime)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("card already exists")
		}
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("card references a missing note or deck")
		}
		return fmt.Errorf("collection: create card: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetCard(ctx context.Context, userID, id string) (*Card, error) {
	c, err := scanCard(r.db.QueryRow(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("collection: get card: %w", mapNoRows(err, "card not found"))
	}
	return c, nil
}

func (r *PostgresRepository) GetCardsByIDs(ctx context.Context, userID string, ids []string) ([]Card, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE user_id = $1 AND id = ANY($2) ORDER BY id ASC`, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("collection: get cards by ids: %w", err)
	}
	return collectCards(rows, "get cards by ids")
}

func (r *PostgresRepository) ListCardsByNote(ctx context.Context, userID, noteID string) ([]Card, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE user_id = $1 AND note_id = $2 ORDER BY template_ord ASC`, userID, noteID)
	if err != nil {
		return nil, fmt.Errorf("collection: list cards by note: %w", err)
	}
	return collectCards(rows, "list cards by note")
}

func (r *PostgresRepository) DeleteCard(ctx context.Context, userID, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM cards WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("collection: delete card: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("card not found")
	}
	return nil
}

func (r *PostgresRepository) ListCardIDs(ctx context.Context, userID string, page Page) ([]string, string, error) {
	token, limit := pageBounds(page)
	rows, err := r.db.Query(ctx,
		`SELECT id FROM cards WHERE user_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3`, userID, token, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("collection: list card ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, "", fmt.Errorf("collection: list card ids scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("collection: list card ids rows: %w", err)
	}
	return paginate(ids, limit, func(id string) string { return id })
}

// GetSearchDocs joins cards to their owning note and deck and assembles the
// denormalized documents the Search indexer consumes. Exactly one selector list
// must be non-empty; an empty selector returns no docs. deck_ids expands to the
// deck subtree (path <@ root.path) so a parent rename reindexes every descendant.
func (r *PostgresRepository) GetSearchDocs(ctx context.Context, userID string, sel SearchDocSelector) ([]SearchDoc, error) {
	var pred string
	var arg any
	switch {
	case len(sel.CardIDs) > 0:
		pred, arg = "c.id = ANY($2)", sel.CardIDs
	case len(sel.NoteIDs) > 0:
		pred, arg = "c.note_id = ANY($2)", sel.NoteIDs
	case len(sel.DeckIDs) > 0:
		pred, arg = `c.deck_id IN (
			SELECT s.id FROM decks s
			JOIN decks root ON root.user_id = s.user_id AND root.id = ANY($2)
			WHERE s.user_id = $1 AND s.path <@ root.path)`, sel.DeckIDs
	default:
		return nil, nil
	}

	// deck_path is the human-readable name path ("Parent::Child"), not the
	// internal ltree id-path: the Search service's `deck:` token matches by name
	// and a deck rename must change it (which is why deck.updated reindexes the
	// subtree). It is assembled from the deck's ltree ancestors, ordered root→leaf.
	// nt.fields carries the note type's field descriptors so per-field language tags
	// reach the SearchDoc (for per-language search analyzers); nt.fields is never
	// null (note types always define their fields), so an INNER join is safe.
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.user_id, c.profile_id, c.note_id, c.deck_id, c.template_ord,
		       c.state, c.queue, c.due, c.reps, c.lapses, c.last_review, c.usn,
		       n.fields, n.tags, n.flags, n.usn, nt.fields,
		       (SELECT string_agg(a.name, '::' ORDER BY nlevel(a.path))
		        FROM decks a
		        WHERE a.user_id = c.user_id AND a.path @> d.path), d.usn
		FROM cards c
		JOIN notes n ON n.user_id = c.user_id AND n.id = c.note_id
		JOIN note_types nt ON nt.user_id = c.user_id AND nt.id = n.note_type_id
		JOIN decks d ON d.user_id = c.user_id AND d.id = c.deck_id
		WHERE c.user_id = $1 AND `+pred+`
		ORDER BY c.id ASC`, userID, arg)
	if err != nil {
		return nil, fmt.Errorf("collection: get search docs: %w", err)
	}
	defer rows.Close()

	var docs []SearchDoc
	for rows.Next() {
		c := Card{}
		n := Note{}
		d := Deck{}
		var ord, state, queue, flags int16
		var fields, noteTypeFields []byte
		if err := rows.Scan(
			&c.ID, &c.UserID, &c.ProfileID, &c.NoteID, &c.DeckID, &ord,
			&state, &queue, &c.Due, &c.Reps, &c.Lapses, &c.LastReview, &c.USN,
			&fields, &n.Tags, &flags, &n.USN, &noteTypeFields,
			&d.Path, &d.USN,
		); err != nil {
			return nil, fmt.Errorf("collection: get search docs scan: %w", err)
		}
		c.TemplateOrd = int(ord)
		c.State = CardState(state)
		c.Queue = int(queue)
		if err := json.Unmarshal(fields, &n.Fields); err != nil {
			return nil, fmt.Errorf("collection: get search docs decode fields: %w", err)
		}
		var descriptors []Field
		if err := json.Unmarshal(noteTypeFields, &descriptors); err != nil {
			return nil, fmt.Errorf("collection: get search docs decode note type fields: %w", err)
		}
		n.Flags = int32(flags)
		docs = append(docs, newSearchDoc(&c, &n, &d, descriptors))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collection: get search docs rows: %w", err)
	}
	return docs, nil
}

// collectCards drains rows into a Card slice; rows is closed before returning.
func collectCards(rows pgx.Rows, op string) ([]Card, error) {
	defer rows.Close()
	var cards []Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, fmt.Errorf("collection: %s scan: %w", op, err)
		}
		cards = append(cards, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collection: %s rows: %w", op, err)
	}
	return cards, nil
}

// ─── Deck ────────────────────────────────────────────────────────────────────

func scanDeck(s rowScanner) (*Deck, error) {
	d := &Deck{}
	if err := s.Scan(&d.ID, &d.UserID, &d.ProfileID, &d.ParentID, &d.Name, &d.Path, &d.ConfigID, &d.USN, &d.Mtime); err != nil {
		return nil, err
	}
	return d, nil
}

func (r *PostgresRepository) CreateDeck(ctx context.Context, d *Deck) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO decks (id, user_id, profile_id, parent_id, name, path, config_id, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6::ltree, $7, $8, $9)`,
		d.ID, d.UserID, d.ProfileID, d.ParentID, d.Name, d.Path, d.ConfigID, d.USN, d.Mtime)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("deck already exists")
		}
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("deck parent or config does not exist")
		}
		return fmt.Errorf("collection: create deck: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetDeck(ctx context.Context, userID, id string) (*Deck, error) {
	d, err := scanDeck(r.db.QueryRow(ctx,
		`SELECT `+deckColumns+` FROM decks WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("collection: get deck: %w", mapNoRows(err, "deck not found"))
	}
	return d, nil
}

func (r *PostgresRepository) ListDecks(ctx context.Context, userID string, page Page) ([]Deck, string, error) {
	token, limit := pageBounds(page)
	rows, err := r.db.Query(ctx,
		`SELECT `+deckColumns+` FROM decks WHERE user_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3`,
		userID, token, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("collection: list decks: %w", err)
	}
	defer rows.Close()

	var items []Deck
	for rows.Next() {
		d, err := scanDeck(rows)
		if err != nil {
			return nil, "", fmt.Errorf("collection: list decks scan: %w", err)
		}
		items = append(items, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("collection: list decks rows: %w", err)
	}
	return paginate(items, limit, func(d Deck) string { return d.ID })
}

func (r *PostgresRepository) UpdateDeck(ctx context.Context, d *Deck) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE decks SET parent_id = $3, name = $4, path = $5::ltree, config_id = $6, usn = $7, mtime = $8
		WHERE user_id = $1 AND id = $2`,
		d.UserID, d.ID, d.ParentID, d.Name, d.Path, d.ConfigID, d.USN, d.Mtime)
	if err != nil {
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("deck parent or config does not exist")
		}
		return fmt.Errorf("collection: update deck: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("deck not found")
	}
	return nil
}

func (r *PostgresRepository) DeleteDeck(ctx context.Context, userID, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM decks WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return platformerrors.FailedPrecondition("deck still holds cards; move or delete them first")
		}
		return fmt.Errorf("collection: delete deck: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("deck not found")
	}
	return nil
}

func (r *PostgresRepository) ListSubtreeDecks(ctx context.Context, userID, rootPath string) ([]Deck, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+deckColumns+` FROM decks WHERE user_id = $1 AND path <@ $2::ltree ORDER BY path ASC`,
		userID, rootPath)
	if err != nil {
		return nil, fmt.Errorf("collection: list subtree decks: %w", err)
	}
	defer rows.Close()

	var items []Deck
	for rows.Next() {
		d, err := scanDeck(rows)
		if err != nil {
			return nil, fmt.Errorf("collection: list subtree decks scan: %w", err)
		}
		items = append(items, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collection: list subtree decks rows: %w", err)
	}
	return items, nil
}

// ─── DeckConfig ──────────────────────────────────────────────────────────────

func scanDeckConfig(s rowScanner) (*DeckConfig, error) {
	dc := &DeckConfig{}
	var rollover int16
	if err := s.Scan(&dc.ID, &dc.UserID, &dc.ProfileID, &dc.Name, &dc.NewPerDay, &dc.RevPerDay,
		&dc.DesiredRetention, &dc.FSRSWeights, &dc.LearningSteps, &rollover, &dc.Timezone, &dc.USN, &dc.Mtime); err != nil {
		return nil, err
	}
	dc.RolloverHour = int32(rollover)
	return dc, nil
}

func (r *PostgresRepository) CreateDeckConfig(ctx context.Context, dc *DeckConfig) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO deck_configs (id, user_id, profile_id, name, new_per_day, rev_per_day, desired_retention, fsrs_weights, learning_steps, rollover_hour, timezone, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		dc.ID, dc.UserID, dc.ProfileID, dc.Name, dc.NewPerDay, dc.RevPerDay, dc.DesiredRetention,
		normSlice(dc.FSRSWeights), normSlice(dc.LearningSteps), int16(dc.RolloverHour), dc.Timezone, dc.USN, dc.Mtime)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("deck config already exists")
		}
		return fmt.Errorf("collection: create deck config: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetDeckConfig(ctx context.Context, userID, id string) (*DeckConfig, error) {
	dc, err := scanDeckConfig(r.db.QueryRow(ctx,
		`SELECT `+deckConfigColumns+` FROM deck_configs WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("collection: get deck config: %w", mapNoRows(err, "deck config not found"))
	}
	return dc, nil
}

func (r *PostgresRepository) ListDeckConfigs(ctx context.Context, userID string, page Page) ([]DeckConfig, string, error) {
	token, limit := pageBounds(page)
	rows, err := r.db.Query(ctx,
		`SELECT `+deckConfigColumns+` FROM deck_configs WHERE user_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3`,
		userID, token, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("collection: list deck configs: %w", err)
	}
	defer rows.Close()

	var items []DeckConfig
	for rows.Next() {
		dc, err := scanDeckConfig(rows)
		if err != nil {
			return nil, "", fmt.Errorf("collection: list deck configs scan: %w", err)
		}
		items = append(items, *dc)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("collection: list deck configs rows: %w", err)
	}
	return paginate(items, limit, func(dc DeckConfig) string { return dc.ID })
}

func (r *PostgresRepository) UpdateDeckConfig(ctx context.Context, dc *DeckConfig) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE deck_configs SET name = $3, new_per_day = $4, rev_per_day = $5, desired_retention = $6,
			fsrs_weights = $7, learning_steps = $8, rollover_hour = $9, timezone = $10, usn = $11, mtime = $12
		WHERE user_id = $1 AND id = $2`,
		dc.UserID, dc.ID, dc.Name, dc.NewPerDay, dc.RevPerDay, dc.DesiredRetention,
		normSlice(dc.FSRSWeights), normSlice(dc.LearningSteps), int16(dc.RolloverHour), dc.Timezone, dc.USN, dc.Mtime)
	if err != nil {
		return fmt.Errorf("collection: update deck config: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("deck config not found")
	}
	return nil
}

func (r *PostgresRepository) DeleteDeckConfig(ctx context.Context, userID, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM deck_configs WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("collection: delete deck config: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("deck config not found")
	}
	return nil
}

// ─── FilteredDeck ────────────────────────────────────────────────────────────

func scanFilteredDeck(s rowScanner) (*FilteredDeck, error) {
	fd := &FilteredDeck{}
	if err := s.Scan(&fd.ID, &fd.UserID, &fd.ProfileID, &fd.Name, &fd.Query, &fd.USN, &fd.Mtime); err != nil {
		return nil, err
	}
	return fd, nil
}

func (r *PostgresRepository) CreateFilteredDeck(ctx context.Context, fd *FilteredDeck) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO filtered_decks (id, user_id, profile_id, name, query, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		fd.ID, fd.UserID, fd.ProfileID, fd.Name, fd.Query, fd.USN, fd.Mtime)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("filtered deck already exists")
		}
		return fmt.Errorf("collection: create filtered deck: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetFilteredDeck(ctx context.Context, userID, id string) (*FilteredDeck, error) {
	fd, err := scanFilteredDeck(r.db.QueryRow(ctx,
		`SELECT `+filteredDeckColumns+` FROM filtered_decks WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("collection: get filtered deck: %w", mapNoRows(err, "filtered deck not found"))
	}
	return fd, nil
}

func (r *PostgresRepository) ListFilteredDecks(ctx context.Context, userID string, page Page) ([]FilteredDeck, string, error) {
	token, limit := pageBounds(page)
	rows, err := r.db.Query(ctx,
		`SELECT `+filteredDeckColumns+` FROM filtered_decks WHERE user_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3`,
		userID, token, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("collection: list filtered decks: %w", err)
	}
	defer rows.Close()

	var items []FilteredDeck
	for rows.Next() {
		fd, err := scanFilteredDeck(rows)
		if err != nil {
			return nil, "", fmt.Errorf("collection: list filtered decks scan: %w", err)
		}
		items = append(items, *fd)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("collection: list filtered decks rows: %w", err)
	}
	return paginate(items, limit, func(fd FilteredDeck) string { return fd.ID })
}

func (r *PostgresRepository) DeleteFilteredDeck(ctx context.Context, userID, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM filtered_decks WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("collection: delete filtered deck: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return platformerrors.NotFound("filtered deck not found")
	}
	return nil
}

// ─── Tag registry + tombstones ───────────────────────────────────────────────

// UpsertTag inserts a tag or, when (user_id, name) already exists, bumps its usn
// and mtime — keeping the registry idempotent for autocomplete refresh.
func (r *PostgresRepository) UpsertTag(ctx context.Context, t *Tag) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO tags (id, user_id, profile_id, name, usn, mtime)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, name) DO UPDATE SET usn = excluded.usn, mtime = excluded.mtime`,
		t.ID, t.UserID, t.ProfileID, t.Name, t.USN, t.Mtime)
	if err != nil {
		return fmt.Errorf("collection: upsert tag: %w", err)
	}
	return nil
}

func (r *PostgresRepository) WriteTombstone(ctx context.Context, userID, typ, id string, usn int64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO tombstones (user_id, type, id, usn)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, type, id) DO UPDATE SET usn = excluded.usn, deleted_at = now()`,
		userID, typ, id, usn)
	if err != nil {
		return fmt.Errorf("collection: write tombstone: %w", err)
	}
	return nil
}

// ─── Bulk operations ─────────────────────────────────────────────────────────

func (r *PostgresRepository) MoveCards(ctx context.Context, userID string, cardIDs []string, deckID string, usn int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE cards SET deck_id = $3, usn = $4, mtime = now()
		WHERE user_id = $1 AND id = ANY($2)
		RETURNING id`,
		userID, cardIDs, deckID, usn)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, platformerrors.FailedPrecondition("target deck does not exist")
		}
		return nil, fmt.Errorf("collection: move cards: %w", err)
	}
	return collectIDs(rows, "move cards")
}

// suspendedQueue is the queue sentinel for suspended cards (negative = out of
// the study queue).
const suspendedQueue = -1

func (r *PostgresRepository) SuspendCards(ctx context.Context, userID string, cardIDs []string, usn int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE cards SET queue = $3, usn = $4, mtime = now()
		WHERE user_id = $1 AND id = ANY($2)
		RETURNING id`,
		userID, cardIDs, int16(suspendedQueue), usn)
	if err != nil {
		return nil, fmt.Errorf("collection: suspend cards: %w", err)
	}
	return collectIDs(rows, "suspend cards")
}

func (r *PostgresRepository) DeleteCardsByIDs(ctx context.Context, userID string, cardIDs []string) ([]Card, error) {
	rows, err := r.db.Query(ctx,
		`DELETE FROM cards WHERE user_id = $1 AND id = ANY($2) RETURNING `+cardColumns, userID, cardIDs)
	if err != nil {
		return nil, fmt.Errorf("collection: delete cards by ids: %w", err)
	}
	return collectCards(rows, "delete cards by ids")
}

func (r *PostgresRepository) AddTagToNotesOfCards(ctx context.Context, userID string, cardIDs []string, tag string, usn int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE notes SET tags = array_append(tags, $3), usn = $4, mtime = now()
		WHERE user_id = $1
		  AND id IN (SELECT note_id FROM cards WHERE user_id = $1 AND id = ANY($2))
		  AND NOT (tags @> ARRAY[$3]::text[])
		RETURNING id`,
		userID, cardIDs, tag, usn)
	if err != nil {
		return nil, fmt.Errorf("collection: add tag to notes of cards: %w", err)
	}
	return collectIDs(rows, "add tag to notes of cards")
}

func (r *PostgresRepository) RemoveTagFromNotesOfCards(ctx context.Context, userID string, cardIDs []string, tag string, usn int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE notes SET tags = array_remove(tags, $3), usn = $4, mtime = now()
		WHERE user_id = $1
		  AND id IN (SELECT note_id FROM cards WHERE user_id = $1 AND id = ANY($2))
		  AND tags @> ARRAY[$3]::text[]
		RETURNING id`,
		userID, cardIDs, tag, usn)
	if err != nil {
		return nil, fmt.Errorf("collection: remove tag from notes of cards: %w", err)
	}
	return collectIDs(rows, "remove tag from notes of cards")
}

func (r *PostgresRepository) SetFlagOnNotesOfCards(ctx context.Context, userID string, cardIDs []string, flag int32, usn int64) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE notes SET flags = $3, usn = $4, mtime = now()
		WHERE user_id = $1
		  AND id IN (SELECT note_id FROM cards WHERE user_id = $1 AND id = ANY($2))
		RETURNING id`,
		userID, cardIDs, int16(flag), usn)
	if err != nil {
		return nil, fmt.Errorf("collection: set flag on notes of cards: %w", err)
	}
	return collectIDs(rows, "set flag on notes of cards")
}

// WriteOutbox appends outbox entries to the outbox table within the current
// transaction. It panics if called on a pool-backed repository (i.e. outside
// a WithTx callback), because the atomicity guarantee requires a tx.
func (r *PostgresRepository) WriteOutbox(ctx context.Context, entries []events.OutboxEntry) error {
	tx, ok := r.db.(pgx.Tx)
	if !ok {
		panic("collection: WriteOutbox called outside a WithTx transaction")
	}
	return events.WriteOutbox(ctx, tx, entries)
}

// ─── Generic helpers ─────────────────────────────────────────────────────────

// paginate trims an over-fetched slice (limit+1) to limit and returns the next
// keyset token (the id of the last returned item) when more rows remain.
func paginate[T any](items []T, limit int, idOf func(T) string) ([]T, string, error) {
	if len(items) > limit {
		items = items[:limit]
		return items, idOf(items[len(items)-1]), nil
	}
	return items, "", nil
}

// collectIDs drains a pgx.Rows result that contains a single `id` text column
// and returns the values as a string slice. Used by bulk UPDATE ... RETURNING id.
func collectIDs(rows pgx.Rows, op string) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("collection: %s scan id: %w", op, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collection: %s rows: %w", op, err)
	}
	return ids, nil
}

// normSlice replaces a nil slice with an empty, non-nil one so pgx encodes a SQL
// empty array rather than NULL (the array columns are NOT NULL DEFAULT '{}').
func normSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
