package memory

import (
	"context"
	"strings"
	"testing"
)

func TestPeerDialogue_IngestAndCommunitySection(t *testing.T) {
	agentDB, cleanup := openTestAgentDB(t)
	defer cleanup()
	p := NewPipeline(agentDB, &mockEmbedder{})
	ctx := context.Background()

	// Old (outside the 10 min window), then 6 recent statements.
	if err := p.IngestPeerChat(ctx, "me", PeerChat{SenderUUID: "u0", SenderName: "Old", Message: "ancient", Distance: 3, WorldTick: 100}, "1,2,3"); err != nil {
		t.Fatal(err)
	}
	now := int64(50000)
	for i := 0; i < 6; i++ {
		err := p.IngestPeerChat(ctx, "me", PeerChat{
			SenderUUID: "u1", SenderName: "Bran (the Smith)", Message: "need\nlogs " + string(rune('A'+i)),
			Distance: 12.34, WorldTick: now - int64(i)*200,
		}, "1,2,3")
		if err != nil {
			t.Fatal(err)
		}
	}
	// Empty message is ignored; unrelated CHAT memory is not a peer statement.
	_ = p.IngestPeerChat(ctx, "me", PeerChat{SenderName: "X", Message: "  "}, "")
	if _, err := p.Ingest(ctx, MemoryEvent{WorldTick: now, Category: CategoryChat, SummaryText: "Spoke with player"}); err != nil {
		t.Fatal(err)
	}

	out := p.RecentCommunityDialogue(ctx, now)
	if !strings.HasPrefix(out, CommunityDialogueHeader) {
		t.Fatalf("missing header: %q", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 1+CommunityDialogueMaxStatements {
		t.Fatalf("expected %d statements, got %d:\n%s", CommunityDialogueMaxStatements, len(lines)-1, out)
	}
	if !strings.Contains(lines[1], "Bran (the Smith) (12m away, 0s ago): \"need logs A\"") {
		t.Errorf("unexpected newest line: %q", lines[1])
	}
	if strings.Contains(out, "ancient") || strings.Contains(out, "Spoke with player") {
		t.Errorf("stale/unrelated memory leaked: %s", out)
	}

	// Sender UUID persisted in actors_json.
	mems, _ := agentDB.GetRecentMemoriesByCategory(ctx, "CHAT", now, 5)
	found := false
	for _, m := range mems {
		if strings.Contains(m.SummaryText, PeerDialogueTag) && strings.Contains(m.ActorsJSON, "u1") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected sender uuid in actors_json, got %+v", mems)
	}

	// Window excludes everything far in the future.
	if got := p.RecentCommunityDialogue(ctx, now+CommunityDialogueWindowTicks*3); got != "" {
		t.Errorf("expected empty section outside window, got %q", got)
	}
	var nilPipe *Pipeline
	if nilPipe.RecentCommunityDialogue(ctx, now) != "" {
		t.Error("nil pipeline must return empty")
	}
}

func TestParsePeerDialogueSummary(t *testing.T) {
	ev := PeerChatEvent("me", PeerChat{SenderUUID: "u", SenderName: "Ann", Message: "hi (dist 3m): there", Distance: 4.5}, "")
	name, dist, msg, ok := ParsePeerDialogueSummary(ev.SummaryText)
	if !ok || name != "Ann" || dist != 4.5 || msg != "hi (dist 3m): there" {
		t.Fatalf("parse mismatch: %q %v %q %v", name, dist, msg, ok)
	}
	if _, _, _, ok := ParsePeerDialogueSummary("plain text"); ok {
		t.Error("expected no match")
	}
}
