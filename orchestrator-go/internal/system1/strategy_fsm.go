package system1

// EventStrategy is the playtest event category of a strategy change.
const EventStrategy = "STRATEGY"

// advanceStrategy moves the agent's strategic machine on for this tick.
func (fsm *AgentFSM) advanceStrategy(ctx *FSMContext) {
	if ctx == nil || ctx.State == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return
	}
	home := isStateShelterOrBedSatisfied(ctx.State)
	change, ok := ctx.State.advanceStrategy(ctx.Frame.WorldTick, ctx.Frame.TimeOfDay, home)
	if !ok {
		return
	}
	fsm.mu.RLock()
	sink := fsm.eventSink
	fsm.mu.RUnlock()
	if sink != nil {
		sink(ctx.Frame.WorldTick, EventStrategy, string(change.From)+" -> "+string(change.To)+": "+change.Why,
			map[string]any{"from": string(change.From), "to": string(change.To), "why": change.Why})
	}
}
