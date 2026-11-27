package db_test

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// cq-gey.3: the play test settled an icy waterside. Water beside ice counts for nothing, and
// plain wooded ground beats it.
func TestScoreSite_IcyWatersideLosesToPlainsAndForest(t *testing.T) {
	icy := db.ScoreSite(db.SiteFeatures{Water: 6, Ice: 8, Forest: 6, Plains: 4, Ore: true, Livestock: true})
	green := db.ScoreSite(db.SiteFeatures{Forest: 6, Plains: 10})
	if icy >= green {
		t.Errorf("icy waterside %.1f, plains and forest %.1f", icy, green)
	}
	wet := db.ScoreSite(db.SiteFeatures{Water: 4, Forest: 6, Plains: 10})
	if wet <= green {
		t.Errorf("water is still a very good thing away from ice: %.1f vs %.1f", wet, green)
	}
	for _, f := range []string{"WATER", "OPEN_PLAINS"} {
		if got := db.SiteFeatureOf(f, true); got != db.FeatureIce {
			t.Errorf("frozen %s counts as %s", f, got)
		}
	}
	if got := db.SiteFeatureOf("FOREST_CANOPY", true); got != "FOREST_CANOPY" {
		t.Errorf("trees stay trees in the cold: %s", got)
	}
}
