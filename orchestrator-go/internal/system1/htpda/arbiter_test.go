package htpda

import "testing"

func bidder(kind Kind, band Band, utility float64) *funcAct {
	return &funcAct{kind: kind, propose: func(World) Bid { return Bid{Band: band, Utility: utility, Reason: string(kind)} }}
}

func arb(t *testing.T, w World, prio PriorityTable, acts ...Activity) (Candidate, bool) {
	t.Helper()
	reg, err := NewRegistry(acts...)
	if err != nil {
		t.Fatal(err)
	}
	return Arbitrate(w, reg, prio)
}

func TestArbiterPrioritiesComeFromTable(t *testing.T) {
	acts := []Activity{bidder("forage", BandCommitted, 1), bidder("shelter", BandCommitted, 1)}
	w := fakeWorld{stage: "bootstrap"}
	prio := PriorityTable{
		"bootstrap": {"forage": 0.4, "shelter": 0.9},
		"colonize":  {"forage": 0.8, "shelter": 0.2},
	}
	if c, _ := arb(t, w, prio, acts...); c.Bid.Kind != "shelter" || c.Score != 0.9 {
		t.Fatalf("bootstrap winner = %+v", c)
	}
	w.stage = "colonize"
	if c, _ := arb(t, w, prio, acts...); c.Bid.Kind != "forage" {
		t.Fatalf("colonize winner = %+v", c)
	}
}

func TestArbiterBandDominatesScore(t *testing.T) {
	prio := PriorityTable{AnyStage: {"big": 100, "flee": 0.01}}
	c, ok := arb(t, fakeWorld{}, prio, bidder("big", BandCommitted, 1), bidder("flee", BandEmergency, 1))
	if !ok || c.Bid.Kind != "flee" {
		t.Fatalf("winner = %+v", c)
	}
}

func TestArbiterUtilityScalesAndTiesAreDeterministic(t *testing.T) {
	prio := PriorityTable{AnyStage: {"a": 1, "b": 1, "c": 1}}
	c, _ := arb(t, fakeWorld{}, prio, bidder("c", BandRoutine, 1), bidder("b", BandRoutine, 1), bidder("a", BandRoutine, 0.5))
	if c.Bid.Kind != "b" {
		t.Fatalf("tie-break winner = %+v", c)
	}
}

func TestArbiterDropsAbstainAndUnweighted(t *testing.T) {
	prio := PriorityTable{"s": {"zero": 0}, AnyStage: {"abstain": 1}}
	_, ok := arb(t, fakeWorld{stage: "s"}, prio,
		bidder("abstain", BandEmergency, 0), bidder("zero", BandEmergency, 1), bidder("unlisted", BandEmergency, 1))
	if ok {
		t.Fatal("expected no winner")
	}
}

func TestPriorityTableFallback(t *testing.T) {
	prio := PriorityTable{"s": {"a": 2}, AnyStage: {"a": 1, "b": 3}}
	if w, _ := prio.Weight("s", "a"); w != 2 {
		t.Fatalf("stage row: %v", w)
	}
	if w, _ := prio.Weight("s", "b"); w != 3 {
		t.Fatalf("fallback row: %v", w)
	}
	if _, ok := prio.Weight("s", "c"); ok {
		t.Fatal("missing kind found")
	}
}

func TestPreempts(t *testing.T) {
	c := Candidate{Bid: Bid{Band: BandCommitted}}
	if !Preempts(c, nil) || !Preempts(c, &Frame{Band: BandRoutine}) {
		t.Fatal("should preempt")
	}
	if Preempts(c, &Frame{Band: BandCommitted}) || Preempts(c, &Frame{Band: BandEmergency}) {
		t.Fatal("equal/higher band must not be preempted")
	}
}
