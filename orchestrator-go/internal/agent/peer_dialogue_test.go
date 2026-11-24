package agent

import (
	"testing"

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
	peer := &civquestv1.ChatEvent{SenderName: "Bran", SenderUuid: OfflinePlayerUUID("Bran")}
	if !isPeerAgentChat(peer) {
		t.Error("offline-uuid sender should be a peer agent")
	}
	if isPeerAgentChat(&civquestv1.ChatEvent{SenderName: "Steve", SenderUuid: "11111111-1111-1111-1111-111111111111"}) {
		t.Error("online player must not be a peer agent")
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
