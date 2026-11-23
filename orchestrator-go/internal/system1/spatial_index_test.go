package system1

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestSpatialIndex_BucketPartitioningAndCRUD(t *testing.T) {
	si := NewSpatialIndex()

	// Coordinates across negative and positive 16x16x16 bucket boundaries
	p1 := db.AgentPOI{
		POIID:          "poi_workstation_-1_64_-1",
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              -0.5,
		Y:              64.0,
		Z:              -0.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	}
	p2 := db.AgentPOI{
		POIID:          "poi_workstation_16_64_16",
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:furnace",
		X:              16.5,
		Y:              64.0,
		Z:              16.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	}

	si.UpsertPOI(p1)
	si.UpsertPOI(p2)

	if gotBuckets := len(si.buckets); gotBuckets != 2 {
		t.Fatalf("expected 2 spatial buckets, got %d", gotBuckets)
	}
	if _, ok := si.buckets[[3]int32{-1, 4, -1}]; !ok {
		t.Errorf("expected bucket [-1, 4, -1] for (-0.5, 64, -0.5)")
	}
	if _, ok := si.buckets[[3]int32{1, 4, 1}]; !ok {
		t.Errorf("expected bucket [1, 4, 1] for (16.5, 64, 16.5)")
	}

	got1, ok := si.GetPOI(p1.POIID)
	if !ok || got1.BlockID != "minecraft:crafting_table" {
		t.Fatalf("GetPOI failed: ok=%v, got=%+v", ok, got1)
	}
	// Defensive copy check
	got1.BlockID = "mutated"
	got1Again, _ := si.GetPOI(p1.POIID)
	if got1Again.BlockID != "minecraft:crafting_table" {
		t.Fatalf("GetPOI did not return a defensive copy")
	}

	// Move p1 to a new bucket via UpsertPOI
	p1Moved := p1
	p1Moved.X = 32.5
	p1Moved.Z = 32.5
	si.UpsertPOI(p1Moved)
	if _, existsOld := si.buckets[[3]int32{-1, 4, -1}]; existsOld {
		t.Errorf("expected old bucket [-1, 4, -1] to be deleted after moving POI")
	}
	if _, existsNew := si.buckets[[3]int32{2, 4, 2}]; !existsNew {
		t.Errorf("expected new bucket [2, 4, 2] after moving POI")
	}

	all := si.AllPOIs()
	if len(all) != 2 {
		t.Fatalf("expected 2 POIs in AllPOIs, got %d", len(all))
	}

	si.RemovePOI(p1.POIID)
	if _, ok := si.GetPOI(p1.POIID); ok {
		t.Errorf("expected removed POI to be absent")
	}
	if _, exists := si.buckets[[3]int32{2, 4, 2}]; exists {
		t.Errorf("expected bucket [2, 4, 2] to be pruned on RemovePOI")
	}

	// LoadFromPOIs replaces previous contents
	si.LoadFromPOIs([]db.AgentPOI{p1})
	if len(si.AllPOIs()) != 1 {
		t.Fatalf("expected 1 POI after LoadFromPOIs, got %d", len(si.AllPOIs()))
	}
}

func TestSpatialIndex_FindNearestAndRadiusAndElevationFilter(t *testing.T) {
	si := NewSpatialIndex()

	pois := []db.AgentPOI{
		{
			POIID:          "poi_workstation_surface_far",
			Category:       POICategoryWorkstation,
			BlockID:        "minecraft:crafting_table",
			X:              20.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		},
		{
			POIID:          "poi_workstation_dugout_near",
			Category:       POICategoryWorkstation,
			BlockID:        "minecraft:crafting_table",
			X:              2.5,
			Y:              58.0,
			Z:              0.5,
			ElevationLayer: ElevationDugout,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		},
		{
			POIID:          "poi_workstation_inactive",
			Category:       POICategoryWorkstation,
			BlockID:        "minecraft:crafting_table",
			X:              1.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       false,
		},
		{
			POIID:          "poi_container_surface",
			Category:       POICategoryContainer,
			BlockID:        "minecraft:chest",
			X:              5.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		},
		{
			POIID:          "poi_workstation_furnace",
			Category:       POICategoryWorkstation,
			BlockID:        "minecraft:furnace",
			X:              4.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		},
	}
	si.LoadFromPOIs(pois)

	origin := &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}

	// Filter by elevation layer SURFACE + blockID crafting_table -> skips dugout & inactive, finds surface_far
	nearestSurface := si.FindNearestPOI(origin, POICategoryWorkstation, "minecraft:crafting_table", 32.0, ElevationSurface)
	if nearestSurface == nil || nearestSurface.POIID != "poi_workstation_surface_far" {
		t.Fatalf("expected poi_workstation_surface_far, got %+v", nearestSurface)
	}

	// Short blockID without namespace also matches
	nearestShort := si.FindNearestPOI(origin, POICategoryWorkstation, "furnace", 16.0, ElevationSurface)
	if nearestShort == nil || nearestShort.POIID != "poi_workstation_furnace" {
		t.Fatalf("expected poi_workstation_furnace via short blockID, got %+v", nearestShort)
	}

	// Empty elevation layer allows dugout (closer)
	nearestAnyLayer := si.FindNearestPOI(origin, POICategoryWorkstation, "crafting_table", 32.0, "")
	if nearestAnyLayer == nil || nearestAnyLayer.POIID != "poi_workstation_dugout_near" {
		t.Fatalf("expected poi_workstation_dugout_near when elevationLayer is empty, got %+v", nearestAnyLayer)
	}

	// MaxDist too small returns nil
	if tooFar := si.FindNearestPOI(origin, POICategoryWorkstation, "minecraft:crafting_table", 10.0, ElevationSurface); tooFar != nil {
		t.Fatalf("expected nil outside maxDist=10, got %+v", tooFar)
	}

	// Huge maxDist exercises populated-bucket scan path cleanly
	nearestHuge := si.FindNearestPOI(origin, POICategoryContainer, "", 100000.0, "")
	if nearestHuge == nil || nearestHuge.POIID != "poi_container_surface" {
		t.Fatalf("expected poi_container_surface with huge maxDist, got %+v", nearestHuge)
	}

	// FindPOIsInRadius returns active matching POIs sorted by distance ascending
	workstations := si.FindPOIsInRadius(origin, POICategoryWorkstation, 25.0)
	if len(workstations) != 3 {
		t.Fatalf("expected 3 active workstations within radius 25, got %d", len(workstations))
	}
	if workstations[0].POIID != "poi_workstation_furnace" ||
		workstations[1].POIID != "poi_workstation_dugout_near" ||
		workstations[2].POIID != "poi_workstation_surface_far" {
		t.Fatalf("unexpected FindPOIsInRadius order: %s, %s, %s",
			workstations[0].POIID, workstations[1].POIID, workstations[2].POIID)
	}

	allInRadius := si.FindPOIsInRadius(origin, "", 6.0)
	if len(allInRadius) != 2 {
		t.Fatalf("expected 2 active POIs within radius 6 (furnace at 4, chest at 5; dugout is ~6.32), got %d", len(allInRadius))
	}
}

func TestClassifyElevationLayer(t *testing.T) {
	cases := []struct {
		poiY, selfY float64
		want        string
	}{
		{64, 64, ElevationSurface},
		{67, 64, ElevationSurface},
		{61, 64, ElevationSurface},
		{70, 64, ElevationElevated},
		{85, 64, ElevationElevated},
		{60, 64, ElevationDugout},
		{50, 64, ElevationDugout},
		{48, 64, ElevationUnderground},
		{12, 64, ElevationUnderground},
	}
	for _, tc := range cases {
		if got := ClassifyElevationLayer(tc.poiY, tc.selfY); got != tc.want {
			t.Errorf("ClassifyElevationLayer(%v, %v) = %q, want %q", tc.poiY, tc.selfY, got, tc.want)
		}
	}
}

func TestCategorizeBlockID(t *testing.T) {
	cases := []struct {
		blockID string
		wantCat string
		wantOK  bool
	}{
		{"minecraft:crafting_table", POICategoryWorkstation, true},
		{"minecraft:furnace", POICategoryWorkstation, true},
		{"minecraft:blast_furnace", POICategoryWorkstation, true},
		{"minecraft:smoker", POICategoryWorkstation, true},
		{"minecraft:anvil", POICategoryWorkstation, true},
		{"minecraft:grindstone", POICategoryWorkstation, true},
		{"minecraft:brewing_stand", POICategoryWorkstation, true},
		{"minecraft:cartography_table", POICategoryWorkstation, true},
		{"minecraft:smithing_table", POICategoryWorkstation, true},
		{"minecraft:loom", POICategoryWorkstation, true},
		{"minecraft:fletching_table", POICategoryWorkstation, true},
		{"minecraft:stonecutter", POICategoryWorkstation, true},
		{"minecraft:chest", POICategoryContainer, true},
		{"minecraft:barrel", POICategoryContainer, true},
		{"minecraft:trapped_chest", POICategoryContainer, true},
		{"minecraft:red_bed", POICategoryHabitation, true},
		{"minecraft:white_bed", POICategoryHabitation, true},
		{"minecraft:campfire", POICategoryHabitation, true},
		{"minecraft:soul_campfire", POICategoryHabitation, true},
		{"minecraft:oak_log", POICategoryResource, true},
		{"minecraft:crimson_stem", POICategoryResource, true},
		{"minecraft:iron_ore", POICategoryResource, true},
		{"minecraft:deepslate_diamond_ore", POICategoryResource, true},
		{"minecraft:lava", POICategoryHazard, true},
		{"minecraft:fire", POICategoryHazard, true},
		{"minecraft:soul_fire", POICategoryHazard, true},
		{"minecraft:bedrock", "", false},
		{"minecraft:stone", "", false},
		{"minecraft:dirt", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		gotCat, gotOK := CategorizeBlockID(tc.blockID)
		if gotCat != tc.wantCat || gotOK != tc.wantOK {
			t.Errorf("CategorizeBlockID(%q) = (%q, %v), want (%q, %v)", tc.blockID, gotCat, gotOK, tc.wantCat, tc.wantOK)
		}
	}
}

func TestSpatialIndex_IngestSensoryBlocks(t *testing.T) {
	si := NewSpatialIndex()

	frame1 := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 12.0, Y: 64.0, Z: 10.0},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}, BlockId: "minecraft:crafting_table"},
			{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10}, BlockId: "minecraft:chest"}, // blocks +X side of crafting_table
			{Pos: &civquestv1.BlockPosProto{X: 10, Y: 56, Z: 10}, BlockId: "minecraft:furnace"},
			{Pos: &civquestv1.BlockPosProto{X: 15, Y: 64, Z: 15}, BlockId: "minecraft:stone"}, // non-POI
		},
	}

	discovered := si.IngestSensoryBlocks(frame1, 64.0)
	if len(discovered) != 3 {
		t.Fatalf("expected 3 newly discovered POIs, got %d", len(discovered))
	}

	table, ok := si.GetPOI("poi_workstation_10_64_10")
	if !ok {
		t.Fatalf("expected poi_workstation_10_64_10 in SpatialIndex")
	}
	if table.ElevationLayer != ElevationSurface || table.DiscoveredTick != 100 || !table.IsActive {
		t.Fatalf("unexpected table POI metadata: %+v", table)
	}
	// Since (11,64,10) is occupied by chest, access pos must NOT be (11.5, 64, 10.5)
	if table.AccessX == 11.5 && table.AccessZ == 10.5 {
		t.Errorf("expected access position to avoid occupied neighbor (11.5, 64, 10.5)")
	}

	furnace, ok := si.GetPOI("poi_workstation_10_56_10")
	if !ok || furnace.ElevationLayer != ElevationDugout {
		t.Fatalf("expected dugout furnace POI, got ok=%v, %+v", ok, furnace)
	}

	// Next 5Hz frame with unchanged blocks should not re-emit unchanged POIs for DB spam
	frame2 := &civquestv1.TelemetryFrame{
		WorldTick:         104,
		Status:            frame1.Status,
		InterestingBlocks: frame1.InterestingBlocks,
	}
	reemitted := si.IngestSensoryBlocks(frame2, 64.0)
	if len(reemitted) != 0 {
		t.Fatalf("expected 0 updated POIs on immediate repeat frame, got %d", len(reemitted))
	}
	tableAfter, _ := si.GetPOI("poi_workstation_10_64_10")
	if tableAfter.LastVerifiedTick != 104 || tableAfter.DiscoveredTick != 100 {
		t.Errorf("expected LastVerifiedTick=104 and DiscoveredTick=100, got %+v", tableAfter)
	}

	// Frame >= 100 ticks later triggers periodic verification update
	frame3 := &civquestv1.TelemetryFrame{
		WorldTick:         204,
		Status:            frame1.Status,
		InterestingBlocks: frame1.InterestingBlocks[:1],
	}
	verified := si.IngestSensoryBlocks(frame3, 64.0)
	if len(verified) != 1 || verified[0].POIID != "poi_workstation_10_64_10" {
		t.Fatalf("expected periodic verification update after 100 ticks, got %+v", verified)
	}
}

func TestAgentState_SpatialIndexInitializedAndThreadSafe(t *testing.T) {
	state := NewAgentState()
	si := state.SpatialIndex()
	if si == nil {
		t.Fatalf("expected NewAgentState().SpatialIndex() to be non-nil")
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				si.UpsertPOI(db.AgentPOI{
					POIID:          fmt.Sprintf("poi_resource_%d_%d", id, i),
					Category:       POICategoryResource,
					BlockID:        "minecraft:oak_log",
					X:              float64(id*10 + i),
					Y:              64.0,
					Z:              float64(i),
					ElevationLayer: ElevationSurface,
					SafetyScore:    1.0,
					Reliability:    1.0,
					IsActive:       true,
				})
				_ = si.FindNearestPOI(&civquestv1.Vec3{X: float64(id * 10), Y: 64.0, Z: 0}, POICategoryResource, "", 32.0, ElevationSurface)
			}
		}(g)
	}
	wg.Wait()

	if len(si.AllPOIs()) != 400 {
		t.Fatalf("expected 400 POIs after concurrent upserts, got %d", len(si.AllPOIs()))
	}
}

func TestSpatialIndex_SubPointOneMillisecondLookup(t *testing.T) {
	si := NewSpatialIndex()
	// Populate 10,000 POIs across a 500x500 area
	for i := 0; i < 10000; i++ {
		x := float64((i % 100) * 5)
		z := float64((i / 100) * 5)
		si.UpsertPOI(db.AgentPOI{
			POIID:          fmt.Sprintf("poi_resource_%d", i),
			Category:       POICategoryResource,
			BlockID:        "minecraft:oak_log",
			X:              x + 0.5,
			Y:              64.0,
			Z:              z + 0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		})
	}
	// Place one workstation nearby
	si.UpsertPOI(db.AgentPOI{
		POIID:          "poi_workstation_target",
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              252.5,
		Y:              64.0,
		Z:              252.5,
		ElevationLayer: ElevationSurface,
		SafetyScore:    1.0,
		Reliability:    1.0,
		IsActive:       true,
	})

	queryPos := &civquestv1.Vec3{X: 250.0, Y: 64.0, Z: 250.0}
	const iterations = 1000
	start := time.Now()
	for i := 0; i < iterations; i++ {
		got := si.FindNearestPOI(queryPos, POICategoryWorkstation, "minecraft:crafting_table", 32.0, ElevationSurface)
		if got == nil || got.POIID != "poi_workstation_target" {
			t.Fatalf("unexpected lookup result: %+v", got)
		}
	}
	elapsed := time.Since(start)
	avgNs := float64(elapsed.Nanoseconds()) / float64(iterations)
	avgMs := avgNs / 1e6
	t.Logf("10,000-POI FindNearestPOI avg: %.5f ms (%.0f ns)", avgMs, avgNs)
	if avgMs >= 0.1 {
		t.Fatalf("expected FindNearestPOI average latency < 0.1 ms, got %.5f ms", avgMs)
	}
}

func BenchmarkSpatialIndex_FindNearestPOI(b *testing.B) {
	si := NewSpatialIndex()
	for i := 0; i < 10000; i++ {
		x := float64((i % 100) * 5)
		z := float64((i / 100) * 5)
		si.UpsertPOI(db.AgentPOI{
			POIID:          fmt.Sprintf("poi_resource_%d", i),
			Category:       POICategoryResource,
			BlockID:        "minecraft:oak_log",
			X:              x + 0.5,
			Y:              64.0,
			Z:              z + 0.5,
			ElevationLayer: ElevationSurface,
			SafetyScore:    1.0,
			Reliability:    1.0,
			IsActive:       true,
		})
	}
	queryPos := &civquestv1.Vec3{X: 250.0, Y: 64.0, Z: 250.0}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = si.FindNearestPOI(queryPos, POICategoryResource, "minecraft:oak_log", 32.0, ElevationSurface)
	}
}

func TestSpatialIndex_FamilySuffixMatchAndReliabilityDecay(t *testing.T) {
	si := NewSpatialIndex()
	si.LoadFromPOIs([]db.AgentPOI{
		{
			POIID:          FormatPOIID(POICategoryResource, 10, 64, 0),
			Category:       POICategoryResource,
			BlockID:        "minecraft:iron_ore",
			X:              10.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			Reliability:    1.0,
			IsActive:       true,
		},
		{
			POIID:          FormatPOIID(POICategoryResource, 20, 64, 0),
			Category:       POICategoryResource,
			BlockID:        "minecraft:oak_log",
			X:              20.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			Reliability:    1.0,
			IsActive:       true,
		},
		{
			POIID:          FormatPOIID(POICategoryHabitation, 15, 64, 0),
			Category:       POICategoryHabitation,
			BlockID:        "minecraft:red_bed",
			X:              15.5,
			Y:              64.0,
			Z:              0.5,
			ElevationLayer: ElevationSurface,
			Reliability:    1.0,
			IsActive:       true,
		},
	})

	origin := &civquestv1.Vec3{X: 0, Y: 64, Z: 0}
	logPOI := si.FindNearestPOI(origin, POICategoryResource, "log", 64.0, ElevationSurface)
	if logPOI == nil || logPOI.BlockID != "minecraft:oak_log" {
		t.Fatalf("expected 'log' query to match oak_log and skip iron_ore, got %+v", logPOI)
	}
	bedPOI := si.FindNearestPOI(origin, POICategoryHabitation, "bed", 64.0, ElevationSurface)
	if bedPOI == nil || bedPOI.BlockID != "minecraft:red_bed" {
		t.Fatalf("expected 'bed' query to match red_bed, got %+v", bedPOI)
	}

	si.DecayPOIReliability(logPOI.POIID, 0.6)
	decayed, ok := si.GetPOI(logPOI.POIID)
	if !ok || decayed.Reliability < 0.39 || decayed.Reliability > 0.41 {
		t.Fatalf("expected reliability ~0.4 after 0.6 decay, got %+v", decayed)
	}
	si.DecayPOIReliability(logPOI.POIID, 1.0)
	clamped, _ := si.GetPOI(logPOI.POIID)
	if clamped.Reliability != 0.0 {
		t.Fatalf("expected reliability clamped to 0.0, got %f", clamped.Reliability)
	}
}

func TestSpatialIndex_FindPOIAtBlockPosAndTargetPos(t *testing.T) {
	si := NewSpatialIndex()
	poi := db.AgentPOI{
		POIID:          "poi_workstation_10_64_10",
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              10.5,
		Y:              64.0,
		Z:              10.5,
		AccessX:        11.5,
		AccessY:        64.0,
		AccessZ:        10.5,
		ElevationLayer: ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	si.UpsertPOI(poi)

	// Exact block pos lookup
	found := si.FindPOIAtBlockPos(10, 64, 10)
	if found == nil || found.POIID != poi.POIID {
		t.Fatalf("expected to find POI at (10,64,10), got %+v", found)
	}
	if notFound := si.FindPOIAtBlockPos(11, 64, 10); notFound != nil {
		t.Fatalf("expected nil for empty block pos, got %+v", notFound)
	}

	// Target pos matching access coordinates
	byAccess := si.FindPOIByTargetPos(&civquestv1.Vec3{X: 11.5, Y: 64.0, Z: 10.5}, 1.0)
	if byAccess == nil || byAccess.POIID != poi.POIID {
		t.Fatalf("expected to find POI by access target, got %+v", byAccess)
	}

	// Target pos matching block center coordinates
	byCenter := si.FindPOIByTargetPos(&civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5}, 1.0)
	if byCenter == nil || byCenter.POIID != poi.POIID {
		t.Fatalf("expected to find POI by block center target, got %+v", byCenter)
	}

	// Distant target
	if distant := si.FindPOIByTargetPos(&civquestv1.Vec3{X: 100.0, Y: 64.0, Z: 100.0}, 1.0); distant != nil {
		t.Fatalf("expected nil for distant target, got %+v", distant)
	}
}

func TestSpatialIndex_VerifyCloseRangePOIs(t *testing.T) {
	si := NewSpatialIndex()

	poi1 := db.AgentPOI{
		POIID:          "poi_workstation_10_64_10",
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:crafting_table",
		X:              10.5,
		Y:              64.0,
		Z:              10.5,
		ElevationLayer: ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	poi2 := db.AgentPOI{
		POIID:          "poi_workstation_11_64_10",
		Category:       POICategoryWorkstation,
		BlockID:        "minecraft:furnace",
		X:              11.5,
		Y:              64.0,
		Z:              10.5,
		ElevationLayer: ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}
	distantPOI := db.AgentPOI{
		POIID:          "poi_habitation_30_64_30",
		Category:       POICategoryHabitation,
		BlockID:        "minecraft:red_bed",
		X:              30.5,
		Y:              64.0,
		Z:              30.5,
		ElevationLayer: ElevationSurface,
		Reliability:    1.0,
		IsActive:       true,
	}

	si.UpsertPOI(poi1)
	si.UpsertPOI(poi2)
	si.UpsertPOI(distantPOI)

	// Agent is at (10.0, 64.0, 10.0), close to poi1 (dist ~0.7m) and poi2 (dist ~1.6m), far from distantPOI (>25m).
	pos := &civquestv1.Vec3{X: 10.0, Y: 64.0, Z: 10.0}

	// Telemetry interesting blocks only contains poi1 (crafting table). poi2 (furnace) is missing!
	visible := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}, BlockId: "minecraft:crafting_table"},
	}

	pruned := si.VerifyCloseRangePOIs(pos, visible, 3.5)
	if len(pruned) != 1 || pruned[0] != poi2.POIID {
		t.Fatalf("expected pruned to contain exactly poi2 (%s), got %+v", poi2.POIID, pruned)
	}

	if _, ok := si.GetPOI(poi2.POIID); ok {
		t.Fatalf("expected poi2 to be removed from SpatialIndex")
	}
	if _, ok := si.GetPOI(poi1.POIID); !ok {
		t.Fatalf("expected poi1 to remain in SpatialIndex")
	}
	if _, ok := si.GetPOI(distantPOI.POIID); !ok {
		t.Fatalf("expected distantPOI to remain untouched outside close-range radius")
	}

	// Now test category change: replace poi1 with a non-POI block (e.g. stone) at (10, 64, 10)
	visibleReplaced := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10}, BlockId: "minecraft:stone"},
	}
	prunedReplaced := si.VerifyCloseRangePOIs(pos, visibleReplaced, 3.5)
	if len(prunedReplaced) != 1 || prunedReplaced[0] != poi1.POIID {
		t.Fatalf("expected poi1 to be pruned due to category change, got %+v", prunedReplaced)
	}
	if _, ok := si.GetPOI(poi1.POIID); ok {
		t.Fatalf("expected poi1 to be removed from SpatialIndex after category change")
	}
}

func TestSpatialIndex_DecayPOIReliabilityReturn(t *testing.T) {
	si := NewSpatialIndex()
	poi := db.AgentPOI{
		POIID:       "poi_test_1",
		Category:    POICategoryResource,
		BlockID:     "minecraft:oak_log",
		X:           1.5,
		Y:           64.0,
		Z:           1.5,
		Reliability: 1.0,
		IsActive:    true,
	}
	si.UpsertPOI(poi)

	rel, found := si.DecayPOIReliability("poi_test_1", 0.25)
	if !found || rel < 0.74 || rel > 0.76 {
		t.Fatalf("expected found=true and rel=0.75, got found=%v, rel=%f", found, rel)
	}

	rel, found = si.DecayPOIReliability("poi_test_1", 0.7)
	if !found || rel < 0.04 || rel > 0.06 {
		t.Fatalf("expected found=true and rel=0.05, got found=%v, rel=%f", found, rel)
	}

	_, found = si.DecayPOIReliability("non_existent", 0.25)
	if found {
		t.Fatalf("expected found=false for non_existent POI")
	}
}
