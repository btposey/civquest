package system1

// Gates shared by several activities' Propose (ADR-036, bead cq-2sr.10). Each is a pure read of
// the FSM context; the numeric weights live in activity_priorities.go.

func isColonizeNightShelterGated(routine Routine, ctx *FSMContext) bool {
	if ctx == nil {
		return false
	}
	return (ctx.IsDuskOrNight() || routine == RoutineSleep) &&
		!ctx.HasAssignedBed() && !ctx.IsShelterComplete() && !ctx.IsShelterOnCooldown()
}

func isBootstrapWindowOpen(routine Routine, ctx *FSMContext) bool {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil {
		return false
	}
	if len(ctx.Frame.IncomingChats) > 0 || ctx.Frame.PendingTrade != nil || ctx.Frame.Status.IsSleeping {
		return false
	}
	shelterSatisfied := ctx.HasAssignedBed() || ctx.IsShelterComplete()
	return !shelterSatisfied || (!ctx.IsDuskOrNight() && routine != RoutineSleep && routine != RoutineTavernSocial)
}
