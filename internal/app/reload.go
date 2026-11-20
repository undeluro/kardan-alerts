package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/undeluro/kardan-alerts/internal/directory"
	"github.com/undeluro/kardan-alerts/internal/ingest"
	"github.com/undeluro/kardan-alerts/internal/platform/bus"
	"github.com/undeluro/kardan-alerts/internal/rules"
	"github.com/undeluro/kardan-alerts/internal/schema"
)

// Reloader keeps every replica's view of configuration current without
// restarts. Three triggers, from fastest to most robust:
//   - Redis pub/sub notices from the API replica that made a change;
//   - a poll of the config directory (works on Docker Desktop bind mounts,
//     where file-system events from the host do not arrive);
//   - a periodic database version check, in case a notice was missed.
type Reloader struct {
	ConfigDir string
	Window    time.Duration
	Engine    *rules.Engine
	RuleStore *rules.Store
	Schemas   *schema.Registry
	Sources   *ingest.Manager // nil unless this replica runs the ingest role
	Redis     *redis.Client
	Log       *slog.Logger

	dir atomic.Pointer[directory.Directory]

	mu        sync.Mutex
	fileRules []rules.Rule
	fileErrs  []string
	errors    map[string]string // config file -> problem, shown in the UI
	dbVersion string
	filesHash string
}

func (r *Reloader) Directory() *directory.Directory { return r.dir.Load() }

// Errors lists current configuration problems (bad YAML, invalid rules).
func (r *Reloader) Errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for f, e := range r.errors {
		out = append(out, f+": "+e)
	}
	sort.Strings(out)
	return append(out, r.Engine.Current().Errors...)
}

// LoadAll performs the initial load. A broken teams.yaml at startup is fatal;
// later it only logs and keeps the last good version.
func (r *Reloader) LoadAll(ctx context.Context) error {
	r.errors = map[string]string{}
	d, err := directory.Load(filepath.Join(r.ConfigDir, "teams.yaml"))
	if err != nil {
		return fmt.Errorf("load teams: %w", err)
	}
	r.dir.Store(d)
	r.loadFileRules()
	if err := r.rebuildRules(ctx); err != nil {
		return err
	}
	if err := r.Schemas.ReloadManual(ctx); err != nil {
		r.Log.Warn("load schema facet overrides", "err", err)
	}
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

	rs := rules.Build(merged, r.Window)
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
	if d, err := directory.Load(filepath.Join(r.ConfigDir, "teams.yaml")); err != nil {
		r.Log.Error("teams.yaml invalid, keeping previous version", "err", err)
		r.mu.Lock()
		r.errors["teams.yaml"] = err.Error()
		r.mu.Unlock()
	} else {
		r.dir.Store(d)
		r.mu.Lock()
		delete(r.errors, "teams.yaml")
		r.mu.Unlock()
	}
	r.loadFileRules()
	if err := r.rebuildRules(ctx); err != nil {
		r.Log.Error("reload rules", "err", err)
	}
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
