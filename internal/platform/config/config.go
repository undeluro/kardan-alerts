// Package config loads runtime configuration from environment variables.
//
// Every setting has a default that works with docker-compose.yml, so a fresh
// clone runs without an .env file.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Role names a responsibility a replica can take on. One binary runs any mix
// of roles, which is how the service scales horizontally: add replicas of the
// role that is the bottleneck.
type Role string

const (
	RoleAPI        Role = "api"        // REST, SSE live feed, embedded UI
	RoleIngest     Role = "ingest"     // source adapters -> events topic
	RoleProcessor  Role = "processor"  // rules, dedup, alerts, outbox
	RoleDispatcher Role = "dispatcher" // outbox -> channels, retries
)

var allRoles = []Role{RoleAPI, RoleIngest, RoleProcessor, RoleDispatcher}

type Config struct {
	Roles      map[Role]bool
	InstanceID string

	HTTPAddr  string
	PublicURL string // base URL used in links inside emails / SMS

	PostgresURL string
	RedisAddr   string

	KafkaBrokers     []string
	EventsTopic      string
	EventsPartitions int
	ProcessorGroup   string
	ProcessorBatch   int

	SMTPAddr string
	SMTPFrom string

	// SMS / voice provider. In docker-compose this is the fake gateway with a
	// phone simulator; a real provider would be another channel adapter.
	GatewayURL       string
	GatewayPublicURL string // where people open the phone simulator
	GatewayToken     string // shared secret for outbound calls and inbound replies

	// Fallbacks maps a channel to the one used when it keeps failing.
	Fallbacks map[string]string

	// DemoMode enables the shared business clock offset (time travel for demos).
	DemoMode       bool
	DemoSSO        bool
	DemoSamToken   string
	DemoMariaToken string

	// ApprovalSecret signs one-click approval links in emails. Every replica
	// needs the same value.
	ApprovalSecret string

	ConfigDir string // rules, teams, sources (YAML)

	DedupWindow     time.Duration
	DispatchWorkers int
	MaxAttempts     int
}

func (c Config) Has(r Role) bool { return c.Roles[r] }

func (c Config) RoleList() []string {
	out := make([]string, 0, len(c.Roles))
	for _, r := range allRoles {
		if c.Roles[r] {
			out = append(out, string(r))
		}
	}
	return out
}

func Load() (Config, error) {
	roles, err := parseRoles(env("ROLES", "all"))
	if err != nil {
		return Config{}, err
	}
	host, _ := os.Hostname()
	c := Config{
		Roles:            roles,
		InstanceID:       env("INSTANCE_ID", host),
		HTTPAddr:         env("HTTP_ADDR", ":8080"),
		PublicURL:        strings.TrimRight(env("PUBLIC_URL", "http://localhost:8080"), "/"),
		PostgresURL:      env("POSTGRES_URL", "postgres://kardan:kardan@localhost:15432/kardan?sslmode=disable"),
		RedisAddr:        env("REDIS_ADDR", "localhost:16379"),
		KafkaBrokers:     strings.Split(env("KAFKA_BROKERS", "localhost:19092"), ","),
		EventsTopic:      env("EVENTS_TOPIC", "kardan.events"),
		EventsPartitions: envInt("EVENTS_PARTITIONS", 12),
		ProcessorGroup:   env("PROCESSOR_GROUP", "kardan-processor"),
		ProcessorBatch:   envInt("PROCESSOR_BATCH", 2000),
		SMTPAddr:         env("SMTP_ADDR", "localhost:1025"),
		SMTPFrom:         env("SMTP_FROM", "kardan-alerts@example.com"),
		GatewayURL:       env("GATEWAY_URL", "http://localhost:8090"),
		GatewayPublicURL: strings.TrimRight(env("GATEWAY_PUBLIC_URL", "http://localhost:8090"), "/"),
		GatewayToken:     env("GATEWAY_TOKEN", ""),
		Fallbacks:        parsePairs(env("FALLBACKS", "phone:sms,sms:email")),
		DemoMode:         env("DEMO_MODE", "false") == "true",
		DemoSSO:          env("DEMO_SSO", "false") == "true",
		DemoSamToken:     env("DEMO_SSO_SAM_TOKEN", ""),
		DemoMariaToken:   env("DEMO_SSO_MARIA_TOKEN", ""),
		ApprovalSecret:   env("APPROVAL_SECRET", "dev-approval-secret-change-me"),
		ConfigDir:        env("CONFIG_DIR", "./config"),
		DedupWindow:      envDuration("DEDUP_WINDOW", 5*time.Minute),
		DispatchWorkers:  envInt("DISPATCH_WORKERS", 16),
		MaxAttempts:      envInt("MAX_ATTEMPTS", 8),
	}
	if c.DemoSSO && (!c.DemoMode || c.DemoSamToken == "" || c.DemoMariaToken == "" || c.DemoSamToken == c.DemoMariaToken) {
		return Config{}, fmt.Errorf("DEMO_SSO requires DEMO_MODE and different nonempty account tokens")
	}
	return c, nil
}

func parseRoles(s string) (map[Role]bool, error) {
	out := map[Role]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		if part == "all" {
			for _, r := range allRoles {
				out[r] = true
			}
			continue
		}
		r := Role(part)
		valid := false
		for _, known := range allRoles {
			if r == known {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("unknown role %q (valid: api, ingest, processor, dispatcher, all)", part)
		}
		out[r] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ROLES is empty")
	}
	return out, nil
}

// parsePairs reads "phone:sms,sms:email".
func parsePairs(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pair), ":"); ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}
