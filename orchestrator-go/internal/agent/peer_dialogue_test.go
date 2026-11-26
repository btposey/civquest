package agent

import (
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestPeerConversationGate_PairwiseCooldown(t *testing.T) {
	var g peerConversationGate
	if !g.Allow("a", 1000, false) {
		t.Fatal("first engagement must be allowed")
	}
	if g.Allow("a", 1000+PeerConversationCooldownTicks-1, false) {
		t.Fatal("reply within 90s must be suppressed")
	}
	if !g.Allow("b", 1001, false) {
		t.Fatal("cooldown is per peer; other peer must be allowed")
	}
	if !g.Allow("a", 1000+PeerConversationCooldownTicks, false) {
		t.Fatal("reply after 90s must be allowed")
	}
	// Bypass (question / hazard) is allowed during cooldown and restamps.
	if !g.Allow("a", 1000+PeerConversationCooldownTicks+10, true) {
		t.Fatal("bypass must be allowed")
	}
	if g.Allow("a", 1000+PeerConversationCooldownTicks+20, false) {
		t.Fatal("bypass must restamp cooldown")
	}
	// World tick regression (server restart) resets.
	if !g.Allow("a", 5, false) {
		t.Fatal("regressed tick must reset cooldown")
	}
}

func TestPeerChatDetectionAndBypass(t *testing.T) {
	peer := &civquestv1.ChatEvent{SenderName: "Bran", SenderUuid: OfflinePlayerUUID("Bran"), SenderIsAgent: true}
	if !isPeerAgentChat(peer) {
		t.Error("server-flagged sender should be a peer agent")
	}
	// cq-yn1.8: an offline-mode player has a name-derived UUID too and must not be gated.
	if isPeerAgentChat(&civquestv1.ChatEvent{SenderName: "pbots", SenderUuid: OfflinePlayerUUID("pbots")}) {
		t.Error("offline-mode player must not be a peer agent")
	}
	if isPeerAgentChat(nil) {
		t.Error("nil must not be peer")
	}
	for msg, want := range map[string]bool{
		"Where is the well?":        true,
		"CREEPER behind you":        true,
		"nice weather we're having": false,
	} {
		if peerChatBypassesCooldown(msg) != want {
			t.Errorf("bypass(%q) != %v", msg, want)
		}
	}
}

func TestIsDirectlyAddressed(t *testing.T) {
	cases := []struct {
		listener string
		msg      string
		want     bool
	}{
		{"Aria", "Hey aria, did you chop logs?", true},
		{"aria", "Aria!", true},
		{"Aria", "@Aria check the mine", true},
		{"aria", "Maria went east", false},
		{"aria", "aquaria is nearby", false},
		{"", "aria hello", false},
	}
	for _, tc := range cases {
		if got := isDirectlyAddressed(tc.listener, tc.msg); got != tc.want {
			t.Errorf("isDirectlyAddressed(%q, %q) = %v, want %v", tc.listener, tc.msg, got, tc.want)
		}
	}
}

func TestPeerConversationGate_GlobalCooldownElectionAndHazards(t *testing.T) {
	var g peerConversationGate
	uuidA := OfflinePlayerUUID("Bran")
	uuidB := OfflinePlayerUUID("Cora")

	// 1. Direct address allowed at tick 1000
	if ok, reason := g.AllowPeerChat("Aria", uuidA, "Hey Aria, ready to scout?", 1000); !ok {
		t.Fatalf("expected directly addressed peer chat allowed, got reason=%s", reason)
	}

	// 2. Different peer directly addressed within 30s (600 ticks) is blocked by global cooldown
	if ok, reason := g.AllowPeerChat("Aria", uuidB, "Aria, what is the plan?", 1000+PeerGlobalReplyCooldownTicks-1); ok || reason != "global_cooldown" {
		t.Fatalf("expected global_cooldown suppression, got ok=%v reason=%s", ok, reason)
	}

	// 3. Hazard alert bypasses both global and pairwise cooldowns
	if ok, reason := g.AllowPeerChat("Aria", uuidB, "CREEPER near camp!", 1000+100); !ok {
		t.Fatalf("expected hazard alert to bypass cooldowns, got reason=%s", reason)
	}

	// 4. After 30s global cooldown elapses, peer B (never replied to for non-hazard) is allowed,
	//    while peer A is still blocked by 90s pairwise cooldown
	tickAfterGlobal := int64(1000 + 100 + PeerGlobalReplyCooldownTicks)
	if ok, reason := g.AllowPeerChat("Aria", uuidA, "Aria, check again", tickAfterGlobal); ok || reason != "pairwise_cooldown" {
		t.Fatalf("expected pairwise_cooldown for peerA, got ok=%v reason=%s", ok, reason)
	}
	uuidC := OfflinePlayerUUID("Doran")
	if ok, reason := g.AllowPeerChat("Aria", uuidC, "Aria, need stone", tickAfterGlobal); !ok {
		t.Fatalf("expected peerC allowed after global cooldown, got reason=%s", reason)
	}

	// 5. Ambient broadcast across 20 agents filters most listeners (deterministic ~1 in 4)
	elected := 0
	for i := 0; i < 20; i++ {
		var localGate peerConversationGate
		name := string(rune('A'+i)) + "_agent"
		if ok, _ := localGate.AllowPeerChat(name, uuidA, "Heading out to chop oak trees.", 5000); ok {
			elected++
		}
	}
	if elected == 0 || elected >= 12 {
		t.Fatalf("expected ambient election to select a small subset (~25%%) of 20 listeners, got %d", elected)
	}

	// 6. Jitter delay bounds [250ms, 1500ms]
	d := peerReplyJitterDelay("Aria")
	if d < 250*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("expected jitter in [250ms, 1500ms], got %v", d)
	}
}
