package agent

// Dangers an agent meets are told to its clan, and the clan's are learnt (cq-gey.2).

import (
	"context"
	"log"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

const (
	// dangerLearnTicks is how often the clan's dangers are read.
	dangerLearnTicks = int64(200)
	// dangerShareRadius merges reports of one mob kind.
	dangerShareRadius = 16.0
)

// withCivicDB runs fn on the clan's registry, opening it for the call if none is held.
func (r *AgentRuntime) withCivicDB(fn func(civic *db.CivicRegistryDB, clanID string)) {
	r.mu.RLock()
	civic, res := r.civicRegistryDB, r.resolver
	clanID := strings.TrimSpace(r.clanID)
	if clanID == "" && r.identityConfig != nil {
		clanID = strings.TrimSpace(r.identityConfig.ClanID)
	}
	r.mu.RUnlock()
	if civic == nil {
		if res == nil || clanID == "" {
			return
		}
		opened, err := db.OpenCivicRegistryDBForClan(res, clanID)
		if err != nil {
			log.Printf("[WARN] agent %s could not open the clan registry: %v", r.agentID, err)
			return
		}
		defer func() { _ = opened.Close() }()
		civic = opened
	}
	fn(civic, clanID)
}

// syncDangers publishes freshly met dangers and periodically learns the clan's. The database
// work runs off the frame path, one pass at a time; what a busy pass leaves waits for the next.
func (r *AgentRuntime) syncDangers(frame *civquestv1.TelemetryFrame) {
	select {
	case <-r.done:
		return
	default:
	}
	if !r.dangerBusy.CompareAndSwap(false, true) {
		return
	}
	fresh := r.system1State.DrainFreshDangers()
	r.persistMu.Lock()
	learn := frame.WorldTick-r.dangersLearntTick >= dangerLearnTicks || frame.WorldTick < r.dangersLearntTick
	if learn {
		r.dangersLearntTick = frame.WorldTick
	}
	r.persistMu.Unlock()
	if len(fresh) == 0 && !learn {
		r.dangerBusy.Store(false)
		return
	}
	tick := frame.WorldTick
	r.poiWg.Add(1)
	go func() {
		defer r.poiWg.Done()
		defer r.dangerBusy.Store(false)
		r.exchangeDangers(fresh, learn, tick)
	}()
}

func (r *AgentRuntime) exchangeDangers(fresh []system1.Danger, learn bool, tick int64) {
	r.withCivicDB(func(civic *db.CivicRegistryDB, _ string) {
		ctx, cancel := context.WithTimeout(context.Background(), settlementStoreTimeout)
		defer cancel()
		for _, d := range fresh {
			spot := db.DangerSpot{Kind: d.Kind, X: d.X, Y: d.Y, Z: d.Z, SeenTick: d.Seen, ReportedBy: r.agentID}
			if err := civic.PublishDanger(ctx, spot, dangerShareRadius); err != nil {
				log.Printf("[WARN] agent %s could not share a danger: %v", r.agentID, err)
			}
		}
		if !learn {
			return
		}
		spots, err := civic.ListDangers(ctx, tick-system1.DangerMemoryTicks+1)
		if err != nil {
			log.Printf("[WARN] agent %s could not read the clan's dangers: %v", r.agentID, err)
			return
		}
		known := make([]system1.Danger, 0, len(spots))
		for _, s := range spots {
			known = append(known, system1.Danger{Kind: s.Kind, X: s.X, Y: s.Y, Z: s.Z, Seen: s.SeenTick})
		}
		r.system1State.MergeDangers(known)
	})
}
