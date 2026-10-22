package events

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// ErrTopicsMisconfigured marks topic-creation failures that retrying cannot fix (bad config, missing ACLs).
var ErrTopicsMisconfigured = errors.New("events: topics misconfigured")

// dlqRetention outlives the 7-day broker default so dead letters survive until someone replays them.
const dlqRetention = 30 * 24 * time.Hour

var permanentTopicErrors = []error{
	kafka.InvalidReplicationFactor, kafka.InvalidPartitionNumber, kafka.InvalidConfiguration,
	kafka.InvalidTopic, kafka.TopicAuthorizationFailed, kafka.ClusterAuthorizationFailed,
	kafka.InvalidReplicaAssignment, kafka.InvalidRequest, kafka.PolicyViolation,
}

// EnsureTopics creates missing topics and leaves existing ones untouched. No-op unless cfg.CreateTopics.
func EnsureTopics(ctx context.Context, cfg Config, topics ...string) error {
	if !cfg.CreateTopics || len(topics) == 0 {
		return nil
	}
	cfgs := make([]kafka.TopicConfig, 0, len(topics))
	for _, t := range topics {
		cfgs = append(cfgs, topicConfig(cfg, t))
	}
	client := &kafka.Client{Addr: kafka.TCP(cfg.Brokers...), Timeout: cfg.WriteTimeout}
	resp, err := client.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: cfgs})
	if err != nil {
		return fmt.Errorf("events: create topics: %w", err)
	}
	var errs []error
	for name, err := range resp.Errors {
		if err == nil || errors.Is(err, kafka.TopicAlreadyExists) {
			continue
		}
		for _, p := range permanentTopicErrors {
			if errors.Is(err, p) {
				err = fmt.Errorf("%w: %w", ErrTopicsMisconfigured, err)
				break
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("events: create topics: %w", errors.Join(errs...))
	}
	return nil
}

func topicConfig(cfg Config, topic string) kafka.TopicConfig {
	tc := kafka.TopicConfig{Topic: topic, NumPartitions: cfg.TopicPartitions, ReplicationFactor: cfg.TopicReplication}
	// acks=all only guards against loss when a write needs more than the leader; with RF=2 that
	// would also forbid losing any broker, so the broker default stays below RF 3.
	if cfg.TopicReplication >= 3 {
		tc.ConfigEntries = append(tc.ConfigEntries, kafka.ConfigEntry{ConfigName: "min.insync.replicas", ConfigValue: "2"})
	}
	if strings.HasSuffix(topic, dlqSuffix) {
		tc.ConfigEntries = append(tc.ConfigEntries, kafka.ConfigEntry{
			ConfigName: "retention.ms", ConfigValue: strconv.FormatInt(dlqRetention.Milliseconds(), 10),
		})
	}
	return tc
}

const dlqSuffix = ".dlq"

func dlqTopic(topic string) string { return topic + dlqSuffix }
