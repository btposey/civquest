package system1

// Professions begin with a settlement (cq-6l3.12, extending ADR-027): until a verified
// settlement exists every agent explores and gathers, whatever profession it is configured for.

import "github.com/btposey/civquest/orchestrator-go/internal/xdg"

const professionExplorer = "EXPLORER"

// SettlementEstablished reports whether the agent knows of a verified settlement or hearth.
func (s *AgentState) SettlementEstablished() bool {
	if s == nil {
		return false
	}
	si := s.SpatialIndex()
	return si != nil && si.HasSettlementPOI(nil, 0)
}

// EffectiveIdentity returns the identity behaviour and speech must use: cfg itself once a
// settlement is established, before that a copy that is an explorer. cfg is never modified.
func EffectiveIdentity(state *AgentState, cfg *xdg.AgentIdentityConfig) *xdg.AgentIdentityConfig {
	if cfg == nil || state.SettlementEstablished() || isExplorerProfession(cfg.Profession) {
		return cfg
	}
	cp := *cfg
	cp.Profession = professionExplorer
	return &cp
}
