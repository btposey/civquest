package memory

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// Peer dialogue ingestion and community-awareness formatting (ADR-030).
const (
	// PeerDialogueTag prefixes the summary of every ingested inbound chat event.
	PeerDialogueTag = "[PEER_DIALOGUE]"

	// CommunityDialogueHeader is the System 2 prompt section header.
	CommunityDialogueHeader = "[COMMUNITY DIALOGUE & RECENT PEER STATEMENTS]"

	// CommunityDialogueWindowTicks is the 10-minute recency window (12000 ticks at 20 TPS).
	CommunityDialogueWindowTicks int64 = 12000

	// CommunityDialogueMaxStatements bounds how many statements are injected into a prompt.
	CommunityDialogueMaxStatements = 5

	maxPeerStatementRunes = 240
)

// PeerChat is a heard chat event (sender identity, content, distance, tick).
type PeerChat struct {
	SenderUUID string
	SenderName string
	Message    string
	Distance   float32
	WorldTick  int64
}

func cleanStatement(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxPeerStatementRunes {
		s = string(r[:maxPeerStatementRunes]) + "…"
	}
	return s
}

// PeerChatEvent builds the CHAT-category MemoryEvent recording a heard peer statement.
// Sender UUID is kept in actors_json as [listener, senderName, senderUUID].
func PeerChatEvent(listenerID string, c PeerChat, coords string) MemoryEvent {
	name := cleanStatement(c.SenderName)
	if name == "" {
		name = "Unknown"
	}
	return MemoryEvent{
		WorldTick:      c.WorldTick,
		Category:       CategoryChat,
		SaliencyScore:  SaliencyChat,
		Actors:         []string{listenerID, name, c.SenderUUID},
		LocationCoords: coords,
		SummaryText: fmt.Sprintf("%s %s (dist %.1fm): %s",
			PeerDialogueTag, name, c.Distance, cleanStatement(c.Message)),
	}
}

// IngestPeerChat stores a heard peer chat into episodic memory (no-op on nil pipeline/empty message).
func (p *Pipeline) IngestPeerChat(ctx context.Context, listenerID string, c PeerChat, coords string) error {
	if p == nil || strings.TrimSpace(c.Message) == "" {
		return nil
	}
	_, err := p.Ingest(ctx, PeerChatEvent(listenerID, c, coords))
	return err
}

var peerSummaryRe = regexp.MustCompile(`^(.*?) \(dist ([0-9.]+)m\): (.*)$`)

// ParsePeerDialogueSummary extracts (name, distance, message) from a PEER_DIALOGUE summary.
func ParsePeerDialogueSummary(summary string) (name string, dist float64, msg string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(summary), PeerDialogueTag)
	if !found {
		return "", 0, "", false
	}
	m := peerSummaryRe.FindStringSubmatch(strings.TrimSpace(rest))
	if m == nil {
		return "", 0, "", false
	}
	d, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return "", 0, "", false
	}
	return m[1], d, m[3], true
}

// FormatCommunityDialogue formats up to maxStatements recent peer statements (within
// windowTicks of nowTick) as a prompt section. Input must be ordered newest first.
// Returns "" when there is nothing to report.
func FormatCommunityDialogue(mems []db.EpisodicMemory, nowTick, windowTicks int64, maxStatements int) string {
	if maxStatements <= 0 {
		maxStatements = CommunityDialogueMaxStatements
	}
	if windowTicks <= 0 {
		windowTicks = CommunityDialogueWindowTicks
	}
	var lines []string
	for _, m := range mems {
		if len(lines) >= maxStatements {
			break
		}
		if nowTick > 0 && (m.WorldTick > nowTick || nowTick-m.WorldTick > windowTicks) {
			continue
		}
		name, dist, msg, ok := ParsePeerDialogueSummary(m.SummaryText)
		if !ok {
			continue
		}
		ageSec := 0.0
		if nowTick > m.WorldTick {
			ageSec = float64(nowTick-m.WorldTick) / 20.0
		}
		lines = append(lines, fmt.Sprintf("- %s (%.0fm away, %.0fs ago): %q", name, dist, ageSec, msg))
	}
	if len(lines) == 0 {
		return ""
	}
	return CommunityDialogueHeader + "\n" + strings.Join(lines, "\n")
}

// RecentCommunityDialogue retrieves and formats recent peer statements for System 2 prompts.
func (p *Pipeline) RecentCommunityDialogue(ctx context.Context, nowTick int64) string {
	if p == nil {
		return ""
	}
	return RecentCommunityDialogueFromDB(ctx, p.agentDB, nowTick)
}

// RecentCommunityDialogueFromDB queries up to 5 peer statements heard within the last 10 minutes
// and formats them as the [COMMUNITY DIALOGUE & RECENT PEER STATEMENTS] prompt section.
func RecentCommunityDialogueFromDB(ctx context.Context, agentDB *db.AgentDB, nowTick int64) string {
	if agentDB == nil {
		return ""
	}
	minTick := nowTick - CommunityDialogueWindowTicks
	if minTick < 0 || nowTick <= 0 {
		minTick = 0
	}
	mems, err := agentDB.GetRecentMemoriesByCategory(ctx, string(CategoryChat), minTick, CommunityDialogueMaxStatements*4)
	if err != nil {
		return ""
	}
	return FormatCommunityDialogue(mems, nowTick, CommunityDialogueWindowTicks, CommunityDialogueMaxStatements)
}
