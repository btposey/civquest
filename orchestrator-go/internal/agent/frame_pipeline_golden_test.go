package agent_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type goldenFrameExpectation struct {
	Name              string
	Frame             *civquestv1.TelemetryFrame
	WantStage         system1.Stage
	WantRoutinePrefix string
	WantImmediateGoal string
	WantIntents       []string
	AfterCheck        func(t *testing.T, rt *agent.AgentRuntime, agentDB *db.AgentDB)
}

func intentKindList(intents []*civquestv1.AgentIntent) []string {
	out := make([]string, 0, len(intents))
	for _, in := range intents {
		if in == nil || in.Action == nil {
			out = append(out, "nil")
			continue
		}
		switch a := in.Action.(type) {
		case *civquestv1.AgentIntent_Navigate:
			out = append(out, "Navigate")
		case *civquestv1.AgentIntent_InteractBlock:
			out = append(out, "InteractBlock:"+a.InteractBlock.InteractionType.String())
		case *civquestv1.AgentIntent_SurveyHorizon:
			out = append(out, "SurveyHorizon")
		case *civquestv1.AgentIntent_Attack:
			if a.Attack.FleeImmediately {
				out = append(out, "Attack:Flee")
			} else {
				out = append(out, "Attack:Engage")
			}
		case *civquestv1.AgentIntent_CraftItem:
			out = append(out, "CraftItem")
		case *civquestv1.AgentIntent_UseItem:
			out = append(out, "UseItem")
		default:
			out = append(out, "Other")
		}
	}
	return out
}

func makeGoldenSurveyReport(ox, oy, oz float64, tick int64, forestSectors, waterSectors int) *civquestv1.HorizonSurveyReport {
	sectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		bearing := float32(i * 10)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)
		sectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i),
			BearingDeg:  bearing,
			HitDistance: 64.0,
			FeatureType: "OPEN_PLAINS",
			HitBlockId:  "minecraft:air",
			HitX:        ox + dx*64.0,
			HitY:        oy,
			HitZ:        oz + dz*64.0,
			OpenHorizon: true,
		}
	}
	for i := 0; i < forestSectors && i < 36; i++ {
		bearing := float32(i * 10)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)
		sectors[i].FeatureType = "FOREST_CANOPY"
		sectors[i].HitBlockId = "minecraft:oak_leaves"
		sectors[i].HitDistance = 42.0
		sectors[i].HitX = ox + dx*42.0
		sectors[i].HitY = oy + 6.0
		sectors[i].HitZ = oz + dz*42.0
		sectors[i].ElevationDelta = 6.0
		sectors[i].OpenHorizon = false
	}
	for i := forestSectors; i < forestSectors+waterSectors && i < 36; i++ {
		bearing := float32(i * 10)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)
		sectors[i].FeatureType = "WATER"
		sectors[i].HitBlockId = "minecraft:water"
		sectors[i].HitDistance = 36.0
		sectors[i].HitX = ox + dx*36.0
		sectors[i].HitY = oy - 1.0
		sectors[i].HitZ = oz + dz*36.0
		sectors[i].ElevationDelta = -1.0
		sectors[i].OpenHorizon = false
	}
	return &civquestv1.HorizonSurveyReport{
		OriginX:       ox,
		OriginY:       oy,
		OriginZ:       oz,
		CompletedTick: tick,
		MaxRange:      64.0,
		Sectors:       sectors,
	}
}

func TestHandleTelemetryFrame_PipelineGoldenSequence(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "golden_aria"
	if err := res.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories: %v", err)
	}

	idCfg := xdg.DefaultAgentIdentityConfig(agentID)
	idCfg.ClanID = "pioneers"
	if err := res.SaveAgentConfig(idCfg); err != nil {
		t.Fatalf("SaveAgentConfig: %v", err)
	}

	agentDB, err := db.OpenAgentDB(res.AgentDataDir(agentID))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	defer agentDB.Close()

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:        agentID,
		ClanID:         "pioneers",
		IdentityConfig: idCfg,
		Resolver:       res,
		AgentDB:        agentDB,
		SocketPath:     filepath.Join(tmpDir, "golden_aria.sock"),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Stop()

	treeBlocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
	}
	treePOIID := "poi_resource_10_64_0"

	steps := []goldenFrameExpectation{
		{
			Name: "01_dawn_idle",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 100,
				TimeOfDay: 1000,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "GATHER_TIMBER: Harvest nearby tree logs to craft basic survival tools",
			WantIntents:       []string{},
		},
		{
			Name: "02_tree_in_frustum_starts_harvest_task",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick:         120,
				TimeOfDay:         1200,
				InterestingBlocks: treeBlocks,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
			WantStage:         system1.StageBootstrap,
			WantRoutinePrefix: string(system1.RoutineHarvestTree),
			WantImmediateGoal: "GATHER_TIMBER: Harvest committed tree trunk logs and collect dropped wood",
			WantIntents:       []string{"Navigate"},
			AfterCheck: func(t *testing.T, rt *agent.AgentRuntime, _ *db.AgentDB) {
				t.Helper()
				hasTask, kind, _, _ := rt.System1State().ActiveTaskInfo()
				if !hasTask || kind != "harvest_tree" {
					t.Fatalf("expected active harvest_tree task, got hasTask=%v kind=%q", hasTask, kind)
				}
				if _, ok := rt.System1State().SpatialIndex().GetPOI(treePOIID); !ok {
					t.Fatalf("expected %s registered in SpatialIndex", treePOIID)
				}
			},
		},
		{
			Name: "03_navigation_stuck_decays_poi_waits_cooldown",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick:         140,
				TimeOfDay:         1400,
				InterestingBlocks: treeBlocks,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
				},
			},
			WantStage:         system1.StageBootstrap,
			WantRoutinePrefix: string(system1.RoutineHarvestTree),
			WantImmediateGoal: "GATHER_TIMBER: Harvest committed tree trunk logs and collect dropped wood",
			WantIntents:       []string{},
			AfterCheck: func(t *testing.T, rt *agent.AgentRuntime, _ *db.AgentDB) {
				t.Helper()
				hasTask, kind, _, _ := rt.System1State().ActiveTaskInfo()
				if !hasTask || kind != "harvest_tree" {
					t.Fatalf("expected active harvest_tree task during stuck cooldown, got hasTask=%v kind=%q", hasTask, kind)
				}
				if got := rt.NavTargetPOIID(); got != "" {
					t.Fatalf("expected NavTargetPOIID cleared on NAVIGATION_STATE_STUCK, got %q", got)
				}
			},
		},
		{
			Name: "04_navigation_stuck_retry_after_80_ticks",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick:         205,
				TimeOfDay:         2050,
				InterestingBlocks: treeBlocks,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
				},
			},
			WantStage:         system1.StageBootstrap,
			WantRoutinePrefix: string(system1.RoutineHarvestTree),
			WantImmediateGoal: "GATHER_TIMBER: Harvest committed tree trunk logs and collect dropped wood",
			WantIntents:       []string{"Navigate"},
		},
		{
			Name: "05_unstuck_recovery_within_reach_harvests_log",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick:         225,
				TimeOfDay:         2250,
				InterestingBlocks: treeBlocks,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 7.0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
				},
			},
			WantStage:         system1.StageBootstrap,
			WantRoutinePrefix: string(system1.RoutineHarvestTree),
			WantImmediateGoal: "GATHER_TIMBER: Harvest committed tree trunk logs and collect dropped wood",
			WantIntents:       []string{"InteractBlock:HARVEST_BREAK"},
		},
		{
			Name: "06_harvest_completed_triggers_horizon_survey",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 250,
				TimeOfDay: 2500,
				Receipt: &civquestv1.IntentReceipt{
					Status: civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
				},
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 7.0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
					},
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "CRAFT_BASIC_TOOLS: Craft wooden axe, wooden sword, and campfire from gathered timber",
			WantIntents:       []string{"SurveyHorizon"},
		},
		{
			Name: "07_horizon_survey_complete_persists_36_sectors",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick:     310,
				TimeOfDay:     3100,
				HorizonSurvey: makeGoldenSurveyReport(7.0, 64.0, 0.0, 310, 4, 2),
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 7.0, Y: 64, Z: 0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
					},
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "CRAFT_BASIC_TOOLS: Craft wooden axe, wooden sword, and campfire from gathered timber",
			WantIntents:       []string{"Navigate"},
			AfterCheck: func(t *testing.T, rt *agent.AgentRuntime, agentDB *db.AgentDB) {
				t.Helper()
				rt.AwaitPOIPersistence()
				sectors, err := agentDB.GetRecentHorizonSectors(ctx, 100)
				if err != nil {
					t.Fatalf("GetRecentHorizonSectors: %v", err)
				}
				if len(sectors) != 36 {
					t.Fatalf("expected 36 persisted horizon_sectors, got %d", len(sectors))
				}
				if summary := rt.System1State().LatestSurveySummary(); !strings.Contains(summary, "forest canopy") {
					t.Fatalf("expected LatestSurveySummary to mention forest canopy, got %q", summary)
				}
				rawLog, err := os.ReadFile(res.AgentDebugJSONLPath(agentID))
				if err != nil {
					t.Fatalf("read debug jsonl: %v", err)
				}
				if !strings.Contains(string(rawLog), `"HORIZON_SURVEY_COMPLETE"`) {
					t.Fatalf("expected HORIZON_SURVEY_COMPLETE in debug log")
				}
			},
		},
		{
			Name: "08_expedition_pathing_no_churn",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 350,
				TimeOfDay: 3500,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 18.0, Y: 64, Z: 2.0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_PATHING,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
					},
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "CRAFT_BASIC_TOOLS: Craft wooden axe, wooden sword, and campfire from gathered timber",
			WantIntents:       []string{},
		},
		{
			Name: "09_dusk_transition_initiates_shelter",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 450,
				TimeOfDay: 12000,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 18.0, Y: 64, Z: 2.0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
						{ItemId: "minecraft:cobblestone", Count: 16},
						{ItemId: "minecraft:torch", Count: 2},
					},
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "BUILD_SHELTER: Construct protective night shelter and light source from carried blocks",
			WantIntents:       []string{"Navigate"},
		},
		{
			Name: "10_dusk_shelter_continuation",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 475,
				TimeOfDay: 12500,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 0.0, Y: 64, Z: 0.0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
						{ItemId: "minecraft:cobblestone", Count: 16},
						{ItemId: "minecraft:torch", Count: 2},
					},
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "BUILD_SHELTER: Construct protective night shelter and light source from carried blocks",
			WantIntents:       []string{"InteractBlock:PLACE_BLOCK"},
		},
		{
			Name: "11_night_hostile_preempts_to_emergency",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 520,
				TimeOfDay: 14000,
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:    9001,
						Uuid:        "00000000-0000-0000-0000-000000009001",
						EntityType:  "minecraft:zombie",
						DisplayName: "Zombie",
						Position:    &civquestv1.Vec3{X: 21.0, Y: 64, Z: 2.0},
						Health:      20,
						IsHostile:   true,
					},
				},
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 18.0, Y: 64, Z: 2.0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
						{ItemId: "minecraft:cobblestone", Count: 14},
						{ItemId: "minecraft:torch", Count: 2},
					},
				},
			},
			WantStage:         system1.StageEmergency,
			WantRoutinePrefix: string(system1.RoutineEmergency),
			WantImmediateGoal: "COMBAT_SURVIVAL: Defend against or evade nearby hostile threats",
			WantIntents:       []string{"Attack:Flee", "Navigate"},
		},
		{
			Name: "12_dawn_threat_cleared_recovery",
			Frame: &civquestv1.TelemetryFrame{
				WorldTick: 600,
				TimeOfDay: 1000,
				Status: &civquestv1.AgentStatus{
					AgentId:         agentID,
					Health:          20,
					MaxHealth:       20,
					FoodLevel:       20,
					Position:        &civquestv1.Vec3{X: 18.0, Y: 64, Z: 2.0},
					NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:oak_log", Count: 2},
						{ItemId: "minecraft:cobblestone", Count: 14},
						{ItemId: "minecraft:torch", Count: 2},
					},
				},
			},
			WantStage:         system1.StageBootstrap,
			WantImmediateGoal: "CRAFT_BASIC_TOOLS: Craft wooden axe, wooden sword, and campfire from gathered timber",
			WantIntents:       []string{"Navigate"},
		},
	}

	if len(steps) != 12 {
		t.Fatalf("expected 12 golden frames, got %d", len(steps))
	}

	for i, step := range steps {
		t.Run(step.Name, func(t *testing.T) {
			rt.HandleTelemetryFrame(step.Frame)
			gotIntents := intentKindList(rt.DrainIntents())

			if gotStage := rt.Stage(); gotStage != step.WantStage {
				t.Fatalf("frame %d (%s): Stage = %q, want %q (routine=%q)",
					i+1, step.Name, gotStage, step.WantStage, rt.Routine())
			}
			if step.WantRoutinePrefix != "" && !strings.HasPrefix(string(rt.Routine()), step.WantRoutinePrefix) {
				t.Fatalf("frame %d (%s): Routine = %q, want prefix %q",
					i+1, step.Name, rt.Routine(), step.WantRoutinePrefix)
			}

			imm, short, long := rt.Goals()
			if imm != step.WantImmediateGoal {
				t.Fatalf("frame %d (%s): ImmediateGoal = %q, want %q",
					i+1, step.Name, imm, step.WantImmediateGoal)
			}
			if short == "" || long == "" {
				t.Fatalf("frame %d (%s): expected non-empty short/long goals, got short=%q long=%q",
					i+1, step.Name, short, long)
			}

			if len(gotIntents) != len(step.WantIntents) {
				t.Fatalf("frame %d (%s): emitted intents = %v, want %v",
					i+1, step.Name, gotIntents, step.WantIntents)
			}
			for j := range gotIntents {
				if gotIntents[j] != step.WantIntents[j] {
					t.Fatalf("frame %d (%s): intent[%d] = %q, want %q (all=%v)",
						i+1, step.Name, j, gotIntents[j], step.WantIntents[j], gotIntents)
				}
			}

			if step.AfterCheck != nil {
				step.AfterCheck(t, rt, agentDB)
			}
		})
	}
}
