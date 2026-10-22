package events

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// ReplayOptions selects which dead letters ReplayDLQ republishes.
type ReplayOptions struct {
	Topic     string        // original topic; dead letters are read from Topic.dlq
	OnlyGroup string        // replay only what this consumer group dead-lettered; "" replays all
	Limit     int           // max messages to replay; 0 = no limit
	Stall     time.Duration // give up if no message arrives for this long while unread ones remain
	DryRun    bool          // log matches without publishing or committing
}

// DefaultReplayGroup keeps progress per target: skipped messages are committed for the replay group.
func DefaultReplayGroup(topic, onlyGroup string) string {
	return "dlq-replay." + topic + "." + cmp.Or(onlyGroup, "all")
}

// ReplayDLQ republishes the dead letters present at start to the original topic and returns how many it sent.
// Every group of that topic sees them again, after newer events for the same key: handlers must tolerate both.
func ReplayDLQ(ctx context.Context, cfg Config, opts ReplayOptions, log *zap.Logger) (int, error) {
	if cfg.GroupID == "" || opts.Topic == "" || opts.Stall <= 0 || opts.Limit < 0 {
		return 0, errors.New("events: replay: group id, topic, positive stall and non-negative limit are required")
	}
	dlq := dlqTopic(opts.Topic)
	pending, err := replayBounds(ctx, cfg, dlq)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}

	r := kafka.NewReader(kafka.ReaderConfig{Brokers: cfg.Brokers, GroupID: cfg.GroupID, Topic: dlq})
	defer func() { _ = r.Close() }()
	w := newWriter(cfg)
	defer func() { _ = w.Close() }()

	n := 0
	// Only in-range messages renew the deadline, so a stream of new dead letters can't hold the run open.
	deadline := time.Now().Add(opts.Stall)
	for len(pending) > 0 && (opts.Limit == 0 || n < opts.Limit) {
		fctx, cancel := context.WithDeadline(ctx, deadline)
		km, err := r.FetchMessage(fctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return n, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return n, fmt.Errorf("events: replay: no message for %s with %d partition(s) unread", opts.Stall, len(pending))
			}
			return n, fmt.Errorf("events: replay: fetch: %w", err)
		}
		end, ok := pending[km.Partition]
		if !ok || km.Offset >= end {
			continue // dead-lettered after start (possibly by this replay); left for the next run
		}
		deadline = time.Now().Add(opts.Stall)
		if km.Offset >= end-1 {
			delete(pending, km.Partition)
		}

		if opts.OnlyGroup == "" || headerValue(km.Headers, "dlq_group") == opts.OnlyGroup {
			fields := []zap.Field{
				zap.Int("partition", km.Partition), zap.Int64("offset", km.Offset),
				zap.String("event_id", headerValue(km.Headers, "event_id")),
				zap.String("dlq_error", headerValue(km.Headers, "dlq_error")),
			}
			if opts.DryRun {
				n++
				log.Info("events: replay: would republish", fields...)
				continue
			}
			if err := w.WriteMessages(ctx, kafka.Message{
				Topic: opts.Topic, Key: km.Key, Value: km.Value, Headers: stripDLQHeaders(km.Headers),
			}); err != nil {
				return n, fmt.Errorf("events: replay: publish offset %d: %w", km.Offset, err)
			}
			n++
			log.Info("events: replay: republished", fields...)
		}
		if opts.DryRun {
			continue
		}
		if err := r.CommitMessages(ctx, km); err != nil {
			return n, fmt.Errorf("events: replay: commit offset %d: %w", km.Offset, err)
		}
	}
	return n, nil
}

// replayBounds maps each DLQ partition with unread messages for cfg.GroupID to its end offset at start.
func replayBounds(ctx context.Context, cfg Config, dlq string) (map[int]int64, error) {
	client := &kafka.Client{Addr: kafka.TCP(cfg.Brokers...), Timeout: cfg.WriteTimeout}
	md, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{dlq}})
	if err != nil {
		return nil, fmt.Errorf("events: replay: metadata: %w", err)
	}
	if len(md.Topics) != 1 || md.Topics[0].Error != nil {
		var topicErr error
		if len(md.Topics) == 1 {
			topicErr = md.Topics[0].Error
		}
		return nil, fmt.Errorf("events: replay: topic %s: %w", dlq, cmp.Or[error](topicErr, kafka.UnknownTopicOrPartition))
	}

	var reqs []kafka.OffsetRequest
	var parts []int
	for _, p := range md.Topics[0].Partitions {
		reqs = append(reqs, kafka.FirstOffsetOf(p.ID), kafka.LastOffsetOf(p.ID))
		parts = append(parts, p.ID)
	}
	offs, err := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{dlq: reqs}})
	if err != nil {
		return nil, fmt.Errorf("events: replay: list offsets: %w", err)
	}
	fetched, err := fetchCommitted(ctx, client, cfg.GroupID, dlq, parts)
	if err != nil {
		return nil, err
	}
	committed := map[int]int64{}
	for _, p := range fetched.Topics[dlq] {
		if p.Error != nil {
			return nil, fmt.Errorf("events: replay: committed offset of partition %d: %w", p.Partition, p.Error)
		}
		committed[p.Partition] = p.CommittedOffset
	}

	pending := map[int]int64{}
	for _, po := range offs.Topics[dlq] {
		if po.Error != nil {
			return nil, fmt.Errorf("events: replay: offsets of partition %d: %w", po.Partition, po.Error)
		}
		start := po.FirstOffset
		if c, ok := committed[po.Partition]; ok && c >= 0 {
			start = max(c, po.FirstOffset)
		}
		if start < po.LastOffset {
			pending[po.Partition] = po.LastOffset
		}
	}
	return pending, nil
}

const maxOffsetFetchAttempts = 20

// fetchCommitted retries coordinator errors, which brokers return while a new group's coordinator loads.
func fetchCommitted(ctx context.Context, client *kafka.Client, group, topic string, parts []int) (*kafka.OffsetFetchResponse, error) {
	for attempt, backoff := 1, 100*time.Millisecond; ; attempt, backoff = attempt+1, min(backoff*2, 2*time.Second) {
		resp, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: parts}})
		if err == nil {
			err = resp.Error
		}
		if err == nil {
			return resp, nil
		}
		var kerr kafka.Error
		if !errors.As(err, &kerr) || !kerr.Temporary() || attempt >= maxOffsetFetchAttempts {
			return nil, fmt.Errorf("events: replay: fetch committed offsets: %w", err)
		}
		if !relaySleep(ctx, backoff) {
			return nil, ctx.Err()
		}
	}
}

func headerValue(hs []kafka.Header, key string) string {
	for _, h := range hs {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func stripDLQHeaders(hs []kafka.Header) []kafka.Header {
	return slices.DeleteFunc(slices.Clone(hs), func(h kafka.Header) bool {
		return strings.HasPrefix(h.Key, "dlq_")
	})
}
