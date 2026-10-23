//go:build integration

package search

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/opensearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	searchv1 "github.com/mindmesh/mindmesh/api/search/v1"
	"github.com/mindmesh/mindmesh/internal/collection"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// Two tenants used to prove cross-user isolation.
const (
	userA = "00000000-0000-0000-0000-0000000000a1"
	profA = "00000000-0000-0000-0000-0000000000a2"
	userB = "00000000-0000-0000-0000-0000000000b1"
	profB = "00000000-0000-0000-0000-0000000000b2"
)

// ─── Container / harness setup ────────────────────────────────────────────────

// setupOpenSearch starts an OpenSearch container (security disabled) and returns
// a Repo with the cards-v1 template installed.
func setupOpenSearch(t *testing.T) *Repo {
	t.Helper()
	ctx := context.Background()

	osCtr, err := opensearch.Run(ctx, "opensearchproject/opensearch:3")
	if err != nil {
		t.Fatalf("start opensearch: %v", err)
	}
	t.Cleanup(func() { _ = osCtr.Terminate(ctx) })

	addr, err := osCtr.Address(ctx)
	if err != nil {
		t.Fatalf("opensearch address: %v", err)
	}
	repo, err := NewRepo(RepoConfig{Addresses: []string{addr}})
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.EnsureIndex(ctx); err != nil {
		t.Fatalf("ensure index: %v", err)
	}
	return repo
}

// newServiceKeys returns the indexer's service signer and a Collection-side verifier trusting it.
func newServiceKeys(t *testing.T) (*auth.ServiceSigner, *auth.ServiceVerifier) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return auth.NewServiceSignerFromKey("search", priv),
		auth.NewServiceVerifierFromKeys(auth.CollectionInternalAudience, map[string]ed25519.PublicKey{"search": pub})
}

// rejectUsers fails any user-token method: the indexer must only reach service-policy methods.
type rejectUsers struct{}

func (rejectUsers) Verify(string) (*auth.Claims, error) {
	return nil, errors.New("no user tokens here")
}

// collectionHarness is a real Collection backed by Postgres+Redis, exposed both
// in-process (svc, for seeding) and over a bufconn internal-gRPC server guarded
// by the production auth interceptor (client, for the indexer to enrich through).
type collectionHarness struct {
	svc    *collection.Service
	repo   *collection.PostgresRepository
	client collectionv1.CollectionInternalServiceClient
	signer *auth.ServiceSigner
	pool   *pgxpool.Pool
}

func setupCollection(t *testing.T) *collectionHarness {
	t.Helper()
	ctx := context.Background()

	pgCtr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("search_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pgCtr.Terminate(ctx) })

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pg connection string: %v", err)
	}
	if err := platformdb.RunMigrations(ctx, dsn, "../../migrations/collection"); err != nil {
		t.Fatalf("run collection migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := collection.NewPostgresRepository(pool)
	svc := collection.NewService(repo, zap.NewNop())
	internalHandler := collection.NewInternalHandler(svc)
	signer, services := newServiceKeys(t)

	// Production policy: the indexer's service token plus user_id becomes the IdentityContext GetSearchDocs scopes by.
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCServiceOrUserAuthInterceptor(rejectUsers{}, services, collection.InternalServicePolicy())))
	collectionv1.RegisterCollectionInternalServiceServer(srv, internalHandler)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn collection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &collectionHarness{
		svc:    svc,
		repo:   repo,
		client: collectionv1.NewCollectionInternalServiceClient(conn),
		signer: signer,
		pool:   pool,
	}
}

// ─── Query helpers ────────────────────────────────────────────────────────────

// queryAs runs a Search as the given tenant, returning the matched card ids.
func queryAs(t *testing.T, h *Handler, userID, profileID, q string) []string {
	t.Helper()
	ctx := auth.WithIdentity(context.Background(), auth.IdentityContext{UserID: userID, ProfileID: profileID})
	resp, err := h.Search(ctx, &searchv1.SearchRequest{Q: q})
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	ids := make([]string, 0, len(resp.GetResults()))
	for _, r := range resp.GetResults() {
		ids = append(ids, r.GetCardId())
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestSearchRepo_IsolationAndDeckPrefix indexes documents for two tenants with a
// nested deck and asserts tenant isolation plus deck-subtree prefix matching,
// driving the repo and handler directly (no Collection/Kafka).
func TestSearchRepo_IsolationAndDeckPrefix(t *testing.T) {
	repo := setupOpenSearch(t)
	ctx := context.Background()
	now := time.Now().UTC()

	docs := []Document{
		{CardID: "a-parent", UserID: userA, ProfileID: profA, DeckPath: "Spanish", State: "new", Tags: []string{"verb"}, FieldsText: "hola hello", AddedAt: now, Version: 1},
		{CardID: "a-child", UserID: userA, ProfileID: profA, DeckPath: "Spanish::Verbs", State: "review", FieldsText: "comer to eat", AddedAt: now.Add(-time.Minute), Version: 1},
		{CardID: "b-parent", UserID: userB, ProfileID: profB, DeckPath: "Spanish", State: "new", FieldsText: "bonjour", AddedAt: now, Version: 1},
	}
	for _, d := range docs {
		if err := repo.Upsert(ctx, d); err != nil {
			t.Fatalf("upsert %s: %v", d.CardID, err)
		}
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	h := NewHandler(repo)

	// deck:Spanish for user A matches the deck and its subtree, but never B's doc.
	got := queryAs(t, h, userA, profA, "deck:Spanish")
	if len(got) != 2 || !containsID(got, "a-parent") || !containsID(got, "a-child") {
		t.Errorf("deck:Spanish (A) = %v, want [a-parent a-child]", got)
	}
	if containsID(got, "b-parent") {
		t.Error("cross-tenant leak: user A saw user B's card")
	}

	// The nested deck path matches only the child.
	if got := queryAs(t, h, userA, profA, "deck:Spanish::Verbs"); len(got) != 1 || got[0] != "a-child" {
		t.Errorf("deck:Spanish::Verbs = %v, want [a-child]", got)
	}

	// User B sees only their own doc for the same deck name.
	if got := queryAs(t, h, userB, profB, "deck:Spanish"); len(got) != 1 || got[0] != "b-parent" {
		t.Errorf("deck:Spanish (B) = %v, want [b-parent]", got)
	}

	// Empty query lists all of A's cards (and none of B's).
	if got := queryAs(t, h, userA, profA, ""); len(got) != 2 {
		t.Errorf("empty query (A) = %v, want 2 cards", got)
	}

	// Full-text + state tokens.
	if got := queryAs(t, h, userA, profA, "comer"); len(got) != 1 || got[0] != "a-child" {
		t.Errorf("comer = %v, want [a-child]", got)
	}
	if got := queryAs(t, h, userA, profA, "is:new"); len(got) != 1 || got[0] != "a-parent" {
		t.Errorf("is:new = %v, want [a-parent]", got)
	}
}

// TestIndexer_EnrichUpsertDelete drives the indexer through Collection's real
// internal API over bufconn (authenticated by a minted per-user token) and
// asserts the resulting documents are queryable, that a deck.updated reindexes
// the subtree, and that a card.deleted removes the document.
func TestIndexer_EnrichUpsertDelete(t *testing.T) {
	repo := setupOpenSearch(t)
	coll := setupCollection(t)
	ctx := context.Background()

	// Seed a card in a nested deck via Collection's real service.
	nt, err := coll.svc.CreateNoteType(ctx, userA, profA, "Basic", "",
		[]collection.Field{{Name: "Front"}, {Name: "Back"}},
		[]collection.CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	parent, err := coll.svc.CreateDeck(ctx, userA, profA, "Spanish", "", "")
	if err != nil {
		t.Fatalf("CreateDeck parent: %v", err)
	}
	child, err := coll.svc.CreateDeck(ctx, userA, profA, "Verbs", parent.ID, "")
	if err != nil {
		t.Fatalf("CreateDeck child: %v", err)
	}
	note, err := coll.svc.CreateNote(ctx, userA, profA, nt.ID, child.ID,
		map[string]string{"Front": "comer", "Back": "to eat"}, []string{"verb"}, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := coll.repo.ListCardsByNote(ctx, userA, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v (n=%d)", err, len(cards))
	}
	cardID := cards[0].ID

	indexer := NewIndexer(repo, coll.client, coll.signer, zap.NewNop())
	h := NewHandler(repo)

	// note.created → indexer enriches all the note's cards and upserts them.
	if err := indexer.Handle(ctx, noteEvent(t, "note.created", note.ID)); err != nil {
		t.Fatalf("handle note.created: %v", err)
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := queryAs(t, h, userA, profA, "deck:Spanish tag:verb"); len(got) != 1 || got[0] != cardID {
		t.Fatalf("after index: deck:Spanish tag:verb = %v, want [%s]", got, cardID)
	}

	// deck.updated on the parent reindexes the subtree (idempotent upsert).
	if err := indexer.Handle(ctx, deckEvent(t, "deck.updated", parent.ID)); err != nil {
		t.Fatalf("handle deck.updated: %v", err)
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := queryAs(t, h, userA, profA, "comer"); len(got) != 1 || got[0] != cardID {
		t.Fatalf("after subtree reindex: comer = %v, want [%s]", got, cardID)
	}

	// card.deleted removes the document.
	if err := indexer.Handle(ctx, cardEvent(t, "card.deleted", cardID)); err != nil {
		t.Fatalf("handle card.deleted: %v", err)
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := queryAs(t, h, userA, profA, "comer"); len(got) != 0 {
		t.Fatalf("after delete: comer = %v, want []", got)
	}
}

// TestIndexer_SchedulingEvents reindexes a card after Scheduler events, which carry the card id differently.
func TestIndexer_SchedulingEvents(t *testing.T) {
	if !slices.Contains(IndexTopics, "review_log") {
		t.Fatalf("IndexTopics = %v, want review_log subscribed", IndexTopics)
	}
	repo := setupOpenSearch(t)
	coll := setupCollection(t)
	ctx := context.Background()

	nt, err := coll.svc.CreateNoteType(ctx, userA, profA, "Basic", "",
		[]collection.Field{{Name: "Front"}, {Name: "Back"}},
		[]collection.CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	deck, err := coll.svc.CreateDeck(ctx, userA, profA, "Spanish", "", "")
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}
	note, err := coll.svc.CreateNote(ctx, userA, profA, nt.ID, deck.ID,
		map[string]string{"Front": "comer", "Back": "to eat"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := coll.repo.ListCardsByNote(ctx, userA, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v (n=%d)", err, len(cards))
	}
	cardID := cards[0].ID

	indexer := NewIndexer(repo, coll.client, coll.signer, zap.NewNop())
	h := NewHandler(repo)
	handle := func(msg events.Message, schedule string) {
		t.Helper()
		// Scheduler writes the scheduling columns and bumps usn, so the upsert wins external_gte.
		if _, err := coll.pool.Exec(ctx, `UPDATE cards SET `+schedule+`, usn = usn + 100 WHERE user_id = $1 AND id = $2`,
			userA, cardID); err != nil {
			t.Fatalf("update card: %v", err)
		}
		if err := indexer.Handle(ctx, msg); err != nil {
			t.Fatalf("handle %s: %v", msg.Headers["event_type"], err)
		}
		if err := repo.Refresh(ctx); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}

	if err := indexer.Handle(ctx, noteEvent(t, "note.created", note.ID)); err != nil {
		t.Fatalf("handle note.created: %v", err)
	}

	handle(cardEvent(t, "card.rescheduled", cardID), "queue = -1")
	if got := queryAs(t, h, userA, profA, "is:suspended"); len(got) != 1 || got[0] != cardID {
		t.Fatalf("after card.rescheduled: is:suspended = %v, want [%s]", got, cardID)
	}

	handle(reviewEvent(t, cardID), "queue = 2, state = 2")
	if got := queryAs(t, h, userA, profA, "is:review"); len(got) != 1 || got[0] != cardID {
		t.Fatalf("after review.recorded: is:review = %v, want [%s]", got, cardID)
	}
	if got := queryAs(t, h, userA, profA, "is:suspended"); len(got) != 0 {
		t.Fatalf("after review.recorded: is:suspended = %v, want []", got)
	}
}

func TestIndexer_PagesDeckReindex(t *testing.T) {
	repo := setupOpenSearch(t)
	coll := setupCollection(t)
	ctx := context.Background()

	nt, err := coll.svc.CreateNoteType(ctx, userA, profA, "Basic", "",
		[]collection.Field{{Name: "Front"}}, []collection.CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Front}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	deck, err := coll.svc.CreateDeck(ctx, userA, profA, "Spanish", "", "")
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}
	for _, w := range []string{"uno", "dos", "tres"} {
		if _, err := coll.svc.CreateNote(ctx, userA, profA, nt.ID, deck.ID, map[string]string{"Front": w}, nil, 0); err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
	}

	indexer := NewIndexer(repo, coll.client, coll.signer, zap.NewNop())
	indexer.pageSize = 2
	if err := indexer.Handle(ctx, deckEvent(t, "deck.updated", deck.ID)); err != nil {
		t.Fatalf("handle deck.updated: %v", err)
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := queryAs(t, NewHandler(repo), userA, profA, "deck:Spanish"); len(got) != 3 {
		t.Fatalf("deck:Spanish = %v, want 3 cards", got)
	}
}

func TestSearchRepo_UpsertSkipsStaleVersion(t *testing.T) {
	repo := setupOpenSearch(t)
	ctx := context.Background()
	now := time.Now().UTC()

	fresh := Document{CardID: "c1", UserID: userA, ProfileID: profA, State: "review", FieldsText: "fresh", AddedAt: now, Version: 5}
	if err := repo.Upsert(ctx, fresh); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}
	stale := fresh
	stale.State, stale.FieldsText, stale.Version = "new", "stale", 4
	other := Document{CardID: "c2", UserID: userA, ProfileID: profA, State: "new", FieldsText: "other", AddedAt: now, Version: 1}
	if err := repo.Upsert(ctx, stale, other); err != nil {
		t.Fatalf("bulk upsert with a stale doc: %v", err)
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	h := NewHandler(repo)
	if got := queryAs(t, h, userA, profA, "fresh"); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("fresh = %v, want [c1]: stale version overwrote it", got)
	}
	if got := queryAs(t, h, userA, profA, "other"); len(got) != 1 || got[0] != "c2" {
		t.Fatalf("other = %v, want [c2]", got)
	}
}

// TestIndexer_KafkaPipeline exercises the full asynchronous path: a change event
// published to Redpanda is consumed by the real events.Consumer + indexer,
// enriched through Collection, and becomes queryable.
func TestIndexer_KafkaPipeline(t *testing.T) {
	repo := setupOpenSearch(t)
	coll := setupCollection(t)
	ctx := context.Background()

	rpCtr, err := redpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:v24.1.1")
	if err != nil {
		t.Fatalf("start redpanda: %v", err)
	}
	t.Cleanup(func() { _ = rpCtr.Terminate(ctx) })
	broker, err := rpCtr.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatalf("kafka broker: %v", err)
	}
	// The producer does not request topic auto-creation, so create the topics up
	// front (the relay's topics are created the same way in deployment).
	createTopics(t, broker, IndexTopics...)

	// Seed a card.
	nt, err := coll.svc.CreateNoteType(ctx, userA, profA, "Basic", "",
		[]collection.Field{{Name: "Front"}, {Name: "Back"}},
		[]collection.CardTemplate{{Name: "Card 1", Front: "{{Front}}", Back: "{{Back}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	deck, err := coll.svc.CreateDeck(ctx, userA, profA, "Geography", "", "")
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}
	note, err := coll.svc.CreateNote(ctx, userA, profA, nt.ID, deck.ID,
		map[string]string{"Front": "capital of France", "Back": "Paris"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	cfg := events.Config{Brokers: []string{broker}, GroupID: "search-test", MaxRetries: 3, WriteTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second}
	producer, err := events.NewProducer(cfg)
	if err != nil {
		t.Fatalf("new producer: %v", err)
	}
	t.Cleanup(func() { _ = producer.Close() })

	// Publish the note.created event the relay would have emitted.
	payload, _ := json.Marshal(eventPayload{ID: note.ID, UserID: userA, ProfileID: profA})
	if err := producer.Publish(ctx, events.Message{
		Topic:   "note",
		Key:     []byte(userA),
		Value:   payload,
		Headers: map[string]string{"event_type": "note.created"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	consumer, err := events.NewConsumer(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new consumer: %v", err)
	}
	indexer := NewIndexer(repo, coll.client, coll.signer, zap.NewNop())

	consumeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = consumer.Subscribe(consumeCtx, IndexTopics, indexer.Handle) }()

	// Poll until the asynchronously-indexed document is queryable. Until the
	// consumer's first upsert the index does not exist, so a 404 is expected and
	// retried — only a persistent failure past the deadline is fatal.
	h := NewHandler(repo)
	ctxA := auth.WithIdentity(context.Background(), auth.IdentityContext{UserID: userA, ProfileID: profA})
	deadline := time.Now().Add(60 * time.Second)
	for {
		_ = repo.Refresh(ctx)
		resp, err := h.Search(ctxA, &searchv1.SearchRequest{Q: "Paris"})
		if err == nil && len(resp.GetResults()) == 1 {
			break // success: the event flowed all the way to the index
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the indexed document (last err: %v)", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestSearch_LanguageAnalyzerSelection proves that per-language fields are analyzed
// by their own analyzer: a Russian/English inflected query matches text in the
// language field (stemming) but NOT the same word placed only in the agnostic
// standard field, and a CJK substring matches the cjk-segmented field. Driven
// through the repo + handler directly.
func TestSearch_LanguageAnalyzerSelection(t *testing.T) {
	repo := setupOpenSearch(t)
	ctx := context.Background()
	now := time.Now().UTC()

	docs := []Document{
		// Russian: inflected "книги" in the ru field should match the lemma "книга".
		{CardID: "ru-lang", UserID: userA, ProfileID: profA, State: "new", FieldsTextRu: "книги", AddedAt: now, Version: 1},
		// Control: same word only in the agnostic standard field — no stemming.
		{CardID: "ru-std", UserID: userA, ProfileID: profA, State: "new", FieldsText: "книги", AddedAt: now.Add(-time.Minute), Version: 1},
		// English: "running" in the en field should match the stem "run".
		{CardID: "en-lang", UserID: userA, ProfileID: profA, State: "new", FieldsTextEn: "running", AddedAt: now.Add(-2 * time.Minute), Version: 1},
		{CardID: "en-std", UserID: userA, ProfileID: profA, State: "new", FieldsText: "running", AddedAt: now.Add(-3 * time.Minute), Version: 1},
		// Chinese: a mid-string bigram must match the cjk-segmented field.
		{CardID: "zh-lang", UserID: userA, ProfileID: profA, State: "new", FieldsTextZh: "你好世界", AddedAt: now.Add(-4 * time.Minute), Version: 1},
		// A second tenant's doc to prove isolation survives the multi_match.
		{CardID: "b-ru", UserID: userB, ProfileID: profB, State: "new", FieldsTextRu: "книги", AddedAt: now, Version: 1},
	}
	for _, d := range docs {
		if err := repo.Upsert(ctx, d); err != nil {
			t.Fatalf("upsert %s: %v", d.CardID, err)
		}
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	h := NewHandler(repo)

	// Russian lemma matches the stemmed field only, not the standard control.
	got := queryAs(t, h, userA, profA, "книга")
	if !containsID(got, "ru-lang") {
		t.Errorf("книга did not match ru-lang (russian stemming); got %v", got)
	}
	if containsID(got, "ru-std") {
		t.Errorf("книга matched ru-std (standard field should not stem); got %v", got)
	}
	if containsID(got, "b-ru") {
		t.Errorf("cross-tenant leak: user A saw user B's card; got %v", got)
	}

	// English stem matches the en field only, not the standard control.
	got = queryAs(t, h, userA, profA, "run")
	if !containsID(got, "en-lang") {
		t.Errorf("run did not match en-lang (english stemming); got %v", got)
	}
	if containsID(got, "en-std") {
		t.Errorf("run matched en-std (standard field should not stem); got %v", got)
	}

	// A mid-string CJK bigram matches the cjk-segmented field.
	if got := queryAs(t, h, userA, profA, "好世"); !containsID(got, "zh-lang") {
		t.Errorf("好世 did not match zh-lang (cjk segmentation); got %v", got)
	}
}

// TestIndexer_LanguageBuckets exercises the full per-language path: a note type
// with language-tagged fields → Collection groups field text by language into
// lang_text → the indexer routes each bucket to its analyzer field → language
// queries match. Proves the Collection→proto→indexer→mapping wiring end to end.
func TestIndexer_LanguageBuckets(t *testing.T) {
	repo := setupOpenSearch(t)
	coll := setupCollection(t)
	ctx := context.Background()

	nt, err := coll.svc.CreateNoteType(ctx, userA, profA, "CN", "",
		[]collection.Field{{Name: "Hanzi", Language: "zh"}, {Name: "Gloss", Language: "en"}},
		[]collection.CardTemplate{{Name: "Card 1", Front: "{{Hanzi}}", Back: "{{Gloss}}"}})
	if err != nil {
		t.Fatalf("CreateNoteType: %v", err)
	}
	deck, err := coll.svc.CreateDeck(ctx, userA, profA, "Chinese", "", "")
	if err != nil {
		t.Fatalf("CreateDeck: %v", err)
	}
	note, err := coll.svc.CreateNote(ctx, userA, profA, nt.ID, deck.ID,
		map[string]string{"Hanzi": "你好世界", "Gloss": "running"}, nil, 0)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	cards, err := coll.repo.ListCardsByNote(ctx, userA, note.ID)
	if err != nil || len(cards) != 1 {
		t.Fatalf("ListCardsByNote: %v (n=%d)", err, len(cards))
	}
	cardID := cards[0].ID

	indexer := NewIndexer(repo, coll.client, coll.signer, zap.NewNop())
	h := NewHandler(repo)

	if err := indexer.Handle(ctx, noteEvent(t, "note.created", note.ID)); err != nil {
		t.Fatalf("handle note.created: %v", err)
	}
	if err := repo.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// English gloss "running" is queryable by its stem via the en-tagged field.
	if got := queryAs(t, h, userA, profA, "run"); len(got) != 1 || got[0] != cardID {
		t.Fatalf("run = %v, want [%s] (english-tagged Gloss)", got, cardID)
	}
	// The Chinese field is queryable by a mid-string bigram via the zh-tagged field.
	if got := queryAs(t, h, userA, profA, "好世"); len(got) != 1 || got[0] != cardID {
		t.Fatalf("好世 = %v, want [%s] (zh-tagged Hanzi)", got, cardID)
	}
}

// ─── Event fixtures ───────────────────────────────────────────────────────────

func eventMsg(t *testing.T, topic, eventType, id string) events.Message {
	t.Helper()
	payload, err := json.Marshal(eventPayload{ID: id, UserID: userA, ProfileID: profA})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return events.Message{
		Topic:   topic,
		Value:   payload,
		Headers: map[string]string{"event_type": eventType},
	}
}

// createTopics provisions Kafka topics on the broker before the producer writes
// to them, since the producer does not request auto-creation.
func createTopics(t *testing.T, broker string, topics ...string) {
	t.Helper()
	conn, err := kafka.Dial("tcp", broker)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	controller, err := conn.Controller()
	if err != nil {
		t.Fatalf("kafka controller: %v", err)
	}
	cc, err := kafka.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer func() { _ = cc.Close() }()
	cfgs := make([]kafka.TopicConfig, 0, len(topics))
	for _, tp := range topics {
		cfgs = append(cfgs, kafka.TopicConfig{Topic: tp, NumPartitions: 1, ReplicationFactor: 1})
	}
	if err := cc.CreateTopics(cfgs...); err != nil {
		t.Fatalf("create topics: %v", err)
	}
}

// reviewEvent mirrors scheduler's review.recorded payload: id is the review log, card_id the card.
func reviewEvent(t *testing.T, cardID string) events.Message {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"id": "0190f3a8-7b2c-7d4e-9f00-0000000000ee", "user_id": userA, "profile_id": profA, "card_id": cardID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return events.Message{Topic: "review_log", Value: payload, Headers: map[string]string{"event_type": "review.recorded"}}
}

func noteEvent(t *testing.T, eventType, id string) events.Message {
	return eventMsg(t, "note", eventType, id)
}
func cardEvent(t *testing.T, eventType, id string) events.Message {
	return eventMsg(t, "card", eventType, id)
}
func deckEvent(t *testing.T, eventType, id string) events.Message {
	return eventMsg(t, "deck", eventType, id)
}
