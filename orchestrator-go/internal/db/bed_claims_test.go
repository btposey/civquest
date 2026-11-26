package db

import (
	"path/filepath"
	"testing"
)

func TestCivicRegistryDB_BedClaims(t *testing.T) {
	c, err := OpenCivicRegistryDB(filepath.Join(t.TempDir(), "civic_registry.db"), "pioneers")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if o, _ := c.BedOwner(1, 64, 2); o != "" {
		t.Fatalf("fresh bed owner = %q", o)
	}
	if o, err := c.ClaimBed(1, 64, 2, "able", 10); err != nil || o != "able" {
		t.Fatalf("first claim: owner=%q err=%v", o, err)
	}
	if o, _ := c.ClaimBed(1, 64, 2, "bert", 11); o != "able" {
		t.Fatalf("contended claim should report able, got %q", o)
	}
	if o, _ := c.ClaimBed(1, 64, 2, "able", 12); o != "able" {
		t.Fatalf("re-claim by owner = %q", o)
	}
	_ = c.ReleaseBed(1, 64, 2, "bert") // not the owner: no-op
	if o, _ := c.BedOwner(1, 64, 2); o != "able" {
		t.Fatalf("foreign release must not free the bed, owner=%q", o)
	}
	owned, _ := c.BedOwnedBy("able")
	if len(owned) != 1 {
		t.Fatalf("able owns %v", owned)
	}
	_ = c.ReleaseBed(1, 64, 2, "able")
	if o, _ := c.ClaimBed(1, 64, 2, "bert", 13); o != "bert" {
		t.Fatalf("after release bert should win, got %q", o)
	}
}
