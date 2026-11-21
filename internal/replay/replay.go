// Package replay reads past events back from the durable events topic, so a
// draft rule can be tried against real traffic ("would have matched 132× in
// the last hour") before anyone is paged by it.
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/undeluro/kardan-alerts/internal/event"
)

// Replayer reads the events topic with a throwaway, group-less consumer, so a
// replay never moves the processors' committed offsets.
type Replayer struct {
	Brokers []string
	Topic   string
}

type Stats struct {
	Scanned   int       `json:"scanned"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Truncated bool      `json:"truncated"` // stopped at the event or time budget
}

// Since calls fn for every event appended at or after `since`, oldest first
// within each partition, stopping after max events or when ctx ends.
func (r *Replayer) Since(ctx context.Context, since time.Time, max int, fn func(*event.Envelope)) (Stats, error) {
	st := Stats{From: since, To: time.Now().UTC()}
	admCl, err := kgo.NewClient(kgo.SeedBrokers(r.Brokers...), kgo.ClientID("kardan-replay"))
	if err != nil {
		return st, err
	}
	defer admCl.Close()
	adm := kadm.NewClient(admCl)
	starts, err := adm.ListOffsetsAfterMilli(ctx, since.UnixMilli(), r.Topic)
	if err != nil {
		return st, err
	}
	ends, err := adm.ListEndOffsets(ctx, r.Topic)
	if err != nil {
		return st, err
	}
	if err := errors.Join(starts.Error(), ends.Error()); err != nil {
		return st, err
	}
	from := map[int32]kgo.Offset{}
	remaining := map[int32]int64{} // partition -> offset to reach
	starts.Each(func(o kadm.ListedOffset) {
		end, ok := ends.Lookup(r.Topic, o.Partition)
		if !ok || o.Offset < 0 || o.Offset >= end.Offset {
			return
		}
		from[o.Partition] = kgo.NewOffset().At(o.Offset)
		remaining[o.Partition] = end.Offset
	})
	if len(from) == 0 {
		return st, nil
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(r.Brokers...), kgo.ClientID("kardan-replay"),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{r.Topic: from}),
		kgo.FetchMaxWait(200*time.Millisecond))
	if err != nil {
		return st, err
	}
	defer cl.Close()
	for len(remaining) > 0 {
		if ctx.Err() != nil {
			st.Truncated = true
			return st, nil
		}
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			st.Truncated = true
			return st, nil
		}
		for _, fe := range fetches.Errors() {
			if !errors.Is(fe.Err, context.DeadlineExceeded) && !errors.Is(fe.Err, context.Canceled) {
				return st, fe.Err
			}
		}
		for _, rec := range fetches.Records() {
			end, open := remaining[rec.Partition]
			if !open {
				continue
			}
			if rec.Offset >= end-1 {
				delete(remaining, rec.Partition)
			}
			if rec.Offset >= end {
				continue
			}
			env := &event.Envelope{}
			if json.Unmarshal(rec.Value, env) != nil {
				continue
			}
			st.Scanned++
			fn(env)
			if st.Scanned >= max {
				st.Truncated = true
				return st, nil
			}
		}
	}
	return st, nil
}
