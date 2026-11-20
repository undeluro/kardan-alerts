// Package mockops simulates the operations estate runbook actions talk to:
// a workflow engine, a service manager, ticketing, chat, the risk desk, market
// data, regions and deployments, the payments hub and trading controls. It
// also answers as counterparty-service (/health), whose health can be
// switched off from its page to show a runbook's check failing and its
// retries escalating. Every request is logged and idempotent by key.
package mockops

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	At      time.Time         `json:"at"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Status  int               `json:"status"`
	Message string            `json:"message"`
	By      string            `json:"by,omitempty"` // requested / approved by
	Params  map[string]string `json:"params,omitempty"`
	Replay  bool              `json:"replay,omitempty"`
}

type State struct {
	CounterpartyHealthy bool   `json:"counterpartyHealthy"`
	Retries             string `json:"retries"` // follow (counterparty health) | succeed | fail
}

type Server struct {
	mu      sync.Mutex
	state   State
	log     []Entry
	replies map[string]reply // idempotency key -> first answer
	seq     int
	logger  *slog.Logger
}

type reply struct {
	status int
	body   map[string]any
}

func New(log *slog.Logger) *Server {
	return &Server{state: State{CounterpartyHealthy: true, Retries: "follow"}, replies: map[string]reply{}, seq: 1040, logger: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"state": s.state, "log": s.log})
	})
	mux.HandleFunc("POST /api/state", s.setState)

	// counterparty-service (network alias in docker-compose)
	mux.HandleFunc("GET /health", s.op(func(r *http.Request, p map[string]string) (int, string) {
		if s.healthy() {
			return 200, "counterparty-service healthy (p99 42 ms)"
		}
		return 503, "counterparty-service unavailable: connection pool exhausted"
	}))
	mux.HandleFunc("POST /workflows/{id}/retry", s.op(func(r *http.Request, p map[string]string) (int, string) {
		id := r.PathValue("id")
		if s.retrySucceeds() {
			return 200, fmt.Sprintf("workflow %s resumed from the failed step and completed", id)
		}
		return 502, fmt.Sprintf("retry of %s failed: counterparty-service did not respond in 5000 ms", id)
	}))
	mux.HandleFunc("POST /services/{name}/restart", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("%s restarted, healthy after %.1f s", r.PathValue("name"), 2+rand.Float64()*2)
	}))
	mux.HandleFunc("POST /tickets", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 201, fmt.Sprintf("created %s for team %s: %s", s.next("OPS"), orDefault(p["team"], "ops"), p["summary"])
	}))
	mux.HandleFunc("POST /chat/{channel}", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("posted to %s: %s", r.PathValue("channel"), orDefault(p["message"], "update"))
	}))
	mux.HandleFunc("POST /risk/notifications", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("risk desk notified (case %s): %s", s.next("RSK"), p["summary"])
	}))
	mux.HandleFunc("POST /feeds/{instrument}/failover", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("%s now priced from the secondary vendor feed", r.PathValue("instrument"))
	}))
	mux.HandleFunc("POST /regions/failover/{service}", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("%s traffic moved from eu-west to eu-central", r.PathValue("service"))
	}))
	mux.HandleFunc("POST /deployments/{service}/rollback", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("%s rolled back to the previous release", r.PathValue("service"))
	}))
	mux.HandleFunc("POST /payments/{id}/resubmit", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("payment %s resubmitted as %s", r.PathValue("id"), s.next("PAY"))
	}))
	mux.HandleFunc("POST /trading/desks/{desk}/pause", s.op(func(r *http.Request, p map[string]string) (int, string) {
		return 200, fmt.Sprintf("desk %s paused: new orders blocked, %d working orders cancelled", r.PathValue("desk"), 3+rand.IntN(12))
	}))
	return mux
}

func (s *Server) healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.CounterpartyHealthy
}

func (s *Server) retrySucceeds() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state.Retries {
	case "succeed":
		return true
	case "fail":
		return false
	}
	return s.state.CounterpartyHealthy
}

func (s *Server) next(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("%s-%d", prefix, s.seq)
}

// op wraps an operation: idempotency by key, logging, JSON answer.
func (s *Server) op(fn func(r *http.Request, params map[string]string) (int, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Params      map[string]string `json:"params"`
			RequestedBy string            `json:"requestedBy"`
			ApprovedBy  string            `json:"approvedBy"`
		}
		if r.Body != nil {
			data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			_ = json.Unmarshal(data, &body)
		}
		key := r.Header.Get("Idempotency-Key")
		s.mu.Lock()
		prev, seen := s.replies[key]
		s.mu.Unlock()
		if key != "" && seen && r.Method != http.MethodGet {
			s.record(Entry{Method: r.Method, Path: r.URL.Path, Status: prev.status, Message: fmt.Sprint(prev.body["message"]), Replay: true})
			w.Header().Set("Idempotent-Replay", "true")
			writeJSON(w, prev.status, prev.body)
			return
		}
		status, msg := fn(r, body.Params)
		out := map[string]any{"message": msg, "ok": status < 300}
		if key != "" && r.Method != http.MethodGet {
			s.mu.Lock()
			s.replies[key] = reply{status, out}
			s.mu.Unlock()
		}
		by := body.RequestedBy
		if body.ApprovedBy != "" {
			by += ", approved by " + body.ApprovedBy
		}
		s.record(Entry{Method: r.Method, Path: r.URL.Path, Status: status, Message: msg, By: by, Params: body.Params})
		writeJSON(w, status, out)
	}
}

func (s *Server) record(e Entry) {
	e.At = time.Now().UTC()
	s.mu.Lock()
	s.log = append([]Entry{e}, s.log...)
	if len(s.log) > 200 {
		s.log = s.log[:200]
	}
	s.mu.Unlock()
	s.logger.Info("operation", "method", e.Method, "path", e.Path, "status", e.Status, "message", e.Message, "replay", e.Replay)
}

func (s *Server) setState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CounterpartyHealthy *bool   `json:"counterpartyHealthy"`
		Retries             *string `json:"retries"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	if req.CounterpartyHealthy != nil {
		s.state.CounterpartyHealthy = *req.CounterpartyHealthy
	}
	if req.Retries != nil {
		switch *req.Retries {
		case "follow", "succeed", "fail":
			s.state.Retries = *req.Retries
		}
	}
	st := s.state
	s.mu.Unlock()
	s.record(Entry{Method: "SET", Path: "/state", Status: 200,
		Message: fmt.Sprintf("counterparty healthy: %v, workflow retries: %s", st.CounterpartyHealthy, st.Retries)})
	writeJSON(w, 200, st)
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageHTML))
}

const pageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Mock operations</title>
<style>
:root{--bg:#f6f7f9;--card:#fff;--text:#111827;--muted:#6b7280;--line:#e5e7eb;--ok:#16a34a;--bad:#dc2626;--chip:#f3f4f6}
@media (prefers-color-scheme:dark){:root{--bg:#030712;--card:#111827;--text:#f3f4f6;--muted:#9ca3af;--line:#1f2937;--chip:#1f2937}}
body{margin:0;font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:var(--bg);color:var(--text)}
main{max-width:980px;margin:auto;padding:20px 16px}
h1{font-size:20px;margin:0 0 4px}.muted{color:var(--muted);font-size:13px}
.card{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:16px;margin-top:16px}
.row{display:flex;flex-wrap:wrap;gap:12px;align-items:center}
button{border:1px solid var(--line);background:var(--card);color:var(--text);border-radius:6px;padding:7px 12px;cursor:pointer;font-size:14px}
button.on{background:var(--text);color:var(--bg)}
.dot{width:10px;height:10px;border-radius:50%;display:inline-block;margin-right:6px}
table{width:100%;border-collapse:collapse;font-size:13px}td{padding:6px 8px;border-top:1px solid var(--line);vertical-align:top}
code{background:var(--chip);padding:1px 5px;border-radius:4px;font-size:12px}
.s2{color:var(--ok)}.s5,.s4{color:var(--bad)}
</style></head><body><main>
<h1>Mock operations</h1>
<div class="muted">What runbook actions call in the demo: workflow engine, service manager, ticketing, chat, risk, market data, regions, deployments, payments hub, trading controls. Also answers as <code>counterparty-service</code>.</div>
<div class="card">
<div class="row"><b style="min-width:180px"><span id="dot" class="dot"></span>counterparty-service</b>
<button id="up" onclick="set({counterpartyHealthy:true})">Healthy</button>
<button id="down" onclick="set({counterpartyHealthy:false})">Down</button></div>
<div class="row" style="margin-top:10px"><b style="min-width:180px">Workflow retries</b>
<button id="r-follow" onclick="set({retries:'follow'})">Follow counterparty health</button>
<button id="r-succeed" onclick="set({retries:'succeed'})">Always succeed</button>
<button id="r-fail" onclick="set({retries:'fail'})">Always fail</button></div>
</div>
<div class="card"><b>Operations</b> <span class="muted">(newest first; repeated requests with the same Idempotency-Key are answered from the first result)</span>
<table><tbody id="log"></tbody></table></div>
</main><script>
async function set(body){await fetch('/api/state',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});load()}
function esc(s){return String(s??'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]))}
async function load(){
 try{const r=await fetch('/api/state');const d=await r.json();const st=d.state;
 document.getElementById('dot').style.background=st.counterpartyHealthy?'var(--ok)':'var(--bad)';
 document.getElementById('up').className=st.counterpartyHealthy?'on':'';document.getElementById('down').className=st.counterpartyHealthy?'':'on';
 for(const k of ['follow','succeed','fail'])document.getElementById('r-'+k).className=st.retries===k?'on':'';
 document.getElementById('log').innerHTML=(d.log||[]).map(e=>'<tr><td class="muted">'+new Date(e.at).toLocaleTimeString()+'</td><td><code>'+esc(e.method)+' '+esc(e.path)+'</code>'+(e.replay?' <span class="muted">(replay)</span>':'')+'</td><td class="s'+String(e.status)[0]+'">'+e.status+'</td><td>'+esc(e.message)+(e.by?'<div class="muted">'+esc(e.by)+'</div>':'')+'</td></tr>').join('')||'<tr><td class="muted">Nothing yet.</td></tr>';
 }catch(e){}
}
load();setInterval(load,2000);
</script></body></html>`
