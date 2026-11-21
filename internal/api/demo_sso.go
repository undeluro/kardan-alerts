package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/undeluro/kardan-alerts/internal/directory"
)

const demoSessionCookie = "kardan_session"
const demoSessionTTL = 8 * time.Hour

type sessionCache interface {
	Get(context.Context, string) *redis.StringCmd
	Set(context.Context, string, any, time.Duration) *redis.StatusCmd
	Del(context.Context, ...string) *redis.IntCmd
}

type demoAccount struct {
	id    string
	admin bool
	token [sha256.Size]byte
}

type demoIdentity struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Admin bool     `json:"admin"`
	Teams []string `json:"teams"`
}

type demoIdentityKey struct{}

// DemoSSO exchanges configured demo access tokens for opaque Redis-backed
// sessions. The seeded people come from the same directory used by approvals.
type DemoSSO struct {
	cache        sessionCache
	directory    func() *directory.Directory
	accounts     []demoAccount
	gatewayToken string
	secure       bool
}

func NewDemoSSO(cache sessionCache, dir func() *directory.Directory, samToken, mariaToken, gatewayToken string, secure bool) *DemoSSO {
	s := &DemoSSO{cache: cache, directory: dir, gatewayToken: gatewayToken, secure: secure}
	if samToken != "" {
		s.accounts = append(s.accounts, demoAccount{id: "sam", token: sha256.Sum256([]byte(samToken))})
	}
	if mariaToken != "" {
		s.accounts = append(s.accounts, demoAccount{id: "maria", admin: true, token: sha256.Sum256([]byte(mariaToken))})
	}
	return s
}

// Register also serves a session probe when demo SSO is disabled, allowing
// existing trusted-network setups to keep their current console behavior.
func (s *DemoSSO) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", s.session)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
}

func (s *DemoSSO) session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "user": nil})
		return
	}
	user, err := s.sessionIdentity(r)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "session storage is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "user": user})
}

func (s *DemoSSO) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s == nil {
		writeErr(w, http.StatusNotFound, "demo SSO is disabled")
		return
	}
	var input struct {
		Account string `json:"account"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.Account == "" || input.Token == "" {
		writeErr(w, http.StatusBadRequest, "enter an account and access token")
		return
	}
	user := s.tokenIdentity(input.Account, input.Token)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "invalid demo account or access token")
		return
	}
	if err := s.revoke(r); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "session storage is unavailable")
		return
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	token := hex.EncodeToString(secret[:])
	if err := s.cache.Set(r.Context(), demoSessionKey(token), user.ID, demoSessionTTL).Err(); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "session storage is unavailable")
		return
	}
	s.setCookie(w, r, token, int(demoSessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "user": user})
}

func (s *DemoSSO) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s != nil {
		if err := s.revoke(r); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "could not sign out. Try again")
			return
		}
		s.setCookie(w, r, "", -1)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *DemoSSO) revoke(r *http.Request) error {
	if token := demoSessionToken(r); token != "" {
		return s.cache.Del(r.Context(), demoSessionKey(token)).Err()
	}
	return nil
}

func (s *DemoSSO) setCookie(w http.ResponseWriter, r *http.Request, token string, age int) {
	http.SetCookie(w, &http.Cookie{
		Name: demoSessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.secure || r.TLS != nil, MaxAge: age,
	})
}

func demoSessionKey(token string) string {
	hash := sha256.Sum256([]byte(token))
	return "kardan:sso:" + hex.EncodeToString(hash[:])
}

func demoSessionToken(r *http.Request) string {
	cookie, err := r.Cookie(demoSessionCookie)
	if err != nil || len(cookie.Value) != 64 {
		return ""
	}
	return cookie.Value
}

func (s *DemoSSO) sessionIdentity(r *http.Request) (*demoIdentity, error) {
	token := demoSessionToken(r)
	if token == "" {
		return nil, nil
	}
	id, err := s.cache.Get(r.Context(), demoSessionKey(token)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, account := range s.accounts {
		if account.id == id {
			return s.identity(account), nil
		}
	}
	return nil, nil
}

func (s *DemoSSO) tokenIdentity(id, raw string) *demoIdentity {
	if raw == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(raw))
	for _, account := range s.accounts {
		if subtle.ConstantTimeCompare(hash[:], account.token[:]) == 1 && (id == "" || id == account.id) {
			return s.identity(account)
		}
	}
	return nil
}

func (s *DemoSSO) identity(account demoAccount) *demoIdentity {
	dir := s.directory()
	person, ok := dir.Person(account.id)
	if !ok {
		return nil
	}
	teams := []string{}
	for _, team := range dir.Teams {
		if slices.Contains(team.Members, account.id) {
			teams = append(teams, team.ID)
		}
	}
	return &demoIdentity{ID: account.id, Name: person.Name, Admin: account.admin, Teams: teams}
}

func (s *DemoSSO) Protect(next http.Handler) http.Handler {
	if s == nil {
		return next
	}
	sameOrigin := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := sameOrigin.Check(r); err != nil {
			writeErr(w, http.StatusForbidden, "cross-origin requests are not allowed")
			return
		}
		switch p {
		case "/api/auth/session", "/api/auth/login", "/api/auth/logout":
			next.ServeHTTP(w, r)
			return
		}
		var user *demoIdentity
		var err error
		raw, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bearer {
			if r.Method == http.MethodGet && p == "/api/teams" && s.gatewayToken != "" &&
				subtle.ConstantTimeCompare([]byte(raw), []byte(s.gatewayToken)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			user = s.tokenIdentity("", raw)
		} else {
			user, err = s.sessionIdentity(r)
		}
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "session storage is unavailable")
			return
		}
		if user == nil {
			writeErr(w, http.StatusUnauthorized, "sign in to continue")
			return
		}
		if !user.Admin && adminRequired(r) {
			writeErr(w, http.StatusForbidden, "administrator access is required")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), demoIdentityKey{}, user))
		if p == "/api/stream" && !bearer {
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			go s.watchSession(r, cancel, user.ID)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *DemoSSO) watchSession(r *http.Request, cancel context.CancelFunc, id string) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			user, err := s.sessionIdentity(r)
			if err != nil || user == nil || user.ID != id {
				cancel()
				return
			}
		}
	}
}

func (s *DemoSSO) hasAccount(id string) bool {
	if s != nil {
		for _, account := range s.accounts {
			if account.id == id {
				return true
			}
		}
	}
	return false
}

func adminRequired(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	p := r.URL.Path
	switch p {
	case "/api/rules/test", "/api/rules/backtest", "/api/runbooks/parse", "/api/proposals":
		return r.Method != http.MethodPost
	case "/api/rules", "/api/runbooks", "/api/clock":
		return true
	}
	for _, prefix := range []string{"/api/teams/", "/api/people/", "/api/rules/", "/api/runbooks/", "/api/schemas/", "/api/proposals/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func sessionUser(r *http.Request) *demoIdentity {
	user, _ := r.Context().Value(demoIdentityKey{}).(*demoIdentity)
	return user
}

func actionActor(r *http.Request, legacy string) string {
	if user := sessionUser(r); user != nil {
		return user.Name
	}
	if name := strings.TrimSpace(legacy); name != "" {
		return name
	}
	return actor(r)
}

func (s *Server) canActOnTeam(r *http.Request, team string) bool {
	user := sessionUser(r)
	if user == nil {
		return true
	}
	return slices.Contains(user.Teams, team)
}
