// Command loadgen measures Kardan under load: throughput, ingest
// acknowledgement latency and end-to-end latency (event accepted -> SMS
// accepted by the provider), checking that nothing is lost.
//
// It sets up its own fixtures through the API (a synthetic person, team and
// rule that sends every matching event by SMS), drives HTTP or Kafka ingest
// at a target rate, waits until every expected SMS reached the fake gateway,
// and prints a Markdown report. Scenarios:
//
//	steady     constant rate, every event a new alert (worst case: no dedup)
//	realistic  constant rate, 10% new alerts, 90% repeats folded by dedup
//	storm      "market open": base rate, then 10x for the middle third, then base
//
// Example (inside the compose network):
//
//	docker compose run --rm loadgen -scenario storm -rate 200 -duration 60s
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type config struct {
	api, gateway, prom string
	transport          string
	kafka, topic       string
	scenario           string
	rate               int
	duration           time.Duration
	batch, workers     int
	drain              time.Duration
	keep               bool
	label, out         string
}

func main() {
	var c config
	flag.StringVar(&c.api, "api", env("KARDAN_URL", "http://localhost:8080"), "Kardan API / ingest base URL")
	flag.StringVar(&c.gateway, "gateway", env("GATEWAY_URL", "http://localhost:8090"), "fake SMS gateway base URL")
	flag.StringVar(&c.prom, "prometheus", env("PROMETHEUS_URL", "http://localhost:9090"), "Prometheus base URL (server-side view; optional)")
	flag.StringVar(&c.transport, "transport", "http", "http (POST /ingest/loadtest/batch) or kafka")
	flag.StringVar(&c.kafka, "kafka", env("KAFKA_BROKERS", "localhost:19092"), "Kafka brokers for -transport kafka")
	flag.StringVar(&c.topic, "topic", "trade.events", "source topic for -transport kafka (consumed by an ingest source)")
	flag.StringVar(&c.scenario, "scenario", "steady", "steady | realistic | storm")
	flag.IntVar(&c.rate, "rate", 200, "events per second (base rate for storm)")
	flag.DurationVar(&c.duration, "duration", 30*time.Second, "how long to send")
	flag.IntVar(&c.batch, "batch", 20, "events per HTTP request / Kafka produce")
	flag.IntVar(&c.workers, "workers", 16, "concurrent senders")
	flag.DurationVar(&c.drain, "drain", 3*time.Minute, "how long to wait for deliveries after sending")
	flag.BoolVar(&c.keep, "keep", false, "keep the loadtest rule, team and person afterwards")
	flag.StringVar(&c.label, "label", "", "label for this run in the report (e.g. \"3 workers\")")
	flag.StringVar(&c.out, "out", "", "append the JSON result to this file")
	flag.Parse()
	if err := run(c); err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---- fixtures ---------------------------------------------------------------------

const (
	personID = "loadtest-bot"
	teamID   = "loadtest"
	ruleID   = "loadtest-sms"
)

// phone is a fictional number unique to this run (555-01xx range), so the
// gateway can count this run's deliveries separately from anything else.
var phone string

var ruleYAML = `id: loadtest-sms
name: Load test (synthetic, created by loadgen)
team: loadtest
match:
  all:
    - { field: loadtest, op: eq, value: true }
    - { field: run, op: eq, value: "%s" }
routing:
  - { to: "user:loadtest-bot", channels: [sms] }
presentation:
  title: "Load test {{fp}}"
  severity: critical
dedup:
  key: "{{fp}}"
autoResolve:
  after: 2m
`

func call(method, u string, body any, out any) error {
	var r io.Reader
	if s, ok := body.(string); ok {
		r = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, r)
	req.Header.Set("X-User", "loadgen")
	if _, ok := body.(string); ok {
		req.Header.Set("Content-Type", "application/yaml")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func setup(c config, run string) error {
	if err := call("PUT", c.api+"/api/people/"+personID, map[string]any{"name": "Load test bot", "phone": phone, "timezone": "UTC"}, nil); err != nil {
		return err
	}
	if err := call("PUT", c.api+"/api/teams/"+teamID, map[string]any{"name": "Load test (synthetic)", "members": []string{personID}}, nil); err != nil {
		return err
	}
	_ = call("DELETE", c.api+"/api/rules/"+ruleID, nil, nil)
	if err := call("POST", c.api+"/api/rules", fmt.Sprintf(ruleYAML, run), nil); err != nil {
		return err
	}
	time.Sleep(2 * time.Second) // rules reach every replica over Redis; the version check is the safety net
	return nil
}

// teardown closes this run's alerts and waits for their "all clear" messages
// to go out, so they cannot overlap the next run, then removes the fixtures.
func teardown(c config, run string) {
	fmt.Println("  cleaning up: resolving this run's alerts (their all-clear SMS go out now, not during the next run)")
	quick := strings.Replace(fmt.Sprintf(ruleYAML, run), "after: 2m", "after: 1s", 1)
	if err := call("PUT", c.api+"/api/rules/"+ruleID, quick, nil); err == nil {
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			var open []json.RawMessage
			if call("GET", c.api+"/api/alerts?team="+teamID+"&status=active&limit=1", nil, &open) == nil && len(open) == 0 {
				break
			}
			time.Sleep(time.Second)
		}
		_ = waitQuiet(c)
	}
	_ = call("DELETE", c.api+"/api/rules/"+ruleID, nil, nil)
	_ = call("DELETE", c.api+"/api/teams/"+teamID, nil, nil)
	_ = call("DELETE", c.api+"/api/people/"+personID, nil, nil)
}

// ---- sending ----------------------------------------------------------------------

type phase struct {
	Name       string
	Rate       int
	Duration   time.Duration
	Start, End time.Time
}

func phasesFor(c config) []phase {
	if c.scenario == "storm" {
		d := c.duration / 3
		return []phase{{Name: "before open", Rate: c.rate, Duration: d}, {Name: "market open (10x)", Rate: c.rate * 10, Duration: d},
			{Name: "after open", Rate: c.rate, Duration: c.duration - 2*d}}
	}
	return []phase{{Name: c.scenario, Rate: c.rate, Duration: c.duration}}
}

type sender interface {
	send(ctx context.Context, events [][]byte) error
	close()
}

type httpSender struct {
	url    string
	client *http.Client
}

func (h *httpSender) send(ctx context.Context, events [][]byte) error {
	body := bytes.Join(events, []byte("\n"))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ingest answered %d", resp.StatusCode)
	}
	return nil
}
func (h *httpSender) close() {}

type kafkaSender struct {
	cl    *kgo.Client
	topic string
}

func (k *kafkaSender) send(ctx context.Context, events [][]byte) error {
	recs := make([]*kgo.Record, len(events))
	for i, e := range events {
		recs[i] = &kgo.Record{Topic: k.topic, Value: e}
	}
	return k.cl.ProduceSync(ctx, recs...).FirstErr()
}
func (k *kafkaSender) close() { k.cl.Close() }

type result struct {
	Label      string        `json:"label"`
	Scenario   string        `json:"scenario"`
	Transport  string        `json:"transport"`
	Workers    int           `json:"kardanWorkers"`
	TargetRate int           `json:"targetRate"`
	Sent       int64         `json:"sent"`
	SendErrors int64         `json:"sendErrors"`
	SendRate   float64       `json:"achievedRate"`
	Ack        pct           `json:"ingestAckMs"`
	Expected   int64         `json:"expectedDeliveries"`
	Delivered  int64         `json:"delivered"`
	Lost       int64         `json:"lost"`
	Pipeline   float64       `json:"pipelineEventsPerSec"`
	E2E        []phaseResult `json:"endToEnd"`
	ServerE2E  pct           `json:"serverSideE2EMs"`
	StartedAt  time.Time     `json:"startedAt"`
}

type pct struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max,omitempty"`
	N   int     `json:"samples,omitempty"`
}

type phaseResult struct {
	Phase string `json:"phase"`
	Rate  int    `json:"rate"`
	pct
}

func run(c config) error {
	runID := strconv.FormatInt(time.Now().Unix(), 36)
	phone = fmt.Sprintf("+1 555 01%02d %03d", time.Now().Unix()%100, time.Now().UnixNano()/1e6%1000)
	if err := waitQuiet(c); err != nil {
		return err
	}
	fmt.Printf("loadgen run %s: %s, %s, %d ev/s for %s\n", runID, c.scenario, c.transport, c.rate, c.duration)
	if err := setup(c, runID); err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	keep := c.keep
	defer func() {
		if !keep {
			teardown(c, runID)
		}
	}()
	var s sender
	switch c.transport {
	case "http":
		s = &httpSender{url: c.api + "/ingest/loadtest/batch",
			client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: c.workers * 2}}}
	case "kafka":
		cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(c.kafka, ",")...), kgo.ProducerLinger(0))
		if err != nil {
			return err
		}
		s = &kafkaSender{cl: cl, topic: c.topic}
	default:
		return fmt.Errorf("unknown transport %q", c.transport)
	}
	defer s.close()

	// Warm up (connections, caches, consumer group) so the numbers describe
	// steady operation, then start measuring from zero.
	if err := warmUp(c, s, runID); err != nil {
		return err
	}
	if err := call("POST", c.gateway+"/api/stats/reset", nil, nil); err != nil {
		return fmt.Errorf("reset gateway stats: %w", err)
	}

	res := result{Label: c.label, Scenario: c.scenario, Transport: c.transport, TargetRate: c.rate, StartedAt: time.Now().UTC()}
	res.Workers = countWorkers(c)
	phases := phasesFor(c)

	var seq, sent, sendErrs atomic.Int64
	uniq := map[string]bool{}
	var uniqMu sync.Mutex
	var ackMu sync.Mutex
	var acks []float64
	ctx := context.Background()
	jobs := make(chan [][]byte, c.workers*4)
	var wg sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				t0 := time.Now()
				if err := s.send(ctx, batch); err != nil {
					sendErrs.Add(int64(len(batch)))
					continue
				}
				ms := float64(time.Since(t0).Microseconds()) / 1000
				sent.Add(int64(len(batch)))
				ackMu.Lock()
				acks = append(acks, ms)
				ackMu.Unlock()
			}
		}()
	}

	makeEvent := func() []byte {
		n := seq.Add(1)
		fp := runID + "-" + strconv.FormatInt(n, 10)
		if c.scenario == "realistic" && n%10 != 0 {
			fp = runID + "-r" + strconv.FormatInt(n%500, 10) // repeats of a small set of problems
		}
		uniqMu.Lock()
		uniq[fp] = true
		uniqMu.Unlock()
		b, _ := json.Marshal(map[string]any{"loadtest": true, "run": runID, "fp": fp, "seq": n, "type": "LOADTEST",
			"service": "loadgen", "severity": "critical", "sentAt": time.Now().UTC().Format(time.RFC3339Nano)})
		return b
	}

	start := time.Now()
	for i := range phases {
		p := &phases[i]
		p.Start = time.Now()
		fmt.Printf("  %-20s %5d ev/s for %s\n", p.Name, p.Rate, p.Duration.Round(time.Second))
		batchesPerSec := float64(p.Rate) / float64(c.batch)
		interval := time.Duration(float64(time.Second) / batchesPerSec)
		next := time.Now()
		for time.Since(p.Start) < p.Duration {
			batch := make([][]byte, c.batch)
			for j := range batch {
				batch[j] = makeEvent()
			}
			jobs <- batch
			next = next.Add(interval)
			if d := time.Until(next); d > 0 {
				time.Sleep(d)
			}
		}
		p.End = time.Now()
	}
	close(jobs)
	wg.Wait()
	sendTime := time.Since(start)
	res.Sent, res.SendErrors = sent.Load(), sendErrs.Load()
	res.SendRate = float64(res.Sent) / sendTime.Seconds()
	res.Ack = percentiles(acks)
	res.Expected = int64(len(uniq))
	if res.SendErrors > 0 {
		// Events that were refused at ingest are reported as send errors, not losses.
		fmt.Printf("  %d events were refused at ingest\n", res.SendErrors)
	}

	// Wait until every expected SMS reached the gateway.
	fmt.Printf("  sent %d events (%.0f/s); waiting for %d deliveries…\n", res.Sent, res.SendRate, res.Expected)
	deadline := time.Now().Add(c.drain)
	var last struct{ Received int64 }
	lastChange, lastCount := time.Now(), int64(-1)
	for time.Now().Before(deadline) {
		if err := call("GET", c.gateway+"/api/stats?kind=sms&purpose=alert&phone="+url.QueryEscape(phone), nil, &last); err == nil {
			if last.Received != lastCount {
				lastCount, lastChange = last.Received, time.Now()
			}
			if last.Received >= res.Expected {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	res.Delivered = last.Received
	res.Lost = max(0, res.Expected-res.Delivered)
	res.Pipeline = float64(res.Sent) / lastChange.Sub(start).Seconds()

	for _, p := range phases {
		var st struct {
			P50Ms, P95Ms, P99Ms, MaxMs float64
			Samples                    int
		}
		q := url.Values{"from": {p.Start.UTC().Format(time.RFC3339Nano)}, "to": {p.End.UTC().Format(time.RFC3339Nano)}, "phone": {phone}, "kind": {"sms"}, "purpose": {"alert"}}
		_ = call("GET", c.gateway+"/api/stats?"+q.Encode(), nil, &st)
		res.E2E = append(res.E2E, phaseResult{Phase: p.Name, Rate: p.Rate, pct: pct{P50: st.P50Ms, P95: st.P95Ms, P99: st.P99Ms, Max: st.MaxMs, N: st.Samples}})
	}
	time.Sleep(11 * time.Second) // two scrape intervals, so the slowest (latest) observations are in
	res.ServerE2E = promQuantiles(c, time.Since(start))
	report(res)
	if c.out != "" {
		f, err := os.OpenFile(c.out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			b, _ := json.Marshal(res)
			_, _ = f.Write(append(b, '\n'))
			f.Close()
		}
	}
	if res.Lost > 0 {
		keep = true // leave the alerts and outbox rows in place for inspection
		fmt.Printf("  kept rule %s and its alerts for inspection (SMS to %s)\n", ruleID, phone)
		return fmt.Errorf("%d of %d expected notifications did not arrive within %s", res.Lost, res.Expected, c.drain)
	}
	return nil
}

const warmUpEvents = 100

// waitQuiet waits until the gateway has had no deliveries for 5 seconds, so a
// previous run's backlog cannot overlap this one.
func waitQuiet(c config) error {
	deadline := time.Now().Add(10 * time.Minute)
	var last int64 = -1
	still := time.Now()
	for time.Now().Before(deadline) {
		var st struct{ Received int64 }
		if err := call("GET", c.gateway+"/api/stats", nil, &st); err != nil {
			return fmt.Errorf("gateway: %w", err)
		}
		if st.Received != last {
			last, still = st.Received, time.Now()
		} else if time.Since(still) >= 5*time.Second {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("the pipeline did not go quiet within 10 minutes")
}

func warmUp(c config, s sender, runID string) error {
	const n = warmUpEvents
	if err := call("POST", c.gateway+"/api/stats/reset", nil, nil); err != nil {
		return fmt.Errorf("reset gateway stats: %w", err)
	}
	for i := 0; i < n; i += 10 {
		var batch [][]byte
		for j := 0; j < 10; j++ {
			b, _ := json.Marshal(map[string]any{"loadtest": true, "run": runID, "fp": fmt.Sprintf("%s-warm-%d", runID, i+j),
				"type": "LOADTEST", "service": "loadgen", "severity": "critical"})
			batch = append(batch, b)
		}
		if err := s.send(context.Background(), batch); err != nil {
			return fmt.Errorf("warm-up: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var st struct{ Received int64 }
		if call("GET", c.gateway+"/api/stats?kind=sms&purpose=alert&phone="+url.QueryEscape(phone), nil, &st) == nil && st.Received >= n {
			fmt.Println("  warm-up delivered")
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("warm-up events were not delivered within 60s; is the stack healthy?")
}

func percentiles(xs []float64) pct {
	if len(xs) == 0 {
		return pct{}
	}
	sort.Float64s(xs)
	q := func(p float64) float64 { return xs[min(len(xs)-1, int(p*float64(len(xs))))] }
	return pct{P50: q(0.5), P95: q(0.95), P99: q(0.99), Max: xs[len(xs)-1], N: len(xs)}
}

// countWorkers asks Prometheus how many worker replicas it scrapes.
func countWorkers(c config) int {
	v, err := promQuery(c, `count(up{job="kardan-worker"} == 1)`)
	if err != nil || math.IsNaN(v) {
		return 0
	}
	return int(v)
}

// promQuantiles reads the server-side ingest-to-SMS histogram over the run.
func promQuantiles(c config, window time.Duration) pct {
	w := fmt.Sprintf("%ds", int(math.Ceil(window.Seconds()))) // measured run plus the scrape wait, not the warm-up
	get := func(q float64) float64 {
		v, err := promQuery(c, fmt.Sprintf(`histogram_quantile(%g, sum(increase(kardan_e2e_latency_seconds_bucket{channel="sms"}[%s])) by (le))`, q, w))
		if err != nil || math.IsNaN(v) {
			return 0
		}
		return v * 1000
	}
	return pct{P50: get(0.5), P95: get(0.95), P99: get(0.99)}
}

func promQuery(c config, q string) (float64, error) {
	if c.prom == "" {
		return math.NaN(), errors.New("no prometheus")
	}
	var out struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := call("GET", c.prom+"/api/v1/query?query="+url.QueryEscape(q), nil, &out); err != nil {
		return math.NaN(), err
	}
	if len(out.Data.Result) == 0 {
		return math.NaN(), errors.New("no data")
	}
	s, _ := out.Data.Result[0].Value[1].(string)
	return strconv.ParseFloat(s, 64)
}

func report(r result) {
	fmt.Println()
	title := r.Label
	if title == "" {
		title = r.Scenario
	}
	fmt.Printf("### %s\n\n", title)
	fmt.Printf("| | |\n|---|---|\n")
	fmt.Printf("| Scenario | %s over %s |\n", r.Scenario, r.Transport)
	if r.Workers > 0 {
		fmt.Printf("| Worker replicas | %d |\n", r.Workers)
	}
	fmt.Printf("| Target / achieved send rate | %d / %.0f events/s |\n", r.TargetRate, r.SendRate)
	fmt.Printf("| Events sent (refused) | %d (%d) |\n", r.Sent, r.SendErrors)
	fmt.Printf("| Ingest ack p50 / p99 | %.1f / %.1f ms per batch |\n", r.Ack.P50, r.Ack.P99)
	fmt.Printf("| Pipeline throughput | %.0f events/s (send start to last delivery) |\n", r.Pipeline)
	fmt.Printf("| Alert SMS expected / delivered / lost | %d / %d / %d |\n", r.Expected, r.Delivered, r.Lost)
	if r.ServerE2E.P50 > 0 {
		fmt.Printf("| Server-side e2e p50 / p95 / p99 (Prometheus) | %.0f / %.0f / %.0f ms |\n", r.ServerE2E.P50, r.ServerE2E.P95, r.ServerE2E.P99)
	}
	fmt.Printf("\nEnd to end (event accepted -> SMS accepted by provider, measured at the gateway):\n\n")
	fmt.Printf("| Phase | Rate (ev/s) | Samples | p50 ms | p95 ms | p99 ms | max ms |\n|---|---|---|---|---|---|---|\n")
	for _, p := range r.E2E {
		fmt.Printf("| %s | %d | %d | %.0f | %.0f | %.0f | %.0f |\n", p.Phase, p.Rate, p.N, p.P50, p.P95, p.P99, p.Max)
	}
	fmt.Println()
}
