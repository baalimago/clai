package skills

import (
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type LogLevel int

const (
	LogLevelInfo LogLevel = iota
	LogLevelWarn
	LogLevelError
)

const skillsLogLevelEnv = "LOG_LEVEL_SKILLS"

func parseLogLevel(in string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "warn", "warning":
		return LogLevelWarn
	case "error":
		return LogLevelError
	case "", "info":
		return LogLevelInfo
	default:
		return LogLevelInfo
	}
}

func ParseLogLevelFromEnv() LogLevel {
	return parseLogLevel(os.Getenv(skillsLogLevelEnv))
}

type logger struct {
	level LogLevel
}

func newLogger(level LogLevel) logger {
	return logger{level: level}
}

func (l logger) enabled(level LogLevel) bool {
	return level >= l.level
}

func (l logger) Infof(format string, args ...any) {
	if !l.enabled(LogLevelInfo) {
		return
	}
	ancli.Noticef(format, args...)
}

func (l logger) Warnf(format string, args ...any) {
	if !l.enabled(LogLevelWarn) {
		return
	}
	ancli.Warnf(format, args...)
}

func (l logger) Errorf(format string, args ...any) {
	if !l.enabled(LogLevelError) {
		return
	}
	ancli.Errf(format, args...)
}
