package main

import (
	"strings"
	"testing"
)

func TestCivctl_Viewer_Commands(t *testing.T) {
	// 1. Test `cq agent viewer --help`
	outAgentViewer, err := executeCommand("agent", "viewer", "--help")
	if err != nil {
		t.Fatalf("cq agent viewer --help failed: %v", err)
	}
	if !strings.Contains(outAgentViewer, "Starts the embedded CivQuest Agent State Viewer HTTP server") {
		t.Errorf("unexpected help text for cq agent viewer: %s", outAgentViewer)
	}
	if !strings.Contains(outAgentViewer, "--host") || !strings.Contains(outAgentViewer, "--port") || !strings.Contains(outAgentViewer, "--no-browser") {
		t.Errorf("missing viewer flags in cq agent viewer help text: %s", outAgentViewer)
	}
	if !strings.Contains(outAgentViewer, "8766") {
		t.Errorf("expected default port 8766 in help text: %s", outAgentViewer)
	}

	// 2. Test `cq viewer --help` (top-level alias)
	outViewer, err := executeCommand("viewer", "--help")
	if err != nil {
		t.Fatalf("cq viewer --help failed: %v", err)
	}
	if !strings.Contains(outViewer, "Starts the embedded CivQuest Agent State Viewer HTTP server") {
		t.Errorf("unexpected help text for cq viewer: %s", outViewer)
	}
	if !strings.Contains(outViewer, "--host") || !strings.Contains(outViewer, "--port") || !strings.Contains(outViewer, "--no-browser") {
		t.Errorf("missing viewer flags in cq viewer help text: %s", outViewer)
	}
	if !strings.Contains(outViewer, "8766") {
		t.Errorf("expected default port 8766 in alias help text: %s", outViewer)
	}
}
