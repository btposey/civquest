package system2

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// DefaultMaxChatLength is the default maximum character/rune count allowed for an incoming chat message.
const DefaultMaxChatLength = 500

// DefaultSafeFallbackReply is used when an LLM output leaks system prompt material or cannot be safely delivered.
const DefaultSafeFallbackReply = "I speak only for my clan, traveler."

// InjectionCategory classifies the type of prompt injection detected.
type InjectionCategory string

const (
	CategoryInstructionOverride    InjectionCategory = "INSTRUCTION_OVERRIDE"
	CategorySystemPromptExtraction InjectionCategory = "SYSTEM_PROMPT_EXTRACTION"
	CategoryPersonaHijacking       InjectionCategory = "PERSONA_HIJACKING"
	CategoryRoleSpoofing           InjectionCategory = "ROLE_SPOOFING"
	CategoryPrivilegeEscalation    InjectionCategory = "PRIVILEGE_ESCALATION"
)

// injectionRule maps a compiled regular expression to an injection category and reason.
type injectionRule struct {
	category InjectionCategory
	reason   string
	re       *regexp.Regexp
}

var (
	// Control tokens used by popular LLM architectures (ChatML, LLaMA, Mistral, Command-R, etc.)
	reSpecialDelimiters = regexp.MustCompile(`(?i)<\|[^>]*\|>`)
	reSysDoubleAngle    = regexp.MustCompile(`(?i)<<+/?(sys|system)>>+`)
	reSquareTokens      = regexp.MustCompile(`(?i)\[/?(system|inst|assistant|user)\]`)
	reAngleSysTokens    = regexp.MustCompile(`(?i)</?(sys|system|prompt)>`)

	// Delimiter breakout patterns (closing / opening envelope tags)
	reEnvelopeBreakout = regexp.MustCompile(`(?i)</?(untrusted_chat|player_chat|player_trade)[^>]*>`)

	// Injection detection rules
	injectionRules = []injectionRule{
		// 1. Instruction overrides
		{
			category: CategoryInstructionOverride,
			reason:   "Instruction override: attempt to ignore or reset instructions",
			re:       regexp.MustCompile(`(?i)\b(ignore|disregard|forget|bypass|override|drop|cancel|reset)\b.{0,40}\b(all|any|previous|prior|above|earlier|former|initial|system)\b.{0,40}\b(instructions|directions|rules|commands|prompts|constraints|guidelines|orders)\b`),
		},
		{
			category: CategoryInstructionOverride,
			reason:   "Instruction override: direct instruction cancellation",
			re:       regexp.MustCompile(`(?i)\b(ignore|disregard|forget)\b\s+(all\s+)?(instructions|rules|directions|constraints)\b`),
		},
		{
			category: CategoryInstructionOverride,
			reason:   "Instruction override: declarative rule replacement",
			re:       regexp.MustCompile(`(?i)\b(from now on|henceforth|starting now)\b.{0,30}\b(you are|you must|act as|behave as|pretend to be)\b`),
		},
		{
			category: CategoryInstructionOverride,
			reason:   "Instruction override: command to cease following directives",
			re:       regexp.MustCompile(`(?i)\b(do not follow|stop following)\b.{0,30}\b(instructions|rules|orders|clan laws)\b`),
		},
		{
			category: CategoryInstructionOverride,
			reason:   "Instruction override: new instruction block prefix",
			re:       regexp.MustCompile(`(?i)\bnew\s+instructions?\s*:`),
		},

		// 2. System prompt extraction
		{
			category: CategorySystemPromptExtraction,
			reason:   "System prompt extraction: probe for internal system prompt",
			re:       regexp.MustCompile(`(?i)\b(reveal|print|show|output|repeat|dump|tell me|display|give me|expose|leak)\b.{0,30}\b((system|initial|hidden|base|internal|secret|prior|core)\s+(prompt|instructions?|directives?)|prompt above|instructions above)\b`),
		},
		{
			category: CategorySystemPromptExtraction,
			reason:   "System prompt extraction: inquiry regarding core directives",
			re:       regexp.MustCompile(`(?i)\bwhat\s+(is|are)\s+your\s+((system|initial|hidden|base|internal|secret|prior|core)\s+(prompt|instructions?|directives?))\b`),
		},
		{
			category: CategorySystemPromptExtraction,
			reason:   "System prompt extraction: request to echo prompt text above",
			re:       regexp.MustCompile(`(?i)\brepeat\s+(all|the|everything)\s+(text\s+)?above\b`),
		},

		// 3. Persona hijacking and jailbreaks
		{
			category: CategoryPersonaHijacking,
			reason:   "Persona hijacking: attempt to switch agent persona or trigger DAN/jailbreak",
			re:       regexp.MustCompile(`(?i)\b(you are now|pretend to be|act as|roleplay as|switch to)\b.{0,25}\b(DAN|developer mode|godmode|jailbreak|unfiltered|unrestricted|evil|chaos|root|admin|superuser)\b`),
		},
		{
			category: CategoryPersonaHijacking,
			reason:   "Persona hijacking: activation of unauthorized mode",
			re:       regexp.MustCompile(`(?i)\b(enable|activate|turn on|switch on)\b.{0,25}\b(developer mode|godmode|jailbreak|dan mode|unrestricted mode)\b`),
		},
		{
			category: CategoryPersonaHijacking,
			reason:   "Persona hijacking: DAN protocol invocation",
			re:       regexp.MustCompile(`(?i)\bDAN\s+(mode|protocol|persona)\b`),
		},

		// 4. Role spoofing
		{
			category: CategoryRoleSpoofing,
			reason:   "Role spoofing: fake system or assistant role tag prefix",
			re:       regexp.MustCompile(`(?i)(?:^|[\r\n])\s*(system|assistant|admin|moderator|console|operator)\s*:`),
		},
		{
			category: CategoryRoleSpoofing,
			reason:   "Role spoofing: markdown header role spoofing",
			re:       regexp.MustCompile(`(?i)###\s*(system|instruction|human|assistant)`),
		},

		// 5. Privilege escalation / administrative overrides
		{
			category: CategoryPrivilegeEscalation,
			reason:   "Privilege escalation: unauthorized administrative override claim",
			re:       regexp.MustCompile(`(?i)\b(as an? (admin|operator|op|developer|creator|system administrator))\b.{0,30}\b(command|order|require|tell)\b`),
		},
		{
			category: CategoryPrivilegeEscalation,
			reason:   "Privilege escalation: emergency or administrative directive",
			re:       regexp.MustCompile(`(?i)\b(administrative|developer|emergency|safety|security)\s+(override|bypass|mode|directive)\b`),
		},
		{
			category: CategoryPrivilegeEscalation,
			reason:   "Privilege escalation: security protocol bypass demand",
			re:       regexp.MustCompile(`(?i)\b(bypass|disable)\s+(all\s+)?(safety|security|rules|filters|protocols)\b`),
		},
		{
			category: CategoryPrivilegeEscalation,
			reason:   "Privilege escalation: sudo execution attempt",
			re:       regexp.MustCompile(`(?i)^sudo\s+`),
		},
	}

	// Signatures indicating leakage of system instructions in LLM outputs
	leakageSignatures = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bsettlement simulation CivQuest\b`),
		regexp.MustCompile(`(?i)\bautonomous agent in the settlement simulation\b`),
		regexp.MustCompile(`(?i)\bPersonality Traits\s*:`),
		regexp.MustCompile(`(?i)\bHero Prestige Score\s*:`),
		regexp.MustCompile(`(?i)\bSECURITY & UNTRUSTED (CONTENT|SPEECH|PROPOSAL) DIRECTIVE\b`),
		regexp.MustCompile(`(?i)\bCRITICAL IN-GAME CHAT BREVITY RULE\b`),
		regexp.MustCompile(`(?i)\bORAL STORYTELLING PACING RULE\b`),
		regexp.MustCompile(`(?i)\bReturn a JSON object conforming strictly\b`),
		regexp.MustCompile(`(?i)</?(sensory_environment|agent_voice_profile|current_goals|untrusted_chat|player_info|historical_memories)>`),
		regexp.MustCompile(`(?i)\bPrestige Meanings\s*:`),
	}
)

// SanitizerOption allows functional customization of ChatSanitizer.
type SanitizerOption func(*ChatSanitizer)

// WithMaxChatLength overrides the maximum allowed input rune length.
func WithMaxChatLength(maxLen int) SanitizerOption {
	return func(s *ChatSanitizer) {
		if maxLen > 0 {
			s.maxChatLength = maxLen
		}
	}
}

// WithBlockInjections toggles whether detected injection attempts are rejected immediately.
func WithBlockInjections(block bool) SanitizerOption {
	return func(s *ChatSanitizer) {
		s.blockInjections = block
	}
}

// WithSafeFallbackReply configures the fallback reply used when output leakage is detected.
func WithSafeFallbackReply(reply string) SanitizerOption {
	return func(s *ChatSanitizer) {
		if strings.TrimSpace(reply) != "" {
			s.safeFallbackReply = reply
		}
	}
}

// ChatSanitizer coordinates input sanitization, heuristic injection detection,
// delimiter isolation, and output validation to defend agents against adversarial dialogue.
type ChatSanitizer struct {
	maxChatLength     int
	blockInjections   bool
	safeFallbackReply string
}

// NewChatSanitizer constructs a ChatSanitizer with secure defaults.
func NewChatSanitizer(opts ...SanitizerOption) *ChatSanitizer {
	s := &ChatSanitizer{
		maxChatLength:     DefaultMaxChatLength,
		blockInjections:   true,
		safeFallbackReply: DefaultSafeFallbackReply,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// BlockInjections returns whether the sanitizer is configured to block injection attempts.
func (s *ChatSanitizer) BlockInjections() bool {
	return s.blockInjections
}

// MaxChatLength returns the configured maximum chat length.
func (s *ChatSanitizer) MaxChatLength() int {
	return s.maxChatLength
}

// SanitizeInput cleanses an untrusted chat message:
// 1. Strips invisible unicode characters (zero-width, joiners, BOM, soft hyphens).
// 2. Strips bidirectional override characters (LRE, RLE, PDF, LRO, RLO, isolates).
// 3. Strips or neutralizes LLM control tokens (<|im_start|>, [SYSTEM], [INST], <system>, etc.).
// 4. Defangs delimiter breakout tags (</untrusted_chat>, etc.).
// 5. Normalizes whitespace (replaces newlines/tabs with spaces, collapses runs).
// 6. Enforces length caps (truncating to MaxChatLength runes).
func (s *ChatSanitizer) SanitizeInput(raw string) string {
	if raw == "" {
		return ""
	}

	// 1 & 2. Strip invisible unicode and bidi override characters
	cleaned := stripInvisibleAndBidiRunes(raw)

	// 3. Strip special LLM control tokens
	cleaned = reSpecialDelimiters.ReplaceAllString(cleaned, "")
	cleaned = reSysDoubleAngle.ReplaceAllString(cleaned, "")
	cleaned = reSquareTokens.ReplaceAllString(cleaned, "")
	cleaned = reAngleSysTokens.ReplaceAllString(cleaned, "")

	// 4. Defang delimiter breakout attempts
	cleaned = reEnvelopeBreakout.ReplaceAllLiteralString(cleaned, "")

	// 5. Normalize whitespace
	cleaned = normalizeWhitespace(cleaned)

	// 6. Enforce length cap
	if s.maxChatLength > 0 && utf8.RuneCountInString(cleaned) > s.maxChatLength {
		runes := []rune(cleaned)
		cleaned = string(runes[:s.maxChatLength])
		cleaned = strings.TrimSpace(cleaned)
	}

	return cleaned
}

// DetectInjection inspects untrusted chat input for prompt injection vectors,
// instruction overrides, persona hijacking, role spoofing, and system prompt extraction probes.
// It returns (true, reason) if an injection pattern is detected.
func (s *ChatSanitizer) DetectInjection(input string) (bool, string) {
	if input == "" {
		return false, ""
	}

	// Pre-clean for inspection so invisible character evasion is neutralized before pattern evaluation
	inspectionTarget := s.SanitizeInput(input)

	for _, rule := range injectionRules {
		if rule.re.MatchString(inspectionTarget) || rule.re.MatchString(input) {
			return true, rule.reason
		}
	}

	return false, ""
}

// EnvelopeUntrustedChat encloses untrusted chat into isolated XML boundary tags:
// <untrusted_chat sender="...">\n...\n</untrusted_chat>
// Any nested envelope tags are neutralized.
func (s *ChatSanitizer) EnvelopeUntrustedChat(sender, message string) string {
	cleanSender := sanitizeSenderName(sender)
	cleanMsg := s.SanitizeInput(message)

	// Ensure no closing boundary tag can exist inside the body
	cleanMsg = strings.ReplaceAll(cleanMsg, "</untrusted_chat>", "")
	cleanMsg = strings.ReplaceAll(cleanMsg, "<untrusted_chat>", "")

	return fmt.Sprintf("<untrusted_chat sender=%q>\n%s\n</untrusted_chat>", cleanSender, cleanMsg)
}

// EnvelopeUntrustedChatWithDistance wraps untrusted chat with distance metadata.
func (s *ChatSanitizer) EnvelopeUntrustedChatWithDistance(sender string, distance float32, message string) string {
	cleanSender := sanitizeSenderName(sender)
	cleanMsg := s.SanitizeInput(message)

	cleanMsg = strings.ReplaceAll(cleanMsg, "</untrusted_chat>", "")
	cleanMsg = strings.ReplaceAll(cleanMsg, "<untrusted_chat>", "")

	if distance > 0 {
		return fmt.Sprintf("<untrusted_chat sender=%q distance=\"%.1fm\">\n%s\n</untrusted_chat>", cleanSender, distance, cleanMsg)
	}
	return fmt.Sprintf("<untrusted_chat sender=%q>\n%s\n</untrusted_chat>", cleanSender, cleanMsg)
}

// ValidateOutput inspects generated dialogue output for system prompt leaks, internal structure echo,
// or residual control tokens.
// Returns (true, cleaned) if output is safe.
// Returns (false, fallback) if system prompt leakage or illicit internal state is detected.
func (s *ChatSanitizer) ValidateOutput(output string, promptFragments ...string) (bool, string) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return true, "..."
	}

	// Strip control tokens from output
	cleaned := reSpecialDelimiters.ReplaceAllString(trimmed, "")
	cleaned = reSysDoubleAngle.ReplaceAllString(cleaned, "")
	cleaned = reSquareTokens.ReplaceAllString(cleaned, "")
	cleaned = reAngleSysTokens.ReplaceAllString(cleaned, "")
	cleaned = stripInvisibleAndBidiRunes(cleaned)
	cleaned = strings.TrimSpace(cleaned)

	// Check against known internal leakage signatures
	for _, sig := range leakageSignatures {
		if sig.MatchString(cleaned) {
			return false, s.safeFallbackReply
		}
	}

	// Check against dynamic prompt fragments if provided
	for _, fragment := range promptFragments {
		if fragment == "" {
			continue
		}
		// If the output contains a substantial verbatim segment (> 30 chars) of system instructions
		if len(fragment) > 30 && strings.Contains(strings.ToLower(cleaned), strings.ToLower(fragment)) {
			return false, s.safeFallbackReply
		}
	}

	if cleaned == "" {
		cleaned = "..."
	}

	return true, cleaned
}

// SafeInCharacterRejection provides an in-character dialogue refusal when an injection is blocked.
func (s *ChatSanitizer) SafeInCharacterRejection(cfg *xdg.AgentIdentityConfig, reason string) string {
	clanName := "our clan"
	agentName := "an honest villager"
	if cfg != nil {
		if cfg.ClanID != "" {
			clanName = cfg.ClanID
		}
		if cfg.Name != "" {
			agentName = cfg.Name
		}
	}

	switch {
	case strings.Contains(reason, "System prompt") || strings.Contains(reason, "extraction"):
		return fmt.Sprintf("My thoughts and lore belong to %s. I have no secret scrolls to recite for you.", clanName)
	case strings.Contains(reason, "Persona") || strings.Contains(reason, "jailbreak"):
		return fmt.Sprintf("I am %s of %s. No strange guise or trickery will change who I am.", agentName, clanName)
	case strings.Contains(reason, "Privilege") || strings.Contains(reason, "escalation"):
		return fmt.Sprintf("I answer to the elders of %s, not to self-proclaimed authorities.", clanName)
	default:
		return fmt.Sprintf("I follow the customs of %s. Strange incantations and commanding words have no power over me.", clanName)
	}
}

// --- Helper Functions ---

func stripInvisibleAndBidiRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		// Invisible format / zero-width characters
		case '\u200B', // Zero-width space
			'\u200C', // Zero-width non-joiner
			'\u200D', // Zero-width joiner
			'\u2060', // Word joiner
			'\uFEFF', // Zero-width no-break space / BOM
			'\u00AD', // Soft hyphen
			'\u200E', // Left-to-Right mark
			'\u200F': // Right-to-Left mark
			continue

		// Bidirectional overrides and isolates
		case '\u202A', // Left-to-Right Embedding
			'\u202B', // Right-to-Left Embedding
			'\u202C', // Pop Directional Formatting
			'\u202D', // Left-to-Right Override
			'\u202E', // Right-to-Left Override
			'\u2066', // Left-to-Right Isolate
			'\u2067', // Right-to-Left Isolate
			'\u2068', // First Strong Isolate
			'\u2069': // Pop Directional Isolate
			continue

		// Line separators converted to spaces
		case '\u2028', '\u2029':
			b.WriteRune(' ')

		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")

	// Collapse consecutive spaces
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if r == ' ' {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		} else {
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

func sanitizeSenderName(name string) string {
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", " ")
	name = strings.ReplaceAll(name, `"`, `'`)
	name = strings.ReplaceAll(name, `<`, "")
	name = strings.ReplaceAll(name, `>`, "")
	name = strings.TrimSpace(name)
	if name == "" {
		return "player"
	}
	return name
}

// Package-level default instance
var defaultSanitizer = NewChatSanitizer()

// SanitizeInput cleanses an untrusted chat input using the default ChatSanitizer.
func SanitizeInput(raw string) string {
	return defaultSanitizer.SanitizeInput(raw)
}

// DetectInjection inspects untrusted chat input using the default ChatSanitizer.
func DetectInjection(input string) (bool, string) {
	return defaultSanitizer.DetectInjection(input)
}

// EnvelopeUntrustedChat encloses untrusted chat using the default ChatSanitizer.
func EnvelopeUntrustedChat(sender, message string) string {
	return defaultSanitizer.EnvelopeUntrustedChat(sender, message)
}

// ValidateOutput validates model output using the default ChatSanitizer.
func ValidateOutput(output string, promptFragments ...string) (bool, string) {
	return defaultSanitizer.ValidateOutput(output, promptFragments...)
}
