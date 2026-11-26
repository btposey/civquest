package agent

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/btposey/civquest/orchestrator-go/internal/memory"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// PeerConversationCooldownTicks is the pairwise conversational cooldown (ADR-030):
// 90 seconds at 20 TPS. After engaging a peer agent in LLM dialogue, further
// autonomous replies to that same peer are suppressed until it elapses, preventing
// runaway LLM ping-pong loops between adjacent agents.
const PeerConversationCooldownTicks int64 = 90 * 20

// PeerGlobalReplyCooldownTicks is the per-listener global cooldown across all peer senders (ADR-031):
// 30 seconds at 20 TPS. Prevents one agent from chaining rapid LLM replies across multiple peers.
const PeerGlobalReplyCooldownTicks int64 = 30 * 20

// peerHazardKeywords are emergency terms that bypass the cooldown gate.
var peerHazardKeywords = []string{
	"creeper", "zombie", "skeleton", "spider", "enderman", "witch", "pillager", "raid",
	"danger", "help", "run!", "fire", "lava", "attack", "under attack", "warning", "beware",
}

// peerConversationGate enforces pairwise cooldown, global peer-reply cooldown,
// and ambient broadcast election (ADR-030 / ADR-031). The zero value is ready to use.
type peerConversationGate struct {
	mu            sync.Mutex
	last          map[string]int64 // peer UUID -> world tick of last engagement
	lastGlobal    int64
	hasLastGlobal bool
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

// AllowPeerChat applies ADR-031 peer-chat LLM contention relief:
//  1. Directly addressed messages or hazard alerts bypass the ambient broadcast election filter.
//  2. Ambient broadcasts elect at most ~1 responder deterministically by hashing (senderUUID, message, listenerName).
//  3. Non-hazard replies enforce both the 30s global cooldown across all peers and 90s pairwise cooldown.
//  4. Hazard alerts bypass cooldowns and restamp them.
func (g *peerConversationGate) AllowPeerChat(listenerName, peerUUID, message string, worldTick int64) (bool, string) {
	hazard := isPeerHazardAlert(message)
	addressed := isDirectlyAddressed(listenerName, message)

	if !addressed && !hazard && !shouldElectAmbientResponder(peerUUID, message, listenerName) {
		return false, "ambient_broadcast_filtered"
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = make(map[string]int64)
	}

	globalActive := g.hasLastGlobal && worldTick >= g.lastGlobal &&
		worldTick-g.lastGlobal < PeerGlobalReplyCooldownTicks
	prev, seen := g.last[peerUUID]
	pairActive := seen && worldTick >= prev &&
		worldTick-prev < PeerConversationCooldownTicks

	if !hazard {
		if globalActive {
			return false, "global_cooldown"
		}
		if pairActive {
			return false, "pairwise_cooldown"
		}
	}

	g.last[peerUUID] = worldTick
	g.lastGlobal = worldTick
	g.hasLastGlobal = true
	return true, ""
}

// isDirectlyAddressed reports whether message mentions listenerName as a whole word (case-insensitive).
func isDirectlyAddressed(listenerName, message string) bool {
	target := strings.ToLower(strings.TrimSpace(listenerName))
	if target == "" || message == "" {
		return false
	}
	lowerMsg := strings.ToLower(message)
	start := 0
	for {
		idx := strings.Index(lowerMsg[start:], target)
		if idx < 0 {
			return false
		}
		absStart := start + idx
		absEnd := absStart + len(target)

		beforeOK := absStart == 0 || !isWordByteOrRune(lowerMsg[:absStart], true)
		afterOK := absEnd == len(lowerMsg) || !isWordByteOrRune(lowerMsg[absEnd:], false)
		if beforeOK && afterOK {
			return true
		}
		start = absStart + 1
	}
}

func isWordByteOrRune(s string, checkLast bool) bool {
	if s == "" {
		return false
	}
	var r rune
	if checkLast {
		for _, ch := range s {
			r = ch
		}
	} else {
		for _, ch := range s {
			r = ch
			break
		}
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isPeerHazardAlert(msg string) bool {
	lower := strings.ToLower(msg)
	for _, kw := range peerHazardKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func shouldElectAmbientResponder(senderUUID, message, listenerName string) bool {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(senderUUID))))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(message)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(listenerName))))

	mod := uint32(4)
	if strings.Contains(message, "?") {
		mod = 3
	}
	return h.Sum32()%mod == 0
}

func peerReplyJitterDelay(listenerName string) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(listenerName))))
	offsetMs := int64(h.Sum32() % 1251) // 250ms..1500ms inclusive
	return time.Duration(250+offsetMs) * time.Millisecond
}

// isPeerAgentChat reports whether a chat originates from a peer CivQuest agent. Only the
// server knows: an offline-mode player's UUID is derived from their name just like an
// agent's (ADR-007), so humans are never inferred to be peers.
func isPeerAgentChat(chat *civquestv1.ChatEvent) bool {
	return chat.GetSenderIsAgent()
}

// peerChatBypassesCooldown returns true for explicit questions and emergency hazard calls.
func peerChatBypassesCooldown(msg string) bool {
	if strings.Contains(msg, "?") {
		return true
	}
	return isPeerHazardAlert(msg)
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
