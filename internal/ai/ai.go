// Package ai adds optional Claude assistance: turning a plain-English sentence
// into a rule draft and pasted runbook text into structured steps.
//
// Design rules (see docs/ai-usage.md):
//   - Opt-in: off without credentials, and per team (teams.yaml `aiAssist: true`).
//   - Data minimisation: only the user's sentence or runbook text, field
//     *names* and the action catalogue are sent, never event values; obvious
//     secrets and personal data are redacted first.
//   - Output is data: schema-constrained JSON, compiled and validated by the
//     same code as hand-written rules and runbooks, shown as a draft that a
//     person reviews and saves. Nothing is saved or executed automatically.
//   - Always a fallback: every feature has a heuristic implementation that
//     works offline and is used when Claude is off, refuses or fails.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Model is the Claude model used for drafting.
const Model = "claude-opus-5-5"

// Assistant calls Claude when configured. A nil or disabled Assistant makes
// every caller use its heuristic path.
type Assistant struct {
	client  *anthropic.Client
	Log     *slog.Logger
	Timeout time.Duration
}

// New returns an Assistant that is enabled when Anthropic credentials are in
// the environment and AI_ASSIST is not "off".
func New(log *slog.Logger) *Assistant {
	a := &Assistant{Log: log, Timeout: 90 * time.Second}
	if strings.EqualFold(os.Getenv("AI_ASSIST"), "off") {
		return a
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		return a
	}
	c := anthropic.NewClient(option.WithMaxRetries(2))
	a.client = &c
	return a
}

// Enabled reports whether Claude can be called at all (credentials present).
func (a *Assistant) Enabled() bool { return a != nil && a.client != nil }

// ErrDeclined means Claude did not produce a usable answer (refusal, cut off,
// invalid JSON); callers fall back to the heuristic.
var ErrDeclined = errors.New("claude did not return a usable draft")

// generate asks for JSON matching schema and decodes it into out.
func (a *Assistant) generate(ctx context.Context, system, user string, schema json.RawMessage, out any) error {
	if !a.Enabled() {
		return errors.New("claude is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()
	msg, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     Model,
		MaxTokens: 16000,
		// The system prompt is stable, so it is cached across requests.
		System: []anthropic.BetaTextBlockParam{{Text: system, CacheControl: anthropic.NewBetaCacheControlEphemeralParam()}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user)),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortMedium,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: schema},
		},
		// A policy decline is re-served by a fallback model inside the same call.
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return fmt.Errorf("claude API %d: %w", apiErr.StatusCode, err)
		}
		return err
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal, anthropic.BetaStopReasonMaxTokens:
		a.Log.Warn("claude draft not usable", "stop", msg.StopReason)
		return ErrDeclined
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	if err := json.Unmarshal([]byte(text.String()), out); err != nil {
		return fmt.Errorf("%w: %v", ErrDeclined, err)
	}
	a.Log.Info("claude draft", "input_tokens", msg.Usage.InputTokens, "output_tokens", msg.Usage.OutputTokens,
		"cache_read", msg.Usage.CacheReadInputTokens)
	return nil
}

var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)\b(bearer|token|api[_-]?key|secret|password)\s*[:=]\s*\S+`), "$1=[redacted]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`), "[redacted-key]"},
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), "[email]"},
	{regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`), "[iban]"},
	{regexp.MustCompile(`\+?\d[\d ()-]{8,}\d`), "[phone]"},
}

// Redact removes credentials and personal data from free text before it
// leaves the service.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}
