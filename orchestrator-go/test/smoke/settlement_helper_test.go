package smoke

import "github.com/btposey/civquest/orchestrator-go/internal/system1"

// consecrate makes the agent an established member of a settlement at home, where professions
// apply.
func consecrate(st *system1.AgentState) {
	st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	st.ObserveFarming(system1.FarmingNeeds{Shelter: true, Water: true, Bucket: true})
	st.AssignStrategy(system1.StrategyProfession, 0)
}
