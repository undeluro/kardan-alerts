package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/undeluro/kardan-alerts/internal/actions"
	"github.com/undeluro/kardan-alerts/internal/calendar"
	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/finance"
	"github.com/undeluro/kardan-alerts/internal/ingest"
	"github.com/undeluro/kardan-alerts/internal/platform/bus"
	"github.com/undeluro/kardan-alerts/internal/privacy"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/runbooks"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Reloader keeps every replica's view of configuration current without
// restarts. Three triggers, from fastest to most robust:
//   - Redis pub/sub notices from the API replica that made a change;
//   - a poll of the config directory (works on Docker Desktop bind mounts,
//     where file-system events from the host do not arrive);
//   - a periodic database version check, in case a notice was missed.
type Reloader struct {
	ConfigDir    string
	Window       time.Duration
	Engine       *rules.Engine
	RuleStore    *rules.Store
	Schemas      *schema.Registry
	Sources      *ingest.Manager // nil unless this replica runs the ingest role
	Runbooks     *runbooks.Holder
	RunbookStore *runbooks.Store
	DirStore     *directory.Store // people/teams edited in the UI
	Redis        *redis.Client
	Log          *slog.Logger

	dir     atomic.Pointer[directory.Directory]
	origins atomic.Pointer[directory.Origins]
	privacy atomic.Pointer[privacy.Policy]
	fx      atomic.Pointer[finance.FX]
	corr    atomic.Pointer[finance.Correlation]
	cals    atomic.Pointer[calendar.Set]
	acts    atomic.Pointer[actions.Registry]

	mu        sync.Mutex
	fileRules []rules.Rule
	fileErrs  []string
	errors    map[string]string // config file -> problem, shown in the UI
	dbVersion string
	rbVersion string
	dirVer    string
	filesHash string
}

func (r *Reloader) Directory() *directory.Directory { return r.dir.Load() }

// DirectoryOrigins says where each person and team comes from (file, api, override).
func (r *Reloader) DirectoryOrigins() directory.Origins {
	if o := r.origins.Load(); o != nil {
		return *o
	}
	return directory.Origins{}
}

// rebuildDirectory merges teams.yaml with stored entries. At startup a broken
// teams.yaml is fatal; later it keeps the last good version.
func (r *Reloader) rebuildDirectory(ctx context.Context, startup bool) error {
	var entries []directory.Entry
	if r.DirStore != nil {
		var err error
		if entries, err = r.DirStore.List(ctx); err != nil {
			r.Log.Error("load stored people and teams", "err", err)
		}
		v, _ := r.DirStore.Version(ctx)
		r.mu.Lock()
		r.dirVer = v
		r.mu.Unlock()
	}
	d, origins, problems, err := directory.LoadMerged(filepath.Join(r.ConfigDir, "teams.yaml"), entries)
	r.noteErr("teams.yaml", err)
	if err != nil {
		if startup {
			return fmt.Errorf("load teams: %w", err)
		}
		return nil
	}
	r.dir.Store(d)
	r.origins.Store(&origins)
	r.mu.Lock()
	if len(problems) > 0 {
		r.errors["directory (UI edits)"] = strings.Join(problems, "; ")
	} else {
		delete(r.errors, "directory (UI edits)")
	}
	r.mu.Unlock()
	return nil
}

// CheckDirectory validates a candidate edit against the current file and
// stored entries before it is saved.
func (r *Reloader) CheckDirectory(ctx context.Context, e directory.Entry) error {
	var entries []directory.Entry
	if r.DirStore != nil {
		var err error
		if entries, err = r.DirStore.List(ctx); err != nil {
			return err
		}
	}
	kept := entries[:0:0]
	for _, x := range entries {
		if x.Kind != e.Kind || x.ID != e.ID {
			kept = append(kept, x)
		}
	}
	kept = append(kept, e)
	_, _, problems, err := directory.LoadMerged(filepath.Join(r.ConfigDir, "teams.yaml"), kept)
	if err != nil {
		return err
	}
	for _, p := range problems {
		if strings.Contains(p, " "+e.Kind+" "+e.ID+" ") {
			_, msg, _ := strings.Cut(p, "skipped: ")
			return errors.New(msg)
		}
	}
	return nil
}
func (r *Reloader) Privacy() *privacy.Policy          { return r.privacy.Load() }
func (r *Reloader) FX() *finance.FX                   { return r.fx.Load() }
func (r *Reloader) Correlation() *finance.Correlation { return r.corr.Load() }
func (r *Reloader) Calendars() *calendar.Set          { return r.cals.Load() }
func (r *Reloader) Actions() *actions.Registry        { return r.acts.Load() }

// loadMoney (re)loads FX rates and correlation settings, keeping the last
// good version on error.
func (r *Reloader) loadMoney(startup bool) error {
	fx, err := finance.LoadFX(filepath.Join(r.ConfigDir, "fx.yaml"))
	if err == nil {
		r.fx.Store(fx)
	}
	r.noteErr("fx.yaml", err)
	if err != nil && startup {
		return fmt.Errorf("load fx: %w", err)
	}
	corr, err := finance.LoadCorrelation(filepath.Join(r.ConfigDir, "correlation.yaml"))
	if err == nil {
		r.corr.Store(corr)
	}
	r.noteErr("correlation.yaml", err)
	if err != nil && startup {
		return fmt.Errorf("load correlation: %w", err)
	}
	acts, err := actions.Load(filepath.Join(r.ConfigDir, "actions.yaml"))
	if err == nil {
		r.acts.Store(acts)
	}
	r.noteErr("actions.yaml", err)
	if err != nil && startup {
		return fmt.Errorf("load actions: %w", err)
	}
	cals, err := calendar.Load(filepath.Join(r.ConfigDir, "calendars.yaml"))
	if err == nil {
		r.cals.Store(cals)
	}
	r.noteErr("calendars.yaml", err)
	if err != nil && startup {
		return fmt.Errorf("load calendars: %w", err)
	}
	return nil
}

// checkCalendars rejects rules that name a calendar or cut-off that does not exist.
func (r *Reloader) checkCalendars(c *rules.Compiled) error {
	cals := r.Calendars()
	for _, id := range c.Calendars() {
		if _, ok := cals.Get(id); !ok {
			return fmt.Errorf("unknown calendar %q (see calendars.yaml)", id)
		}
	}
	if m := c.CutoffMode(); m != "" && m != "auto" {
		if _, ok := cals.Named(m, time.Now()); !ok {
			return fmt.Errorf("unknown cut-off %q (use auto or a cut-off name from calendars.yaml)", m)
		}
	}
	return nil
}

func (r *Reloader) noteErr(file string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.errors[file] = err.Error()
		r.Log.Error(file+" invalid, keeping previous version", "err", err)
	} else {
		delete(r.errors, file)
	}
}

// Errors lists current configuration problems (bad YAML, invalid rules).
func (r *Reloader) Errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for f, e := range r.errors {
		out = append(out, f+": "+e)
	}
	sort.Strings(out)
	out = append(out, r.Engine.Current().Errors...)
	if r.Runbooks != nil {
		out = append(out, r.Runbooks.Current().Errors...)
	}
	return out
}

// rebuildRunbooks reloads runbook files and stored runbooks into the catalog.
func (r *Reloader) rebuildRunbooks(ctx context.Context) {
	if r.Runbooks == nil {
		return
	}
	files, errs := runbooks.LoadDir(filepath.Join(r.ConfigDir, "runbooks"))
	var stored []*runbooks.Runbook
	if r.RunbookStore != nil {
		rows, err := r.RunbookStore.List(ctx)
		if err != nil {
			r.Log.Error("load stored runbooks", "err", err)
			return
		}
		var perr []string
		stored, perr = runbooks.Parsed(rows)
		errs = append(errs, perr...)
		v, _ := r.RunbookStore.Version(ctx)
		r.mu.Lock()
		r.rbVersion = v
		r.mu.Unlock()
	}
	list := runbooks.Merge(files, stored)
	reg := r.Actions()
	for _, rb := range list {
		for i, s := range rb.Steps {
			if s.Action != "" && !s.Inferred {
				if _, ok := reg.Get(s.Action); !ok {
					errs = append(errs, fmt.Sprintf("runbook %s step %d: unknown action %q (shown as text; add it to actions.yaml)", rb.ID, i+1, s.Action))
				}
			}
		}
	}
	r.Runbooks.Swap(runbooks.NewCatalog(list, errs))
	r.Log.Info("runbooks loaded", "count", len(list), "fromFiles", len(files), "fromAPI", len(stored), "problems", len(errs))
}

// LoadAll performs the initial load. A broken teams.yaml at startup is fatal;
// later it only logs and keeps the last good version.
func (r *Reloader) LoadAll(ctx context.Context) error {
	r.errors = map[string]string{}
	if err := r.rebuildDirectory(ctx, true); err != nil {
		return err
	}
	pol, err := privacy.Load(filepath.Join(r.ConfigDir, "classification.yaml"))
	if err != nil {
		return fmt.Errorf("load classification: %w", err)
	}
	r.privacy.Store(pol)
	if err := r.loadMoney(true); err != nil {
		return err
	}
	r.loadFileRules()
	if err := r.rebuildRules(ctx); err != nil {
		return err
	}
	if err := r.Schemas.ReloadManual(ctx); err != nil {
		r.Log.Warn("load schema facet overrides", "err", err)
	}
	r.rebuildRunbooks(ctx)
	r.applySources(ctx)
	r.filesHash, _ = hashDir(r.ConfigDir)
	return nil
}

func (r *Reloader) loadFileRules() {
	list, errs := rules.LoadDir(filepath.Join(r.ConfigDir, "rules"))
	r.mu.Lock()
	r.fileRules, r.fileErrs = list, errs
	r.mu.Unlock()
}

// rebuildRules merges file and stored rules and swaps the engine's set.
func (r *Reloader) rebuildRules(ctx context.Context) error {
	stored, err := r.RuleStore.List(ctx)
	if err != nil {
		return fmt.Errorf("load stored rules: %w", err)
	}
	version, _ := r.RuleStore.Version(ctx)
	r.mu.Lock()
	merged := rules.Merge(r.fileRules, stored)
	fileErrs := append([]string(nil), r.fileErrs...)
	r.dbVersion = version
	r.mu.Unlock()

	rs := rules.Build(merged, r.Window, r.checkCalendars)
	rs.Errors = append(fileErrs, rs.Errors...)
	r.Engine.Swap(rs)
	r.Log.Info("rules loaded", "count", len(rs.Rules), "fromFiles", len(r.fileRules), "fromAPI", len(stored),
		"skipped", len(rs.Errors))
	for _, e := range rs.Errors {
		r.Log.Warn("rule skipped", "err", e)
	}
	return nil
}

func (r *Reloader) applySources(ctx context.Context) {
	if r.Sources == nil {
		return
	}
	path := filepath.Join(r.ConfigDir, "sources.yaml")
	cfgs, err := ingest.LoadSources(path)
	r.mu.Lock()
	if err != nil {
		r.errors["sources.yaml"] = err.Error()
	} else {
		delete(r.errors, "sources.yaml")
	}
	r.mu.Unlock()
	if err != nil {
		r.Log.Error("sources.yaml invalid, keeping running sources", "err", err)
		return
	}
	r.Sources.Apply(ctx, cfgs)
}

func (r *Reloader) reloadFiles(ctx context.Context) {
	r.Log.Info("config directory changed, reloading")
	_ = r.rebuildDirectory(ctx, false)
	if pol, err := privacy.Load(filepath.Join(r.ConfigDir, "classification.yaml")); err != nil {
		r.Log.Error("classification.yaml invalid, keeping previous version", "err", err)
		r.mu.Lock()
		r.errors["classification.yaml"] = err.Error()
		r.mu.Unlock()
	} else {
		r.privacy.Store(pol)
		r.mu.Lock()
		delete(r.errors, "classification.yaml")
		r.mu.Unlock()
	}
	_ = r.loadMoney(false)
	r.loadFileRules()
	if err := r.rebuildRules(ctx); err != nil {
		r.Log.Error("reload rules", "err", err)
	}
	r.rebuildRunbooks(ctx)
	r.applySources(ctx)
}

// Run watches for changes until ctx ends.
func (r *Reloader) Run(ctx context.Context) error {
	notices := make(chan string, 16)
	go r.subscribe(ctx, notices)
	filesTick := time.NewTicker(2 * time.Second)
	dbTick := time.NewTicker(15 * time.Second)
	defer filesTick.Stop()
	defer dbTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case kind := <-notices:
			switch kind {
			case "rules":
				if err := r.rebuildRules(ctx); err != nil {
					r.Log.Error("reload rules", "err", err)
				}
			case "schemas":
				if err := r.Schemas.ReloadManual(ctx); err != nil {
					r.Log.Error("reload schema facets", "err", err)
				}
			case "runbooks":
				r.rebuildRunbooks(ctx)
			case "directory":
				_ = r.rebuildDirectory(ctx, false)
			}
		case <-filesTick.C:
			h, err := hashDir(r.ConfigDir)
			if err == nil && h != r.filesHash {
				r.filesHash = h
				r.reloadFiles(ctx)
			}
		case <-dbTick.C:
			v, err := r.RuleStore.Version(ctx)
			r.mu.Lock()
			changed := err == nil && v != r.dbVersion
			r.mu.Unlock()
			if changed {
				r.Log.Info("rule change detected by version check")
				_ = r.rebuildRules(ctx)
			}
			_ = r.Schemas.ReloadManual(ctx)
			if r.DirStore != nil {
				v, err := r.DirStore.Version(ctx)
				r.mu.Lock()
				changed := err == nil && v != r.dirVer
				r.mu.Unlock()
				if changed {
					_ = r.rebuildDirectory(ctx, false)
				}
			}
			if r.RunbookStore != nil {
				v, err := r.RunbookStore.Version(ctx)
				r.mu.Lock()
				changed := err == nil && v != r.rbVersion
				r.mu.Unlock()
				if changed {
					r.rebuildRunbooks(ctx)
				}
			}
		}
	}
}

func (r *Reloader) subscribe(ctx context.Context, out chan<- string) {
	for ctx.Err() == nil {
		sub := r.Redis.Subscribe(ctx, bus.ConfigChannel)
		ch := sub.Channel()
		for msg := range ch {
			select {
			case out <- msg.Payload:
			default:
			}
		}
		_ = sub.Close()
		time.Sleep(time.Second)
	}
}

// hashDir fingerprints file names, sizes and modification times.
func hashDir(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // directory may not exist yet
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s|%d|%d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}
