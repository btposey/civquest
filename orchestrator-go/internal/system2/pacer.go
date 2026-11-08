package system2

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultChatTokensPerSec is the default comfortable human reading speed for paced in-game chat
	// (~4.5 tokens/sec ≈ 18 characters/sec ≈ 215 words/minute).
	DefaultChatTokensPerSec = 4.5

	// DefaultMaxChatUtteranceRunes is the maximum character length of a single in-game chat utterance
	// before the pacer splits multi-sentence output into sequential paced lines.
	DefaultMaxChatUtteranceRunes = 140

	// DefaultMinChatSegmentDelay is the minimum pause between sequential chat utterances.
	DefaultMinChatSegmentDelay = 600 * time.Millisecond

	// DefaultMaxChatSegmentDelay is the maximum pause between sequential chat utterances.
	DefaultMaxChatSegmentDelay = 5 * time.Second
)

// PacedChatSegment represents a single utterance in a multi-part chat response
// along with the delay to wait before emitting it so players can read at a natural pace.
type PacedChatSegment struct {
	Text  string        `json:"text"`
	Delay time.Duration `json:"delay"`
}

// EstimateReadingTokens estimates the reading token count of an utterance
// using the standard 1 token ≈ 4 Unicode runes heuristic (minimum 1 token for non-empty text).
func EstimateReadingTokens(text string) float64 {
	runes := utf8.RuneCountInString(strings.TrimSpace(text))
	if runes == 0 {
		return 0
	}
	return math.Max(1.0, float64(runes)/4.0)
}

// SegmentChatUtterances splits a spoken reply into concise, natural chat utterances
// suitable for sequential display in Minecraft chat.
// Short replies (<= maxUtteranceRunes) remain a single utterance. Longer multi-sentence
// stories (such as Historian oral recounts) are broken cleanly on sentence/clause boundaries.
func SegmentChatUtterances(text string, maxUtteranceRunes int) []string {
	if maxUtteranceRunes <= 0 {
		maxUtteranceRunes = DefaultMaxChatUtteranceRunes
	}

	cleaned := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if cleaned == "" {
		return nil
	}

	if utf8.RuneCountInString(cleaned) <= maxUtteranceRunes && !strings.Contains(cleaned, "\n") {
		return []string{cleaned}
	}

	sentences := splitSentences(cleaned)
	var chunks []string
	var current string

	for _, sent := range sentences {
		sent = strings.TrimSpace(sent)
		if sent == "" {
			continue
		}

		// If a single sentence exceeds maxUtteranceRunes, wrap it on word boundaries
		if utf8.RuneCountInString(sent) > maxUtteranceRunes {
			if current != "" {
				chunks = append(chunks, current)
				current = ""
			}
			chunks = append(chunks, wrapWords(sent, maxUtteranceRunes)...)
			continue
		}

		if current == "" {
			current = sent
		} else if utf8.RuneCountInString(current)+1+utf8.RuneCountInString(sent) <= maxUtteranceRunes {
			current = current + " " + sent
		} else {
			chunks = append(chunks, current)
			current = sent
		}
	}

	if current != "" {
		chunks = append(chunks, current)
	}

	return chunks
}

// ComputePacedSchedule segments a spoken response and computes inter-utterance delays
// regulated by tokensPerSec so multi-part stories are delivered at a comfortable human reading speed.
// The first segment always has Delay = 0 for immediate responsiveness; each subsequent segment i
// waits for the estimated reading time of segment i-1.
func ComputePacedSchedule(text string, tokensPerSec float64, maxUtteranceRunes int) []PacedChatSegment {
	if tokensPerSec <= 0 {
		tokensPerSec = DefaultChatTokensPerSec
	}
	utterances := SegmentChatUtterances(text, maxUtteranceRunes)
	if len(utterances) == 0 {
		return nil
	}

	schedule := make([]PacedChatSegment, len(utterances))
	for i, u := range utterances {
		var delay time.Duration
		if i > 0 {
			prevTokens := EstimateReadingTokens(utterances[i-1])
			sec := prevTokens / tokensPerSec
			delay = time.Duration(sec * float64(time.Second))
			if delay < DefaultMinChatSegmentDelay {
				delay = DefaultMinChatSegmentDelay
			} else if delay > DefaultMaxChatSegmentDelay {
				delay = DefaultMaxChatSegmentDelay
			}
		}
		schedule[i] = PacedChatSegment{
			Text:  u,
			Delay: delay,
		}
	}
	return schedule
}

func splitSentences(text string) []string {
	var out []string
	var buf []rune
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\n' {
			if s := strings.TrimSpace(string(buf)); s != "" {
				out = append(out, s)
			}
			buf = nil
			continue
		}
		buf = append(buf, ch)
		if ch == '.' || ch == '!' || ch == '?' {
			// Split if at end of string or followed by whitespace
			if i+1 == len(runes) || runes[i+1] == ' ' || runes[i+1] == '\t' || runes[i+1] == '\n' {
				if s := strings.TrimSpace(string(buf)); s != "" {
					out = append(out, s)
				}
				buf = nil
			}
		}
	}
	if s := strings.TrimSpace(string(buf)); s != "" {
		out = append(out, s)
	}
	return out
}

func wrapWords(text string, maxRunes int) []string {
	words := strings.Fields(text)
	var chunks []string
	var current string

	for _, w := range words {
		if current == "" {
			current = w
		} else if utf8.RuneCountInString(current)+1+utf8.RuneCountInString(w) <= maxRunes {
			current += " " + w
		} else {
			chunks = append(chunks, current)
			current = w
		}
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}
