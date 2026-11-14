package genetics

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const (
	// DefaultMutationStdDev is the Gaussian mutation standard deviation per ADR-008:
	// Trait_child = clamp((Trait_A + Trait_B)/2 + N(0, 0.08^2), 0.0, 1.0).
	DefaultMutationStdDev = 0.08

	// MinTraitValue is the lower bound for personality traits.
	MinTraitValue = 0.0

	// MaxTraitValue is the upper bound for personality traits.
	MaxTraitValue = 1.0
)

// ClampTrait clamps val into the [0.0, 1.0] range and rounds to 4 decimal places.
func ClampTrait(val float64) float64 {
	if math.IsNaN(val) {
		return 0.5
	}
	if val < MinTraitValue {
		return MinTraitValue
	}
	if val > MaxTraitValue {
		return MaxTraitValue
	}
	return math.Round(val*10000) / 10000
}

// BlendTrait blends two parental trait values using Gaussian perturbation:
// Trait_child = clamp((Trait_A + Trait_B)/2 + N(0, 0.08^2), 0.0, 1.0).
func BlendTrait(traitA, traitB float64, rng *rand.Rand) float64 {
	mid := (traitA + traitB) / 2.0
	var perturbation float64
	if rng != nil {
		perturbation = rng.NormFloat64() * DefaultMutationStdDev
	} else {
		perturbation = rand.NormFloat64() * DefaultMutationStdDev
	}
	return ClampTrait(mid + perturbation)
}

// BigFiveTraits represents the 5 canonical psychological personality dimensions:
// Openness, Conscientiousness, Extraversion, Agreeableness, and Neuroticism.
type BigFiveTraits struct {
	Openness          float64 `json:"openness" yaml:"openness"`
	Conscientiousness float64 `json:"conscientiousness" yaml:"conscientiousness"`
	Extraversion      float64 `json:"extraversion" yaml:"extraversion"`
	Agreeableness     float64 `json:"agreeableness" yaml:"agreeableness"`
	Neuroticism       float64 `json:"neuroticism" yaml:"neuroticism"`
}

// AgentTraitsToBigFive projects the CivQuest 5 continuous dimensions into Big Five space:
// - Curiosity        <-> Openness
// - Industriousness  <-> Conscientiousness
// - Sociability      <-> Extraversion
// - Aggressiveness   <-> 1.0 - Agreeableness
// - Bravery          <-> 1.0 - Neuroticism
func AgentTraitsToBigFive(t xdg.AgentTraits) BigFiveTraits {
	return BigFiveTraits{
		Openness:          ClampTrait(t.Curiosity),
		Conscientiousness: ClampTrait(t.Industriousness),
		Extraversion:      ClampTrait(t.Sociability),
		Agreeableness:     ClampTrait(1.0 - t.Aggressiveness),
		Neuroticism:       ClampTrait(1.0 - t.Bravery),
	}
}

// BigFiveToAgentTraits projects Big Five traits back into CivQuest continuous dimensions:
// - Curiosity        := Openness
// - Industriousness  := Conscientiousness
// - Sociability      := Extraversion
// - Aggressiveness   := 1.0 - Agreeableness
// - Bravery          := 1.0 - Neuroticism
func BigFiveToAgentTraits(bf BigFiveTraits) xdg.AgentTraits {
	return xdg.AgentTraits{
		Bravery:         ClampTrait(1.0 - bf.Neuroticism),
		Industriousness: ClampTrait(bf.Conscientiousness),
		Sociability:     ClampTrait(bf.Extraversion),
		Curiosity:       ClampTrait(bf.Openness),
		Aggressiveness:  ClampTrait(1.0 - bf.Agreeableness),
	}
}

// BlendAgentTraits blends all 5 CivQuest personality traits using Gaussian perturbation.
func BlendAgentTraits(parentA, parentB xdg.AgentTraits, rng *rand.Rand) xdg.AgentTraits {
	return xdg.AgentTraits{
		Bravery:         BlendTrait(parentA.Bravery, parentB.Bravery, rng),
		Industriousness: BlendTrait(parentA.Industriousness, parentB.Industriousness, rng),
		Sociability:     BlendTrait(parentA.Sociability, parentB.Sociability, rng),
		Curiosity:       BlendTrait(parentA.Curiosity, parentB.Curiosity, rng),
		Aggressiveness:  BlendTrait(parentA.Aggressiveness, parentB.Aggressiveness, rng),
	}
}

// BlendBigFiveTraits blends all 5 Big Five personality traits using Gaussian perturbation.
func BlendBigFiveTraits(parentA, parentB BigFiveTraits, rng *rand.Rand) BigFiveTraits {
	return BigFiveTraits{
		Openness:          BlendTrait(parentA.Openness, parentB.Openness, rng),
		Conscientiousness: BlendTrait(parentA.Conscientiousness, parentB.Conscientiousness, rng),
		Extraversion:      BlendTrait(parentA.Extraversion, parentB.Extraversion, rng),
		Agreeableness:     BlendTrait(parentA.Agreeableness, parentB.Agreeableness, rng),
		Neuroticism:       BlendTrait(parentA.Neuroticism, parentB.Neuroticism, rng),
	}
}

// BlendTraitMap blends arbitrary named personality traits with Gaussian perturbation.
// If a trait exists in only one parent, it is blended with a neutral baseline 0.5.
func BlendTraitMap(traitsA, traitsB map[string]float64, rng *rand.Rand) map[string]float64 {
	keys := make(map[string]struct{})
	for k := range traitsA {
		keys[k] = struct{}{}
	}
	for k := range traitsB {
		keys[k] = struct{}{}
	}

	result := make(map[string]float64, len(keys))
	for k := range keys {
		valA, okA := traitsA[k]
		if !okA {
			valA = 0.5
		}
		valB, okB := traitsB[k]
		if !okB {
			valB = 0.5
		}
		result[k] = BlendTrait(valA, valB, rng)
	}
	return result
}

// BlendVoiceConfig synthesizes a child's speech profile by inheriting parental quirks
// and matching cadence to the blended traits.
func BlendVoiceConfig(parentA, parentB xdg.AgentVoiceConfig, childTraits xdg.AgentTraits, rng *rand.Rand) xdg.AgentVoiceConfig {
	quirkSet := make(map[string]struct{})
	for _, q := range parentA.SpeechQuirks {
		if q != "" {
			quirkSet[q] = struct{}{}
		}
	}
	for _, q := range parentB.SpeechQuirks {
		if q != "" {
			quirkSet[q] = struct{}{}
		}
	}

	var blendedQuirks []string
	for q := range quirkSet {
		blendedQuirks = append(blendedQuirks, q)
		if len(blendedQuirks) >= 3 {
			break
		}
	}

	pickParentStr := func(a, b string) string {
		if a == "" {
			return b
		}
		if b == "" {
			return a
		}
		if rng != nil && rng.Float64() < 0.5 {
			return a
		}
		return b
	}

	disposition := pickParentStr(parentA.Disposition, parentB.Disposition)
	if childTraits.Sociability > 0.70 && childTraits.Aggressiveness < 0.30 {
		disposition = "warm, inquisitive, and eager to learn"
	} else if childTraits.Curiosity > 0.75 {
		disposition = "wide-eyed, observant, and asking endless questions"
	}

	return xdg.AgentVoiceConfig{
		Cadence:      pickParentStr(parentA.Cadence, parentB.Cadence),
		Diction:      pickParentStr(parentA.Diction, parentB.Diction),
		Disposition:  disposition,
		SpeechQuirks: blendedQuirks,
	}
}

// CompositeSkinImages composites two 64x64 skin textures through genetic crossover and color interpolation.
func CompositeSkinImages(imgA, imgB image.Image, rng *rand.Rand) (image.Image, error) {
	boundsA := imgA.Bounds()
	boundsB := imgB.Bounds()

	width := 64
	height := 64
	if boundsA.Dx() > width {
		width = boundsA.Dx()
	}
	if boundsA.Dy() > height {
		height = boundsA.Dy()
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var rA, gA, bA, aA uint32
			var rB, gB, bB, aB uint32

			if x < boundsA.Max.X && y < boundsA.Max.Y {
				rA, gA, bA, aA = imgA.At(x, y).RGBA()
			}
			if x < boundsB.Max.X && y < boundsB.Max.Y {
				rB, gB, bB, aB = imgB.At(x, y).RGBA()
			}

			// 8-bit conversion
			r8A, g8A, b8A, a8A := uint8(rA>>8), uint8(gA>>8), uint8(bA>>8), uint8(aA>>8)
			r8B, g8B, b8B, a8B := uint8(rB>>8), uint8(gB>>8), uint8(bB>>8), uint8(aB>>8)

			if a8A == 0 && a8B == 0 {
				dst.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
				continue
			}
			if a8A == 0 {
				dst.SetRGBA(x, y, color.RGBA{R: r8B, G: g8B, B: b8B, A: a8B})
				continue
			}
			if a8B == 0 {
				dst.SetRGBA(x, y, color.RGBA{R: r8A, G: g8A, B: b8A, A: a8A})
				continue
			}

			// Decide genetic crossover: with 40% probability pick parent A, 40% parent B, 20% interpolated blend
			var choice float64
			if rng != nil {
				choice = rng.Float64()
			} else {
				choice = rand.Float64()
			}

			if choice < 0.40 {
				dst.SetRGBA(x, y, color.RGBA{R: r8A, G: g8A, B: b8A, A: a8A})
			} else if choice < 0.80 {
				dst.SetRGBA(x, y, color.RGBA{R: r8B, G: g8B, B: b8B, A: a8B})
			} else {
				// Interpolate color channels
				rMid := uint8((int(r8A) + int(r8B)) / 2)
				gMid := uint8((int(g8A) + int(g8B)) / 2)
				bMid := uint8((int(b8A) + int(b8B)) / 2)
				aMid := uint8((int(a8A) + int(a8B)) / 2)
				dst.SetRGBA(x, y, color.RGBA{R: rMid, G: gMid, B: bMid, A: aMid})
			}
		}
	}

	return dst, nil
}

// GenerateSynthesizedSkin creates a standard 64x64 Minecraft skin PNG with genetically blended colors.
func GenerateSynthesizedSkin(targetPath string, childTraits xdg.AgentTraits, rng *rand.Rand) error {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))

	// Base skin tone with slight genetic variance
	var noise int
	if rng != nil {
		noise = int(rng.NormFloat64() * 6)
	}
	rSkin := clampColor(210 + noise)
	gSkin := clampColor(161 + noise)
	bSkin := clampColor(125 + noise)
	skinColor := color.RGBA{R: rSkin, G: gSkin, B: bSkin, A: 255}

	// Tunic color varies by traits (e.g. higher curiosity -> greener/bluer, higher bravery -> redder/warmer)
	rTunic := clampColor(int(100 + childTraits.Bravery*80))
	gTunic := clampColor(int(70 + childTraits.Industriousness*80))
	bTunic := clampColor(int(40 + childTraits.Curiosity*120))
	tunicColor := color.RGBA{R: rTunic, G: gTunic, B: bTunic, A: 255}

	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if y < 16 {
				img.Set(x, y, skinColor)
			} else {
				img.Set(x, y, tunicColor)
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating skin directory: %w", err)
	}

	f, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("creating skin file %s: %w", targetPath, err)
	}
	defer f.Close()

	return png.Encode(f, img)
}

func clampColor(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// CompositeSkin loads two parental skin PNG files, composites them, and writes the child skin PNG.
// If one or both skin files are missing or unreadable, synthesizes a valid blended skin PNG.
func CompositeSkin(parentSkinAPath, parentSkinBPath, childSkinPath string, childTraits xdg.AgentTraits, rng *rand.Rand) error {
	var imgA, imgB image.Image

	if parentSkinAPath != "" {
		if fA, err := os.Open(parentSkinAPath); err == nil {
			imgA, _ = png.Decode(fA)
			_ = fA.Close()
		}
	}
	if parentSkinBPath != "" {
		if fB, err := os.Open(parentSkinBPath); err == nil {
			imgB, _ = png.Decode(fB)
			_ = fB.Close()
		}
	}

	// If neither image exists, synthesize directly
	if imgA == nil && imgB == nil {
		return GenerateSynthesizedSkin(childSkinPath, childTraits, rng)
	}

	// If only one exists, composite with a synthesized fallback or duplicate
	if imgA == nil {
		imgA = imgB
	} else if imgB == nil {
		imgB = imgA
	}

	composited, err := CompositeSkinImages(imgA, imgB, rng)
	if err != nil {
		return fmt.Errorf("compositing skin images: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(childSkinPath), 0o755); err != nil {
		return fmt.Errorf("creating skin output directory: %w", err)
	}

	f, err := os.Create(childSkinPath)
	if err != nil {
		return fmt.Errorf("creating child skin file: %w", err)
	}
	defer f.Close()

	return png.Encode(f, composited)
}

// InheritSkinModel decides the child's avatar skin model ("default" or "slim") from parents.
func InheritSkinModel(modelA, modelB string, rng *rand.Rand) string {
	mA := strings.ToLower(strings.TrimSpace(modelA))
	mB := strings.ToLower(strings.TrimSpace(modelB))
	if mA == "" || mA == "steve" {
		mA = "default"
	}
	if mB == "" || mB == "steve" {
		mB = "default"
	}
	if mA == "alex" {
		mA = "slim"
	}
	if mB == "alex" {
		mB = "slim"
	}

	if mA == mB {
		return mA
	}

	if rng != nil && rng.Float64() < 0.5 {
		return mA
	}
	return mB
}

// InheritSkin creates the child's skin asset on disk and returns its AgentSkinConfig metadata.
func InheritSkin(parentA, parentB *xdg.AgentIdentityConfig, childSkinPath string, childTraits xdg.AgentTraits, rng *rand.Rand) (*xdg.AgentSkinConfig, error) {
	modelA := "default"
	modelB := "default"
	var skinPathA, skinPathB string

	if parentA != nil {
		modelA = parentA.Skin.Model
		skinPathA = parentA.Skin.TexturePath
	}
	if parentB != nil {
		modelB = parentB.Skin.Model
		skinPathB = parentB.Skin.TexturePath
	}

	childModel := InheritSkinModel(modelA, modelB, rng)

	if err := CompositeSkin(skinPathA, skinPathB, childSkinPath, childTraits, rng); err != nil {
		return nil, fmt.Errorf("generating child skin: %w", err)
	}

	return &xdg.AgentSkinConfig{
		TexturePath: childSkinPath,
		Model:       childModel,
	}, nil
}
