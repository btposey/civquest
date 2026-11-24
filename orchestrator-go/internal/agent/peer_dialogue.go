package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/memory"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// PeerConversationCooldownTicks is the pairwise conversational cooldown (ADR-030):
// 90 seconds at 20 TPS. After engaging a peer agent in LLM dialogue, further
// autonomous replies to that same peer are suppressed until it elapses, preventing
// runaway LLM ping-pong loops between adjacent agents.
const PeerConversationCooldownTicks int64 = 90 * 20

// peerHazardKeywords are emergency terms that bypass the cooldown gate.
var peerHazardKeywords = []string{
	"creeper", "zombie", "skeleton", "spider", "enderman", "witch", "pillager", "raid",
	"danger", "help", "run!", "fire", "lava", "attack", "under attack", "warning", "beware",
}

// peerConversationGate enforces the pairwise cooldown. The zero value is ready to use.
type peerConversationGate struct {
	mu   sync.Mutex
	last map[string]int64 // peer UUID -> world tick of last engagement
}

// Allow reports whether an LLM reply to peerUUID may proceed at worldTick, and if so
// stamps the engagement. bypass (explicit question or hazard) always allows but still stamps.
func (g *peerConversationGate) Allow(peerUUID string, worldTick int64, bypass bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = make(map[string]int64)
	}
	prev, seen := g.last[peerUUID]
	// A world tick that moved backwards (server restart) invalidates the stamp.
	active := seen && worldTick >= prev && worldTick-prev < PeerConversationCooldownTicks
	if active && !bypass {
		return false
	}
	g.last[peerUUID] = worldTick
	return true
}

// isPeerAgentChat reports whether a chat originates from a peer CivQuest agent, whose
// UUID is the deterministic offline UUID of its name (ADR-007).
func isPeerAgentChat(chat *civquestv1.ChatEvent) bool {
	if chat == nil || chat.SenderUuid == "" || chat.SenderName == "" {
		return false
	}
	return strings.EqualFold(chat.SenderUuid, OfflinePlayerUUID(chat.SenderName))
}

// peerChatBypassesCooldown returns true for explicit questions and emergency hazard calls.
func peerChatBypassesCooldown(msg string) bool {
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "?") {
		return true
	}
	for _, kw := range peerHazardKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ingestPeerChat records a heard chat into episodic memory (ADR-030 §3).
func (r *AgentRuntime) ingestPeerChat(ctx context.Context, chat *civquestv1.ChatEvent, frame *civquestv1.TelemetryFrame, worldTick int64) {
	r.mu.RLock()
	memPipe := r.memoryPipeline
	r.mu.RUnlock()
	if memPipe == nil || chat == nil {
		return
	}
	coords := ""
	if frame != nil && frame.Status != nil && frame.Status.Position != nil {
		p := frame.Status.Position
		coords = formatCoords(p.X, p.Y, p.Z)
	}
	_ = memPipe.IngestPeerChat(ctx, r.agentID, memory.PeerChat{
		SenderUUID: chat.SenderUuid,
		SenderName: chat.SenderName,
		Message:    chat.Message,
		Distance:   chat.Distance,
		WorldTick:  worldTick,
	}, coords)
}

func formatCoords(x, y, z float64) string {
	return fmt.Sprintf("%.0f,%.0f,%.0f", x, y, z)
}
