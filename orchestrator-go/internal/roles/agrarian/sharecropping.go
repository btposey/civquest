package agrarian

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Sentinel errors.
var (
	ErrNilSurveyEvent    = errors.New("farmland survey event cannot be nil")
	ErrNilBounds         = errors.New("farmland survey bounds cannot be nil")
	ErrInvalidChestPos   = errors.New("designated chest coordinates cannot be nil")
	ErrNilContract       = errors.New("contract cannot be nil")
	ErrNegotiationFailed = errors.New("tenancy proposal was not accepted by player")
)

// SharecroppingProposal holds the pending terms of a sharecropping tenancy before final agreement.
type SharecroppingProposal struct {
	ProposalID   string
	PlayerUUID   string
	PlayerName   string
	AgentID      string
	Bounds       *civquestv1.BoundingBoxProto
	ChestPos     *civquestv1.BlockPosProto
	CropType     string
	SpokenOffer  string
	CreatedTick  int64
	ProposedTick int64
}

// SharecroppingEngine orchestrates farmland survey evaluation, tenancy chat negotiations,
// contract database registration, daily harvesting/replanting intent planning,
// and 50/50 yield split deposits into landowner and clan granary chests.
type SharecroppingEngine struct {
	mu          sync.RWMutex
	agentID     string
	clanID      string
	contractsDB *db.ContractsDB
	proposals   map[string]*SharecroppingProposal // keyed by PlayerUUID
}

// NewSharecroppingEngine constructs an engine for an agent with its shared clan contracts DB.
func NewSharecroppingEngine(agentID, clanID string, contractsDB *db.ContractsDB) *SharecroppingEngine {
	return &SharecroppingEngine{
		agentID:     agentID,
		clanID:      clanID,
		contractsDB: contractsDB,
		proposals:   make(map[string]*SharecroppingProposal),
	}
}

// GenerateProposalMessage composes the natural language spoken proposal for farmland tenancy.
func GenerateProposalMessage(playerName string, bounds *civquestv1.BoundingBoxProto, chestPos *civquestv1.BlockPosProto, cropType string) string {
	if bounds == nil || bounds.Min == nil || bounds.Max == nil || chestPos == nil {
		return fmt.Sprintf("Greetings, %s! Would you be open to a 50/50 sharecrop tenancy for your farmland?", playerName)
	}

	return fmt.Sprintf(
		"Greetings, %s! Our clan observed your fertile cultivated plot (%s) spanning [%d..%d, %d..%d]. "+
			"We propose a 50/50 sharecrop tenancy: our agrarians will tend, harvest, and replant your fields daily, "+
			"depositing half of all yields directly into your chest at (%d, %d, %d) while reserving half for our granary. "+
			"Do you agree?",
		playerName, cropType,
		bounds.Min.X, bounds.Max.X, bounds.Min.Z, bounds.Max.Z,
		chestPos.X, chestPos.Y, chestPos.Z,
	)
}

// IsTenancyAccepted checks if an incoming player chat message indicates consent to the sharecrop offer.
func IsTenancyAccepted(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}

	affirmations := []string{
		"agree", "accept", "deal", "yes", "sounds good",
		"shake on it", "ok", "okay", "i agree", "confirmed",
		"sure", "let's do it", "lets do it", "approved",
	}

	for _, aff := range affirmations {
		if strings.Contains(lower, aff) {
			return true
		}
	}
	return false
}

// HandleSurveyEvent evaluates an incoming FarmlandSurveyEvent and prepares a pending proposal.
func (e *SharecroppingEngine) HandleSurveyEvent(
	ctx context.Context,
	survey *civquestv1.FarmlandSurveyEvent,
	chestPos *civquestv1.BlockPosProto,
	currentTick int64,
) (*SharecroppingProposal, error) {
	if survey == nil {
		return nil, ErrNilSurveyEvent
	}
	if survey.Bounds == nil || survey.Bounds.Min == nil || survey.Bounds.Max == nil {
		return nil, ErrNilBounds
	}
	if chestPos == nil {
		return nil, ErrInvalidChestPos
	}

	playerUUID := strings.TrimSpace(survey.InferredOwnerUuid)
	if playerUUID == "" {
		return nil, errors.New("inferred owner uuid cannot be empty")
	}

	cropType := survey.PrimaryCropId
	if cropType == "" {
		cropType = "minecraft:wheat"
	}

	proposalID := generateRandomID("prop")
	proposal := &SharecroppingProposal{
		ProposalID:   proposalID,
		PlayerUUID:   playerUUID,
		PlayerName:   playerUUID, // Will be updated if player name is provided in chat
		AgentID:      e.agentID,
		Bounds:       survey.Bounds,
		ChestPos:     chestPos,
		CropType:     cropType,
		SpokenOffer:  GenerateProposalMessage(playerUUID, survey.Bounds, chestPos, cropType),
		CreatedTick:  currentTick,
		ProposedTick: currentTick,
	}

	e.mu.Lock()
	e.proposals[playerUUID] = proposal
	e.mu.Unlock()

	return proposal, nil
}

// ProcessChatNegotiation evaluates a player's chat reply against any pending proposal.
// If accepted, it registers the active 50/50 contract in contracts.db and returns confirmation speech.
func (e *SharecroppingEngine) ProcessChatNegotiation(
	ctx context.Context,
	chat *civquestv1.ChatEvent,
	currentTick int64,
) (*db.SharecroppingContract, string, error) {
	if chat == nil {
		return nil, "", errors.New("chat event cannot be nil")
	}

	e.mu.Lock()
	proposal, exists := e.proposals[chat.SenderUuid]
	if !exists {
		e.mu.Unlock()
		return nil, "", nil // No active proposal for this sender
	}
	e.mu.Unlock()

	if !IsTenancyAccepted(chat.Message) {
		return nil, "", ErrNegotiationFailed
	}

	playerName := chat.SenderName
	if playerName == "" {
		playerName = proposal.PlayerName
	}

	contractID := generateRandomID("sc")
	contract := &db.SharecroppingContract{
		ContractID:         contractID,
		PlayerUUID:         proposal.PlayerUUID,
		PlayerName:         playerName,
		AgentID:            e.agentID,
		PlotMinX:           int(proposal.Bounds.Min.X),
		PlotMinY:           int(proposal.Bounds.Min.Y),
		PlotMinZ:           int(proposal.Bounds.Min.Z),
		PlotMaxX:           int(proposal.Bounds.Max.X),
		PlotMaxY:           int(proposal.Bounds.Max.Y),
		PlotMaxZ:           int(proposal.Bounds.Max.Z),
		ChestX:             int(proposal.ChestPos.X),
		ChestY:             int(proposal.ChestPos.Y),
		ChestZ:             int(proposal.ChestPos.Z),
		SplitPlayerPercent: 50.0,
		SplitClanPercent:   50.0,
		CropType:           proposal.CropType,
		Status:             db.ContractStatusActive,
		CreatedTick:        currentTick,
		ExpiresTick:        currentTick + 24000*30, // 30 in-game days default
	}

	if e.contractsDB != nil {
		if err := e.contractsDB.CreateContractContext(ctx, contract); err != nil {
			return nil, "", fmt.Errorf("registering contract in db: %w", err)
		}
	}

	e.mu.Lock()
	delete(e.proposals, chat.SenderUuid)
	e.mu.Unlock()

	spokenConfirmation := fmt.Sprintf(
		"Splendid, %s! The sharecropping agreement is struck. We shall tend these acres daily and deposit your 50%% share in your chest at (%d, %d, %d).",
		playerName, contract.ChestX, contract.ChestY, contract.ChestZ,
	)

	return contract, spokenConfirmation, nil
}

// Calculate5050YieldSplit computes a strict 50/50 division of harvested crops and seeds.
// For odd quantities, the landowner receives the initial remainder as a goodwill gesture.
func Calculate5050YieldSplit(harvested []*civquestv1.ItemStackProto) (playerItems []*civquestv1.ItemStackProto, clanItems []*civquestv1.ItemStackProto) {
	for _, stack := range harvested {
		if stack == nil || stack.Count <= 0 {
			continue
		}

		total := stack.Count
		playerCount := (total + 1) / 2 // Favors landowner on odd counts (e.g. 7 -> 4 to player, 3 to clan)
		clanCount := total - playerCount

		if playerCount > 0 {
			playerItems = append(playerItems, &civquestv1.ItemStackProto{
				ItemId:     stack.ItemId,
				Count:      playerCount,
				Durability: stack.Durability,
				CustomName: stack.CustomName,
			})
		}
		if clanCount > 0 {
			clanItems = append(clanItems, &civquestv1.ItemStackProto{
				ItemId:     stack.ItemId,
				Count:      clanCount,
				Durability: stack.Durability,
				CustomName: stack.CustomName,
			})
		}
	}
	return playerItems, clanItems
}

// IsCropMature checks if a visible block is a fully grown crop ready for harvest.
func IsCropMature(blockID, stateString string) bool {
	id := strings.ToLower(blockID)
	state := strings.ToLower(stateString)

	switch {
	case strings.Contains(id, "wheat"), strings.Contains(id, "carrots"), strings.Contains(id, "potatoes"):
		return strings.Contains(state, "age=7") || strings.Contains(state, "age: 7")
	case strings.Contains(id, "beetroot"):
		return strings.Contains(state, "age=3") || strings.Contains(state, "age: 3")
	case strings.Contains(id, "cabbage"), strings.Contains(id, "tomato"), strings.Contains(id, "onion"), strings.Contains(id, "rice"):
		// Farmers Delight crops typically have max age 7 or 3 depending on variant
		return strings.Contains(state, "age=7") || strings.Contains(state, "age=3")
	case strings.Contains(id, "grape"), strings.Contains(id, "vinery"):
		return strings.Contains(state, "age=3") || strings.Contains(state, "age=4") || strings.Contains(state, "mature=true")
	default:
		return false
	}
}

// SeedForItem returns the appropriate seed/crop item used to replant after harvest.
func SeedForItem(blockID string) string {
	id := strings.ToLower(blockID)
	switch {
	case strings.Contains(id, "wheat"):
		return "minecraft:wheat_seeds"
	case strings.Contains(id, "carrot"):
		return "minecraft:carrot"
	case strings.Contains(id, "potato"):
		return "minecraft:potato"
	case strings.Contains(id, "beetroot"):
		return "minecraft:beetroot_seeds"
	case strings.Contains(id, "tomato"):
		return "farmersdelight:tomato_seeds"
	case strings.Contains(id, "cabbage"):
		return "farmersdelight:cabbage_seeds"
	case strings.Contains(id, "onion"):
		return "farmersdelight:onion"
	case strings.Contains(id, "rice"):
		return "farmersdelight:rice"
	case strings.Contains(id, "red_grape"):
		return "vinery:red_grape_seeds"
	case strings.Contains(id, "white_grape"):
		return "vinery:savanna_grape_seeds"
	default:
		return "minecraft:wheat_seeds"
	}
}

// PlanDailyHarvestRoutine scans visible blocks inside the contract's plot bounds
// and generates atomic navigation, harvesting, and replanting intents.
func PlanDailyHarvestRoutine(
	agentID string,
	contract *db.SharecroppingContract,
	visibleBlocks []*civquestv1.VisibleBlock,
) []*civquestv1.AgentIntent {
	if contract == nil || len(visibleBlocks) == 0 {
		return nil
	}

	var intents []*civquestv1.AgentIntent
	var seq int64 = 1

	for _, b := range visibleBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		x := int(b.Pos.X)
		y := int(b.Pos.Y)
		z := int(b.Pos.Z)

		// Check spatial bounds
		if x < contract.PlotMinX || x > contract.PlotMaxX ||
			y < contract.PlotMinY || y > contract.PlotMaxY ||
			z < contract.PlotMinZ || z > contract.PlotMaxZ {
			continue
		}

		if !IsCropMature(b.BlockId, b.BlockStateString) {
			continue
		}

		// 1. Navigate to mature crop block
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: float64(x),
						Y: float64(y),
						Z: float64(z),
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 1,
				},
			},
		})
		seq++

		// 2. Harvest break
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_InteractBlock{
				InteractBlock: &civquestv1.InteractBlockIntent{
					Pos:             b.Pos,
					InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
					BlockOrItemId:   b.BlockId,
				},
			},
		})
		seq++

		// 3. Replant seed
		seedItem := SeedForItem(b.BlockId)
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_InteractBlock{
				InteractBlock: &civquestv1.InteractBlockIntent{
					Pos:             b.Pos,
					InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
					BlockOrItemId:   seedItem,
				},
			},
		})
		seq++
	}

	return intents
}

// GenerateDepositIntents creates sequential intents to deliver the 50% split to the player's
// chest and the remaining 50% split to the clan granary chest.
func GenerateDepositIntents(
	agentID string,
	contract *db.SharecroppingContract,
	playerShare []*civquestv1.ItemStackProto,
	clanShare []*civquestv1.ItemStackProto,
	clanGranaryChest *civquestv1.BlockPosProto,
) []*civquestv1.AgentIntent {
	if contract == nil {
		return nil
	}

	var intents []*civquestv1.AgentIntent
	var seq int64 = 100

	// Step 1: Deposit player share in designated player chest
	if len(playerShare) > 0 {
		playerChestPos := &civquestv1.BlockPosProto{
			X: int32(contract.ChestX),
			Y: int32(contract.ChestY),
			Z: int32(contract.ChestZ),
		}

		// Navigate to player chest
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: float64(contract.ChestX),
						Y: float64(contract.ChestY),
						Z: float64(contract.ChestZ),
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 1,
				},
			},
		})
		seq++

		// Transfer into player chest
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_TransferContainer{
				TransferContainer: &civquestv1.TransferContainerIntent{
					ContainerPos:         playerChestPos,
					DepositIntoContainer: true,
					Items:                playerShare,
				},
			},
		})
		seq++
	}

	// Step 2: Deposit clan share in clan granary chest
	if len(clanShare) > 0 && clanGranaryChest != nil {
		// Navigate to clan granary chest
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target: &civquestv1.Vec3{
						X: float64(clanGranaryChest.X),
						Y: float64(clanGranaryChest.Y),
						Z: float64(clanGranaryChest.Z),
					},
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 1,
				},
			},
		})
		seq++

		// Transfer into granary chest
		intents = append(intents, &civquestv1.AgentIntent{
			AgentId:    agentID,
			SequenceId: seq,
			Action: &civquestv1.AgentIntent_TransferContainer{
				TransferContainer: &civquestv1.TransferContainerIntent{
					ContainerPos:         clanGranaryChest,
					DepositIntoContainer: true,
					Items:                clanShare,
				},
			},
		})
		seq++
	}

	return intents
}

func generateRandomID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}
