package genetics

import (
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestClampTrait(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{input: -0.5, expected: 0.0},
		{input: 0.0, expected: 0.0},
		{input: 0.54321, expected: 0.5432},
		{input: 1.0, expected: 1.0},
		{input: 1.5, expected: 1.0},
		{input: math.NaN(), expected: 0.5},
	}

	for _, tc := range tests {
		got := ClampTrait(tc.input)
		if got != tc.expected {
			t.Errorf("ClampTrait(%f) = %f; want %f", tc.input, got, tc.expected)
		}
	}
}

func TestBlendTrait_GaussianDistribution(t *testing.T) {
	traitA := 0.60
	traitB := 0.40
	expectedMean := (traitA + traitB) / 2.0 // 0.50
	expectedStdDev := DefaultMutationStdDev  // 0.08

	rng := rand.New(rand.NewSource(42))
	n := 2000
	var sum float64
	var sumSq float64
	samples := make([]float64, n)

	for i := 0; i < n; i++ {
		val := BlendTrait(traitA, traitB, rng)
		if val < 0.0 || val > 1.0 {
			t.Fatalf("BlendTrait produced out-of-bounds value: %f", val)
		}
		samples[i] = val
		sum += val
	}

	mean := sum / float64(n)
	for _, val := range samples {
		diff := val - mean
		sumSq += diff * diff
	}
	stdDev := math.Sqrt(sumSq / float64(n))

	// Verify empirical mean is within 2 standard errors of expected
	if math.Abs(mean-expectedMean) > 0.02 {
		t.Errorf("empirical mean = %f; want close to %f (diff %f > 0.02)", mean, expectedMean, math.Abs(mean-expectedMean))
	}

	// Verify empirical standard deviation is close to 0.08
	if math.Abs(stdDev-expectedStdDev) > 0.02 {
		t.Errorf("empirical stdDev = %f; want close to %f", stdDev, expectedStdDev)
	}
}

func TestBlendTrait_BoundaryClamping(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	// Near upper boundary
	for i := 0; i < 100; i++ {
		val := BlendTrait(0.99, 1.0, rng)
		if val > 1.0 || val < 0.0 {
			t.Fatalf("upper boundary violation: %f", val)
		}
	}

	// Near lower boundary
	for i := 0; i < 100; i++ {
		val := BlendTrait(0.0, 0.01, rng)
		if val < 0.0 || val > 1.0 {
			t.Fatalf("lower boundary violation: %f", val)
		}
	}
}

func TestBlendAgentTraits(t *testing.T) {
	parentA := xdg.AgentTraits{
		Bravery:         0.80,
		Industriousness: 0.90,
		Sociability:     0.30,
		Curiosity:       0.70,
		Aggressiveness:  0.20,
	}
	parentB := xdg.AgentTraits{
		Bravery:         0.40,
		Industriousness: 0.50,
		Sociability:     0.70,
		Curiosity:       0.50,
		Aggressiveness:  0.60,
	}

	rng := rand.New(rand.NewSource(999))
	child := BlendAgentTraits(parentA, parentB, rng)

	if err := child.Validate(); err != nil {
		t.Fatalf("child traits failed validation: %v", err)
	}

	// Check traits are reasonably blended near parental midpoint
	checkNear := func(name string, val, mid float64) {
		t.Helper()
		if math.Abs(val-mid) > 0.30 {
			t.Errorf("trait %s = %f was too far from expected mid %f", name, val, mid)
		}
	}

	checkNear("Bravery", child.Bravery, 0.60)
	checkNear("Industriousness", child.Industriousness, 0.70)
	checkNear("Sociability", child.Sociability, 0.50)
	checkNear("Curiosity", child.Curiosity, 0.60)
	checkNear("Aggressiveness", child.Aggressiveness, 0.40)
}

func TestBigFiveMappingAndBlending(t *testing.T) {
	original := xdg.AgentTraits{
		Bravery:         0.80,
		Industriousness: 0.75,
		Sociability:     0.60,
		Curiosity:       0.90,
		Aggressiveness:  0.25,
	}

	bf := AgentTraitsToBigFive(original)
	if bf.Openness != 0.90 {
		t.Errorf("bf.Openness = %f, want 0.90", bf.Openness)
	}
	if bf.Conscientiousness != 0.75 {
		t.Errorf("bf.Conscientiousness = %f, want 0.75", bf.Conscientiousness)
	}
	if bf.Extraversion != 0.60 {
		t.Errorf("bf.Extraversion = %f, want 0.60", bf.Extraversion)
	}
	if bf.Agreeableness != 0.75 {
		t.Errorf("bf.Agreeableness = %f, want 0.75", bf.Agreeableness)
	}
	if bf.Neuroticism != 0.20 {
		t.Errorf("bf.Neuroticism = %f, want 0.20", bf.Neuroticism)
	}

	restored := BigFiveToAgentTraits(bf)
	if math.Abs(restored.Bravery-original.Bravery) > 0.001 ||
		math.Abs(restored.Industriousness-original.Industriousness) > 0.001 ||
		math.Abs(restored.Sociability-original.Sociability) > 0.001 ||
		math.Abs(restored.Curiosity-original.Curiosity) > 0.001 ||
		math.Abs(restored.Aggressiveness-original.Aggressiveness) > 0.001 {
		t.Errorf("BigFive roundtrip mismatch: got %+v, want %+v", restored, original)
	}

	rng := rand.New(rand.NewSource(55))
	bfB := BigFiveTraits{
		Openness:          0.5,
		Conscientiousness: 0.5,
		Extraversion:      0.5,
		Agreeableness:     0.5,
		Neuroticism:       0.5,
	}
	childBF := BlendBigFiveTraits(bf, bfB, rng)
	if childBF.Openness < 0.0 || childBF.Openness > 1.0 {
		t.Errorf("childBF.Openness out of bounds: %f", childBF.Openness)
	}
}

func TestBlendTraitMap(t *testing.T) {
	mapA := map[string]float64{"Bravery": 0.8, "Discipline": 0.9, "Empathy": 0.7}
	mapB := map[string]float64{"Bravery": 0.4, "Discipline": 0.5, "Curiosity": 0.6}

	rng := rand.New(rand.NewSource(77))
	blended := BlendTraitMap(mapA, mapB, rng)

	for k, val := range blended {
		if val < 0.0 || val > 1.0 {
			t.Errorf("blended trait %s out of bounds: %f", k, val)
		}
	}

	if _, ok := blended["Bravery"]; !ok {
		t.Errorf("missing blended Bravery")
	}
	if _, ok := blended["Discipline"]; !ok {
		t.Errorf("missing blended Discipline")
	}
	if _, ok := blended["Empathy"]; !ok {
		t.Errorf("missing blended Empathy (inherited with baseline)")
	}
	if _, ok := blended["Curiosity"]; !ok {
		t.Errorf("missing blended Curiosity (inherited with baseline)")
	}
}

func TestBlendVoiceConfig(t *testing.T) {
	vA := xdg.AgentVoiceConfig{
		Cadence:      "clipped and brisk",
		Diction:      "earthy agrarian vernacular",
		Disposition:  "cheerful and welcoming",
		SpeechQuirks: []string{"nods frequently", "mentions the soil"},
	}
	vB := xdg.AgentVoiceConfig{
		Cadence:      "warm and unhurried",
		Diction:      "blunt artisan speech",
		Disposition:  "practical and observant",
		SpeechQuirks: []string{"mentions the anvil", "speaks in short sentences"},
	}

	traits := xdg.AgentTraits{Sociability: 0.8, Aggressiveness: 0.2, Curiosity: 0.6}
	rng := rand.New(rand.NewSource(101))

	childVoice := BlendVoiceConfig(vA, vB, traits, rng)
	if childVoice.Cadence == "" {
		t.Errorf("expected non-empty child cadence")
	}
	if len(childVoice.SpeechQuirks) == 0 {
		t.Errorf("expected inherited speech quirks")
	}
}

func TestInheritSkinModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))

	// Both default
	if got := InheritSkinModel("default", "default", rng); got != "default" {
		t.Errorf("InheritSkinModel(default, default) = %s; want default", got)
	}

	// Both slim
	if got := InheritSkinModel("slim", "alex", rng); got != "slim" {
		t.Errorf("InheritSkinModel(slim, alex) = %s; want slim", got)
	}

	// Mixed
	modelCounts := make(map[string]int)
	for i := 0; i < 100; i++ {
		model := InheritSkinModel("default", "slim", rng)
		modelCounts[model]++
	}
	if modelCounts["default"] == 0 || modelCounts["slim"] == 0 {
		t.Errorf("expected mixed model inheritance distribution, got %+v", modelCounts)
	}
}

func TestCompositeSkin_SynthesizedAndFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Synthesize two distinct parent skins
	skinAPath := filepath.Join(tmpDir, "parent_a.png")
	skinBPath := filepath.Join(tmpDir, "parent_b.png")
	childSkinPath := filepath.Join(tmpDir, "child_skin.png")

	traitsA := xdg.AgentTraits{Bravery: 0.8, Industriousness: 0.9, Curiosity: 0.2}
	traitsB := xdg.AgentTraits{Bravery: 0.2, Industriousness: 0.3, Curiosity: 0.9}

	rng := rand.New(rand.NewSource(888))
	if err := GenerateSynthesizedSkin(skinAPath, traitsA, rng); err != nil {
		t.Fatalf("failed generating skin A: %v", err)
	}
	if err := GenerateSynthesizedSkin(skinBPath, traitsB, rng); err != nil {
		t.Fatalf("failed generating skin B: %v", err)
	}

	// Verify parent skin files exist and are valid PNGs
	imgAFile, err := os.Open(skinAPath)
	if err != nil {
		t.Fatalf("opening skin A: %v", err)
	}
	imgA, err := png.Decode(imgAFile)
	_ = imgAFile.Close()
	if err != nil {
		t.Fatalf("decoding skin A: %v", err)
	}
	if imgA.Bounds().Dx() != 64 || imgA.Bounds().Dy() != 64 {
		t.Fatalf("skin A dimensions = %dx%d, want 64x64", imgA.Bounds().Dx(), imgA.Bounds().Dy())
	}

	// 2. Composite skins into child skin
	childTraits := BlendAgentTraits(traitsA, traitsB, rng)
	if err := CompositeSkin(skinAPath, skinBPath, childSkinPath, childTraits, rng); err != nil {
		t.Fatalf("CompositeSkin failed: %v", err)
	}

	// Verify child skin exists and is 64x64 RGBA
	childFile, err := os.Open(childSkinPath)
	if err != nil {
		t.Fatalf("opening child skin: %v", err)
	}
	defer childFile.Close()

	childImg, err := png.Decode(childFile)
	if err != nil {
		t.Fatalf("decoding child skin: %v", err)
	}
	if childImg.Bounds().Dx() != 64 || childImg.Bounds().Dy() != 64 {
		t.Errorf("child skin dimensions = %dx%d, want 64x64", childImg.Bounds().Dx(), childImg.Bounds().Dy())
	}

	// 3. Test InheritSkin end-to-end
	parentCfgA := &xdg.AgentIdentityConfig{
		Name: "Father",
		Skin: xdg.AgentSkinConfig{Model: "default", TexturePath: skinAPath},
	}
	parentCfgB := &xdg.AgentIdentityConfig{
		Name: "Mother",
		Skin: xdg.AgentSkinConfig{Model: "slim", TexturePath: skinBPath},
	}

	e2eChildSkinPath := filepath.Join(tmpDir, "e2e_child.png")
	skinCfg, err := InheritSkin(parentCfgA, parentCfgB, e2eChildSkinPath, childTraits, rng)
	if err != nil {
		t.Fatalf("InheritSkin failed: %v", err)
	}
	if skinCfg.TexturePath != e2eChildSkinPath {
		t.Errorf("skinCfg.TexturePath = %s, want %s", skinCfg.TexturePath, e2eChildSkinPath)
	}
	if skinCfg.Model != "default" && skinCfg.Model != "slim" {
		t.Errorf("invalid skin model: %s", skinCfg.Model)
	}
	if _, err := os.Stat(e2eChildSkinPath); err != nil {
		t.Errorf("child skin file was not created: %v", err)
	}
}

func TestCompositeSkin_MissingParentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	childSkinPath := filepath.Join(tmpDir, "child_fallback.png")
	traits := xdg.AgentTraits{Bravery: 0.5, Industriousness: 0.5, Sociability: 0.5, Curiosity: 0.5, Aggressiveness: 0.5}

	rng := rand.New(rand.NewSource(42))
	// Calling with non-existent parent paths should cleanly synthesize a valid skin
	if err := CompositeSkin("/nonexistent/a.png", "/nonexistent/b.png", childSkinPath, traits, rng); err != nil {
		t.Fatalf("CompositeSkin with missing parent files failed: %v", err)
	}

	fi, err := os.Stat(childSkinPath)
	if err != nil {
		t.Fatalf("child skin was not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("child skin is empty")
	}
}
