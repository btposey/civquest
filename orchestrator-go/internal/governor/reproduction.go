package governor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/domain"
	"github.com/btposey/civquest/orchestrator-go/internal/genetics"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Standard dynastic reproduction constants per ADR-008.
const (
	DefaultMaxActiveAgents    = 16
	DefaultMinFoodSurplusDays = 7
	DefaultDailyFoodPerAgent  = 3
	MinSpouseDisposition      = 80.0
	KinshipRoleSpouse         = "SPOUSE"
	KinshipRoleParent         = "PARENT"
	KinshipRoleChild          = "CHILD"
)

// AgentProcessLauncher starts an independent OS worker process for an agent avatar.
type AgentProcessLauncher interface {
	StartAgent(agentName string) error
}

// EntitySpawner spawns the agent avatar into Minecraft via gRPC.
type EntitySpawner interface {
	SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error)
}

// ReproductionGovernorConfig holds configuration options for the ReproductionGovernor.
type ReproductionGovernorConfig struct {
	Resolver             *xdg.Resolver
	MaxActiveAgents      int
	MinFoodSurplusDays   int
	DailyFoodPerAgent    int
	ClanID               string
	Launcher             AgentProcessLauncher
	Spawner              EntitySpawner
	RNG                  *rand.Rand
	ActiveAgentsProvider func() int
	WorldTickProvider    func() int64
}

// MarriedPair encapsulates two adult agents who share mutual SPOUSE status and high disposition.
type MarriedPair struct {
	ParentA       *xdg.AgentIdentityConfig
	ParentB       *xdg.AgentIdentityConfig
	DispositionAB float64
	DispositionBA float64
}

// ReproductionCheckResult records the detailed evaluation outcome for reproduction invariants.
type ReproductionCheckResult struct {
	Eligible             bool               `json:"eligible"`
	Reason               string             `json:"reason"`
	ActiveAgentsCount    int                `json:"active_agents_count"`
	MaxActiveAgents      int                `json:"max_active_agents"`
	FoodSurplusDays      float64            `json:"food_surplus_days"`
	RequiredFoodUnits    int                `json:"required_food_units"`
	AvailableFoodUnits   int                `json:"available_food_units"`
	UnclaimedHousingZone *db.FunctionalZone `json:"unclaimed_housing_zone,omitempty"`
	EligiblePair         *MarriedPair       `json:"eligible_pair,omitempty"`
}

// ReproductionResult records the dynastic outcome of a completed reproduction event.
type ReproductionResult struct {
	Success           bool                     `json:"success"`
	ChildID           string                   `json:"child_id,omitempty"`
	ChildConfig       *xdg.AgentIdentityConfig `json:"child_config,omitempty"`
	ParentA           string                   `json:"parent_a,omitempty"`
	ParentB           string                   `json:"parent_b,omitempty"`
	Generation        int                      `json:"generation,omitempty"`
	HousingZoneID     string                   `json:"housing_zone_id,omitempty"`
	EntitySpawned     bool                     `json:"entity_spawned"`
	MinecraftEntityID int32                    `json:"minecraft_entity_id,omitempty"`
	OfflineUUID       string                   `json:"offline_uuid,omitempty"`
	ProcessStarted    bool                     `json:"process_started"`
	CheckResult       ReproductionCheckResult  `json:"check_result"`
}

// ReproductionGovernor enforces ADR-008 dynastic reproduction invariants and manages offspring creation.
type ReproductionGovernor struct {
	mu                   sync.Mutex
	resolver             *xdg.Resolver
	maxActiveAgents      int
	minFoodSurplusDays   int
	dailyFoodPerAgent    int
	clanID               string
	launcher             AgentProcessLauncher
	spawner              EntitySpawner
	rng                  *rand.Rand
	activeAgentsProvider func() int
	worldTickProvider    func() int64
}

// NewReproductionGovernor creates an initialized ReproductionGovernor.
func NewReproductionGovernor(cfg ReproductionGovernorConfig) (*ReproductionGovernor, error) {
	r := cfg.Resolver
	if r == nil {
		r = xdg.DefaultResolver()
	}

	maxAgents := cfg.MaxActiveAgents
	minFoodDays := cfg.MinFoodSurplusDays
	dailyFood := cfg.DailyFoodPerAgent

	if gCfg, err := r.LoadGlobalConfig(); err == nil && gCfg != nil {
		if maxAgents <= 0 && gCfg.Supervisor.MaxActiveAgents > 0 {
			maxAgents = gCfg.Supervisor.MaxActiveAgents
		}
		if minFoodDays <= 0 && gCfg.Supervisor.MinFoodSurplusDays > 0 {
			minFoodDays = gCfg.Supervisor.MinFoodSurplusDays
		}
	}

	if maxAgents <= 0 {
		maxAgents = DefaultMaxActiveAgents
	}
	if minFoodDays <= 0 {
		minFoodDays = DefaultMinFoodSurplusDays
	}
	if dailyFood <= 0 {
		dailyFood = DefaultDailyFoodPerAgent
	}

	rng := cfg.RNG
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	return &ReproductionGovernor{
		resolver:             r,
		maxActiveAgents:      maxAgents,
		minFoodSurplusDays:   minFoodDays,
		dailyFoodPerAgent:    dailyFood,
		clanID:               cfg.ClanID,
		launcher:             cfg.Launcher,
		spawner:              cfg.Spawner,
		rng:                  rng,
		activeAgentsProvider: cfg.ActiveAgentsProvider,
		worldTickProvider:    cfg.WorldTickProvider,
	}, nil
}

// SetLauncher assigns or updates the AgentProcessLauncher.
func (g *ReproductionGovernor) SetLauncher(l AgentProcessLauncher) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.launcher = l
}

// SetSpawner assigns or updates the EntitySpawner.
func (g *ReproductionGovernor) SetSpawner(s EntitySpawner) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawner = s
}

// SetActiveAgentsProvider updates the active agent counting callback.
func (g *ReproductionGovernor) SetActiveAgentsProvider(fn func() int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.activeAgentsProvider = fn
}

// Evaluate performs the complete ADR-008 reproduction precondition check for the given clan.
func (g *ReproductionGovernor) Evaluate(ctx context.Context, clanID string) (*ReproductionCheckResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if clanID == "" {
		clanID = g.clanID
	}
	if clanID == "" {
		clanID = "pioneers"
	}

	// -------------------------------------------------------------
	// Invariant 1: Global Limit Check (active_agents < max_active_agents)
	// -------------------------------------------------------------
	activeCount := 0
	if g.activeAgentsProvider != nil {
		activeCount = g.activeAgentsProvider()
	} else {
		// Discover active agents from domain detection
		status, err := domain.DetectAgents(g.resolver)
		if err == nil {
			for _, a := range status.ConfiguredAgents {
				if a.Active && !a.Deceased {
					activeCount++
				}
			}
		}
	}

	res := &ReproductionCheckResult{
		ActiveAgentsCount: activeCount,
		MaxActiveAgents:   g.maxActiveAgents,
	}

	if activeCount >= g.maxActiveAgents {
		res.Eligible = false
		res.Reason = fmt.Sprintf("global active agent limit reached (%d >= %d)", activeCount, g.maxActiveAgents)
		return res, nil
	}

	// -------------------------------------------------------------
	// Invariant 2: Food Reserve Check (clan granary surplus >= 7 days)
	// -------------------------------------------------------------
	civicDB, err := db.OpenCivicRegistryDBForClan(g.resolver, clanID)
	if err != nil {
		res.Eligible = false
		res.Reason = fmt.Sprintf("unable to open civic registry database for clan %s: %v", clanID, err)
		return res, nil
	}
	defer civicDB.Close()

	pop := activeCount
	if pop <= 0 {
		pop = 1
	}
	requiredFood := pop * g.minFoodSurplusDays * g.dailyFoodPerAgent
	res.RequiredFoodUnits = requiredFood

	stocks, err := civicDB.GetGranaryInventoryContext(ctx)
	if err != nil {
		res.Eligible = false
		res.Reason = fmt.Sprintf("failed reading granary inventory: %v", err)
		return res, nil
	}

	availableFood := 0
	for _, s := range stocks {
		if isFoodItem(s.ItemID) {
			avail := s.Quantity - s.ReservedQuantity
			if avail > 0 {
				availableFood += avail
			}
		}
	}
	res.AvailableFoodUnits = availableFood

	dailyDrain := float64(pop * g.dailyFoodPerAgent)
	if dailyDrain > 0 {
		res.FoodSurplusDays = float64(availableFood) / dailyDrain
	}

	if availableFood < requiredFood {
		res.Eligible = false
		res.Reason = fmt.Sprintf("insufficient clan food surplus (%.1f days available < %d days required; %d/%d food units)",
			res.FoodSurplusDays, g.minFoodSurplusDays, availableFood, requiredFood)
		return res, nil
	}

	// -------------------------------------------------------------
	// Invariant 3: Housing Check (unclaimed bed/residential capacity in functional_zones)
	// -------------------------------------------------------------
	unclaimedZone, err := findUnclaimedResidentialZone(ctx, civicDB)
	if err != nil {
		res.Eligible = false
		res.Reason = fmt.Sprintf("evaluating housing capacity: %v", err)
		return res, nil
	}
	if unclaimedZone == nil {
		res.Eligible = false
		res.Reason = "no unclaimed residential or housing capacity in civic registry"
		return res, nil
	}
	res.UnclaimedHousingZone = unclaimedZone

	// -------------------------------------------------------------
	// Invariant 4: Married Pair Check (mutual disposition >= 80.0, kinship_role == 'SPOUSE')
	// -------------------------------------------------------------
	pair, err := g.findEligibleMarriedPair(ctx, clanID)
	if err != nil {
		res.Eligible = false
		res.Reason = fmt.Sprintf("checking married pairs: %v", err)
		return res, nil
	}
	if pair == nil {
		res.Eligible = false
		res.Reason = "no adult married pair with mutual disposition >= 80.0 found in clan"
		return res, nil
	}
	res.EligiblePair = pair

	res.Eligible = true
	res.Reason = "all dynastic reproduction invariants satisfied"
	return res, nil
}

// Trigger evaluates reproduction preconditions and, if satisfied, executes dynastic reproduction.
func (g *ReproductionGovernor) Trigger(ctx context.Context, clanID string) (*ReproductionResult, error) {
	checkRes, err := g.Evaluate(ctx, clanID)
	if err != nil {
		return nil, fmt.Errorf("evaluating reproduction invariants: %w", err)
	}
	if !checkRes.Eligible {
		return &ReproductionResult{
			Success:     false,
			CheckResult: *checkRes,
		}, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	pair := checkRes.EligiblePair
	housingZone := checkRes.UnclaimedHousingZone

	if clanID == "" {
		clanID = g.clanID
	}
	if clanID == "" && pair.ParentA != nil {
		clanID = pair.ParentA.ClanID
	}
	if clanID == "" {
		clanID = "pioneers"
	}

	// 1. Determine Child Identity
	childID, err := g.generateUniqueChildName(pair.ParentA.Name, pair.ParentB.Name)
	if err != nil {
		return nil, fmt.Errorf("generating child name: %w", err)
	}

	genA := pair.ParentA.Generation
	if genA <= 0 {
		genA = 1
	}
	genB := pair.ParentB.Generation
	if genB <= 0 {
		genB = 1
	}
	childGen := maxInt(genA, genB) + 1

	worldTick := int64(0)
	if g.worldTickProvider != nil {
		worldTick = g.worldTickProvider()
	} else {
		worldTick = time.Now().Unix()
	}

	// 2. Blend Personality Traits via Gaussian Perturbation
	childTraits := genetics.BlendAgentTraits(pair.ParentA.Traits, pair.ParentB.Traits, g.rng)

	// 3. Blend Voice Configuration
	childVoice := genetics.BlendVoiceConfig(pair.ParentA.Voice, pair.ParentB.Voice, childTraits, g.rng)

	// 4. Create Directory Structure
	childDataDir := g.resolver.AgentDataDir(childID)
	childConfigDir := filepath.Join(g.resolver.AgentsConfigDir(), childID)
	if err := os.MkdirAll(childDataDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating child data directory %s: %w", childDataDir, err)
	}
	if err := os.MkdirAll(childConfigDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating child config directory %s: %w", childConfigDir, err)
	}

	// 5. Inherit Skin Asset
	childSkinPath := g.resolver.AgentSkinPath(childID)
	skinCfg, err := genetics.InheritSkin(pair.ParentA, pair.ParentB, childSkinPath, childTraits, g.rng)
	if err != nil {
		return nil, fmt.Errorf("inheriting skin texture: %w", err)
	}

	// Also write a copy to {dataDir}/skin.png for convenient access
	destRootSkin := filepath.Join(childDataDir, "skin.png")
	if skinData, readErr := os.ReadFile(childSkinPath); readErr == nil {
		_ = os.WriteFile(destRootSkin, skinData, 0o644)
	}

	// 6. Write child identity.yaml
	childUUID := agent.OfflinePlayerUUID(childID)
	childCfg := &xdg.AgentIdentityConfig{
		Name:           childID,
		UUID:           childUUID,
		ClanID:         clanID,
		Profession:     "APPRENTICE",
		SecondaryHobby: "NONE",
		IsChild:        true,
		Generation:     childGen,
		Lineage: xdg.AgentLineageConfig{
			Generation: childGen,
			Parents:    []string{pair.ParentA.Name, pair.ParentB.Name},
			Mother:     pair.ParentA.Name,
			Father:     pair.ParentB.Name,
			BirthTick:  worldTick,
		},
		Traits: childTraits,
		Voice:  childVoice,
		Skin:   *skinCfg,
	}

	// Save to $XDG_CONFIG_HOME/civquest/agents/{child_id}/identity.yaml
	identityPath := filepath.Join(childConfigDir, "identity.yaml")
	if err := xdg.SaveAgentConfig(childCfg, identityPath); err != nil {
		return nil, fmt.Errorf("saving child identity.yaml at %s: %w", identityPath, err)
	}

	// Also save to $XDG_CONFIG_HOME/civquest/agents/{child_id}.yaml via standard resolver
	if err := g.resolver.SaveAgentConfig(childCfg); err != nil {
		return nil, fmt.Errorf("saving child config yaml: %w", err)
	}

	// 7. Claim Housing Zone in civic_registry.db
	civicDB, err := db.OpenCivicRegistryDBForClan(g.resolver, clanID)
	if err == nil {
		_ = claimResidentialZone(ctx, civicDB, housingZone.ZoneID, childID)
		_ = civicDB.Close()
	}

	// 8. Update Relational Kinship in Agent SQLite Databases
	g.recordKinshipRelationships(ctx, pair.ParentA.Name, pair.ParentB.Name, childID, childUUID, worldTick)

	// 9. Emit Entity Spawn with is_child: true
	entitySpawned := false
	var mcEntityID int32
	var offlineUUID string

	if g.spawner != nil {
		skinBytes, _ := os.ReadFile(childSkinPath)
		spawnReq := &civquestv1.SpawnAgentRequest{
			AgentId:      childID,
			DisplayName:  childID,
			ClanId:       clanID,
			IsChild:      true,
			SkinPngBytes: skinBytes,
		}
		if resp, spawnErr := g.spawner.SpawnAgentEntity(ctx, spawnReq); spawnErr == nil && resp != nil {
			entitySpawned = resp.GetSuccess()
			mcEntityID = resp.GetMinecraftEntityId()
			offlineUUID = resp.GetOfflineUuid()
		}
	}

	// 10. Spawn new child civquest-agent OS worker process via launcher/supervisor
	processStarted := false
	if g.launcher != nil {
		if err := g.launcher.StartAgent(childID); err == nil {
			processStarted = true
		}
	}

	return &ReproductionResult{
		Success:           true,
		ChildID:           childID,
		ChildConfig:       childCfg,
		ParentA:           pair.ParentA.Name,
		ParentB:           pair.ParentB.Name,
		Generation:        childGen,
		HousingZoneID:     housingZone.ZoneID,
		EntitySpawned:     entitySpawned,
		MinecraftEntityID: mcEntityID,
		OfflineUUID:       offlineUUID,
		ProcessStarted:    processStarted,
		CheckResult:       *checkRes,
	}, nil
}

// Tick evaluates reproduction and executes for eligible clans.
func (g *ReproductionGovernor) Tick(ctx context.Context) ([]*ReproductionResult, error) {
	clans := []string{g.clanID}
	if g.clanID == "" {
		clanStatus, err := domain.DetectClans(g.resolver)
		if err == nil && len(clanStatus.Clans) > 0 {
			clans = clanStatus.Clans
		} else {
			clans = []string{"pioneers"}
		}
	}

	var results []*ReproductionResult
	for _, c := range clans {
		res, err := g.Trigger(ctx, c)
		if err != nil {
			continue
		}
		if res != nil && res.Success {
			results = append(results, res)
		}
	}
	return results, nil
}

func (g *ReproductionGovernor) findEligibleMarriedPair(ctx context.Context, clanID string) (*MarriedPair, error) {
	status, err := domain.DetectAgents(g.resolver)
	if err != nil {
		return nil, err
	}

	var adultCandidates []*xdg.AgentIdentityConfig
	for _, summary := range status.ConfiguredAgents {
		if clanID != "" && !strings.EqualFold(summary.ClanID, clanID) {
			continue
		}
		if summary.Deceased {
			continue
		}

		cfg, loadErr := g.resolver.LoadAgentConfig(summary.Name)
		if loadErr != nil || cfg == nil {
			continue
		}
		if cfg.IsChild {
			continue
		}
		adultCandidates = append(adultCandidates, cfg)
	}

	if len(adultCandidates) < 2 {
		return nil, nil
	}

	// Pair deduplication map
	checked := make(map[string]bool)

	for _, pA := range adultCandidates {
		dbA, openErr := db.OpenAgentDBForAgent(g.resolver, pA.Name)
		if openErr != nil {
			continue
		}
		relsA, listErr := dbA.ListRelationships(ctx)
		_ = dbA.Close()
		if listErr != nil {
			continue
		}

		for _, relA := range relsA {
			if !strings.EqualFold(relA.KinshipRole, KinshipRoleSpouse) || relA.DispositionScore < MinSpouseDisposition {
				continue
			}

			// Find spouse config among adult candidates
			var pB *xdg.AgentIdentityConfig
			for _, cand := range adultCandidates {
				if strings.EqualFold(cand.Name, relA.EntityName) || strings.EqualFold(cand.UUID, relA.EntityUUID) {
					pB = cand
					break
				}
			}
			if pB == nil || strings.EqualFold(pA.Name, pB.Name) {
				continue
			}

			pairKey := canonicalPairKey(pA.Name, pB.Name)
			if checked[pairKey] {
				continue
			}
			checked[pairKey] = true

			// Check mutual relationship in B's database
			dbB, openBErr := db.OpenAgentDBForAgent(g.resolver, pB.Name)
			if openBErr != nil {
				continue
			}
			relsB, listBErr := dbB.ListRelationships(ctx)
			_ = dbB.Close()
			if listBErr != nil {
				continue
			}

			for _, relB := range relsB {
				if (strings.EqualFold(relB.EntityName, pA.Name) || strings.EqualFold(relB.EntityUUID, pA.UUID)) &&
					strings.EqualFold(relB.KinshipRole, KinshipRoleSpouse) && relB.DispositionScore >= MinSpouseDisposition {
					return &MarriedPair{
						ParentA:       pA,
						ParentB:       pB,
						DispositionAB: relA.DispositionScore,
						DispositionBA: relB.DispositionScore,
					}, nil
				}
			}
		}
	}

	return nil, nil
}

func (g *ReproductionGovernor) recordKinshipRelationships(ctx context.Context, parentAName, parentBName, childID, childUUID string, worldTick int64) {
	parentAUUID := agent.OfflinePlayerUUID(parentAName)
	parentBUUID := agent.OfflinePlayerUUID(parentBName)

	// Child records Parent A and Parent B
	if childDB, err := db.OpenAgentDBForAgent(g.resolver, childID); err == nil {
		_ = childDB.UpsertRelationship(ctx, db.Relationship{
			EntityUUID:          parentAUUID,
			EntityName:          parentAName,
			EntityType:          "AGENT",
			DispositionScore:    95.0,
			Familiarity:         1.0,
			KinshipRole:         KinshipRoleParent,
			LastInteractionTick: worldTick,
		})
		_ = childDB.UpsertRelationship(ctx, db.Relationship{
			EntityUUID:          parentBUUID,
			EntityName:          parentBName,
			EntityType:          "AGENT",
			DispositionScore:    95.0,
			Familiarity:         1.0,
			KinshipRole:         KinshipRoleParent,
			LastInteractionTick: worldTick,
		})
		_ = childDB.Close()
	}

	// Parent A records Child
	if dbA, err := db.OpenAgentDBForAgent(g.resolver, parentAName); err == nil {
		_ = dbA.UpsertRelationship(ctx, db.Relationship{
			EntityUUID:          childUUID,
			EntityName:          childID,
			EntityType:          "AGENT",
			DispositionScore:    95.0,
			Familiarity:         1.0,
			KinshipRole:         KinshipRoleChild,
			LastInteractionTick: worldTick,
		})
		_ = dbA.Close()
	}

	// Parent B records Child
	if dbB, err := db.OpenAgentDBForAgent(g.resolver, parentBName); err == nil {
		_ = dbB.UpsertRelationship(ctx, db.Relationship{
			EntityUUID:          childUUID,
			EntityName:          childID,
			EntityType:          "AGENT",
			DispositionScore:    95.0,
			Familiarity:         1.0,
			KinshipRole:         KinshipRoleChild,
			LastInteractionTick: worldTick,
		})
		_ = dbB.Close()
	}
}

func (g *ReproductionGovernor) generateUniqueChildName(parentA, parentB string) (string, error) {
	namePool := []string{
		"arthur", "bertha", "cristobal", "dolly", "edouard", "fay",
		"gonzalo", "hanna", "isaias", "josephine", "kyle", "leah",
		"marco", "nana", "omar", "paulette", "rene", "sally",
		"teddy", "vicky", "wilfred",
		"cora", "elias", "rowan", "silas", "lydia", "jonah", "talia", "ezra",
		"milo", "clara", "aspen", "finn", "nora", "gage", "hazel", "titus",
		"iris", "caleb", "wren", "jude", "selah", "tobias", "faye", "soren",
	}

	// Try name pool first
	perm := g.rng.Perm(len(namePool))
	for _, idx := range perm {
		candidate := namePool[idx]
		if _, err := g.resolver.CheckAgentNameUnique(candidate); err == nil {
			return candidate, nil
		}
	}

	// Syllable blending fallback
	prefixA := parentA
	if len(prefixA) > 3 {
		prefixA = prefixA[:3]
	}
	suffixB := parentB
	if len(suffixB) > 3 {
		suffixB = suffixB[len(suffixB)-3:]
	}

	for i := 1; i <= 100; i++ {
		candidate := strings.ToLower(fmt.Sprintf("%s%s%d", prefixA, suffixB, i))
		if _, err := g.resolver.CheckAgentNameUnique(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", errors.New("unable to generate unique child name")
}

func canonicalPairKey(a, b string) string {
	if strings.ToLower(a) < strings.ToLower(b) {
		return strings.ToLower(a) + ":" + strings.ToLower(b)
	}
	return strings.ToLower(b) + ":" + strings.ToLower(a)
}

func isFoodItem(itemID string) bool {
	if _, ok := system1.LookupFoodItem(itemID); ok {
		return true
	}
	lower := strings.ToLower(itemID)
	for _, term := range []string{"wheat", "bread", "stew", "soup", "pie", "carrot", "potato", "apple", "beef", "porkchop", "mutton", "chicken", "fish", "salmon", "cod", "berries"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func findUnclaimedResidentialZone(ctx context.Context, civicDB *db.CivicRegistryDB) (*db.FunctionalZone, error) {
	query := `
		SELECT zone_id, zone_type, min_x, min_y, min_z, max_x, max_y, max_z, owner_agent_id, name, created_tick
		FROM functional_zones
		WHERE (zone_type = 'RESIDENTIAL' OR zone_type = 'HOUSING')
		  AND (owner_agent_id = '' OR owner_agent_id IS NULL)
		ORDER BY created_tick ASC
		LIMIT 1;
	`
	var z db.FunctionalZone
	var owner sql.NullString
	err := civicDB.DB().QueryRowContext(ctx, query).Scan(
		&z.ZoneID, &z.ZoneType, &z.MinX, &z.MinY, &z.MinZ, &z.MaxX, &z.MaxY, &z.MaxZ,
		&owner, &z.Name, &z.CreatedTick,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying unclaimed residential zones: %w", err)
	}
	if owner.Valid {
		z.OwnerAgentID = owner.String
	}
	return &z, nil
}

func claimResidentialZone(ctx context.Context, civicDB *db.CivicRegistryDB, zoneID, childID string) error {
	query := `
		UPDATE functional_zones
		SET owner_agent_id = ?
		WHERE zone_id = ?;
	`
	_, err := civicDB.DB().ExecContext(ctx, query, childID, zoneID)
	return err
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
