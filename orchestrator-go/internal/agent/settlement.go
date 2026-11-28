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

// siteDanger returns the registered danger nearest the candidate, among those near enough to
// veto it, or nil.
func siteDanger(c db.SettlementCandidate, dangers []db.DangerSpot) *db.DangerSpot {
	var near *db.DangerSpot
	best := clanSiteDangerRadius
	for i, d := range dangers {
		if dist := math.Hypot(d.X-c.X, d.Z-c.Z); dist <= best {
			near, best = &dangers[i], dist
		}
	}
	return near
}

// Why a surveyed region is not endorsed (EventSettlementNoVote).
const (
	siteBelowScore      = "below_score"
	siteTooFewExplorers = "too_few_explorers"
	siteNearDanger      = "danger"
)

// clanSiteShortfall says why a surveyed region is not good enough to settle, or "" if it is.
func clanSiteShortfall(c db.SettlementCandidate) string {
	switch {
	case c.Score >= clanSiteStrongScore:
		return ""
	case c.Score < clanSiteSharedScore:
		return siteBelowScore
	case c.ExplorerCount < clanSiteMinExplorers:
		return siteTooFewExplorers
	}
	return ""
}

// siteRefusal says why a region is not to be endorsed, as recorded in the playtest events; nil
// means it is to be endorsed.
func siteRefusal(c db.SettlementCandidate, dangers []db.DangerSpot) map[string]any {
	why := clanSiteShortfall(c)
	var danger *db.DangerSpot
	if why == "" {
		if danger = siteDanger(c, dangers); danger == nil {
			return nil
		}
		why = siteNearDanger
	}
	out := map[string]any{"x": c.X, "y": c.Y, "z": c.Z, "score": c.Score, "explorers": c.ExplorerCount, "why": why}
	if danger != nil {
		out["danger"] = map[string]any{"kind": danger.Kind, "x": danger.X, "y": danger.Y, "z": danger.Z,
			"distance": math.Hypot(danger.X-c.X, danger.Z-c.Z), "seen_tick": danger.SeenTick, "reported_by": danger.ReportedBy}
	}
	return out
}

const (
	// eventSettlementNoVote records why a report at the rendezvous endorsed no site (cq-ix1).
	eventSettlementNoVote = "SETTLEMENT_NO_VOTE"
	// noVoteRegionsListed bounds the regions one such event names, best scored first.
	noVoteRegionsListed = 8
)

// emitNoVote records a report that endorsed nothing: no region has been surveyed, every region
// was refused, or the clan's records could not be used.
func emitNoVote(debugLog *DebugLogger, tick int64, reason, summary string, details map[string]any) {
	if debugLog == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	details["reason"] = reason
	debugLog.Emit(tick, eventSettlementNoVote, "Endorsed no site: "+summary, details)
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
		emitNoVote(debugLog, frame.WorldTick, "store_error", "could not report to the clan", map[string]any{"error": err.Error()})
		return
	}
	site, err := civicDB.GetClanSite(ctx, clanID)
	if err != nil {
		log.Printf("[WARN] agent %s could not read the clan site: %v", agentID, err)
		emitNoVote(debugLog, frame.WorldTick, "store_error", "could not read the clan site", map[string]any{"error": err.Error()})
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
	// unread is a failure before any vote was cast.
	unread := func(what string, err error) *db.ClanSite {
		emitNoVote(debugLog, frame.WorldTick, "store_error", "could not "+what, map[string]any{"error": err.Error()})
		return warn(what, err)
	}
	ranked, err := civicDB.RankCollectiveSettlementCandidates(ctx)
	if err != nil {
		return unread("rank settlement sites", err)
	}
	dangers, err := civicDB.ListDangers(ctx, frame.WorldTick-system1.DangerMemoryTicks+1)
	if err != nil {
		return unread("read the clan's dangers", err)
	}
	var refused []map[string]any
	for _, c := range ranked {
		if why := siteRefusal(c, dangers); why != nil {
			refused = append(refused, why)
			continue
		}
		choice := db.ClanSite{X: c.X, Y: c.Y, Z: c.Z, Score: c.Score, ChosenBy: agentID, ChosenTick: frame.WorldTick}
		if err := civicDB.VoteForSite(ctx, clanID, agentID, choice); err != nil {
			return unread("vote for a site", err)
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
	if len(refused) == 0 {
		emitNoVote(debugLog, frame.WorldTick, "no_surveyed_region", "no region has been surveyed", nil)
		return nil
	}
	best := refused[0]
	summary := fmt.Sprintf("all %d regions refused; the best, at (%.0f, %.0f) scored %.1f by %d explorers, for %s",
		len(refused), best["x"], best["z"], best["score"], best["explorers"], best["why"])
	if d, ok := best["danger"].(map[string]any); ok {
		summary += fmt.Sprintf(" (%s %.0f blocks away)", d["kind"], d["distance"])
	}
	emitNoVote(debugLog, frame.WorldTick, "all_regions_refused", summary, map[string]any{
		"regions": len(refused), "refused": refused[:min(len(refused), noVoteRegionsListed)],
		"strong_score": clanSiteStrongScore, "shared_score": clanSiteSharedScore,
		"min_explorers": clanSiteMinExplorers, "danger_radius": clanSiteDangerRadius,
	})
	return nil
}
