package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/undeluro/kardan-alerts/internal/directory"
)

type testSessions map[string]string

func (s testSessions) Get(_ context.Context, key string) *redis.StringCmd {
	if value, ok := s[key]; ok {
		return redis.NewStringResult(value, nil)
	}
	return redis.NewStringResult("", redis.Nil)
}

func (s testSessions) Set(_ context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	s[key] = value.(string)
	return redis.NewStatusResult("OK", nil)
}

func (s testSessions) Del(_ context.Context, keys ...string) *redis.IntCmd {
	for _, key := range keys {
		delete(s, key)
	}
	return redis.NewIntResult(int64(len(keys)), nil)
}

func TestDemoSSO(t *testing.T) {
	dir, err := directory.Load("../../config/teams.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sso := NewDemoSSO(testSessions{}, func() *directory.Directory { return dir }, "sam-example", "maria-example", "gateway-example", false)
	mux := http.NewServeMux()
	sso.Register(mux)
	mux.HandleFunc("/api/alerts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(actionActor(r, "spoofed body")))
	})
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux.HandleFunc("POST /api/rules", noContent)
	mux.HandleFunc("POST /api/rules/test", noContent)
	mux.HandleFunc("PUT /api/rules/test", noContent)
	mux.HandleFunc("GET /api/teams", noContent)
	mux.HandleFunc("/ingest/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	handler := sso.Protect(mux)

	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-User", "spoofed header")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	assertStatus := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d, want %d. Body: %s", w.Code, status, w.Body.String())
		}
	}

	assertStatus(request("GET", "/api/alerts", "", nil), 401)
	assertStatus(request("POST", "/ingest/test", "{}", nil), 202)
	assertStatus(request("POST", "/api/auth/login", `{"account":"sam","token":"maria-example"}`, nil), 401)

	login := request("POST", "/api/auth/login", `{"account":"sam","token":"sam-example"}`, nil)
	assertStatus(login, 200)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected an HttpOnly, same-site session cookie: %v", cookies)
	}
	sam := cookies[0]
	assertStatus(request("GET", "/api/auth/session", "", sam), 200)
	w := request("POST", "/api/alerts", "{}", sam)
	assertStatus(w, 200)
	if got := w.Body.String(); got != "Sam Okafor" {
		t.Fatalf("audit actor should come from session, got %q", got)
	}
	assertStatus(request("POST", "/api/rules", "{}", sam), 403)
	assertStatus(request("POST", "/api/rules/test", "{}", sam), 204)
	assertStatus(request("PUT", "/api/rules/test", "{}", sam), 403)

	login = request("POST", "/api/auth/login", `{"account":"maria","token":"maria-example"}`, sam)
	assertStatus(login, 200)
	assertStatus(request("GET", "/api/alerts", "", sam), 401)
	maria := login.Result().Cookies()[0]
	assertStatus(request("POST", "/api/rules", "{}", maria), 204)

	assertStatus(request("POST", "/api/auth/logout", "", maria), 204)
	assertStatus(request("GET", "/api/alerts", "", maria), 401)

	r := httptest.NewRequest("POST", "/api/rules", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer maria-example")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertStatus(w, 204)

	for path, status := range map[string]int{"/api/teams": 204, "/api/alerts": 401} {
		r = httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer gateway-example")
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assertStatus(w, status)
	}

	r = httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertStatus(w, 403)
}
