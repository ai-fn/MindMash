// Package app runs a service binary: config, logger, tracer and signals around the
// service's setup, then every registered run function in one errgroup.
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/cache"
	"github.com/mindmesh/mindmesh/internal/platform/config"
	"github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/log"
	"github.com/mindmesh/mindmesh/internal/platform/observability"
)

// App is what a service's setup wires into.
type App struct {
	Name string
	Base *config.BaseConfig
	Log  *zap.Logger

	runs    []func(context.Context) error
	closers []func()
}

// Setup builds the service, registering its run functions with Go and its cleanup with OnClose.
type Setup func(ctx context.Context, a *App) error

// Main runs the service and exits non-zero if it fails.
func Main(name string, setup Setup) {
	if err := Run(context.Background(), name, setup); err != nil {
		fmt.Fprintf(os.Stderr, "%s: fatal: %v\n", name, err)
		os.Exit(1)
	}
}

// Run returns once every run function has returned: after a signal or ctx cancellation, or the first failure.
func Run(ctx context.Context, name string, setup Setup) error {
	base, err := config.Load()
	if err != nil {
		return err
	}
	if err := log.Init(base.ServiceName, base.Env, base.LogLevel); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal kills instead of waiting out the drain

	obsCfg, err := observability.LoadConfig()
	if err != nil {
		return err
	}
	tracerShutdown, err := observability.InitTracer(ctx, obsCfg)
	if err != nil {
		return fmt.Errorf("init tracer: %w", err)
	}
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn(name+": tracer shutdown", zap.Error(err))
		}
	}()

	a := &App{Name: name, Base: base, Log: logger}
	defer a.close()
	if err := setup(ctx, a); err != nil {
		return err
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, run := range a.runs {
		g.Go(func() error { return run(gctx) })
	}
	err = g.Wait()
	a.close()
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	logger.Info(name + ": stopped")
	return nil
}

// Go registers a function that runs until ctx is done; none starts before setup returns.
func (a *App) Go(run func(ctx context.Context) error) { a.runs = append(a.runs, run) }

// OnClose registers cleanup; it runs in reverse order after every run function has returned.
func (a *App) OnClose(fn func()) { a.closers = append(a.closers, fn) }

func (a *App) close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
}

// Postgres connects the pool configured by DB_* and closes it on exit.
func (a *App) Postgres(ctx context.Context) (*pgxpool.Pool, db.Config, error) {
	cfg, err := db.LoadConfig()
	if err != nil {
		return nil, db.Config{}, err
	}
	pool, err := db.New(ctx, cfg)
	if err != nil {
		return nil, db.Config{}, fmt.Errorf("connect db: %w", err)
	}
	a.OnClose(pool.Close)
	return pool, cfg, nil
}

// Redis connects the client configured by REDIS_* and closes it on exit.
func (a *App) Redis(ctx context.Context) (*redis.Client, error) {
	cfg, err := cache.LoadConfig()
	if err != nil {
		return nil, err
	}
	rdb, err := cache.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	a.OnClose(func() { _ = rdb.Close() })
	return rdb, nil
}

// Migrate applies the stores' migrations if RUN_MIGRATIONS says so, by default only in dev:
// elsewhere the Helm migration job owns them.
func (a *App) Migrate(ctx context.Context, dsn string, stores ...string) error {
	on, err := config.Bool("RUN_MIGRATIONS", a.Base.Dev())
	if err != nil || !on {
		return err
	}
	dir := config.Str("MIGRATIONS_DIR", "migrations")
	if err := db.Migrate(ctx, dsn, dir, stores...); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	a.Log.Info(a.Name+": migrations applied", zap.String("dir", dir), zap.Strings("stores", stores))
	return nil
}

// Verifier checks user JWTs against identity's JWKS_URL. Only dev may fall back to the local key files.
func (a *App) Verifier(ctx context.Context) (auth.Verifier, error) {
	if url := config.Str("JWKS_URL", ""); url != "" {
		v, err := auth.NewRemoteVerifier(ctx, url, config.Str("JWKS_ISSUER", ""))
		if err != nil {
			return nil, fmt.Errorf("init jwks verifier: %w", err)
		}
		a.Log.Info(a.Name+": using remote JWKS verifier", zap.String("url", url))
		return v, nil
	}
	if !a.Base.Dev() {
		return nil, errors.New("JWKS_URL is required outside dev")
	}
	authCfg, err := auth.LoadConfig()
	if err != nil {
		return nil, err
	}
	signer, err := auth.NewSigner(authCfg)
	if err != nil {
		return nil, fmt.Errorf("init local key verifier: %w", err)
	}
	a.Log.Info(a.Name + ": using local key verifier (dev fallback)")
	return signer, nil
}

// Relay runs the outbox relay on a producer that is closed on exit.
func (a *App) Relay(pool *pgxpool.Pool, store string) error {
	cfg, err := events.LoadConfig()
	if err != nil {
		return err
	}
	producer, err := events.NewProducer(cfg)
	if err != nil {
		return fmt.Errorf("init kafka producer: %w", err)
	}
	a.OnClose(func() { _ = producer.Close() })
	a.Go(events.NewRelay(pool, producer, a.Log, events.RelayConfig{Store: store}).Run)
	return nil
}

// Consumer opens a consumer in KAFKA_GROUP_ID, or group when that is unset, closed on exit.
func (a *App) Consumer(group string) (*events.KafkaConsumer, error) {
	cfg, err := events.LoadConfig()
	if err != nil {
		return nil, err
	}
	cfg.GroupID = cmp.Or(cfg.GroupID, group)
	consumer, err := events.NewConsumer(cfg, a.Log)
	if err != nil {
		return nil, fmt.Errorf("init kafka consumer: %w", err)
	}
	a.OnClose(func() { _ = consumer.Close() })
	return consumer, nil
}
