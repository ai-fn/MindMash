// Command dlq-replay republishes <topic>.dlq messages to <topic> once their failure is fixed, e.g.
// dlq-replay -topic card -only-group search-indexer -dry-run
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/log"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dlq-replay: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var opts events.ReplayOptions
	group := flag.String("group", "", "consumer group tracking replay progress (default dlq-replay.<topic>.<only-group|all>)")
	flag.StringVar(&opts.Topic, "topic", "", "original topic to replay into (reads <topic>.dlq); required")
	flag.StringVar(&opts.OnlyGroup, "only-group", "", "replay only messages dead-lettered by this consumer group")
	flag.IntVar(&opts.Limit, "limit", 0, "max messages to replay (0 = all)")
	flag.DurationVar(&opts.Stall, "stall", 2*time.Minute, "fail if no dead letter arrives for this long while unread ones remain (keep above group join time, ~15s)")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "log what would be replayed without publishing")
	flag.Parse()
	if opts.Topic == "" {
		flag.Usage()
		return errors.New("-topic is required")
	}

	if err := log.Init("dlq-replay", os.Getenv("ENV"), cmp.Or(os.Getenv("LOG_LEVEL"), "info")); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger := log.L()
	defer func() { _ = logger.Sync() }()

	cfg, err := events.LoadConfig()
	if err != nil {
		return fmt.Errorf("load events config: %w", err)
	}
	cfg.GroupID = cmp.Or(*group, events.DefaultReplayGroup(opts.Topic, opts.OnlyGroup))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	n, err := events.ReplayDLQ(ctx, cfg, opts, logger)
	logger.Info("dlq-replay: done", zap.String("topic", opts.Topic), zap.String("group", cfg.GroupID),
		zap.Int("replayed", n), zap.Bool("dry_run", opts.DryRun))
	if errors.Is(err, context.Canceled) {
		return nil // interrupted; progress is committed, a rerun resumes
	}
	return err
}
