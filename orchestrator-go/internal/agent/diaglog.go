package agent

// Log-only records of what the runtime sends and receives (cq-49c).

import (
	"fmt"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
)

// logBatch records what a batch asks of the body, so a RECEIPT's batch id can be matched to
// the walk or action it reports on.
func (r *AgentRuntime) logBatch(batch *civquestv1.IntentBatch) {
	if r.debugLogger == nil || batch == nil {
		return
	}
	intents := make([]map[string]any, 0, len(batch.Intents))
	names := make([]string, 0, len(batch.Intents))
	for _, in := range batch.Intents {
		d := map[string]any{"sequence_id": in.GetSequenceId(), "action": intentActionName(in)}
		if nav := in.GetNavigate(); nav != nil && nav.Target != nil {
			d["target"] = []float64{nav.Target.X, nav.Target.Y, nav.Target.Z}
			d["tolerance"], d["into_cell"] = nav.ArrivalTolerance, nav.IntoCell
		}
		if p := in.GetInteractBlock().GetPos(); p != nil {
			d["block"] = []int32{p.X, p.Y, p.Z}
		}
		if a := in.GetAttack(); a != nil {
			d["target_entity"], d["flee"] = a.TargetEntityId, a.FleeImmediately
		}
		intents = append(intents, d)
		names = append(names, intentActionName(in))
	}
	r.debugLogger.Emit(r.WorldTick(), "BATCH_SENT", fmt.Sprintf("batch %d: %s", batch.BatchId, strings.Join(names, ", ")),
		map[string]any{"batch": batch.BatchId, "frame": batch.FrameId, "flush": batch.FlushPeriphery, "intents": intents})
}

// logDeath records how the agent died.
func (r *AgentRuntime) logDeath(death *civquestv1.DeathEvent) {
	if r.debugLogger == nil || death == nil {
		return
	}
	d := map[string]any{
		"damage_source": death.DamageSource, "killer": death.KillerDisplayName, "killer_id": death.KillerEntityId,
	}
	if p := death.DeathLocation; p != nil {
		d["position"] = []float64{p.X, p.Y, p.Z}
	}
	var dropped []string
	for _, it := range death.DroppedLoot {
		dropped = append(dropped, fmt.Sprintf("%s x%d", it.GetItemId(), it.GetCount()))
	}
	d["dropped"] = dropped
	r.debugLogger.Emit(death.WorldTick, "DEATH", fmt.Sprintf("died: %s %s", death.DamageSource, death.KillerDisplayName), d)
}

// traceModelCalls records every model request with its answer: System 2's prompts and replies.
func traceModelCalls(client *system2.Client, debugLog *DebugLogger) {
	if client == nil {
		return
	}
	client.SetTrace(func(call system2.ModelCall) {
		d := map[string]any{"endpoint": call.Endpoint, "request": call.Request, "millis": call.Duration.Milliseconds()}
		if call.Err != nil {
			d["error"] = call.Err.Error()
			debugLog.Emit(0, "LLM_ERROR", call.Err.Error(), d)
			return
		}
		d["response"] = call.Response
		debugLog.Emit(0, "LLM_CALL", fmt.Sprintf("%s in %d ms", call.Endpoint, call.Duration.Milliseconds()), d)
	})
}
