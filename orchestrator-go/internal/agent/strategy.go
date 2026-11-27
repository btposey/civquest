package agent

// The agent's strategic machine across restarts, and System 2's say in it (cq-507.7).

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
)

// nightStaggerTicks spreads the agents' night contemplations over this much of the evening.
const (
	nightBeginsTick   = int64(13000)
	nightStaggerTicks = uint64(5000)
)

// contemplationHour is the time of day from which this agent may contemplate at night.
func contemplationHour(agentID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(agentID))
	return nightBeginsTick + int64(h.Sum64()%nightStaggerTicks)
}

func strategyRow(r system1.StrategyRecord) db.StrategyRow {
	return db.StrategyRow{Kind: string(r.Kind), Resume: string(r.Resume), Since: r.Since, Chosen: r.Chosen}
}

// loadStrategy restores the strategy followed in an earlier run.
func (r *AgentRuntime) loadStrategy() {
	if r.agentDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), settlementStoreTimeout)
	defer cancel()
	row, err := r.agentDB.LoadStrategy(ctx)
	if err != nil {
		log.Printf("[WARN] agent %s could not load its strategy: %v", r.agentID, err)
		return
	}
	if row == nil {
		return
	}
	r.system1State.RestoreStrategy(system1.StrategyRecord{
		Kind: system1.Strategy(row.Kind), Resume: system1.Strategy(row.Resume), Since: row.Since, Chosen: row.Chosen,
	})
	r.savedStrategy = *row
}

// persistStrategy writes the strategy when it has changed.
func (r *AgentRuntime) persistStrategy() {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	rec := r.system1State.StrategyRecord()
	row := strategyRow(rec)
	if rec.Kind == "" || row == r.savedStrategy || r.agentDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), settlementStoreTimeout)
	defer cancel()
	if err := r.agentDB.SaveStrategy(ctx, row); err != nil {
		log.Printf("[WARN] agent %s could not save its strategy: %v", r.agentID, err)
		return
	}
	r.savedStrategy = row
}

// strategyOptions lists what the next morning review would accept.
func (r *AgentRuntime) strategyOptions() []string {
	var out []string
	for _, s := range r.system1State.AllowedStrategies() {
		out = append(out, string(s))
	}
	return out
}

// proposeStrategy holds a suggested change for the morning review.
func (r *AgentRuntime) proposeStrategy(p *system2.StrategyProposal, worldTick int64) {
	if p == nil {
		return
	}
	r.system1State.ProposeStrategy(system1.Strategy(p.Strategy), p.Reason, worldTick)
	r.mu.RLock()
	debugLog := r.debugLogger
	r.mu.RUnlock()
	if debugLog != nil {
		debugLog.Emit(worldTick, system1.EventStrategy,
			fmt.Sprintf("Proposed for the morning: %s (%s)", p.Strategy, p.Reason),
			map[string]any{"proposed": p.Strategy, "why": p.Reason})
	}
}
