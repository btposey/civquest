package system2

import (
	"context"
	"math"
	"regexp"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// BondStage defines the relationship intimacy tier with a human player.
type BondStage string

const (
	// BondStranger represents an unfamiliar or neutral player.
	BondStranger BondStage = "STRANGER"
	// BondQuestor represents a trusted companion eligible for escorts and missions.
	BondQuestor BondStage = "QUESTOR"
	// BondCompanion represents a bound, loyal companion who camps and travels with the player.
	BondCompanion BondStage = "COMPANION"
)

// CompanionState defines the active behavior mode of an agent towards a bonded player.
type CompanionState string

const (
	// CompanionStateNone indicates default village agent routines.
	CompanionStateNone CompanionState = "NONE"
	// CompanionStateFollow indicates the agent actively follows the bonded player.
	CompanionStateFollow CompanionState = "FOLLOW"
	// CompanionStateWait indicates the agent holds position at a designated location.
	CompanionStateWait CompanionState = "WAIT"
	// CompanionStateReturnHome indicates the agent is returning to their home village routine.
	CompanionStateReturnHome CompanionState = "RETURN_HOME"
)

// CompanionCommandType classifies voice/chat commands from players.
type CompanionCommandType string

const (
	CommandNone       CompanionCommandType = "NONE"
	CommandFollow     CompanionCommandType = "FOLLOW"
	CommandWait       CompanionCommandType = "WAIT"
	CommandReturnHome CompanionCommandType = "RETURN_HOME"
)

const (
	// Progression thresholds
	QuestorMinDisposition   = 20.0
	QuestorMinFamiliarity   = 0.20
	CompanionMinDisposition = 50.0
	CompanionMinFamiliarity = 0.50

	// Intimacy delta increments
	DialogueFamiliarityDelta  = 0.05
	TravelDistanceThreshold   = 100.0 // blocks traveled together
	TravelFamiliarityDelta    = 0.05
	TravelDispositionDelta    = 2.0
	CombatAidDispositionDelta = 5.0
	CombatAidFamiliarityDelta = 0.08

	// Proximity distances
	MaxCompanionTravelRadius   = 16.0 // max distance to count shared travel
	MinCompanionFollowDistance = 4.0  // min distance before navigating to player
	MaxCompanionFollowDistance = 48.0 // max perception distance for following
)

// ComputeBondStage calculates the BondStage based on disposition score and familiarity.
func ComputeBondStage(disposition float64, familiarity float64) BondStage {
	if disposition >= CompanionMinDisposition && familiarity >= CompanionMinFamiliarity {
		return BondCompanion
	}
	if disposition >= QuestorMinDisposition && familiarity >= QuestorMinFamiliarity {
		return BondQuestor
	}
	return BondStranger
}

var (
	// Command matching regexes strictly anchored or matching explicit multi-word phrases to avoid dialogue false-positives
	reReturnHome = regexp.MustCompile(`(?i)(^\s*(return\s+home|go\s+home|head\s+home|dismiss|leave\s+me)[\.!\?]?$|\b(return\s+home|go\s+home|head\s+home)\b)`)
	reWait       = regexp.MustCompile(`(?i)(^\s*(wait\s+here|stay\s+here|hold\s+position|wait|stay)[\.!\?]?$|\b(wait\s+here|stay\s+here|hold\s+position)\b)`)
	reFollow     = regexp.MustCompile(`(?i)^\s*(please\s+)?(follow\s+me|come\s+with\s+me|with\s+me|let's\s+go|lets\s+go)\b`)
)

// ParseCompanionCommand detects natural voice/chat commands from player messages.
func ParseCompanionCommand(message string) (CompanionCommandType, bool) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return CommandNone, false
	}

	// 1. Check Return Home / Dismiss first to avoid conflicts
	if reReturnHome.MatchString(msg) {
		return CommandReturnHome, true
	}

	// 2. Check Wait / Stay
	if reWait.MatchString(msg) {
		return CommandWait, true
	}

	// 3. Check Follow
	if reFollow.MatchString(msg) {
		return CommandFollow, true
	}

	return CommandNone, false
}

// CompanionCommandResult encapsulates the outcome of parsing and processing a command.
type CompanionCommandResult struct {
	Command    CompanionCommandType
	Recognized bool
	Obeyed     bool
	OldState   CompanionState
	NewState   CompanionState
	BondStage  BondStage
	ReplyText  string
	PlayerUUID string
	PlayerName string
}

// CompanionService manages player relationship progression, companion behavior modes,
// voice command handling, shared travel accumulation, and combat aid tracking.
type CompanionService struct {
	mu           sync.RWMutex
	agentDB      *db.AgentDB
	reputationDB *db.ReputationDB
	agentConfig  *xdg.AgentIdentityConfig

	bondedPlayerUUID string
	bondedPlayerName string
	state            CompanionState
	waitPos          *civquestv1.Vec3

	relCache                  map[string]*db.Relationship
	lastPlayerUUID            string
	lastPlayerPos             *civquestv1.Vec3
	accumulatedTravelDistance float64
	lastCombatAidTick         int64
	persistCh                 chan db.Relationship
	stopOnce                  sync.Once
}

// NewCompanionService initializes a CompanionService with an in-memory relationship cache and async persistence.
func NewCompanionService(agentDB *db.AgentDB, repDB *db.ReputationDB, agentConfig *xdg.AgentIdentityConfig) *CompanionService {
	s := &CompanionService{
		agentDB:      agentDB,
		reputationDB: repDB,
		agentConfig:  agentConfig,
		state:        CompanionStateNone,
		relCache:     make(map[string]*db.Relationship),
		persistCh:    make(chan db.Relationship, 64),
	}

	go func() {
		for rel := range s.persistCh {
			if s.agentDB != nil {
				_ = s.agentDB.UpsertRelationship(context.Background(), rel)
			}
		}
	}()

	return s
}

// Close cleanly flushes and terminates background persistence for CompanionService.
func (s *CompanionService) Close() {
	s.stopOnce.Do(func() {
		if s.persistCh != nil {
			close(s.persistCh)
		}
	})
}

// GetState returns the current companion state.
func (s *CompanionService) GetState() CompanionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetState explicitly sets the companion state.
func (s *CompanionService) SetState(state CompanionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// IsActive returns true if the companion is actively in FOLLOW or WAIT mode.
func (s *CompanionService) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state == CompanionStateFollow || s.state == CompanionStateWait
}

// GetBondedPlayer returns the currently bonded player's UUID and display name.
func (s *CompanionService) GetBondedPlayer() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bondedPlayerUUID, s.bondedPlayerName
}

// SetBondedPlayer sets the bonded player UUID and display name.
func (s *CompanionService) SetBondedPlayer(uuid, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bondedPlayerUUID = uuid
	s.bondedPlayerName = name
}

// GetWaitPosition returns the held position when in WAIT mode.
func (s *CompanionService) GetWaitPosition() *civquestv1.Vec3 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.waitPos == nil {
		return nil
	}
	return &civquestv1.Vec3{
		X: s.waitPos.X,
		Y: s.waitPos.Y,
		Z: s.waitPos.Z,
	}
}

// SetWaitPosition sets the wait position.
func (s *CompanionService) SetWaitPosition(pos *civquestv1.Vec3) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pos == nil {
		s.waitPos = nil
		return
	}
	s.waitPos = &civquestv1.Vec3{
		X: pos.X,
		Y: pos.Y,
		Z: pos.Z,
	}
}

// SetRelationshipInMemory sets or updates a relationship directly in the in-memory cache.
func (s *CompanionService) SetRelationshipInMemory(rel db.Relationship) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relCache == nil {
		s.relCache = make(map[string]*db.Relationship)
	}
	copyRel := rel
	s.relCache[rel.EntityUUID] = &copyRel
}

// GetRelationship retrieves the relationship entry for playerUUID from memory cache or AgentDB.
// If both exist, the entry with the most recent LastInteractionTick is preferred.
func (s *CompanionService) GetRelationship(ctx context.Context, playerUUID string) (*db.Relationship, error) {
	s.mu.RLock()
	var cachedRel *db.Relationship
	if rel, ok := s.relCache[playerUUID]; ok && rel != nil {
		copyRel := *rel
		cachedRel = &copyRel
	}
	s.mu.RUnlock()

	if s.agentDB != nil && playerUUID != "" {
		dbRel, err := s.agentDB.GetRelationship(ctx, playerUUID)
		if err == nil && dbRel != nil {
			if cachedRel == nil || dbRel.LastInteractionTick > cachedRel.LastInteractionTick {
				s.mu.Lock()
				s.relCache[playerUUID] = dbRel
				s.mu.Unlock()
				copyDB := *dbRel
				return &copyDB, nil
			}
		}
	}

	if cachedRel != nil {
		return cachedRel, nil
	}
	return nil, nil
}

// GetBondStage determines the player's BondStage from memory cache or AgentDB relationship.
func (s *CompanionService) GetBondStage(ctx context.Context, playerUUID string) (BondStage, error) {
	rel, err := s.GetRelationship(ctx, playerUUID)
	if err != nil {
		return BondStranger, err
	}
	if rel == nil {
		return BondStranger, nil
	}
	return ComputeBondStage(rel.DispositionScore, rel.Familiarity), nil
}

// ProcessCommand parses an incoming chat message and executes companion state transitions
// if the sender possesses QUESTOR or COMPANION standing. Strangers are rejected.
func (s *CompanionService) ProcessCommand(ctx context.Context, playerUUID, playerName, message string, worldTick int64) (CompanionCommandResult, bool) {
	cmd, ok := ParseCompanionCommand(message)
	if !ok || cmd == CommandNone {
		return CompanionCommandResult{Recognized: false}, false
	}

	stage := BondStranger
	if playerUUID != "" {
		if rel, err := s.GetRelationship(ctx, playerUUID); err == nil && rel != nil {
			stage = ComputeBondStage(rel.DispositionScore, rel.Familiarity)
		}
	}

	res := CompanionCommandResult{
		Command:    cmd,
		Recognized: true,
		BondStage:  stage,
		PlayerUUID: playerUUID,
		PlayerName: playerName,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	res.OldState = s.state

	// Strangers cannot command agents to follow or wait.
	if stage == BondStranger {
		res.Obeyed = false
		res.NewState = s.state
		res.ReplyText = "I do not know you well enough to follow your lead."
		return res, true
	}

	res.Obeyed = true
	switch cmd {
	case CommandFollow:
		s.state = CompanionStateFollow
		s.bondedPlayerUUID = playerUUID
		s.bondedPlayerName = playerName
		res.NewState = CompanionStateFollow
		res.ReplyText = "I am with you!"

	case CommandWait:
		s.state = CompanionStateWait
		s.bondedPlayerUUID = playerUUID
		s.bondedPlayerName = playerName
		res.NewState = CompanionStateWait
		res.ReplyText = "I will wait here."

	case CommandReturnHome:
		s.state = CompanionStateReturnHome
		res.NewState = CompanionStateReturnHome
		res.ReplyText = "Returning to our village."
	}

	return res, true
}

// RecordDialogueInteraction records a chat turn with a player, boosting familiarity (+0.05)
// and applying any prestige delta to the agent's disposition score.
func (s *CompanionService) RecordDialogueInteraction(ctx context.Context, playerUUID, playerName string, prestigeDelta float64, worldTick int64) (*db.Relationship, error) {
	if playerUUID == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rel := s.getOrCreateRelationshipInMemory(playerUUID, playerName, worldTick)

	rel.Familiarity = math.Min(1.0, math.Max(0.0, rel.Familiarity+DialogueFamiliarityDelta))
	if prestigeDelta != 0.0 {
		rel.DispositionScore = math.Min(100.0, math.Max(-100.0, rel.DispositionScore+prestigeDelta))
	}
	rel.LastInteractionTick = worldTick

	s.queuePersistence(*rel)
	return rel, nil
}

// RecordSharedTravel accumulates distance traveled together when the player is nearby (<16 blocks).
// Every ~100 blocks traveled together increases familiarity (+0.05) and disposition (+2.0).
func (s *CompanionService) RecordSharedTravel(ctx context.Context, playerUUID, playerName string, playerPos, agentPos *civquestv1.Vec3, worldTick int64) (float64, bool, error) {
	if playerPos == nil || agentPos == nil || playerUUID == "" {
		return 0, false, nil
	}

	dx := playerPos.X - agentPos.X
	dy := playerPos.Y - agentPos.Y
	dz := playerPos.Z - agentPos.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	s.mu.Lock()
	defer s.mu.Unlock()

	if dist > MaxCompanionTravelRadius {
		s.lastPlayerPos = nil
		return 0, false, nil
	}

	// Invalidate last position if the evaluated player UUID changed
	if s.lastPlayerUUID != playerUUID {
		s.lastPlayerUUID = playerUUID
		s.lastPlayerPos = nil
	}

	var delta float64
	if s.lastPlayerPos != nil {
		pdx := playerPos.X - s.lastPlayerPos.X
		pdy := playerPos.Y - s.lastPlayerPos.Y
		pdz := playerPos.Z - s.lastPlayerPos.Z
		step := math.Sqrt(pdx*pdx + pdy*pdy + pdz*pdz)
		// Clamp valid step to filter out jitter or teleportation
		if step >= 0.05 && step <= 32.0 {
			delta = step
			s.accumulatedTravelDistance += step
		}
	}
	s.lastPlayerPos = &civquestv1.Vec3{
		X: playerPos.X,
		Y: playerPos.Y,
		Z: playerPos.Z,
	}

	stageChanged := false
	if s.accumulatedTravelDistance >= TravelDistanceThreshold {
		increments := int(s.accumulatedTravelDistance / TravelDistanceThreshold)
		s.accumulatedTravelDistance -= float64(increments) * TravelDistanceThreshold

		rel := s.getOrCreateRelationshipInMemory(playerUUID, playerName, worldTick)

		oldStage := ComputeBondStage(rel.DispositionScore, rel.Familiarity)
		rel.DispositionScore = math.Min(100.0, rel.DispositionScore+float64(increments)*TravelDispositionDelta)
		rel.Familiarity = math.Min(1.0, rel.Familiarity+float64(increments)*TravelFamiliarityDelta)
		rel.LastInteractionTick = worldTick

		newStage := ComputeBondStage(rel.DispositionScore, rel.Familiarity)
		stageChanged = (newStage != oldStage)

		s.queuePersistence(*rel)
	}

	return delta, stageChanged, nil
}

// RecordCombatAid awards intimacy when the agent assists in combat or takes damage near the player.
// Increases disposition (+5.0) and familiarity (+0.08).
func (s *CompanionService) RecordCombatAid(ctx context.Context, playerUUID, playerName string, worldTick int64) (bool, error) {
	if playerUUID == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Debounce combat aid events within 20 ticks (1 second) to prevent multi-hit frame spam
	if s.lastCombatAidTick > 0 && worldTick >= s.lastCombatAidTick && worldTick-s.lastCombatAidTick < 20 {
		return false, nil
	}
	s.lastCombatAidTick = worldTick

	rel := s.getOrCreateRelationshipInMemory(playerUUID, playerName, worldTick)

	oldStage := ComputeBondStage(rel.DispositionScore, rel.Familiarity)
	rel.DispositionScore = math.Min(100.0, rel.DispositionScore+CombatAidDispositionDelta)
	rel.Familiarity = math.Min(1.0, rel.Familiarity+CombatAidFamiliarityDelta)
	rel.LastInteractionTick = worldTick

	newStage := ComputeBondStage(rel.DispositionScore, rel.Familiarity)
	stageChanged := (newStage != oldStage)

	s.queuePersistence(*rel)
	return stageChanged, nil
}

func (s *CompanionService) queuePersistence(rel db.Relationship) {
	if s.persistCh == nil {
		return
	}
	select {
	case s.persistCh <- rel:
	default:
		go func(r db.Relationship) {
			if s.agentDB != nil {
				_ = s.agentDB.UpsertRelationship(context.Background(), r)
			}
		}(rel)
	}
}

func (s *CompanionService) getOrCreateRelationshipInMemory(playerUUID, playerName string, worldTick int64) *db.Relationship {
	if s.relCache == nil {
		s.relCache = make(map[string]*db.Relationship)
	}
	rel, ok := s.relCache[playerUUID]
	if !ok {
		name := playerName
		if name == "" {
			name = playerUUID
		}
		if s.agentDB != nil {
			if existing, err := s.agentDB.GetRelationship(context.Background(), playerUUID); err == nil && existing != nil {
				rel = existing
			}
		}
		if rel == nil {
			rel = &db.Relationship{
				EntityUUID:          playerUUID,
				EntityName:          name,
				EntityType:          "PLAYER",
				KinshipRole:         "NONE",
				DispositionScore:    0.0,
				Familiarity:         0.0,
				LastInteractionTick: worldTick,
			}
		}
		s.relCache[playerUUID] = rel
	}
	if playerName != "" && rel.EntityName != playerName {
		rel.EntityName = playerName
	}
	return rel
}

// EvaluateFollowNavigation determines whether the companion should navigate towards the player.
// When distance > 4 blocks and <= 48 blocks, emits NavigateIntent.
func (s *CompanionService) EvaluateFollowNavigation(agentPos, playerPos *civquestv1.Vec3) (bool, *civquestv1.NavigateIntent) {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()

	if state != CompanionStateFollow || agentPos == nil || playerPos == nil {
		return false, nil
	}

	dx := playerPos.X - agentPos.X
	dy := playerPos.Y - agentPos.Y
	dz := playerPos.Z - agentPos.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	if dist <= MinCompanionFollowDistance || dist > MaxCompanionFollowDistance {
		return false, nil
	}

	sprint := dist > 10.0
	speedMultiplier := float32(1.0)
	if sprint {
		speedMultiplier = 1.25
	}

	navIntent := &civquestv1.NavigateIntent{
		Target: &civquestv1.Vec3{
			X: playerPos.X,
			Y: playerPos.Y,
			Z: playerPos.Z,
		},
		SpeedMultiplier:  speedMultiplier,
		Sprint:           sprint,
		ArrivalTolerance: 2,
	}

	return true, navIntent
}
