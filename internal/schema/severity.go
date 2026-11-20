package schema

import "strings"

// Level is a normalized severity, comparable across schemas: "ERROR",
// "critical", "WARN", "P1" and "sev2" all map onto the same ordered scale.
type Level int

const (
	LevelUnknown Level = iota
	LevelDebug
	LevelInfo
	LevelWarning
	LevelError
	LevelCritical
)

var levelNames = map[Level]string{
	LevelUnknown:  "",
	LevelDebug:    "debug",
	LevelInfo:     "info",
	LevelWarning:  "warning",
	LevelError:    "error",
	LevelCritical: "critical",
}

var severityAliases = map[string]Level{
	"critical": LevelCritical, "crit": LevelCritical, "fatal": LevelCritical, "emerg": LevelCritical,
	"emergency": LevelCritical, "alert": LevelCritical, "page": LevelCritical, "p1": LevelCritical,
	"sev1": LevelCritical, "sev-1": LevelCritical, "s1": LevelCritical, "blocker": LevelCritical,

	"error": LevelError, "err": LevelError, "high": LevelError, "major": LevelError,
	"p2": LevelError, "sev2": LevelError, "sev-2": LevelError, "s2": LevelError, "severe": LevelError,

	"warning": LevelWarning, "warn": LevelWarning, "medium": LevelWarning, "minor": LevelWarning,
	"p3": LevelWarning, "sev3": LevelWarning, "sev-3": LevelWarning, "s3": LevelWarning,

	"info": LevelInfo, "information": LevelInfo, "informational": LevelInfo, "notice": LevelInfo,
	"low": LevelInfo, "p4": LevelInfo, "sev4": LevelInfo, "sev-4": LevelInfo, "s4": LevelInfo, "ok": LevelInfo,

	"debug": LevelDebug, "trace": LevelDebug, "verbose": LevelDebug, "p5": LevelDebug,
}

// ParseLevel maps a free-form severity string onto the normalized scale.
func ParseLevel(s string) Level {
	return severityAliases[strings.ToLower(strings.TrimSpace(s))]
}

func (l Level) String() string { return levelNames[l] }

// NormalizeSeverity returns the canonical name, or "" when unrecognised.
func NormalizeSeverity(s string) string { return ParseLevel(s).String() }
