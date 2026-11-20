package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/undeluro/kardan-alerts/internal/event"
)

// Schema is a structure the service has seen, with its fields, detected
// finance meaning and facet mapping.
type Schema struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Sources      []string          `json:"sources"`
	Format       string            `json:"format"`
	Fields       []FieldInfo       `json:"fields"`
	AutoFacets   map[string]string `json:"autoFacets"`
	ManualFacets map[string]string `json:"manualFacets"`
	Sample       json.RawMessage   `json:"sample,omitempty"`
	SampleRaw    string            `json:"sampleRaw,omitempty"`
	Count        int64             `json:"count"`
	FirstSeen    time.Time         `json:"firstSeen"`
	LastSeen     time.Time         `json:"lastSeen"`
}

// Facets is the effective mapping: manual entries override automatic ones.
func (s *Schema) Facets() map[string]string { return merge(s.AutoFacets, s.ManualFacets) }

func merge(auto, manual map[string]string) map[string]string {
	out := make(map[string]string, len(auto)+len(manual))
	for k, v := range auto {
		out[k] = v
	}
	for k, v := range manual {
		if v == "" {
			delete(out, k) // explicit "no mapping"
		} else {
			out[k] = v
		}
	}
	return out
}

// Registry identifies the schema of each event and keeps the catalogue in
// Postgres. Lookups are in-memory; detection runs once per new structure.
type Registry struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger

	mu     sync.RWMutex
	known  map[string]*known // signature -> detection results
	stored map[string]bool   // signatures already persisted by this replica
	manual atomic.Pointer[map[string]map[string]string]
}

const maxKnown = 20000

type known struct {
	auto  map[string]string // facet -> path
	kinds map[string]string // path -> detected finance kind (for classification)
}

func NewRegistry(pool *pgxpool.Pool, log *slog.Logger) *Registry {
	r := &Registry{Pool: pool, Log: log, known: map[string]*known{}, stored: map[string]bool{}}
	empty := map[string]map[string]string{}
	r.manual.Store(&empty)
	return r
}

// Identify returns the event's signature and effective facet overrides.
// fields is non-nil only the first time this replica sees the signature.
func (r *Registry) Identify(payload []byte) (sig string, overrides map[string]string, fields []FieldInfo) {
	sig = Signature(payload)
	k := r.lookup(sig)
	if k == nil {
		fields = Flatten(payload)
		k = &known{auto: AutoFacets(fields), kinds: map[string]string{}}
		for _, f := range fields {
			if f.Detected != "" {
				k.kinds[f.Path] = f.Detected
			}
		}
		r.mu.Lock()
		if len(r.known) < maxKnown {
			r.known[sig] = k
		}
		r.mu.Unlock()
	}
	manual := (*r.manual.Load())[sig]
	if len(manual) == 0 {
		return sig, k.auto, fields
	}
	return sig, merge(k.auto, manual), fields
}

func (r *Registry) lookup(sig string) *known {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.known[sig]
}

// Kinds returns path -> detected finance kind for a signature seen by
// Identify (used to classify fields for masking).
func (r *Registry) Kinds(sig string) map[string]string {
	if k := r.lookup(sig); k != nil {
		return k.kinds
	}
	return nil
}

// Observation aggregates one batch's events of a signature.
type Observation struct {
	Signature string
	Source    string
	Format    string
	Fields    []FieldInfo // set when first seen by this replica
	Sample    *event.Envelope
	Count     int64
	LastSeen  time.Time
}

// Record persists a batch of observations. It returns the schemas that are
// new cluster-wide (for "new schema detected" notifications).
func (r *Registry) Record(ctx context.Context, obs []*Observation) ([]Schema, error) {
	if len(obs) == 0 {
		return nil, nil
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].Signature < obs[j].Signature })
	b := &pgx.Batch{}
	for _, o := range obs {
		r.mu.RLock()
		stored := r.stored[o.Signature]
		r.mu.RUnlock()
		if stored || o.Fields == nil {
			b.Queue(`UPDATE schemas SET count = count + $2, last_seen = GREATEST(last_seen, $3),
			           sources = CASE WHEN $4 = ANY(sources) THEN sources ELSE array_append(sources, $4) END
			         WHERE id = $1 RETURNING false`, o.Signature, o.Count, o.LastSeen, o.Source)
			continue
		}
		fields, _ := json.Marshal(o.Fields)
		auto, _ := json.Marshal(AutoFacets(o.Fields))
		b.Queue(`INSERT INTO schemas (id, name, sources, format, fields, auto_facets, sample, sample_raw, count, first_seen, last_seen)
		         VALUES ($1, $2, ARRAY[$3], $4, $5, $6, $7, $8, $9, $10, $10)
		         ON CONFLICT (id) DO UPDATE SET count = schemas.count + EXCLUDED.count,
		           last_seen = GREATEST(schemas.last_seen, EXCLUDED.last_seen),
		           sources = CASE WHEN $3 = ANY(schemas.sources) THEN schemas.sources ELSE array_append(schemas.sources, $3) END
		         RETURNING (xmax = 0)`,
			o.Signature, nameFor(o), o.Source, o.Format, fields, auto, o.Sample.Payload, o.Sample.Raw, o.Count, o.LastSeen)
	}
	br := r.Pool.SendBatch(ctx, b)
	defer br.Close()
	var created []Schema
	for _, o := range obs {
		var inserted bool
		err := br.QueryRow().Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			// The row does not exist yet (an earlier insert failed): forget
			// the signature so the next event re-detects and inserts it.
			r.mu.Lock()
			delete(r.stored, o.Signature)
			delete(r.known, o.Signature)
			r.mu.Unlock()
			continue
		}
		if err != nil {
			return created, fmt.Errorf("record schema %s: %w", o.Signature, err)
		}
		r.mu.Lock()
		r.stored[o.Signature] = true
		r.mu.Unlock()
		if inserted {
			created = append(created, Schema{ID: o.Signature, Name: nameFor(o), Sources: []string{o.Source},
				Format: o.Format, Fields: o.Fields, Count: o.Count, FirstSeen: o.LastSeen, LastSeen: o.LastSeen})
		}
	}
	return created, br.Close()
}

// nameFor gives a schema a readable default name from its $type, else source.
func nameFor(o *Observation) string {
	d := NewDoc(o.Sample)
	if t := d.Get("$type").String(); t != "" {
		return t
	}
	return o.Source + " event"
}

// ReloadManual refreshes person-set facet overrides from the database.
func (r *Registry) ReloadManual(ctx context.Context) error {
	rows, err := r.Pool.Query(ctx, `SELECT id, manual_facets FROM schemas WHERE manual_facets <> '{}'::jsonb`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[string]map[string]string{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		f := map[string]string{}
		if err := json.Unmarshal(raw, &f); err == nil {
			m[id] = f
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.manual.Store(&m)
	return nil
}

// ---- catalogue queries (API) ---------------------------------------------------

const schemaCols = `id, name, sources, format, fields, manual_facets, sample, sample_raw, count, first_seen, last_seen`

func scanSchema(row pgx.Row) (*Schema, error) {
	var s Schema
	var fields, manual []byte
	err := row.Scan(&s.ID, &s.Name, &s.Sources, &s.Format, &fields, &manual, &s.Sample, &s.SampleRaw,
		&s.Count, &s.FirstSeen, &s.LastSeen)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(fields, &s.Fields)
	_ = json.Unmarshal(manual, &s.ManualFacets)
	// Automatic mappings are a pure function of the fields: recompute them so
	// detection improvements apply to schemas stored before the upgrade.
	s.AutoFacets = AutoFacets(s.Fields)
	return &s, nil
}

func (r *Registry) List(ctx context.Context, limit int) ([]*Schema, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.Pool.Query(ctx, `SELECT `+schemaCols+` FROM schemas ORDER BY last_seen DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Schema{}
	for rows.Next() {
		s, err := scanSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

var ErrNotFound = errors.New("schema not found")

func (r *Registry) Get(ctx context.Context, id string) (*Schema, error) {
	s, err := scanSchema(r.Pool.QueryRow(ctx, `SELECT `+schemaCols+` FROM schemas WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// SetManualFacets stores person-set mappings ({"$severity": "criticity"}) and
// renames the schema if name is non-empty.
func (r *Registry) SetManualFacets(ctx context.Context, id, name string, facets map[string]string) (*Schema, error) {
	for k, p := range facets {
		if !IsFacet(k) || strings.HasPrefix(k, "$source") || k == "$id" || k == "$receivedAt" || k == "$raw" || k == "$format" {
			return nil, fmt.Errorf("%q is not a mappable facet", k)
		}
		if len(p) > 256 || strings.ContainsAny(p, "\n\r") {
			return nil, fmt.Errorf("bad path %q", p)
		}
	}
	raw, _ := json.Marshal(facets)
	s, err := scanSchema(r.Pool.QueryRow(ctx, `
		UPDATE schemas SET manual_facets = $2, name = CASE WHEN $3 = '' THEN name ELSE $3 END
		WHERE id = $1 RETURNING `+schemaCols, id, raw, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}
