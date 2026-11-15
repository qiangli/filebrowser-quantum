// Package logger is the backend's logging facade: a thin forwarder to the
// standard library's log/slog default logger. The embedding host owns the
// handler (bashy bridges slog to OpenTelemetry logs), so this package carries
// no formatting, colour, file or JSON output of its own. It keeps the small
// API the backend already calls, plus the level and API-path filters the
// settings file configures.
package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
)

// JsonConfig is the logging section of the settings file. Only Levels,
// ApiLevels and ApiPathExclude take effect here; Output, NoColors, Json,
// Structured and Utc belong to the host's slog handler.
type JsonConfig struct {
	Levels         string `json:"levels"`
	ApiLevels      string `json:"apiLevels"`
	Output         string `json:"output"`
	NoColors       bool   `json:"noColors"`
	Json           bool   `json:"json"`
	Structured     bool   `json:"structured"`
	Utc            bool   `json:"utc"`
	ApiPathExclude string `json:"apiPathExclude"`
}

var (
	mu          sync.RWMutex
	levels      = map[string]bool{"info": true, "warning": true, "error": true}
	apiLevels   = map[string]bool{"info": true, "warning": true, "error": true}
	apiExclude  *regexp.Regexp
	levelByName = map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warning": slog.LevelWarn, "error": slog.LevelError}
)

// EnableCompatibilityMode applies the settings-file filters. Called once per
// configured logging entry; the last call wins.
func EnableCompatibilityMode(cfg JsonConfig) error {
	var re *regexp.Regexp
	if cfg.ApiPathExclude != "" {
		var err error
		if re, err = regexp.Compile(cfg.ApiPathExclude); err != nil {
			return fmt.Errorf("logger: apiPathExclude: %w", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if cfg.Levels != "" {
		levels = parseLevels(cfg.Levels)
	}
	if cfg.ApiLevels != "" {
		apiLevels = parseLevels(cfg.ApiLevels)
	}
	apiExclude = re
	return nil
}

// DisableCompatibilityMode restores the default filters.
func DisableCompatibilityMode() {
	mu.Lock()
	defer mu.Unlock()
	levels = map[string]bool{"info": true, "warning": true, "error": true}
	apiLevels = map[string]bool{"info": true, "warning": true, "error": true}
	apiExclude = nil
}

func parseLevels(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == '|' || r == ',' || r == ' ' }) {
		if p == "warn" {
			p = "warning"
		}
		out[p] = true
	}
	return out
}

func enabled(level string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return levels[level]
}

func emit(level, msg string, attrs ...any) {
	if !enabled(level) {
		return
	}
	slog.Default().Log(context.Background(), levelByName[level], msg, attrs...)
}

func sprint(a ...any) string {
	s := fmt.Sprintln(a...)
	return strings.TrimSuffix(s, "\n")
}

func Debug(a ...any)                   { emit("debug", sprint(a...)) }
func Debugf(format string, a ...any)   { emit("debug", fmt.Sprintf(format, a...)) }
func Info(a ...any)                    { emit("info", sprint(a...)) }
func Infof(format string, a ...any)    { emit("info", fmt.Sprintf(format, a...)) }
func Warning(a ...any)                 { emit("warning", sprint(a...)) }
func Warningf(format string, a ...any) { emit("warning", fmt.Sprintf(format, a...)) }
func Error(a ...any)                   { emit("error", sprint(a...)) }
func Errorf(format string, a ...any)   { emit("error", fmt.Sprintf(format, a...)) }

// Fatal and Fatalf log at error level and exit the process, as before.
func Fatal(a ...any) {
	slog.Default().Error(sprint(a...))
	os.Exit(1)
}

func Fatalf(format string, a ...any) {
	slog.Default().Error(fmt.Sprintf(format, a...))
	os.Exit(1)
}

// Api logs an access line for an HTTP status; ApiPath also carries the
// request path and honours the apiPathExclude filter.
func Api(statusCode int, a ...any) { apiLine(statusCode, "", sprint(a...)) }

func ApiPath(statusCode int, requestPath string, a ...any) {
	apiLine(statusCode, requestPath, sprint(a...))
}

func apiLevel(status int) string {
	switch {
	case status >= 500:
		return "error"
	case status >= 400:
		return "warning"
	default:
		return "info"
	}
}

func apiLine(status int, path, msg string) {
	level := apiLevel(status)
	mu.RLock()
	ok := apiLevels[level]
	re := apiExclude
	mu.RUnlock()
	if !ok || (re != nil && path != "" && re.MatchString(path)) {
		return
	}
	attrs := []any{"status", status}
	if path != "" {
		attrs = append(attrs, "path", path)
	}
	slog.Default().Log(context.Background(), levelByName[level], msg, attrs...)
}
