package system1

// Professions come with an established place in a settlement (cq-6l3.12, cq-6l3.14, cq-507.7).
// A settlement is the site a clan has selected, or the homestead a lone agent has claimed; a
// campfire or bed on its own is not one. Which trade an agent follows depends on its strategy
// (strategy.go).

import (
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const (
	professionExplorer    = "EXPLORER"
	professionHomesteader = "HOMESTEADER"
	professionSettler     = "SETTLER"
)

// Settlement kinds, as stored in db.SettlementClaim.
const (
	SettlementHomestead = "HOMESTEAD"
	SettlementClanSite  = "CLAN_SITE"
)

// Settlement is the settlement an agent belongs to.
type Settlement struct {
	Kind string
	Pos  [3]float64
}

// Settlement returns the agent's settlement, if it has one.
func (s *AgentState) Settlement() (Settlement, bool) {
	if s == nil {
		return Settlement{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settlementLocked()
}

func (s *AgentState) settlementLocked() (Settlement, bool) {
	switch {
	case s.HomesteadClaimed:
		return Settlement{Kind: SettlementHomestead, Pos: s.HomesteadPos}, true
	case s.CollectiveSiteChosen:
		return Settlement{Kind: SettlementClanSite, Pos: s.CollectiveSitePos}, true
	}
	return Settlement{}, false
}

// RestoreSettlement reinstates a settlement remembered from an earlier run.
func (s *AgentState) RestoreSettlement(st Settlement) {
	if s == nil {
		return
	}
	switch st.Kind {
	case SettlementHomestead:
		s.applyHomesteadClaim(st.Pos)
	case SettlementClanSite:
		s.SetCollectiveSettlementSite(st.Pos[0], st.Pos[1], st.Pos[2])
	}
}

// SettlementEstablished reports whether the agent belongs to a settlement.
func (s *AgentState) SettlementEstablished() bool {
	_, ok := s.Settlement()
	return ok
}

// travelsTheWild reports whether an agent of this profession gathers across unmapped country,
// so it surveys rather than walking straight at an unverified distant sighting.
func travelsTheWild(profession string) bool {
	return isExplorerProfession(profession) || strings.EqualFold(strings.TrimSpace(profession), professionHomesteader) ||
		strings.EqualFold(strings.TrimSpace(profession), professionSettler)
}

// EffectiveIdentity returns the identity behaviour and speech must use, which follows the
// strategy: an explorer while exploring, a settler while making a home, and the configured
// profession once established. A lone agent is a homesteader on its own homestead. cfg is never
// modified.
func EffectiveIdentity(state *AgentState, cfg *xdg.AgentIdentityConfig) *xdg.AgentIdentityConfig {
	if cfg == nil {
		return nil
	}
	want := professionExplorer
	st, settled := state.Settlement()
	switch strategy := state.BaseStrategy(); {
	case strategy == StrategyExplore:
		if isExplorerProfession(cfg.Profession) {
			return cfg
		}
	case settled && st.Kind == SettlementHomestead:
		want = professionHomesteader
	case strategy == StrategySettle:
		want = professionSettler
	default:
		return cfg
	}
	cp := *cfg
	cp.Profession = want
	return &cp
}
