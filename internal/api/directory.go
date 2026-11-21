package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/undeluro/kardan-alerts/internal/audit"
	"github.com/undeluro/kardan-alerts/internal/directory"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func (s *Server) registerDirectory(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/teams/{id}", s.putDirectory(directory.KindTeam))
	mux.HandleFunc("DELETE /api/teams/{id}", s.deleteDirectory(directory.KindTeam))
	mux.HandleFunc("PUT /api/people/{id}", s.putDirectory(directory.KindPerson))
	mux.HandleFunc("DELETE /api/people/{id}", s.deleteDirectory(directory.KindPerson))
}

// teams lists people and teams with where each comes from, so the UI can show
// "teams.yaml", "edited in the UI" or "created in the UI" and offer a revert.
func (s *Server) teams(w http.ResponseWriter, _ *http.Request) {
	d := s.Directory()
	origins := directory.Origins{}
	if s.DirectoryOrigins != nil {
		origins = s.DirectoryOrigins()
	}
	type teamView struct {
		directory.Team
		Origin string `json:"origin"`
	}
	type personView struct {
		directory.Person
		Origin string `json:"origin"`
	}
	teams := make([]teamView, 0, len(d.Teams))
	for _, t := range d.Teams {
		teams = append(teams, teamView{Team: t, Origin: or(origins["team:"+t.ID], "file")})
	}
	people := make([]personView, 0, len(d.People))
	for _, p := range d.People {
		people = append(people, personView{Person: p, Origin: or(origins["person:"+p.ID], "file")})
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": teams, "people": people, "defaultPreferences": d.Defaults,
		"editable": s.DirStore != nil})
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// putDirectory saves a person or team: PUT /api/teams/{id} or /api/people/{id}
// with the same fields as teams.yaml (JSON). If-Match: <version> guards
// against overwriting someone else's edit.
func (s *Server) putDirectory(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.DirStore == nil {
			writeErr(w, http.StatusServiceUnavailable, "editing people and teams is not enabled")
			return
		}
		id := strings.TrimSpace(r.PathValue("id"))
		if !idPattern.MatchString(id) {
			writeErr(w, http.StatusUnprocessableEntity, "id must be lowercase letters, digits, - or _")
			return
		}
		var body json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		body, err := normalizeEntry(kind, id, body)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		e := directory.Entry{Kind: kind, ID: id, Body: body}
		if s.CheckDirectory != nil {
			if err := s.CheckDirectory(r.Context(), e); err != nil {
				writeErr(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
		expect, _ := strconv.Atoi(r.Header.Get("If-Match"))
		saved, err := s.DirStore.Put(r.Context(), e, actor(r), expect)
		if errors.Is(err, directory.ErrConflict) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		s.applyDirectory(r)
		s.record(r, audit.Event{Actor: actor(r), Action: kind + ".save", Subject: kind + ":" + id,
			Detail: map[string]any{"version": saved.Version, "body": json.RawMessage(saved.Body)}})
		s.Log.Info("directory entry saved", "kind", kind, "id", id, "version", saved.Version, "by", actor(r))
		writeJSON(w, http.StatusOK, saved)
	}
}

// deleteDirectory removes the UI version: a file entry comes back, a UI-only
// entry disappears. Teams that still list a removed person are refused.
func (s *Server) deleteDirectory(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.DirStore == nil {
			writeErr(w, http.StatusServiceUnavailable, "editing people and teams is not enabled")
			return
		}
		id := r.PathValue("id")
		if kind == directory.KindPerson {
			if origin := s.originOf("person:" + id); origin == "api" {
				for _, t := range s.Directory().Teams {
					for _, m := range t.Members {
						if m == id {
							writeErr(w, http.StatusConflict, "remove "+id+" from team "+t.ID+" first")
							return
						}
					}
				}
			}
		}
		if err := s.DirStore.Delete(r.Context(), kind, id); errors.Is(err, directory.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "no UI edits for "+kind+" "+id)
			return
		} else if err != nil {
			s.fail(w, err)
			return
		}
		s.applyDirectory(r)
		s.record(r, audit.Event{Actor: actor(r), Action: kind + ".revert", Subject: kind + ":" + id})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) originOf(key string) string {
	if s.DirectoryOrigins == nil {
		return "file"
	}
	return s.DirectoryOrigins()[key]
}

// applyDirectory reloads locally right away (so the response and the next GET
// agree) and tells the other replicas.
func (s *Server) applyDirectory(r *http.Request) {
	if s.ReloadDirectory != nil {
		s.ReloadDirectory(context.WithoutCancel(r.Context()))
	}
	s.notify(r, "directory")
}

// normalizeEntry decodes into the typed struct (dropping unknown fields and the
// origin the UI echoes back) and re-encodes it.
func normalizeEntry(kind, id string, body json.RawMessage) (json.RawMessage, error) {
	var v any
	switch kind {
	case directory.KindTeam:
		var t directory.Team
		if err := json.Unmarshal(body, &t); err != nil {
			return nil, err
		}
		t.ID = id
		if strings.TrimSpace(t.Name) == "" {
			t.Name = id
		}
		if t.Members == nil {
			t.Members = []string{}
		}
		v = t
	case directory.KindPerson:
		var p directory.Person
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		p.ID = id
		if strings.TrimSpace(p.Name) == "" {
			return nil, errors.New("name is required")
		}
		v = p
	}
	return json.Marshal(v)
}
