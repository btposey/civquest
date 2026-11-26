package smoke

import (
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// consecrate records a verified settlement at home: professions apply only once one exists.
func consecrate(st *system1.AgentState) {
	st.SpatialIndex().UpsertPOI(db.AgentPOI{POIID: "test-hearth", Category: system1.POICategorySettlement, X: 0.5, Y: 64, Z: 0.5, Reliability: 1})
}
