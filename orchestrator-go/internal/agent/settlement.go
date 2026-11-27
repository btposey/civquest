package agent

// Settlement selection and memory (cq-6l3.14).

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const (
	// A site worth voting for is outstanding on its own (clanSiteStrongScore), or one two
	// explorers have surveyed that reaches clanSiteSharedScore. Wooded flat ground alone falls
	// short of that: it takes water, ore or a cave, or livestock as well (db.ScoreSite).
	clanSiteStrongScore    = 55.0
	clanSiteSharedScore    = 35.0
	clanSiteMinExplorers   = 2
	defaultSettlementClan  = "default"
	settlementStoreTimeout = 2 * time.Second
	// DESIGN BUDGETS (cq-gey.3): no site this near a registered danger; votes this close are
	// for one site; a member not heard from this long is not counted.
	clanSiteDangerRadius = 48.0
	clanSiteVoteRadius   = 32.0
	clanMemberWindow     = int64(6000)
)

// SiteQuorum is how many members must endorse a site: a majority of the living, and never one
// member alone while the clan has others.
func SiteQuorum(living, clanSize int) int {
	need := living/2 + 1
	if clanSize >= 2 {
		need = max(need, 2)
	}
	return need
}

// siteEndangered reports a registered danger near the candidate.
func siteEndangered(c db.SettlementCandidate, dangers []db.DangerSpot) bool {
	for _, d := range dangers {
		if math.Hypot(d.X-c.X, d.Z-c.Z) <= clanSiteDangerRadius {
			return true
		}
	}
	return false
}

// clanSiteQualifies reports whether a surveyed region is good enough to settle.
func clanSiteQualifies(c db.SettlementCandidate) bool {
	return c.Score >= clanSiteStrongScore || (c.ExplorerCount >= clanSiteMinExplorers && c.Score >= clanSiteSharedScore)
}

// joinClanSite runs at a rendezvous: the agent adopts the site its clan has committed to. If
// there is none yet it endorses the best surveyed region that is worth settling and clear of
// dangers, and claims it for the clan once enough members agree.
func (r *AgentRuntime) joinClanSite(ctx context.Context, frame *civquestv1.TelemetryFrame, civicDB *db.CivicRegistryDB, clanID, agentID string, debugLog *DebugLogger) {
	if r.system1State.SettlementEstablished() {
		return
	}
	if clanID == "" {
		clanID = defaultSettlementClan
	}
	if err := civicDB.MemberHeartbeat(ctx, clanID, agentID, frame.WorldTick); err != nil {
		log.Printf("[WARN] agent %s could not report to its clan: %v", agentID, err)
		return
	}
	site, err := civicDB.GetClanSite(ctx, clanID)
	if err != nil {
		log.Printf("[WARN] agent %s could not read the clan site: %v", agentID, err)
		return
	}
	if site == nil {
		site = r.endorseSite(ctx, frame, civicDB, clanID, agentID, debugLog)
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

// persistSettlement writes the agent's settlement when it has changed.
func (r *AgentRuntime) persistSettlement() {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
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

// endorseSite votes for the best acceptable region and returns the clan's site if that vote
// completed the commitment.
func (r *AgentRuntime) endorseSite(ctx context.Context, frame *civquestv1.TelemetryFrame, civicDB *db.CivicRegistryDB, clanID, agentID string, debugLog *DebugLogger) *db.ClanSite {
	warn := func(what string, err error) *db.ClanSite {
		log.Printf("[WARN] agent %s could not %s: %v", agentID, what, err)
		return nil
	}
	ranked, err := civicDB.RankCollectiveSettlementCandidates(ctx)
	if err != nil {
		return warn("rank settlement sites", err)
	}
	dangers, err := civicDB.ListDangers(ctx, frame.WorldTick-system1.DangerMemoryTicks+1)
	if err != nil {
		return warn("read the clan's dangers", err)
	}
	for _, c := range ranked {
		if !clanSiteQualifies(c) || siteEndangered(c, dangers) {
			continue
		}
		choice := db.ClanSite{X: c.X, Y: c.Y, Z: c.Z, Score: c.Score, ChosenBy: agentID, ChosenTick: frame.WorldTick}
		if err := civicDB.VoteForSite(ctx, clanID, agentID, choice); err != nil {
			return warn("vote for a site", err)
		}
		supporters, living, err := civicDB.SiteSupport(ctx, clanID, c.X, c.Z, clanSiteVoteRadius, frame.WorldTick-clanMemberWindow)
		if err != nil {
			return warn("count site support", err)
		}
		need := SiteQuorum(living, max(living, r.system1State.KnownClanSize()))
		if debugLog != nil {
			debugLog.Emit(frame.WorldTick, "SETTLEMENT_VOTE",
				fmt.Sprintf("Endorsed (%.0f, %.0f, %.0f), scored %.1f: %d of %d living members agree, %d needed", c.X, c.Y, c.Z, c.Score, supporters, living, need),
				map[string]any{"x": c.X, "y": c.Y, "z": c.Z, "score": c.Score, "supporters": supporters, "living": living, "needed": need})
		}
		if supporters < need {
			return nil
		}
		standing, _, err := civicDB.ClaimClanSite(ctx, clanID, choice)
		if err != nil {
			return warn("claim the clan site", err)
		}
		return &standing
	}
	return nil
}
