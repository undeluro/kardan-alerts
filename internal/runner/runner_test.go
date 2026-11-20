package runner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/undeluro/kardan-alerts/internal/alerting"
	"github.com/undeluro/kardan-alerts/internal/directory"
)

func TestSignedLinks(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	tok := SignLink("s3cret", "RUN1", "maria", now.Add(time.Hour))
	run, person, err := VerifyLink("s3cret", tok, now)
	if err != nil || run != "RUN1" || person != "maria" {
		t.Fatalf("verify = %s %s %v", run, person, err)
	}
	if _, _, err := VerifyLink("other", tok, now); err == nil {
		t.Error("a different secret must fail")
	}
	if _, _, err := VerifyLink("s3cret", tok, now.Add(2*time.Hour)); err == nil {
		t.Error("expired links must fail")
	}
	forged := SignLink("s3cret", "RUN1", "maria", now.Add(time.Hour))
	forged = "UlVOMi5tYXJpYS4x" + forged[len("UlVOMS5tYXJpYS4x"):] // swap the run id, keep the signature
	if _, _, err := VerifyLink("s3cret", forged, now); err == nil {
		t.Error("tampered payload must fail")
	}
}

func TestFourEyes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir, err := directory.Load(filepath.Join(filepath.Dir(file), "..", "..", "config", "teams.yaml"))
	if err != nil {
		t.Skip("teams.yaml:", err)
	}
	_ = os.Getenv
	run := &alerting.Run{RequestedBy: "Sam Okafor", Risk: "medium"}
	if _, err := CheckApprover(dir, run, "settlements", "sam"); !errors.Is(err, ErrSelfApproval) {
		t.Errorf("requester approving by id: %v", err)
	}
	if _, err := CheckApprover(dir, run, "settlements", "nobody"); !errors.Is(err, ErrUnknownPerson) {
		t.Errorf("unknown approver: %v", err)
	}
	if p, err := CheckApprover(dir, run, "settlements", "Maria Rossi"); err != nil || p.ID != "maria" {
		t.Errorf("second person: %v", err)
	}
	high := &alerting.Run{RequestedBy: "kardan", Risk: "high"}
	if _, err := CheckApprover(dir, high, "payments", "maria"); !errors.Is(err, ErrNotEntitled) {
		t.Errorf("high risk needs the alert's team: %v", err)
	}
	if _, err := CheckApprover(dir, high, "payments", "tom"); err != nil {
		t.Errorf("payments member approves payments: %v", err)
	}
}
