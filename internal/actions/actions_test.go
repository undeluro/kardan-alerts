package actions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func load(t *testing.T) *Registry {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	r, err := Load(filepath.Join(filepath.Dir(file), "..", "..", "config", "actions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCatalogue(t *testing.T) {
	r := load(t)
	for _, k := range []string{"http_get", "wait", "escalate", "retry_workflow", "resubmit_payment", "pause_trading"} {
		if _, ok := r.Get(k); !ok {
			t.Errorf("missing %s", k)
		}
	}
	if r.Approval("resubmit_payment", "none") != "required" {
		t.Error("high risk always needs approval, even when the runbook says none")
	}
	if r.Approval("retry_workflow", "none") != "none" || r.Approval("escalate", "required") != "required" {
		t.Error("medium/low risk follow the runbook")
	}
	if err := r.Validate("retry_workflow", map[string]string{}); err == nil {
		t.Error("missing workflowId must be reported")
	}
	if err := r.Validate("open_ticket", map[string]string{"summary": "x"}); err != nil {
		t.Errorf("team is optional: %v", err)
	}
	if got := r.Label("retry_workflow", map[string]string{"workflowId": "WF-1"}); got != "Retry workflow WF-1" {
		t.Errorf("label = %q", got)
	}
}

func TestExecuteHTTP(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.EscapedPath(), r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"message":"workflow restarted"}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	os.Setenv("MOCKOPS_URL", srv.URL)
	defer os.Unsetenv("MOCKOPS_URL")
	r := load(t)
	r.allowed = append(r.allowed, u.Hostname())

	res := r.Execute(context.Background(), Call{RunID: "run-1", Action: "retry_workflow",
		Params: map[string]string{"workflowId": "WF/../x"}, AlertID: "A1", ApprovedBy: "maria"})
	if !res.OK || !strings.Contains(res.Output, "workflow restarted") {
		t.Fatalf("result = %+v", res)
	}
	if gotPath != "/workflows/WF%2F..%2Fx/retry" {
		t.Errorf("params must be path-escaped, got %s", gotPath)
	}
	if gotKey != "run-1" || gotBody["approvedBy"] != "maria" {
		t.Errorf("idempotency key %q body %v", gotKey, gotBody)
	}
}

func TestAllowList(t *testing.T) {
	r := load(t)
	for _, target := range []string{"http://169.254.169.254/latest/meta-data", "file:///etc/passwd", "http://evil.example.com/x"} {
		res := r.Execute(context.Background(), Call{Action: "http_get", Params: map[string]string{"url": target}})
		if res.OK || !(strings.Contains(res.Output, "allowedHosts") || strings.Contains(res.Output, "invalid URL")) {
			t.Errorf("%s must be refused without a request: %+v", target, res)
		}
	}
	if !r.HostAllowed("counterparty-service") || r.HostAllowed("counterparty-service.evil.com") {
		t.Error("exact host match")
	}
}
