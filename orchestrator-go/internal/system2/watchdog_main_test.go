package system2_test

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
)

// TestMain fails the package's tests if the watchdog reports a stuck agent (ADR-041 section 4).
func TestMain(m *testing.M) { watchdogtest.Main(m) }
