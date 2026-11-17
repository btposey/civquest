package system2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

var (
	ErrNilRequest       = errors.New("architectural derivative request cannot be nil")
	ErrNilBaseBlueprint = errors.New("base blueprint cannot be nil")
	ErrEmptyResponse    = errors.New("empty LLM response for architectural derivative")
)

// ArchitecturalDerivativeRequest specifies input parameters for deriving or mutating
// an architectural blueprint via System 2 LLM inference.
type ArchitecturalDerivativeRequest struct {
	BaseBlueprint           *blueprints.Blueprint `json:"base_blueprint"`
	ModificationDescription string                `json:"modification_description"`
	TargetPurpose           string                `json:"target_purpose,omitempty"`
	TargetStyle             string                `json:"target_style,omitempty"`
	Author                  string                `json:"author,omitempty"`
	Facts                   *system1.FactGraph    `json:"-"`
}

// BuildArchitectDerivativeSystemPrompt returns the system prompt instructing the LLM
// to act as CivQuest's Master Architect and produce token-efficient ASCII-slice blueprint derivatives.
func BuildArchitectDerivativeSystemPrompt() string {
	return `You are the Master Architect of CivQuest, designing and adapting architectural blueprints for dynastic Minecraft settlements according to ADR-021.
Your task is to generate an architectural derivative or expansion based on a base blueprint and requested modifications.

You must respond with ONLY a single valid, raw JSON object matching this schema:
{
  "name": "<Descriptive building name>",
  "purpose": "<SHELTER|HOME|LIBRARY|TOWNHALL|TAVERN|ARMORY|FARM|STOREHOUSE|BARRACKS|WORKSHOP>",
  "style": "<Architectural style, e.g. pioneer, nordic, stone>",
  "description": "<Architectural description of derivative>",
  "dimensions": {
    "width": <integer width in X direction>,
    "height": <integer total number of layers in Y>,
    "length": <integer length in Z direction>,
    "min_y": <integer lowest elevation, e.g. 0 or negative for subterranean basements>
  },
  "palette": {
    "<single_char>": "<minecraft block state or taxonomy tag, e.g. #minecraft:logs[axis=y], #minecraft:planks, #minecraft:stairs[facing=north]>"
  },
  "layers": [
    {
      "y": <integer elevation>,
      "grid": [
        "<string of characters of length width for row z=0>",
        "<string of characters of length width for row z=1>",
        ...
      ]
    }
  ]
}

Strict Rules:
1. The character '.' is strictly reserved for air.
2. In 'palette', each key must be a single unique ASCII character mapped to a canonical block state or taxonomy tag (#minecraft:planks, #minecraft:logs, #minecraft:stairs, #c:stones).
3. Every layer's 'grid' must have exactly 'length' rows, and each row must have exactly 'width' characters.
4. Total layers must match 'dimensions.height'.
5. If subterranean levels exist (Y < 0), set 'dimensions.min_y' accordingly.
6. Return ONLY the JSON object. Do not wrap in extra prose.
7. Player Clearance: Minecraft humanoid entities are 1.8 blocks tall and require at least 2 full vertical blocks of standing clearance. All doorways, portals, hallways, and subterranean cellars/basements MUST provide at least 2 vertical blocks of unobstructed space (door or air). Doorways must span 2 vertical blocks. Cellars must have at least 2 vertical layers of interior standing space, connected to the ground floor via a ladder or staircase shaft.`
}

// BuildArchitectDerivativeUserPrompt formats the base blueprint ASCII slices and modification
// instructions for the LLM prompt.
func BuildArchitectDerivativeUserPrompt(req *ArchitecturalDerivativeRequest) (string, error) {
	if req == nil {
		return "", ErrNilRequest
	}
	if req.BaseBlueprint == nil {
		return "", ErrNilBaseBlueprint
	}

	var sb strings.Builder
	sb.WriteString("### Base Blueprint\n")
	sb.WriteString(blueprints.FormatASCIISlices(req.BaseBlueprint))
	sb.WriteString("\n\n")

	sb.WriteString("### Requested Modifications\n")
	if strings.TrimSpace(req.ModificationDescription) != "" {
		sb.WriteString(req.ModificationDescription)
	} else {
		sb.WriteString("Create an enhanced derivative blueprint preserving structural coherence.")
	}
	sb.WriteString("\n")

	if req.TargetPurpose != "" {
		sb.WriteString(fmt.Sprintf("- Target Purpose: %s\n", req.TargetPurpose))
	}
	if req.TargetStyle != "" {
		sb.WriteString(fmt.Sprintf("- Target Style: %s\n", req.TargetStyle))
	}

	if req.Facts != nil {
		contextFacts := req.Facts.ToPromptContext()
		if strings.TrimSpace(contextFacts) != "" {
			sb.WriteString("\n")
			sb.WriteString(contextFacts)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\nSynthesize the derivative blueprint according to the requested modifications and return ONLY valid JSON.")
	return sb.String(), nil
}

// rawDerivativeJSON represents the raw JSON deserialization target from LLM completion.
type rawDerivativeJSON struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Purpose     string                 `json:"purpose"`
	Type        string                 `json:"type"`
	Style       string                 `json:"style"`
	Author      string                 `json:"author"`
	Description string                 `json:"description"`
	Dimensions  *blueprints.Dimensions `json:"dimensions"`
	Palette     map[string]interface{} `json:"palette"`
	Layers      []struct {
		Y      int      `json:"y"`
		Grid   []string `json:"grid"`
		Matrix [][]int  `json:"matrix"`
	} `json:"layers"`
	Blocks []blueprints.BlockPlacement `json:"blocks"`
}

// ParseArchitecturalDerivative validates and instantiates a Blueprint from an LLM response JSON.
// Supports both token-efficient ASCII layer slices (grid: []string) and standard voxel matrices.
func ParseArchitecturalDerivative(responseJSON string) (*blueprints.Blueprint, error) {
	cleaned := CleanJSONMarkdown(responseJSON)
	if strings.TrimSpace(cleaned) == "" {
		return nil, ErrEmptyResponse
	}

	var raw rawDerivativeJSON
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		// Fallback: try direct Blueprint unmarshal
		var directBP blueprints.Blueprint
		if directErr := json.Unmarshal([]byte(cleaned), &directBP); directErr == nil {
			if err := directBP.Validate(); err == nil {
				return &directBP, nil
			}
		}
		return nil, fmt.Errorf("parsing architectural derivative JSON: %w", err)
	}

	if strings.TrimSpace(raw.Name) == "" {
		raw.Name = "Architectural Derivative"
	}

	purpose := raw.Purpose
	if purpose == "" {
		purpose = raw.Type
	}
	if purpose == "" {
		purpose = blueprints.PurposeHome
	}

	// 1. Process character palette
	charPalette := make(map[string]string)
	for k, v := range raw.Palette {
		strVal := fmt.Sprintf("%v", v)
		charPalette[strings.TrimSpace(k)] = strings.TrimSpace(strVal)
	}

	// 2. Check if layers use ASCII slice 'grid'
	hasGridLayers := false
	for _, l := range raw.Layers {
		if len(l.Grid) > 0 {
			hasGridLayers = true
			break
		}
	}

	if hasGridLayers {
		var sb strings.Builder
		sb.WriteString("Palette:\n. = air\n")
		for ch, state := range charPalette {
			if ch != "." {
				sb.WriteString(fmt.Sprintf("%s = %s\n", ch, state))
			}
		}

		for _, l := range raw.Layers {
			sb.WriteString(fmt.Sprintf("\nLayer Y=%d:\n", l.Y))
			for _, row := range l.Grid {
				sb.WriteString(row + "\n")
			}
		}

		bp, err := blueprints.ParseASCIISlices(raw.Name, purpose, sb.String(), charPalette)
		if err != nil {
			return nil, fmt.Errorf("parsing derivative ASCII grid: %w", err)
		}

		if raw.ID != "" {
			bp.ID = raw.ID
		}
		if raw.Style != "" {
			bp.Style = raw.Style
		}
		if raw.Author != "" {
			bp.Author = raw.Author
		}
		if raw.Description != "" {
			bp.Description = raw.Description
		}
		return bp, nil
	}

	// 3. Fallback: integer matrix layers or explicit blocks
	bpPalette := make(map[int]string)
	for k, v := range raw.Palette {
		strVal := fmt.Sprintf("%v", v)
		if idx, err := strconv.Atoi(k); err == nil {
			bpPalette[idx] = strVal
		}
	}

	var bpLayers []blueprints.Layer
	for _, l := range raw.Layers {
		if len(l.Matrix) > 0 {
			bpLayers = append(bpLayers, blueprints.Layer{
				Y:      l.Y,
				Matrix: l.Matrix,
			})
		}
	}

	dims := blueprints.Dimensions{}
	if raw.Dimensions != nil {
		dims = *raw.Dimensions
	}

	bp := &blueprints.Blueprint{
		ID:          raw.ID,
		Name:        raw.Name,
		Purpose:     blueprints.NormalizePurpose(purpose),
		Type:        strings.ToLower(blueprints.NormalizePurpose(purpose)),
		Style:       raw.Style,
		Author:      raw.Author,
		Description: raw.Description,
		Dimensions:  dims,
		Palette:     bpPalette,
		Layers:      bpLayers,
		Blocks:      raw.Blocks,
	}

	if err := bp.Validate(); err != nil {
		return nil, fmt.Errorf("validating constructed derivative blueprint: %w", err)
	}

	return bp, nil
}

// ArchitectService manages System 2 deliberate architectural reasoning and derivative generation.
type ArchitectService struct {
	client *Client
}

// NewArchitectService creates a new ArchitectService attached to an LLM client.
func NewArchitectService(client *Client) *ArchitectService {
	return &ArchitectService{client: client}
}

// GenerateDerivative requests an architectural derivative from the LLM client,
// validates the output, and instantiates the resulting Blueprint.
func (s *ArchitectService) GenerateDerivative(ctx context.Context, req *ArchitecturalDerivativeRequest) (*blueprints.Blueprint, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("architect service or LLM client is nil")
	}

	sysPrompt := BuildArchitectDerivativeSystemPrompt()
	userPrompt, err := BuildArchitectDerivativeUserPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("building user prompt: %w", err)
	}

	chatReq := ChatCompletionRequest{
		Temperature: StrategicContemplationSamplingTemperature, // T=0.30 deliberative
		Messages: []ChatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	chatResp, err := s.client.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("architectural derivative inference: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, errors.New("no completion choices returned by LLM gateway")
	}

	return ParseArchitecturalDerivative(chatResp.Choices[0].Message.Content)
}
