package htpda

// Candidate is a scored bid.
type Candidate struct {
	Bid   Bid
	Score float64 // PriorityTable weight * Bid.Utility
}

// better orders candidates by (Band, Score) descending, then Kind ascending
// so ties resolve deterministically.
func better(a, b Candidate) bool {
	if a.Bid.Band != b.Bid.Band {
		return a.Bid.Band > b.Bid.Band
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Bid.Kind < b.Bid.Kind
}

// Arbitrate collects Propose from every registered activity, drops abstaining
// or unweighted bids, and returns the best candidate. It contains no
// eligibility rules (those live in Propose) and no numeric priorities (those
// live in the PriorityTable).
func Arbitrate(w World, reg *Registry, prio PriorityTable) (Candidate, bool) {
	var best Candidate
	found := false
	for _, k := range reg.Kinds() {
		act, _ := reg.Get(k)
		bid := act.Propose(w)
		if bid.Utility <= 0 {
			continue
		}
		weight, ok := prio.Weight(w.Stage(), k)
		if !ok || weight <= 0 {
			continue
		}
		bid.Kind = k
		c := Candidate{Bid: bid, Score: weight * bid.Utility}
		if !found || better(c, best) {
			best, found = c, true
		}
	}
	return best, found
}

// Preempts reports whether candidate c should be pushed given the current top
// frame (nil = empty stack). Equal band never preempts: commitment is the default.
func Preempts(c Candidate, top *Frame) bool {
	return top == nil || c.Bid.Band > top.Band
}
