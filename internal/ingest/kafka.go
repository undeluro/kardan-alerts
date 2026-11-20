package ingest

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/ingest/parse"
	"github.com/undeluro/kardan-alerts/internal/platform/kafka"
	"github.com/undeluro/kardan-alerts/internal/platform/metrics"
)

// runKafka consumes external topics with a consumer group per source, so
// several ingest replicas share the partitions. Offsets are committed only
// after the events are durably in the events topic.
func runKafka(ctx context.Context, c SourceConfig, p parse.Parser, m *Manager, st *sourceStats, log *slog.Logger) error {
	brokers := c.Brokers
	if len(brokers) == 0 {
		brokers = m.DefaultBrokers
	}
	if c.Create {
		admin, err := kafka.NewClient(ctx, brokers, log)
		if err != nil {
			return err
		}
		for _, t := range c.Topics {
			if err := kafka.EnsureTopic(ctx, admin, t, 3, log); err != nil {
				admin.Close()
				return err
			}
		}
		admin.Close()
	}
	reset := kgo.NewOffset().AtEnd()
	if c.From == "beginning" {
		reset = kgo.NewOffset().AtStart()
	}
	cl, err := kafka.NewClient(ctx, brokers, log,
		kgo.ConsumerGroup("kardan-source-"+c.ID),
		kgo.ConsumeTopics(c.Topics...),
		kgo.ConsumeResetOffset(reset),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	)
	if err != nil {
		return err
	}
	defer cl.Close()
	log.Info("consuming", "topics", c.Topics)

	for {
		fetches := cl.PollRecords(ctx, 1000)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return nil
		}
		fetches.EachError(func(t string, part int32, err error) {
			log.Warn("fetch error", "topic", t, "partition", part, "err", err)
		})
		recs := fetches.Records()
		if len(recs) > 0 {
			envs := make([]*event.Envelope, 0, len(recs))
			for _, r := range recs {
				meta := map[string]string{"transport": "kafka", "topic": r.Topic,
					"partition": strconv.Itoa(int(r.Partition)), "offset": strconv.FormatInt(r.Offset, 10)}
				if len(r.Key) > 0 {
					meta["key"] = string(r.Key)
				}
				envs = append(envs, NewEnvelope(p, c.ID, r.Value, meta))
			}
			if err := appendWithRetry(ctx, m.Producer, envs, log); err != nil {
				return err // ctx cancelled; uncommitted records are re-read on restart
			}
			metrics.EventsIngested.WithLabelValues(c.ID).Add(float64(len(envs)))
			st.observe(len(envs))
			if err := cl.CommitRecords(ctx, recs...); err != nil && ctx.Err() == nil {
				log.Warn("commit offsets", "err", err)
			}
		}
		cl.AllowRebalance()
	}
}
