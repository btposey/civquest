package main

import (
	"strings"
	"testing"
)

func TestCivctl_Studio_Commands(t *testing.T) {
	// 1. Test `cq blueprint studio --help`
	outBp, err := executeCommand("blueprint", "studio", "--help")
	if err != nil {
		t.Fatalf("cq blueprint studio --help failed: %v", err)
	}
	if !strings.Contains(outBp, "Starts the embedded Blueprint Studio HTTP server") {
		t.Errorf("unexpected help text for cq blueprint studio: %s", outBp)
	}
	if !strings.Contains(outBp, "--host") || !strings.Contains(outBp, "--port") || !strings.Contains(outBp, "--no-browser") {
		t.Errorf("missing studio flags in help text: %s", outBp)
	}

	// 2. Test `cq studio --help` (alias)
	outStudio, err := executeCommand("studio", "--help")
	if err != nil {
		t.Fatalf("cq studio --help failed: %v", err)
	}
	if !strings.Contains(outStudio, "Starts the embedded Blueprint Studio HTTP server") {
		t.Errorf("unexpected help text for cq studio: %s", outStudio)
	}
	if !strings.Contains(outStudio, "--host") || !strings.Contains(outStudio, "--port") || !strings.Contains(outStudio, "--no-browser") {
		t.Errorf("missing studio flags in alias help text: %s", outStudio)
	}
}
