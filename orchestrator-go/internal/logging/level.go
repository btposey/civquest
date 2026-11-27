// Package logging holds the log levels shared by the orchestrator's process logs and the
// structured agent event stream (cq-49c).
//
//	ERROR  our software failed, or part of it stopped working
//	WARN   something needs attention: an agent hurt or dead, a request that failed
//	INFO   notable events and decisions: what an agent set out to do and how it ended
//	DEBUG  high-volume detail for diagnosis: every frame change, intent, receipt and sample
//
// CIVQUEST_LOG_LEVEL (debug, info, warn, error) sets the lowest level written. The default is
// debug while the project is in play testing.
package logging

import (
	"os"
	"strings"
)

// Level orders log severities.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// EnvVar names the environment variable that sets the lowest level written.
const EnvVar = "CIVQUEST_LOG_LEVEL"

var names = [...]string{"DEBUG", "INFO", "WARN", "ERROR"}

func (l Level) String() string {
	if l < Debug || l > Error {
		return "INFO"
	}
	return names[l]
}

// Parse reads a level name in any case; ok is false for anything else.
func Parse(s string) (Level, bool) {
	for i, n := range names {
		if strings.EqualFold(strings.TrimSpace(s), n) {
			return Level(i), true
		}
	}
	return Info, false
}

// Minimum is the lowest level to write.
func Minimum() Level {
	if l, ok := Parse(os.Getenv(EnvVar)); ok {
		return l
	}
	return Debug
}

// OfLine is the level of one line of process output: its [LEVEL] tag, ERROR for a crash, and
// INFO otherwise. The stream it arrived on says nothing about its importance.
func OfLine(line string) Level {
	if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "fatal error:") {
		return Error
	}
	for l := Error; l >= Debug; l-- {
		if strings.Contains(line, "["+names[l]+"]") {
			return l
		}
	}
	return Info
}
