package smoke

import "github.com/btposey/civquest/orchestrator-go/internal/system1"

// consecrate selects a clan site at home: professions apply only once a settlement exists.
func consecrate(st *system1.AgentState) { st.SetCollectiveSettlementSite(0.5, 64, 0.5) }
