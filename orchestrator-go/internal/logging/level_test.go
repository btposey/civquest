package logging

import "testing"

func TestOfLineAndMinimum(t *testing.T) {
	for line, want := range map[string]Level{
		"2026/11/27 [civquest-agent] named a landmark": Info,
		"2026/11/27 [WARN] agent x could not save":     Warn,
		"[ERROR] teardown failed":                      Error,
		"panic: boom":                                  Error,
		"[DEBUG] detail":                               Debug,
	} {
		if got := OfLine(line); got != want {
			t.Errorf("OfLine(%q) = %v, want %v", line, got, want)
		}
	}
	t.Setenv(EnvVar, "warn")
	if Minimum() != Warn {
		t.Errorf("Minimum() = %v, want WARN", Minimum())
	}
	t.Setenv(EnvVar, "")
	if Minimum() != Debug {
		t.Errorf("default Minimum() = %v, want DEBUG", Minimum())
	}
}
