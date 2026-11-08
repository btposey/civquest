package system2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// MaxMinecraftBookPages is the hard upper limit on pages in a vanilla written book.
	MaxMinecraftBookPages = 100

	// MaxMinecraftLinesPerPage is the maximum number of lines per written book page.
	MaxMinecraftLinesPerPage = 14

	// MaxMinecraftCharsPerLine is the standard Minecraft book font line width.
	MaxMinecraftCharsPerLine = 19

	// MaxMinecraftCharsPerPage is the total character limit per page including newlines.
	MaxMinecraftCharsPerPage = 250

	// MaxMinecraftBookTitleLen is the maximum character limit for book titles.
	MaxMinecraftBookTitleLen = 32

	// DefaultChroniclerSamplingTemperature is Pipeline B sampling temperature.
	DefaultChroniclerSamplingTemperature = 0.90
)

var (
	// ErrNoUnchronicledMemories indicates no memories qualified for synthesis.
	ErrNoUnchronicledMemories = errors.New("no unchronicled high-saliency or permadeath memories found")
)

// ChronicleRequest specifies inputs for composing a clan chronicle.
type ChronicleRequest struct {
	AgentID              string
	ClanID               string
	AuthorName           string
	MinSaliency          float64
	Limit                int
	TargetLecternOrShelf *civquestv1.BlockPosProto
	BookshelfSlot        int32
}

// ChronicleSynthesisResponse is the structured LLM completion response.
type ChronicleSynthesisResponse struct {
	Title string `json:"title"`
	Prose string `json:"prose"`
}

// BookChronicle encapsulates a synthesized, paginated chronicle book and its associated intent.
type BookChronicle struct {
	Title         string
	Author        string
	Pages         []string
	RawProse      string
	ChronicledIDs []int64
	Intent        *civquestv1.AgentIntent
}

// SanitizeBookTitle ensures the book title adheres to vanilla Minecraft's 32-character limit,
// safely slicing on UTF-8 rune boundaries to prevent multi-byte character corruption.
func SanitizeBookTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Clan Chronicle"
	}
	runes := []rune(title)
	if len(runes) > MaxMinecraftBookTitleLen {
		title = strings.TrimSpace(string(runes[:MaxMinecraftBookTitleLen]))
	}
	return title
}

// PaginateProse breaks continuous prose into discrete Minecraft pages according to strict formatting rules:
// - Title <= 32 chars
// - <= 14 lines per page
// - <= 19 chars per line (measured in Unicode runes)
// - <= 250 total characters per page (including \n)
// - <= 100 pages total
// - Preserves word boundaries without mid-word truncation (unless a single word exceeds 19 runes).
// - Operates strictly on UTF-8 rune boundaries to prevent corrupted characters in protobuf payloads.
func PaginateProse(prose string) []string {
	prose = strings.TrimSpace(prose)
	if prose == "" {
		return []string{""}
	}

	paragraphs := strings.Split(prose, "\n")
	var lines []string

	for pIdx, p := range paragraphs {
		trimmedP := strings.TrimSpace(p)
		if trimmedP == "" {
			// Maintain natural blank line separation between paragraphs if preceding line is not empty
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}

		words := strings.Fields(trimmedP)
		var currentLine []rune

		for _, word := range words {
			wordRunes := []rune(word)

			// Handle oversized words exceeding 19 runes by chunking safely on rune boundaries
			if len(wordRunes) > MaxMinecraftCharsPerLine {
				if len(currentLine) > 0 {
					lines = append(lines, string(currentLine))
					currentLine = nil
				}
				for len(wordRunes) > MaxMinecraftCharsPerLine {
					lines = append(lines, string(wordRunes[:MaxMinecraftCharsPerLine]))
					wordRunes = wordRunes[MaxMinecraftCharsPerLine:]
				}
				currentLine = wordRunes
				continue
			}

			if len(currentLine) == 0 {
				currentLine = wordRunes
			} else if len(currentLine)+1+len(wordRunes) <= MaxMinecraftCharsPerLine {
				currentLine = append(currentLine, ' ')
				currentLine = append(currentLine, wordRunes...)
			} else {
				lines = append(lines, string(currentLine))
				currentLine = wordRunes
			}
		}

		if len(currentLine) > 0 {
			lines = append(lines, string(currentLine))
		}

		// If this is not the last paragraph, add an empty line separator
		if pIdx < len(paragraphs)-1 && len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	}

	// Group lines into discrete pages
	var pages []string
	var currentLines []string
	currentCharCount := 0

	for _, line := range lines {
		lineRuneCount := utf8.RuneCountInString(line)
		additionalChars := lineRuneCount
		if len(currentLines) > 0 {
			additionalChars += 1 // for '\n'
		}

		if len(currentLines) >= MaxMinecraftLinesPerPage ||
			(len(currentLines) > 0 && currentCharCount+additionalChars > MaxMinecraftCharsPerPage) {
			pages = append(pages, strings.Join(currentLines, "\n"))
			currentLines = []string{line}
			currentCharCount = lineRuneCount
		} else {
			currentLines = append(currentLines, line)
			currentCharCount += additionalChars
		}
	}

	if len(currentLines) > 0 {
		pages = append(pages, strings.Join(currentLines, "\n"))
	}

	if len(pages) > MaxMinecraftBookPages {
		pages = pages[:MaxMinecraftBookPages]
	}
	if len(pages) == 0 {
		pages = []string{""}
	}

	return pages
}

// ChroniclerService provides Pipeline B deliberative synthesis of episodic memories into bound books.
type ChroniclerService struct {
	client *Client
}

// NewChroniclerService constructs a ChroniclerService instance.
func NewChroniclerService(client *Client) *ChroniclerService {
	return &ChroniclerService{
		client: client,
	}
}

// ComposeChronicle queries unchronicled high-saliency or permadeath events from AgentDB,
// prompts the LLM with creative temperature (T=0.90) to synthesize solemn narrative prose,
// paginates the output into valid Minecraft book pages, marks the events as chronicled in the DB,
// and packages the result into a CreateWrittenBookIntent.
func (s *ChroniclerService) ComposeChronicle(ctx context.Context, agentDB *db.AgentDB, req ChronicleRequest) (*BookChronicle, error) {
	if agentDB == nil {
		return nil, errors.New("agent DB cannot be nil")
	}

	minSaliency := req.MinSaliency
	if minSaliency <= 0 {
		minSaliency = 0.70
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	memories, err := agentDB.GetUnchronicledMemories(ctx, minSaliency, limit)
	if err != nil {
		return nil, fmt.Errorf("fetching unchronicled memories: %w", err)
	}
	if len(memories) == 0 {
		return nil, ErrNoUnchronicledMemories
	}

	author := req.AuthorName
	if author == "" {
		author = req.AgentID
	}
	if author == "" {
		author = "Clan Chronicler"
	}

	clanID := req.ClanID
	if clanID == "" {
		clanID = "pioneers"
	}

	// Build LLM prompt
	systemPrompt := fmt.Sprintf(`You are the Clan Chronicler of the "%s" clan.
Your sacred duty is to transform factual episodic event logs of heroic deeds, mortal perils, landmark discoveries, and tragic permadeath into solemn, epic, high-fantasy narrative chronicles.
Write in an elevated medieval historical register.

Return a JSON object conforming strictly to this JSON schema:
{
  "title": "A concise, evocative title for the bound volume (max 32 characters)",
  "prose": "Rich chronicle narrative prose spanning multiple paragraphs. Use vivid descriptions and record the participants and outcomes."
}`, clanID)

	var memoryDetails []string
	memoryIDs := make([]int64, len(memories))
	for i, m := range memories {
		memoryIDs[i] = m.ID
		var actors []string
		_ = json.Unmarshal([]byte(m.ActorsJSON), &actors)
		actorsStr := strings.Join(actors, ", ")
		if actorsStr == "" {
			actorsStr = "none"
		}
		locStr := m.LocationCoords
		if locStr == "" {
			locStr = "unknown"
		}

		memoryDetails = append(memoryDetails, fmt.Sprintf(
			"- Event: %s | World Tick: %d | Saliency: %.2f | Actors: [%s] | Location: (%s)\n  Summary: %s",
			m.EventCategory, m.WorldTick, m.SaliencyScore, actorsStr, locStr, m.SummaryText,
		))
	}

	userPrompt := fmt.Sprintf(`Compose the official clan chronicle based on the following %d unchronicled historical events:

%s

Provide the official book title (max 32 characters) and narrative prose.`, len(memories), strings.Join(memoryDetails, "\n\n"))

	messages := []ChatMessage{
		{Role: RoleSystem, Content: systemPrompt},
		{Role: RoleUser, Content: userPrompt},
	}

	var synthesis ChronicleSynthesisResponse
	if s.client != nil {
		if err := s.client.CompleteStructuredWithTemp(ctx, messages, &synthesis, DefaultChroniclerSamplingTemperature); err != nil {
			return nil, fmt.Errorf("synthesizing chronicle via LLM: %w", err)
		}
	} else {
		// Fallback deterministic synthesis if no LLM client configured (useful for offline/mock tests)
		synthesis = ChronicleSynthesisResponse{
			Title: SanitizeBookTitle(fmt.Sprintf("Chronicle of %s", toTitle(clanID))),
			Prose: fallbackChronicleProse(clanID, memories),
		}
	}

	title := SanitizeBookTitle(synthesis.Title)
	pages := PaginateProse(synthesis.Prose)

	// Mark memories as chronicled in AgentDB
	if err := agentDB.MarkMemoriesChronicledBatch(ctx, memoryIDs); err != nil {
		return nil, fmt.Errorf("marking memories as chronicled: %w", err)
	}

	// Construct CreateWrittenBookIntent
	intent := &civquestv1.AgentIntent{
		AgentId: req.AgentID,
		Action: &civquestv1.AgentIntent_CreateBook{
			CreateBook: &civquestv1.CreateWrittenBookIntent{
				Title:                title,
				Author:               author,
				Pages:                pages,
				TargetLecternOrShelf: req.TargetLecternOrShelf,
				BookshelfSlot:        req.BookshelfSlot,
			},
		},
	}

	return &BookChronicle{
		Title:         title,
		Author:        author,
		Pages:         pages,
		RawProse:      synthesis.Prose,
		ChronicledIDs: memoryIDs,
		Intent:        intent,
	}, nil
}

func fallbackChronicleProse(clanID string, memories []db.EpisodicMemory) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Chronicle of the %s Clan.\n\n", toTitle(clanID)))
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("At world tick %d, %s occurred.\n%s\n\n", m.WorldTick, m.EventCategory, m.SummaryText))
	}
	sb.WriteString("Recorded for posterity by the Clan Chronicler.")
	return sb.String()
}

func toTitle(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// SynthesizeCompanionMemorial creates narrative chronicle title, prose, and paginated pages
// honoring a fallen companion who gave their life in battle.
func (s *ChroniclerService) SynthesizeCompanionMemorial(ctx context.Context, agentID, clanID, bondedPlayerName, killer, damageSource string, deathTick int64) (string, string, []string, error) {
	if clanID == "" {
		clanID = "pioneers"
	}

	if s != nil && s.client != nil {
		systemPrompt := fmt.Sprintf(`You are the Clan Chronicler of the "%s" clan.
A heroic agent companion, %s, has fallen in mortal battle defending player %s.
Compose a solemn, epic memorial tribute documenting their loyalty, courage, and sacrifice.

Return a JSON object conforming strictly to this JSON schema:
{
  "title": "A concise title (max 32 chars)",
  "prose": "Solemn memorial narrative prose honoring the fallen companion's loyalty and sacrifice."
}`, clanID, agentID, bondedPlayerName)

		userPrompt := fmt.Sprintf("Companion: %s\nClan: %s\nBonded Player: %s\nFell in battle against: %s\nDamage Source: %s\nWorld Tick: %d\n\nCompose the memorial chronicle volume.",
			agentID, clanID, bondedPlayerName, killer, damageSource, deathTick)

		messages := []ChatMessage{
			{Role: RoleSystem, Content: systemPrompt},
			{Role: RoleUser, Content: userPrompt},
		}

		var resp ChronicleSynthesisResponse
		if err := s.client.CompleteStructuredWithTemp(ctx, messages, &resp, DefaultChroniclerSamplingTemperature); err == nil && resp.Prose != "" {
			title := SanitizeBookTitle(resp.Title)
			if title == "" || title == "Clan Chronicle" {
				title = SanitizeBookTitle(fmt.Sprintf("Memorial of %s", agentID))
			}
			pages := PaginateProse(resp.Prose)
			return title, resp.Prose, pages, nil
		}
	}

	title, prose := FallbackCompanionMemorialProse(agentID, clanID, bondedPlayerName, killer, damageSource, deathTick)
	pages := PaginateProse(prose)
	return title, prose, pages, nil
}

// FallbackCompanionMemorialProse provides deterministic solemn prose for offline/fallback environments.
func FallbackCompanionMemorialProse(agentID, clanID, bondedPlayerName, killer, damageSource string, deathTick int64) (string, string) {
	title := SanitizeBookTitle(fmt.Sprintf("Memorial of %s", agentID))
	if killer == "" || killer == "none" {
		killer = "the darkness"
	}
	if bondedPlayerName == "" {
		bondedPlayerName = "their companion"
	}
	prose := fmt.Sprintf(
		"In solemn remembrance of %s of clan %s.\n\n"+
			"On world tick %d, in mortal combat against %s (%s), %s fought faithfully at the side of %s.\n\n"+
			"With unflinching loyalty and selfless devotion, they laid down their life so that their bonded companion might survive.\n\n"+
			"Their shield is broken, but their honor endures forever in the chronicles of our people. Never shall their devotion be forgotten.",
		agentID, toTitle(clanID), deathTick, killer, damageSource, agentID, bondedPlayerName,
	)
	return title, prose
}
