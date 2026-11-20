// Package kafka wraps franz-go client construction and topic setup.
// Any Kafka-API broker works; docker-compose uses Redpanda.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// NewClient builds a client and waits until the cluster answers metadata
// requests, so callers fail fast on misconfiguration but tolerate slow startup.
func NewClient(ctx context.Context, brokers []string, log *slog.Logger, opts ...kgo.Opt) (*kgo.Client, error) {
	base := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ClientID("kardan"),
	}
	cl, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, err
	}
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := cl.Ping(pingCtx)
		cancel()
		if err == nil {
			return cl, nil
		}
		log.Warn("waiting for dependency", "dependency", "kafka", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			cl.Close()
			return nil, fmt.Errorf("kafka: %w (last error: %v)", ctx.Err(), err)
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}

// EnsureTopic creates the topic if it does not exist. Partitions bound the
// number of processor replicas that can consume in parallel.
func EnsureTopic(ctx context.Context, cl *kgo.Client, topic string, partitions int, log *slog.Logger) error {
	adm := kadm.NewClient(cl)
	resp, err := adm.CreateTopic(ctx, int32(partitions), 1, nil, topic)
	if err == nil && resp.Err == nil {
		log.Info("topic created", "topic", topic, "partitions", partitions)
		return nil
	}
	if err == nil {
		err = resp.Err
	}
	if errors.Is(err, kerr.TopicAlreadyExists) {
		return nil
	}
	return fmt.Errorf("create topic %s: %w", topic, err)
}
