package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/redis/go-redis/v9"

	"github.com/undeluro/kardan-alerts/internal/event"
	"github.com/undeluro/kardan-alerts/internal/ingest/parse"
)

// SourceConfig declares a source in config/sources.yaml. Adding a Kafka topic
// or a log file is configuration only; HTTP sources need no entry at all
// (any POST /ingest/{id} works) unless they want a specific parser.
type SourceConfig struct {
	ID          string `yaml:"id" json:"id"`
	Type        string `yaml:"type" json:"type"` // http | kafka | file
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Enabled     *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	Parser  string `yaml:"parser,omitempty" json:"parser,omitempty"`   // auto (default), json, fix, iso20022, xml, logfmt, regex, text
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"` // regex parser

	// kafka
	Topics  []string `yaml:"topics,omitempty" json:"topics,omitempty"`
	Brokers []string `yaml:"brokers,omitempty" json:"brokers,omitempty"` // default: KAFKA_BROKERS
	Create  bool     `yaml:"create,omitempty" json:"create,omitempty"`   // create topics if missing (demo)

	// file
	Path string `yaml:"path,omitempty" json:"path,omitempty"` // file or glob
	From string `yaml:"from,omitempty" json:"from,omitempty"` // end (default) | beginning — for files present at start
}

func (c SourceConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

func (c SourceConfig) hash() string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// LoadSources reads and validates sources.yaml. A missing file means "HTTP only".
func LoadSources(path string) ([]SourceConfig, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Sources []SourceConfig `yaml:"sources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, s := range doc.Sources {
		at := fmt.Sprintf("sources[%d] (%s)", i, s.ID)
		if !sourceIDPattern.MatchString(s.ID) {
			return nil, fmt.Errorf("%s: id must match [A-Za-z0-9._-]{1,64}", at)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("%s: duplicate id", at)
		}
		seen[s.ID] = true
		if _, err := parse.New(parse.Spec{Name: s.Parser, Pattern: s.Pattern}); err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		switch s.Type {
		case "http":
		case "kafka":
			if len(s.Topics) == 0 {
				return nil, fmt.Errorf("%s: kafka source needs topics", at)
			}
		case "file":
			if s.Path == "" {
				return nil, fmt.Errorf("%s: file source needs a path", at)
			}
			if s.From != "" && s.From != "end" && s.From != "beginning" {
				return nil, fmt.Errorf("%s: from must be end or beginning", at)
			}
		default:
			return nil, fmt.Errorf("%s: unknown type %q (http, kafka, file)", at, s.Type)
		}
	}
	return doc.Sources, nil
}

// SourceStatus is reported by the API.
type SourceStatus struct {
	SourceConfig
	Running  bool      `json:"running"`
	Owner    bool      `json:"owner,omitempty"` // file sources: this replica holds the lease
	Events   int64     `json:"events"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	Error    string    `json:"error,omitempty"`
}

type running struct {
	cfg    SourceConfig
	hash   string
	cancel context.CancelFunc
	done   chan struct{}
	stats  *sourceStats
}

type sourceStats struct {
	events   atomic.Int64
	lastSeen atomic.Int64 // unix nanos
	owner    atomic.Bool
	errMu    sync.Mutex
	err      string
}

func (s *sourceStats) observe(n int) {
	s.events.Add(int64(n))
	s.lastSeen.Store(time.Now().UnixNano())
}

func (s *sourceStats) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if err == nil {
		s.err = ""
	} else {
		s.err = err.Error()
	}
}

// Manager runs the configured sources and applies configuration changes
// live: added sources start, removed ones stop, changed ones restart.
type Manager struct {
	Producer       *Producer
	Redis          *redis.Client
	DefaultBrokers []string
	InstanceID     string
	Log            *slog.Logger

	mu      sync.Mutex
	running map[string]*running
	parsers atomic.Pointer[map[string]parse.Parser]
	httpIDs atomic.Pointer[map[string]SourceConfig]
}

// ParserFor returns the parser configured for a source id (auto by default).
func (m *Manager) ParserFor(id string) parse.Parser {
	if p := m.parsers.Load(); p != nil {
		if parser, ok := (*p)[id]; ok {
			return parser
		}
	}
	return parse.Auto{}
}

// Apply reconciles running sources with cfgs.
func (m *Manager) Apply(ctx context.Context, cfgs []SourceConfig) {
	parsers := map[string]parse.Parser{}
	declared := map[string]SourceConfig{}
	for _, c := range cfgs {
		if p, err := parse.New(parse.Spec{Name: c.Parser, Pattern: c.Pattern}); err == nil {
			parsers[c.ID] = p
		}
		declared[c.ID] = c
	}
	m.parsers.Store(&parsers)
	m.httpIDs.Store(&declared)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		m.running = map[string]*running{}
	}
	want := map[string]SourceConfig{}
	for _, c := range cfgs {
		if c.IsEnabled() && c.Type != "http" {
			want[c.ID] = c
		}
	}
	for id, r := range m.running {
		if c, ok := want[id]; !ok || c.hash() != r.hash {
			r.cancel()
			<-r.done
			delete(m.running, id)
			m.Log.Info("source stopped", "source", id)
		}
	}
	for id, c := range want {
		if _, ok := m.running[id]; ok {
			continue
		}
		sctx, cancel := context.WithCancel(ctx)
		r := &running{cfg: c, hash: c.hash(), cancel: cancel, done: make(chan struct{}), stats: &sourceStats{}}
		m.running[id] = r
		parser := parsers[id]
		go func() {
			defer close(r.done)
			m.run(sctx, c, parser, r.stats)
		}()
		m.Log.Info("source started", "source", id, "type", c.Type)
	}
}

func (m *Manager) run(ctx context.Context, c SourceConfig, p parse.Parser, st *sourceStats) {
	log := m.Log.With("source", c.ID, "type", c.Type)
	for ctx.Err() == nil {
		var err error
		switch c.Type {
		case "kafka":
			err = runKafka(ctx, c, p, m, st, log)
		case "file":
			err = runFile(ctx, c, p, m, st, log)
		}
		st.setErr(err)
		if ctx.Err() != nil {
			return
		}
		log.Error("source failed, restarting in 5s", "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

// Status lists declared sources with runtime counters.
func (m *Manager) Status() []SourceStatus {
	declared := m.httpIDs.Load()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SourceStatus
	if declared != nil {
		for _, c := range *declared {
			s := SourceStatus{SourceConfig: c}
			if r, ok := m.running[c.ID]; ok {
				s.Running = true
				s.Owner = r.stats.owner.Load()
				s.Events = r.stats.events.Load()
				if ns := r.stats.lastSeen.Load(); ns > 0 {
					s.LastSeen = time.Unix(0, ns)
				}
				r.stats.errMu.Lock()
				s.Error = r.stats.err
				r.stats.errMu.Unlock()
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// NewEnvelope parses one raw message into an envelope. Parsing never drops a
// message: anything a parser rejects is kept as text.
func NewEnvelope(p parse.Parser, source string, raw []byte, meta map[string]string) *event.Envelope {
	if p == nil {
		p = parse.Auto{}
	}
	res, err := p.Parse(raw)
	if err != nil {
		res, _ = parse.Text{}.Parse(raw)
		res.Format = "unparsed"
	}
	return &event.Envelope{
		ID:         event.NewID(),
		SourceID:   source,
		ReceivedAt: time.Now().UTC(),
		Payload:    res.Payload,
		Format:     res.Format,
		Raw:        res.Raw,
		Meta:       meta,
	}
}
