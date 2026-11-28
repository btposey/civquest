package db

import (
	"path/filepath"
	"testing"
)

func TestClaimPlot_NeverOverlapsAnotherAgent(t *testing.T) {
	c, err := OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic.db"), "clan")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	near, wide, far := Plot{X: 0, Z: 0, Half: 4}, Plot{X: 10, Z: 0, Half: 7}, Plot{X: 30, Z: 0, Half: 4}
	claim := func(who string, want Plot, from ...Plot) {
		t.Helper()
		if got, ok, err := c.ClaimPlot(who, from, 1); err != nil || !ok || got != want {
			t.Fatalf("%s: got %v ok=%v err=%v, want %v", who, got, ok, err, want)
		}
	}
	claim("able", near, near, far)
	claim("bert", far, near, wide, far) // a wider footprint is kept clear too
	claim("able", near, far, near)      // the one held is kept
	if _, ok, err := c.ClaimPlot("carl", []Plot{near, far}, 1); ok || err != nil {
		t.Fatalf("carl got a taken plot (ok=%v err=%v)", ok, err)
	}
}
