package system2

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// MaxToponymNameLen is the maximum character limit for landmark cultural names.
	MaxToponymNameLen = 24

	// MaxSignpostLineLen is the maximum character limit per line on a physical signpost.
	MaxSignpostLineLen = 15

	// DefaultToponymSamplingTemperature is Pipeline C sampling temperature.
	DefaultToponymSamplingTemperature = 0.85
)

// Vec3Coords holds 3D coordinates for geographical landmarks.
type Vec3Coords struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// ToponymRequest specifies inputs for generating a geographical toponym and signpost.
type ToponymRequest struct {
	AgentID       string
	ClanID        string
	Category      string // 'NATURAL', 'SETTLEMENT', 'RUINS', 'RESOURCE', 'MEMORIAL', 'MONUMENT'
	Coordinates   Vec3Coords
	ContextPrompt string
	IsHangingSign bool
	WorldTick     int64
	LandmarksDB   *db.LandmarksDB
}

// ToponymSynthesisResponse is the structured LLM completion response for Pipeline C.
type ToponymSynthesisResponse struct {
	DisplayName     string `json:"display_name"`     // Max 24 chars
	CulturalEpithet string `json:"cultural_epithet"` // Short descriptor
	Line1           string `json:"line1"`            // Max 15 chars
	Line2           string `json:"line2"`            // Max 15 chars
	Line3           string `json:"line3"`            // Max 15 chars
	Line4           string `json:"line4"`            // Max 15 chars
	ChronicleLore   string `json:"chronicle_lore"`   // Historical or geographical lore
}

// ToponymResult represents a generated cultural landmark and its signpost intent.
type ToponymResult struct {
	LandmarkID      string
	DisplayName     string
	CulturalEpithet string
	Line1           string
	Line2           string
	Line3           string
	Line4           string
	LoreText        string
	Category        string
	Coordinates     Vec3Coords
	IsHangingSign   bool
	Intent          *civquestv1.AgentIntent
}

// SanitizeToponymName clamps landmark display names to <= 24 Unicode runes without splitting UTF-8 characters.
func SanitizeToponymName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Unnamed Landmark"
	}
	runes := []rune(name)
	if len(runes) > MaxToponymNameLen {
		name = strings.TrimSpace(string(runes[:MaxToponymNameLen]))
	}
	return name
}

// SanitizeSignLine clamps signpost lines to <= 15 Unicode runes without splitting UTF-8 characters.
func SanitizeSignLine(line string) string {
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > MaxSignpostLineLen {
		line = strings.TrimSpace(string(runes[:MaxSignpostLineLen]))
	}
	return line
}

// ToponymyService provides Pipeline C deliberative cultural toponym naming and signpost generation.
type ToponymyService struct {
	client *Client
}

// NewToponymyService constructs a ToponymyService instance.
func NewToponymyService(client *Client) *ToponymyService {
	return &ToponymyService{
		client: client,
	}
}

// GenerateToponym synthesizes a cultural name, 4-line sign text (<=15 chars/line), and lore for a geographical site,
// persists the landmark in landmarks.db, and dispatches a PlaceSignpostIntent.
func (s *ToponymyService) GenerateToponym(ctx context.Context, req ToponymRequest) (*ToponymResult, error) {
	category := strings.ToUpper(strings.TrimSpace(req.Category))
	if category == "" {
		category = db.LandmarkCategoryNatural
	}
	if !db.IsValidLandmarkCategory(category) {
		return nil, fmt.Errorf("invalid landmark category %q", req.Category)
	}

	clanID := req.ClanID
	if clanID == "" {
		clanID = "pioneers"
	}

	agentID := req.AgentID
	if agentID == "" {
		agentID = "scout"
	}

	systemPrompt := fmt.Sprintf(`You are the Cultural Toponymist and Mapmaker of the "%s" clan.
Your sacred duty is to name newly discovered geographical biomes, natural wonders, consecrated buildings, ancient ruins, and fallen hero memorials.
Generate an evocative, culturally rich name and a 4-line physical wooden signpost inscription.

CRITICAL INVARIANTS:
- display_name must NOT exceed 24 characters.
- line1, line2, line3, line4 must NOT exceed 15 characters each. Keep them short, solemn, and punchy.

Return a JSON object conforming strictly to this JSON schema:
{
  "display_name": "Evocative Site Name (max 24 chars)",
  "cultural_epithet": "Brief cultural epithet or descriptor",
  "line1": "Sign Line 1 (max 15 chars)",
  "line2": "Sign Line 2 (max 15 chars)",
  "line3": "Sign Line 3 (max 15 chars)",
  "line4": "Sign Line 4 (max 15 chars)",
  "chronicle_lore": "Rich historical lore describing the site for clan archives."
}`, clanID)

	userPrompt := fmt.Sprintf(`Category: %s
Coordinates: (%.1f, %.1f, %.1f)
Context: %s
Discovered by: %s

Synthesize the cultural toponym and signpost inscription.`, category, req.Coordinates.X, req.Coordinates.Y, req.Coordinates.Z, req.ContextPrompt, agentID)

	messages := []ChatMessage{
		{Role: RoleSystem, Content: systemPrompt},
		{Role: RoleUser, Content: userPrompt},
	}

	var synthesis ToponymSynthesisResponse
	if s.client != nil {
		if err := s.client.CompleteStructuredWithTemp(ctx, messages, &synthesis, DefaultToponymSamplingTemperature); err != nil {
			return nil, fmt.Errorf("synthesizing toponym via LLM: %w", err)
		}
	} else {
		synthesis = fallbackToponym(category, clanID, req.Coordinates)
	}

	displayName := SanitizeToponymName(synthesis.DisplayName)
	culturalEpithet := strings.TrimSpace(synthesis.CulturalEpithet)
	line1 := SanitizeSignLine(synthesis.Line1)
	line2 := SanitizeSignLine(synthesis.Line2)
	line3 := SanitizeSignLine(synthesis.Line3)
	line4 := SanitizeSignLine(synthesis.Line4)

	if line1 == "" {
		line1 = SanitizeSignLine(displayName)
	}
	if line2 == "" && culturalEpithet != "" {
		line2 = SanitizeSignLine(culturalEpithet)
	}

	loreText := strings.TrimSpace(synthesis.ChronicleLore)
	if loreText == "" {
		loreText = fmt.Sprintf("%s consecrated by %s at tick %d.", displayName, agentID, req.WorldTick)
	}

	landmarkID := fmt.Sprintf("lm-%s-%d-%s-%d", strings.ToLower(category), req.WorldTick, agentID, int(math.Abs(req.Coordinates.X+req.Coordinates.Z))%10000)

	// Persist to landmarks.db if handle provided
	if req.LandmarksDB != nil {
		lm := &db.Landmark{
			LandmarkID:          landmarkID,
			Name:                displayName,
			Category:            category,
			X:                   req.Coordinates.X,
			Y:                   req.Coordinates.Y,
			Z:                   req.Coordinates.Z,
			SignpostLine1:       line1,
			SignpostLine2:       line2,
			LoreText:            loreText,
			DiscoveredByAgentID: agentID,
			DiscoveredTick:      req.WorldTick,
		}
		if err := req.LandmarksDB.RegisterLandmarkContext(ctx, lm); err != nil {
			return nil, fmt.Errorf("registering landmark in database: %w", err)
		}
	}

	// Construct PlaceSignpostIntent
	intent := &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_PlaceSignpost{
			PlaceSignpost: &civquestv1.PlaceSignpostIntent{
				Pos: &civquestv1.BlockPosProto{
					X: int32(math.Floor(req.Coordinates.X)),
					Y: int32(math.Floor(req.Coordinates.Y)),
					Z: int32(math.Floor(req.Coordinates.Z)),
				},
				IsHangingSign: req.IsHangingSign,
				Line1:         line1,
				Line2:         line2,
				Line3:         line3,
				Line4:         line4,
			},
		},
	}

	return &ToponymResult{
		LandmarkID:      landmarkID,
		DisplayName:     displayName,
		CulturalEpithet: culturalEpithet,
		Line1:           line1,
		Line2:           line2,
		Line3:           line3,
		Line4:           line4,
		LoreText:        loreText,
		Category:        category,
		Coordinates:     req.Coordinates,
		IsHangingSign:   req.IsHangingSign,
		Intent:          intent,
	}, nil
}

func fallbackToponym(category, clanID string, coords Vec3Coords) ToponymSynthesisResponse {
	titleClan := toTitle(clanID)
	switch category {
	case db.LandmarkCategoryMemorial:
		return ToponymSynthesisResponse{
			DisplayName:     fmt.Sprintf("%s Memorial", titleClan),
			CulturalEpithet: "Fallen Hero",
			Line1:           "In Memory Of",
			Line2:           SanitizeSignLine(titleClan),
			Line3:           "Fallen Guard",
			Line4:           "Rest In Peace",
			ChronicleLore:   "A sacred burial cairn honoring the fallen protectors of the frontier.",
		}
	case db.LandmarkCategorySettlement:
		return ToponymSynthesisResponse{
			DisplayName:     fmt.Sprintf("%s Outpost", titleClan),
			CulturalEpithet: "Frontier Gate",
			Line1:           SanitizeSignLine(titleClan),
			Line2:           "Outpost Gate",
			Line3:           "Welcome Friend",
			Line4:           "Stand Vigil",
			ChronicleLore:   "A fortified gateway marking the civilized boundaries of the clan.",
		}
	case db.LandmarkCategoryRuins:
		return ToponymSynthesisResponse{
			DisplayName:     "Forgotten Sanctum",
			CulturalEpithet: "Ancient Stone",
			Line1:           "Ancient Sanctum",
			Line2:           "Old Gods Rest",
			Line3:           "Disturb Not",
			Line4:           "Ancient Ruin",
			ChronicleLore:   "Weathered stone megaliths from a bygone epoch.",
		}
	default:
		return ToponymSynthesisResponse{
			DisplayName:     fmt.Sprintf("%s Valley", titleClan),
			CulturalEpithet: "Verdant Reach",
			Line1:           SanitizeSignLine(titleClan),
			Line2:           "High Valley",
			Line3:           "Water Spring",
			Line4:           "Safe Haven",
			ChronicleLore:   "A sheltered geological formation rich with clean springs and game.",
		}
	}
}
