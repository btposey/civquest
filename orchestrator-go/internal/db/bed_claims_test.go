package db

import (
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
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

// cq-390.5: claims are scoped by world; legacy unscoped rows are ignored; permadeath releases all.
func TestCivicRegistryDB_BedClaimsScopedByWorld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "civic_registry.db")
	open := func(world string) *CivicRegistryDB {
		c, err := OpenCivicRegistryDB(path, "pioneers")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		c.SetWorldID(world)
		return c
	}
	old := open("world-a")
	if _, err := old.DB().Exec(`CREATE TABLE bed_claims (x INTEGER, y INTEGER, z INTEGER, owner_agent_id TEXT, claimed_tick INTEGER);
		INSERT INTO bed_claims VALUES (9, 64, 9, 'ghost', 1)`); err != nil {
		t.Fatal(err)
	}
	_, _ = old.ClaimBed(1, 64, 2, "able", 1)
	_, _ = old.ClaimBed(2, 64, 2, "able", 1)

	cur := open("world-b")
	for _, p := range [][3]int32{{1, 64, 2}, {9, 64, 9}} {
		if o, err := cur.BedOwner(p[0], p[1], p[2]); err != nil || o != "" {
			t.Fatalf("world-b must ignore claim at %v: %q %v", p, o, err)
		}
	}
	if o, _ := cur.ClaimBed(1, 64, 2, "bert", 2); o != "bert" {
		t.Fatalf("claim in the new world: %q", o)
	}
	if owned, _ := cur.BedOwnedBy("able"); len(owned) != 0 {
		t.Fatalf("able owns nothing in world-b: %v", owned)
	}
	if err := old.ReleaseBedsOwnedBy("able"); err != nil {
		t.Fatal(err)
	}
	if owned, _ := old.BedOwnedBy("able"); len(owned) != 0 {
		t.Fatalf("claims survive release: %v", owned)
	}
	if o, _ := cur.BedOwner(1, 64, 2); o != "bert" {
		t.Fatalf("release in world-a touched world-b: %q", o)
	}
}

// cq-390.5: the resolver's server instance is the world the handle is scoped to.
func TestOpenCivicRegistryDBForClan_ScopesToServerID(t *testing.T) {
	r := xdg.NewResolver(xdg.WithCustomRoot(t.TempDir()), xdg.WithServerID("world-f01fc0af"))
	c, err := OpenCivicRegistryDBForClan(r, "pioneers")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.WorldID() != "world-f01fc0af" {
		t.Fatalf("world id = %q", c.WorldID())
	}
}
