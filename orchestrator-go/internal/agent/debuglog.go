package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DebugEvent represents a single structured JSONL event emitted during playtesting
// to $XDG_STATE_HOME/civquest/logs/ so sessions can be reconstructed and evaluated.
type DebugEvent struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	AgentID   string         `json:"agent_id"`
	ClanID    string         `json:"clan_id,omitempty"`
	WorldTick int64          `json:"world_tick,omitempty"`
	Category  string         `json:"category"` // e.g. INCOMING_CHAT, DIALOGUE_EVAL, INTENT_OUT, SYSTEM1_ROUTINE, PERMADEATH
	Summary   string         `json:"summary"`
	Details   map[string]any `json:"details,omitempty"`
}

// DebugLogger writes structured JSONL events to both the per-agent debug log
// ($XDG_STATE_HOME/civquest/logs/agent-{id}-debug.jsonl) and the unified playtest stream
// ($XDG_STATE_HOME/civquest/logs/playtest-events.jsonl).
type DebugLogger struct {
	mu           sync.Mutex
	agentID      string
	clanID       string
	agentFile    *os.File
	playtestFile *os.File
}

// NewDebugLogger opens or creates the structured JSONL debug log files in the well-known XDG logs directory.
func NewDebugLogger(agentDebugPath, playtestEventsPath, agentID, clanID string) *DebugLogger {
	dl := &DebugLogger{
		agentID: agentID,
		clanID:  clanID,
	}
	if agentDebugPath != "" {
		_ = os.MkdirAll(filepath.Dir(agentDebugPath), 0o750)
		if f, err := os.OpenFile(agentDebugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640); err == nil {
			dl.agentFile = f
		}
	}
	if playtestEventsPath != "" {
		_ = os.MkdirAll(filepath.Dir(playtestEventsPath), 0o750)
		if f, err := os.OpenFile(playtestEventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640); err == nil {
			dl.playtestFile = f
		}
	}
	return dl
}

// Emit writes a structured DebugEvent to the JSONL log streams.
func (d *DebugLogger) Emit(worldTick int64, category, summary string, details map[string]any) {
	if d == nil {
		return
	}
	ev := DebugEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     "DEBUG",
		AgentID:   d.agentID,
		ClanID:    d.clanID,
		WorldTick: worldTick,
		Category:  category,
		Summary:   summary,
		Details:   details,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line = append(line, '\n')

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.agentFile != nil {
		_, _ = d.agentFile.Write(line)
	}
	if d.playtestFile != nil {
		_, _ = d.playtestFile.Write(line)
	}
}

// Close closes any underlying file handles.
func (d *DebugLogger) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.agentFile != nil {
		_ = d.agentFile.Close()
		d.agentFile = nil
	}
	if d.playtestFile != nil {
		_ = d.playtestFile.Close()
		d.playtestFile = nil
	}
	return nil
}
