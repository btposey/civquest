package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const clanSizeRefreshIntervalTicks int64 = 6000

// resolveClanSize determines the number of agents belonging to this runtime's clan
// from $XDG_CONFIG_HOME/civquest/agents/ (and server override dir) as well as
// civic_registry.db, defaulting to 1 for solo explorers.
func (r *AgentRuntime) resolveClanSize() int {
	if r == nil {
		return 1
	}

	r.mu.RLock()
	clanID := strings.TrimSpace(r.clanID)
	if clanID == "" && r.identityConfig != nil {
		clanID = strings.TrimSpace(r.identityConfig.ClanID)
	}
	res := r.resolver
	civicDB := r.civicRegistryDB
	r.mu.RUnlock()

	if clanID == "" {
		return 1
	}

	matchedAgents := make(map[string]struct{})
	scanDir := func(dir string) {
		if dir == "" {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			var cfgPath string
			if entry.IsDir() {
				candidate := filepath.Join(dir, entry.Name(), "identity.yaml")
				if _, statErr := os.Stat(candidate); statErr == nil {
					cfgPath = candidate
				}
			} else {
				ext := strings.ToLower(filepath.Ext(entry.Name()))
				if ext == ".yaml" || ext == ".yml" {
					cfgPath = filepath.Join(dir, entry.Name())
				}
			}
			if cfgPath == "" {
				continue
			}
			cfg, err := xdg.LoadAgentConfig(cfgPath)
			if err != nil || cfg == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(cfg.ClanID), clanID) && strings.TrimSpace(cfg.Name) != "" {
				matchedAgents[strings.ToLower(strings.TrimSpace(cfg.Name))] = struct{}{}
			}
		}
	}

	if res != nil {
		scanDir(res.AgentsConfigDir())
		if srvDir := res.ServerAgentsConfigDir(); srvDir != "" && srvDir != res.AgentsConfigDir() {
			scanDir(srvDir)
		}
	}

	count := len(matchedAgents)
	if civicDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if pop, err := civicDB.GetSettlementPopulationContext(ctx); err == nil && pop > count {
			count = pop
		}
		cancel()
	}

	if count < 1 {
		return 1
	}
	return count
}

// processHorizonSurveyAndRendezvous ingests completed 360-degree HorizonSurveyReports
// into System 1 and map_graph.db, and executes clan rendezvous sync, collective site
// scoring, and local chat reporting upon expedition return (ADR-032).
func (r *AgentRuntime) processHorizonSurveyAndRendezvous(frame *civquestv1.TelemetryFrame) {
	if r == nil || frame == nil || r.IsDead() {
		return
	}

	// Lazily resolve and periodically refresh clan size.
	shouldResolve := r.system1State.TryClaimClanSizeRefresh(frame.WorldTick, clanSizeRefreshIntervalTicks)
	r.mu.RLock()
	agentID := r.agentID
	agentDB := r.agentDB
	debugLog := r.debugLogger
	r.mu.RUnlock()

	if shouldResolve {
		r.system1State.SetResolvedClanSize(r.resolveClanSize())
	}

	// 1. Ingest and persist any completed 360-degree HorizonSurveyReport.
	if survey := frame.GetHorizonSurvey(); survey != nil {
		tick := frame.WorldTick
		if tick == 0 {
			tick = survey.CompletedTick
		}

		r.system1State.IngestHorizonSurveyReport(survey, tick)

		if agentDB != nil && len(survey.Sectors) > 0 {
			dbSectors := protoSectorsToDB(survey, tick)
			originX, originY, originZ := survey.OriginX, survey.OriginY, survey.OriginZ
			r.poiWg.Add(1)
			go func() {
				defer r.poiWg.Done()
				dbCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := agentDB.InsertHorizonSurveyBatch(dbCtx, originX, originY, originZ, tick, dbSectors); err != nil {
					log.Printf("[WARN] agent %s failed to persist horizon_sectors: %v", agentID, err)
				}
			}()
		}

		if debugLog != nil {
			summary := system1.FormatHorizonSurveySummary(survey)
			debugLog.Emit(tick, "HORIZON_SURVEY_COMPLETE", fmt.Sprintf("360° horizon survey complete (%d sectors): %s", len(survey.Sectors), summary), map[string]any{
				"sector_count": len(survey.Sectors),
				"origin_x":     survey.OriginX,
				"origin_y":     survey.OriginY,
				"origin_z":     survey.OriginZ,
				"summary":      summary,
			})
		}
	}
}

// syncPendingRendezvous handles the RendezvousReport events emitted by expeditions that
// returned within 20m of camp since the last FSM tick (events, not a state mailbox).
func (r *AgentRuntime) syncPendingRendezvous(frame *civquestv1.TelemetryFrame) {
	if r == nil || frame == nil || r.IsDead() {
		return
	}
	r.mu.RLock()
	fsm := r.agentFSM
	r.mu.RUnlock()
	for _, rep := range fsm.DrainRendezvousReports() {
		r.syncRendezvousReport(frame, rep.Summary)
	}
}

func (r *AgentRuntime) syncRendezvousReport(frame *civquestv1.TelemetryFrame, summary string) {

	r.mu.RLock()
	agentID := r.agentID
	clanID := strings.TrimSpace(r.clanID)
	if clanID == "" && r.identityConfig != nil {
		clanID = strings.TrimSpace(r.identityConfig.ClanID)
	}
	agentDB := r.agentDB
	civicDB := r.civicRegistryDB
	res := r.resolver
	debugLog := r.debugLogger
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var closeCivic func()
	if civicDB == nil && res != nil && clanID != "" {
		if opened, err := db.OpenCivicRegistryDBForClan(res, clanID); err == nil {
			civicDB = opened
			closeCivic = func() { _ = opened.Close() }
		}
	}
	if closeCivic != nil {
		defer closeCivic()
	}

	if civicDB != nil {
		// Merge recent horizon sectors into clan_horizon_sectors grouped by survey origin.
		var sectorsToMerge []db.HorizonSector
		if agentDB != nil {
			if recent, err := agentDB.GetRecentHorizonSectors(ctx, 200); err == nil && len(recent) > 0 {
				sectorsToMerge = recent
			}
		}
		if len(sectorsToMerge) == 0 {
			if latest := r.system1State.GetLatestSurvey(); latest != nil {
				sectorsToMerge = protoSectorsToDB(latest, frame.WorldTick)
			}
		}

		if len(sectorsToMerge) > 0 {
			type originGroupKey struct {
				ox, oy, oz float64
				tick       int64
			}
			grouped := make(map[originGroupKey][]db.HorizonSector)
			var order []originGroupKey
			for _, sec := range sectorsToMerge {
				k := originGroupKey{ox: sec.OriginX, oy: sec.OriginY, oz: sec.OriginZ, tick: sec.SurveyedTick}
				if _, exists := grouped[k]; !exists {
					order = append(order, k)
				}
				grouped[k] = append(grouped[k], sec)
			}
			for _, k := range order {
				if err := civicDB.MergeExplorerHorizonSurvey(ctx, agentID, k.ox, k.oy, k.oz, k.tick, grouped[k]); err != nil {
					log.Printf("[WARN] agent %s failed to merge clan horizon survey: %v", agentID, err)
				}
			}
		}

		// Sync discovered active POIs into clan settlement_pois ledger (ADR-024).
		var poisToSync []db.AgentPOI
		if agentDB != nil {
			if dbPOIs, err := agentDB.ListPOIs(ctx, "", true); err == nil && len(dbPOIs) > 0 {
				poisToSync = dbPOIs
			}
		}
		if len(poisToSync) == 0 {
			if si := r.system1State.SpatialIndex(); si != nil {
				for _, p := range si.AllPOIs() {
					if p != nil && p.IsActive {
						poisToSync = append(poisToSync, *p)
					}
				}
			}
		}
		if len(poisToSync) > 0 {
			settlementID := clanID
			if settlementID == "" {
				settlementID = "default"
			}
			if err := civicDB.UpsertSettlementPOIsBatch(ctx, settlementID, agentID, poisToSync); err != nil {
				log.Printf("[WARN] agent %s failed to sync settlement_pois at rendezvous: %v", agentID, err)
			}
		}

		// Score collective settlement candidates across all merged clan explorers.
		if cand, err := civicDB.ScoreCollectiveSettlementCandidates(ctx); err == nil && cand != nil {
			if cand.ExplorerCount >= 2 || cand.Score >= 35.0 {
				r.system1State.SetCollectiveSettlementSite(cand.X, cand.Y, cand.Z)
				if debugLog != nil {
					debugLog.Emit(frame.WorldTick, "COLLECTIVE_SETTLEMENT_SITE",
						fmt.Sprintf("Collective clan settlement site scored %.1f at (%.0f, %.0f, %.0f): %s",
							cand.Score, cand.X, cand.Y, cand.Z, cand.Summary),
						map[string]any{
							"x":              cand.X,
							"y":              cand.Y,
							"z":              cand.Z,
							"score":          cand.Score,
							"explorer_count": cand.ExplorerCount,
							"summary":        cand.Summary,
						})
				}
			}
		}
	}

	// Broadcast local chat report to nearby clanmates & players (ADR-030).
	if strings.TrimSpace(summary) != "" {
		_ = r.QueueIntent(&civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_Chat{
				Chat: &civquestv1.SendChatIntent{
					Message:       summary,
					TargetChannel: civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL,
				},
			},
		})
	}
}

func protoSectorsToDB(survey *civquestv1.HorizonSurveyReport, fallbackTick int64) []db.HorizonSector {
	if survey == nil {
		return nil
	}
	tick := fallbackTick
	if tick == 0 {
		tick = survey.CompletedTick
	}
	out := make([]db.HorizonSector, 0, len(survey.Sectors))
	for _, sec := range survey.Sectors {
		if sec == nil {
			continue
		}
		out = append(out, db.HorizonSector{
			OriginX:        survey.OriginX,
			OriginY:        survey.OriginY,
			OriginZ:        survey.OriginZ,
			SectorIndex:    int(sec.SectorIndex),
			BearingDeg:     float64(sec.BearingDeg),
			HitDistance:    float64(sec.HitDistance),
			FeatureType:    sec.FeatureType,
			HitBlockID:     sec.HitBlockId,
			HitX:           sec.HitX,
			HitY:           sec.HitY,
			HitZ:           sec.HitZ,
			ElevationDelta: float64(sec.ElevationDelta),
			Occluded:       sec.Occluded,
			OpenHorizon:    sec.OpenHorizon,
			SurveyedTick:   tick,
		})
	}
	return out
}
