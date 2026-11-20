package runbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Catalog is the loaded set of runbooks, swapped atomically on reload.
type Catalog struct {
	LoadedAt time.Time
	Runbooks []*Runbook
	Errors   []string
	byID     map[string]*Runbook
}

func NewCatalog(list []*Runbook, errs []string) *Catalog {
	c := &Catalog{LoadedAt: time.Now(), Errors: errs, byID: map[string]*Runbook{}}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	for _, rb := range list {
		c.byID[rb.ID] = rb
	}
	c.Runbooks = list
	return c
}

func (c *Catalog) Get(id string) (*Runbook, bool) {
	if c == nil {
		return nil, false
	}
	rb, ok := c.byID[id]
	return rb, ok
}

// Target describes an alert for attachment: its rule, the runbooks the rule
// names, its event's schema, and the event itself.
type Target struct {
	RuleID       string
	RuleRunbooks []string
	SchemaID     string
	SchemaName   string
	Doc          *schema.Doc
}

// For returns the runbooks attached to an alert, in a stable order: those the
// rule names first, then by rule, schema and matching condition.
func (c *Catalog) For(t Target) []*Runbook {
	if c == nil {
		return nil
	}
	var out []*Runbook
	seen := map[string]bool{}
	add := func(rb *Runbook) {
		if rb != nil && !seen[rb.ID] {
			seen[rb.ID] = true
			out = append(out, rb)
		}
	}
	for _, id := range t.RuleRunbooks {
		add(c.byID[id])
	}
	for _, rb := range c.Runbooks {
		switch {
		case t.RuleID != "" && contains(rb.Rules, t.RuleID):
			add(rb)
		case t.SchemaID != "" && (contains(rb.Schemas, t.SchemaID) || (t.SchemaName != "" && contains(rb.Schemas, t.SchemaName))):
			add(rb)
		case t.Doc != nil && rb.MatchesDoc(t.Doc):
			add(rb)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Holder keeps the current catalog for every component of a replica.
type Holder struct{ p atomic.Pointer[Catalog] }

func (h *Holder) Current() *Catalog {
	if c := h.p.Load(); c != nil {
		return c
	}
	return NewCatalog(nil, nil)
}
func (h *Holder) Swap(c *Catalog) { h.p.Store(c) }

// ---- files ---------------------------------------------------------------------

var extFormat = map[string]string{".yaml": "", ".yml": "", ".md": "", ".markdown": "", ".txt": FormatText, ".url": FormatLink}

// LoadDir reads config/runbooks: YAML, Markdown/checklists (.md), free text
// (.txt) and links (.url). The file name is the id unless the file says one.
func LoadDir(dir string) ([]*Runbook, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{err.Error()}
	}
	var list []*Runbook
	var errs []string
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		format, ok := extFormat[ext]
		if e.IsDir() || !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		rb, err := Parse(string(data), Meta{ID: id, Format: format})
		if err != nil {
			errs = append(errs, fmt.Sprintf("runbooks/%s: %v", e.Name(), err))
			continue
		}
		rb.Origin = "file:" + e.Name()
		list = append(list, rb)
	}
	return list, errs
}

// Merge lays stored runbooks over file ones (same id: the stored one wins).
func Merge(files, stored []*Runbook) []*Runbook {
	byID := map[string]*Runbook{}
	for _, rb := range files {
		byID[rb.ID] = rb
	}
	for _, rb := range stored {
		if f, ok := byID[rb.ID]; ok && f.Origin != "api" {
			rb.Notes = append(rb.Notes, "overrides "+f.Origin)
		}
		byID[rb.ID] = rb
	}
	out := make([]*Runbook, 0, len(byID))
	for _, rb := range byID {
		out = append(out, rb)
	}
	return out
}

// ToYAML writes a runbook as structured YAML (Appendix B style), so a parsed
// Markdown or free-text runbook can be reviewed and corrected as data.
func ToYAML(rb *Runbook) string {
	type yStep map[string]any
	doc := map[string]any{"runbook": rb.ID}
	var keys []string
	add := func(k string, v any) {
		doc[k] = v
		keys = append(keys, k)
	}
	keys = append(keys, "runbook")
	if rb.Name != "" && rb.Name != rb.ID {
		add("name", rb.Name)
	}
	if rb.Team != "" {
		add("team", rb.Team)
	}
	if rb.Summary != "" {
		add("summary", rb.Summary)
	}
	if rb.Matches != nil {
		add("matches", rb.Matches)
	}
	if len(rb.Rules) > 0 {
		add("rules", rb.Rules)
	}
	if rb.Auto {
		add("auto", true)
	}
	var steps []yaml.MapSlice
	for _, s := range rb.Steps {
		var m yaml.MapSlice
		switch {
		case s.Kind == KindCheck:
			m = append(m, yaml.MapItem{Key: "check", Value: s.Text})
		case s.Text != "":
			m = append(m, yaml.MapItem{Key: "text", Value: s.Text})
		}
		if s.If != "" {
			m = append(m, yaml.MapItem{Key: "if", Value: s.If})
		}
		if s.Action != "" {
			m = append(m, yaml.MapItem{Key: "action", Value: s.Action})
		}
		pk := make([]string, 0, len(s.Params))
		for k := range s.Params {
			pk = append(pk, k)
		}
		sort.Strings(pk)
		for _, k := range pk {
			m = append(m, yaml.MapItem{Key: k, Value: s.Params[k]})
		}
		if s.Approval != "" {
			m = append(m, yaml.MapItem{Key: "approval", Value: s.Approval})
		}
		if s.Every != "" {
			m = append(m, yaml.MapItem{Key: "every", Value: s.Every})
		}
		if s.Then != "" {
			m = append(m, yaml.MapItem{Key: "then", Value: s.Then})
		}
		steps = append(steps, m)
	}
	_ = yStep{}
	var ms yaml.MapSlice
	for _, k := range keys {
		ms = append(ms, yaml.MapItem{Key: k, Value: doc[k]})
	}
	ms = append(ms, yaml.MapItem{Key: "steps", Value: steps})
	out, err := yaml.MarshalWithOptions(ms, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return "# could not convert: " + err.Error()
	}
	return string(out)
}

// ---- database ------------------------------------------------------------------

// Store keeps runbooks created in the UI/API.
type Store struct {
	Pool *pgxpool.Pool
}

// Stored is a runbook row: the source as written plus metadata.
type Stored struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Meta      Meta      `json:"meta"`
	Version   int       `json:"version"`
	UpdatedBy string    `json:"updatedBy"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var ErrVersionConflict = errors.New("runbook was changed by someone else; reload and try again")

func (s *Store) List(ctx context.Context) ([]Stored, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, source, meta, version, updated_by, updated_at FROM runbooks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var st Stored
		var meta []byte
		if err := rows.Scan(&st.ID, &st.Source, &meta, &st.Version, &st.UpdatedBy, &st.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &st.Meta)
		out = append(out, st)
	}
	return out, rows.Err()
}

// Put creates or updates a runbook (expectVersion 0 = no check).
func (s *Store) Put(ctx context.Context, st Stored, by string, expectVersion int) (*Stored, error) {
	meta, _ := json.Marshal(st.Meta)
	var out Stored
	var metaOut []byte
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO runbooks (id, source, meta, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET source = EXCLUDED.source, meta = EXCLUDED.meta,
		    version = runbooks.version + 1, updated_by = EXCLUDED.updated_by, updated_at = now()
		WHERE $5 = 0 OR runbooks.version = $5
		RETURNING id, source, meta, version, updated_by, updated_at`,
		st.ID, st.Source, meta, by, expectVersion).Scan(&out.ID, &out.Source, &metaOut, &out.Version, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVersionConflict
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(metaOut, &out.Meta)
	return &out, nil
}

func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM runbooks WHERE id = $1`, id)
	return tag.RowsAffected() > 0, err
}

// Version changes whenever any stored runbook changes.
func (s *Store) Version(ctx context.Context) (string, error) {
	var n int64
	var latest *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), max(updated_at) FROM runbooks`).Scan(&n, &latest); err != nil {
		return "", err
	}
	v := fmt.Sprint(n)
	if latest != nil {
		v += "@" + latest.UTC().Format(time.RFC3339Nano)
	}
	return v, nil
}

// Parsed turns stored rows into runbooks, reporting the ones that no longer parse.
func Parsed(stored []Stored) ([]*Runbook, []string) {
	var out []*Runbook
	var errs []string
	for _, st := range stored {
		m := st.Meta
		if m.ID == "" {
			m.ID = st.ID
		}
		rb, err := Parse(st.Source, m)
		if err != nil {
			errs = append(errs, fmt.Sprintf("runbook %s: %v", st.ID, err))
			continue
		}
		rb.ID, rb.Origin = st.ID, "api"
		out = append(out, rb)
	}
	return out, errs
}
