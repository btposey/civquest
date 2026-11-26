package agent

// Settlement selection and memory (cq-6l3.14).

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const (
	// A clan selects a site that is outstanding on its own (clanSiteStrongScore), or one two
	// explorers have surveyed that scores above zero, i.e. whose resources outweigh its ridges
	// and blocked sight lines.
	clanSiteStrongScore    = 35.0
	clanSiteMinExplorers   = 2
	defaultSettlementClan  = "default"
	settlementStoreTimeout = 2 * time.Second
)

// clanSiteQualifies reports whether a surveyed region is good enough to settle.
func clanSiteQualifies(c db.SettlementCandidate) bool {
	return c.Score >= clanSiteStrongScore || (c.ExplorerCount >= clanSiteMinExplorers && c.Score > 0)
}

// joinClanSite runs at a rendezvous: the agent adopts the site its clan has selected, selecting
// the best surveyed region first if the clan has none and one qualifies.
func (r *AgentRuntime) joinClanSite(ctx context.Context, frame *civquestv1.TelemetryFrame, civicDB *db.CivicRegistryDB, clanID, agentID string, debugLog *DebugLogger) {
	if r.system1State.SettlementEstablished() {
		return
	}
	if clanID == "" {
		clanID = defaultSettlementClan
	}
	site, err := civicDB.GetClanSite(ctx, clanID)
	if err != nil {
		log.Printf("[WARN] agent %s could not read the clan site: %v", agentID, err)
		return
	}
	if site == nil {
		ranked, err := civicDB.RankCollectiveSettlementCandidates(ctx)
		if err != nil {
			log.Printf("[WARN] agent %s could not rank settlement sites: %v", agentID, err)
			return
		}
		for _, c := range ranked {
			if !clanSiteQualifies(c) {
				continue
			}
			standing, _, err := civicDB.ClaimClanSite(ctx, clanID, db.ClanSite{
				X: c.X, Y: c.Y, Z: c.Z, Score: c.Score, ChosenBy: agentID, ChosenTick: frame.WorldTick,
			})
			if err != nil {
				log.Printf("[WARN] agent %s could not claim the clan site: %v", agentID, err)
				return
			}
			site = &standing
			break
		}
	}
	if site == nil {
		return
	}
	r.system1State.SetCollectiveSettlementSite(site.X, site.Y, site.Z)
	if debugLog != nil {
		debugLog.Emit(frame.WorldTick, "COLLECTIVE_SETTLEMENT_SITE",
			fmt.Sprintf("Settling the clan site at (%.0f, %.0f, %.0f), scored %.1f, chosen by %s", site.X, site.Y, site.Z, site.Score, site.ChosenBy),
			map[string]any{"x": site.X, "y": site.Y, "z": site.Z, "score": site.Score, "chosen_by": site.ChosenBy})
	}
}

// loadSettlement restores the settlement remembered from an earlier run.
func (r *AgentRuntime) loadSettlement() {
	if r.agentDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), settlementStoreTimeout)
	defer cancel()
	c, err := r.agentDB.LoadSettlementClaim(ctx)
	if err != nil {
		log.Printf("[WARN] agent %s could not load its settlement: %v", r.agentID, err)
		return
	}
	if c == nil {
		return
	}
	st := system1.Settlement{Kind: c.Kind, Pos: [3]float64{c.X, c.Y, c.Z}}
	r.system1State.RestoreSettlement(st)
	r.savedSettlement = st
}

// persistSettlement writes the agent's settlement when it has changed. It is called from the
// frame pipeline only, which owns savedSettlement after construction.
func (r *AgentRuntime) persistSettlement() {
	st, ok := r.system1State.Settlement()
	if !ok || st == r.savedSettlement || r.agentDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), settlementStoreTimeout)
	defer cancel()
	if err := r.agentDB.SaveSettlementClaim(ctx, db.SettlementClaim{Kind: st.Kind, X: st.Pos[0], Y: st.Pos[1], Z: st.Pos[2]}); err != nil {
		log.Printf("[WARN] agent %s could not save its settlement: %v", r.agentID, err)
		return
	}
	r.savedSettlement = st
}
