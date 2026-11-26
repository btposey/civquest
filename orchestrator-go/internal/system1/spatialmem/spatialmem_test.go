package spatialmem

import (
	"math/rand"
	"testing"
)

func TestCellMapping(t *testing.T) {
	s := New(Config{})
	cases := []struct {
		x, z float64
		want Cell
	}{{0, 0, Cell{0, 0}}, {7.9, 8, Cell{0, 1}}, {-0.1, -8.1, Cell{-1, -2}}, {-8, 16, Cell{-1, 2}}}
	for _, c := range cases {
		if got := s.CellOf(c.x, c.z); got != c.want {
			t.Errorf("CellOf(%v,%v)=%v want %v", c.x, c.z, got, c.want)
		}
	}
	if got := s.CellAt(Pos{-1, 64, -9}); got != (Cell{-1, -2}) {
		t.Errorf("CellAt=%v", got)
	}
	if got := s.SectorOf(Cell{-1, 8}); got != (Sector{-1, 1}) {
		t.Errorf("SectorOf=%v", got)
	}
}

func TestImmutability(t *testing.T) {
	a := New(Config{})
	b := a.Visit(Cell{1, 1}, 10)
	if a.Len() != 0 || b.Len() != 1 {
		t.Fatalf("receiver mutated: %d %d", a.Len(), b.Len())
	}
	c := b.Visit(Cell{2, 2}, 11)
	if b.Len() != 1 || c.Len() != 2 {
		t.Fatal("visit aliasing")
	}
	d := c.MarkDepleted(Pos{1, 2, 3}, KindOre, 5).MarkExplored(Sector{0, 0})
	if len(c.ExploredSectors()) != 0 || c.Depleted(Pos{1, 2, 3}, KindOre, 5) {
		t.Fatal("marks leaked into prior snapshot")
	}
	if !d.Depleted(Pos{1, 2, 3}, KindOre, 5) || len(d.ExploredSectors()) != 1 {
		t.Fatal("marks missing")
	}
	e := d.ClearDepleted(Pos{1, 2, 3}, KindOre)
	if !d.Depleted(Pos{1, 2, 3}, KindOre, 5) || e.Depleted(Pos{1, 2, 3}, KindOre, 5) {
		t.Fatal("clear aliasing")
	}
}

func TestRevisitCostTable(t *testing.T) {
	s := New(Config{HalfLifeTicks: 100, ForageRegrowTicks: 24000})
	c := Cell{0, 0}
	if s.RevisitCost(c, 0) != 0 || s.Novelty(c, 0) != 1 {
		t.Fatal("unvisited cell should be free and fully novel")
	}
	s = s.Visit(c, 0)
	if got := s.RevisitCost(c, 0); got != 1 {
		t.Fatalf("cost after 1 visit = %v", got)
	}
	if got := s.RevisitCost(c, 100); got < 0.499 || got > 0.501 {
		t.Fatalf("cost after one half-life = %v", got)
	}
	s = s.Visit(c, 0)
	if got := s.RevisitCost(c, 0); got != 2 {
		t.Fatalf("cost after 2 visits = %v", got)
	}
	// Depleted marks add penalty; forage lapses, trunk persists.
	s = s.MarkDepleted(Pos{1, 64, 1}, KindTrunk, 0).MarkDepleted(Pos{2, 64, 2}, KindForage, 0)
	if got := s.RevisitCost(c, 0); got != 4 {
		t.Fatalf("cost with 2 marks = %v", got)
	}
	late := int64(DefaultConfig().ForageRegrowTicks) + 1
	if n := s.DepletedIn(c, late); n != 1 {
		t.Fatalf("live marks after regrow = %d", n)
	}
	if !s.Depleted(Pos{1, 64, 1}, KindTrunk, late) || s.Depleted(Pos{2, 64, 2}, KindForage, late) {
		t.Fatal("regrow semantics wrong")
	}
}

func TestExplored(t *testing.T) {
	s := New(Config{SectorCells: 2, ExploredCoverage: 0.5})
	sec := Sector{0, 0}
	if s.IsExplored(sec, 0) {
		t.Fatal("empty sector explored")
	}
	s = s.Visit(Cell{0, 0}, 0)
	if s.IsExplored(sec, 0) {
		t.Fatal("1/4 coverage should not be explored")
	}
	s = s.Visit(Cell{1, 0}, 0)
	if !s.IsExplored(sec, 0) {
		t.Fatal("2/4 coverage should be explored")
	}
	if s.IsExplored(sec, 1_000_000) {
		t.Fatal("coverage should fade with decay")
	}
	m := New(Config{}).MarkExplored(Sector{3, -1})
	if !m.IsExplored(Sector{3, -1}, 1<<40) {
		t.Fatal("explicit mark must persist")
	}
	if got, base := m.Novelty(Cell{24, -8}, 0), 1.0; got >= base {
		t.Fatalf("explored sector novelty %v", got)
	}
}

func TestPrune(t *testing.T) {
	s := New(Config{HalfLifeTicks: 10}).Visit(Cell{0, 0}, 0).Visit(Cell{1, 0}, 1000)
	p := s.Prune(1000)
	if p.Len() != 1 || s.Len() != 2 {
		t.Fatalf("prune: %d/%d", p.Len(), s.Len())
	}
}

// Property: cost never increases with time and never decreases with a visit.
func TestPropertyDecayAndCostOrdering(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for it := 0; it < 300; it++ {
		s := New(Config{HalfLifeTicks: float64(1 + r.Intn(500))})
		c := Cell{int32(r.Intn(5)), int32(r.Intn(5))}
		now := int64(0)
		for i := 0; i < 1+r.Intn(8); i++ {
			now += int64(r.Intn(300))
			before := s.RevisitCost(c, now)
			s = s.Visit(c, now)
			if after := s.RevisitCost(c, now); after < before+1-1e-9 {
				t.Fatalf("visit must add >=1: %v -> %v", before, after)
			}
		}
		prev := s.RevisitCost(c, now)
		for dt := int64(0); dt < 2000; dt += int64(1 + r.Intn(100)) {
			cur := s.RevisitCost(c, now+dt)
			if cur > prev+1e-12 || cur < 0 {
				t.Fatalf("cost not monotone non-increasing: %v -> %v", prev, cur)
			}
			prev = cur
			if n := s.Novelty(c, now+dt); n <= 0 || n > 1 {
				t.Fatalf("novelty out of range %v", n)
			}
		}
		// Ordering: a more recently/more often visited cell costs more.
		a, b := Cell{10, 10}, Cell{11, 10}
		s2 := s.Visit(a, now).Visit(a, now).Visit(b, now)
		if !(s2.RevisitCost(a, now) > s2.RevisitCost(b, now)) {
			t.Fatal("more visits must cost more")
		}
		if !(s2.Novelty(a, now) < s2.Novelty(b, now)) {
			t.Fatal("novelty must invert cost ordering")
		}
		s3 := New(Config{HalfLifeTicks: 100}).Visit(a, 0).Visit(b, 500)
		if !(s3.RevisitCost(b, 500) > s3.RevisitCost(a, 500)) {
			t.Fatal("recent visit must cost more than old")
		}
	}
}

// Property: same operations in the same order give identical results, and
// Marks/ExploredSectors ordering is stable.
func TestPropertyDeterminism(t *testing.T) {
	build := func(seed int64) Snapshot {
		r := rand.New(rand.NewSource(seed))
		s := New(Config{})
		for i := 0; i < 200; i++ {
			switch r.Intn(3) {
			case 0:
				s = s.Visit(Cell{int32(r.Intn(20) - 10), int32(r.Intn(20) - 10)}, int64(i*10))
			case 1:
				s = s.MarkDepleted(Pos{int32(r.Intn(50)), 64, int32(r.Intn(50))}, Kind(r.Intn(3)), int64(i))
			default:
				s = s.MarkExplored(Sector{int32(r.Intn(4)), int32(r.Intn(4))})
			}
		}
		return s
	}
	a, b := build(3), build(3)
	for x := int32(-10); x < 10; x++ {
		for z := int32(-10); z < 10; z++ {
			c := Cell{x, z}
			if a.RevisitCost(c, 5000) != b.RevisitCost(c, 5000) || a.Novelty(c, 5000) != b.Novelty(c, 5000) {
				t.Fatal("nondeterministic")
			}
		}
	}
	ma, mb := a.Marks(1), b.Marks(1)
	if len(ma) != len(mb) {
		t.Fatal("marks len")
	}
	for i := range ma {
		if ma[i] != mb[i] {
			t.Fatal("marks order")
		}
	}
}
