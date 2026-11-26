package system1

// Replay scenarios for the planning layer (cq-dcx.4): golden traces of the plan frame.

func planGraph(st *AgentState) { st.SetRecipeGraph(newTestRecipeGraph()) }

// Commitment + hysteresis: the committed stone (x=8) is held when a slightly nearer one appears
// and replaced only by one more than 2x cheaper (x=2).
func scenarioPlanCommitmentHysteresis() replayScenario {
	a, b, c := blk(8, 64, 0, "minecraft:stone"), blk(0, 64, 6, "minecraft:stone"), blk(2, 64, 1, "minecraft:stone")
	return replayScenario{
		Name: "plan_commitment_hysteresis", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, stoneKit(), withBlocks(a)), Setup: planGraph, Annotation: "wooden tools in hand: plan gathers cobblestone, commits the stone at x=8"},
			{Frame: rf(140, 3000, stoneKit(), withBlocks(a, b)), Annotation: "nearer stone at z=6 (not 2x cheaper): commitment held, re-issue to x=8"},
			{Frame: rf(180, 3000, stoneKit(), withBlocks(a, b, c)), Annotation: "stone at x=2 is past the hysteresis ratio: switch"},
		},
	}
}

// Replan on invalidation: the known table vanishes (site lost), the plan re-derives and crafts a table.
func scenarioPlanReplanSiteLost() replayScenario {
	inv := withInv(item(0, "minecraft:oak_planks", 8), item(1, "minecraft:stick", 4))
	return replayScenario{
		Name: "plan_replan_site_lost", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, inv, withBlocks(blk(10, 64, 0, "minecraft:crafting_table"))), Setup: planGraph, Annotation: "planks+sticks, table known: Goto table"},
			{Frame: rf(140, 3000, inv), Annotation: "table gone: site lost, replan crafts a table"},
		},
	}
}

// Resume after emergency: the plan frame is suspended under emergency and resumes its committed target.
func scenarioPlanResumeAfterEmergency() replayScenario {
	a := blk(8, 64, 0, "minecraft:stone")
	return replayScenario{
		Name: "plan_resume_after_emergency", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, stoneKit(), withBlocks(a)), Setup: planGraph, Annotation: "plan commits the stone at x=8"},
			{Frame: rf(110, 3000, stoneKit(), withBlocks(a), withHP(6), withEntities(creeperAt(9, 2.5, 0.5, 2.5))), Annotation: "hostile: emergency over the plan frame"},
			{Frame: rf(160, 3000, stoneKit(), withBlocks(a, blk(0, 64, 6, "minecraft:stone"))), Annotation: "cleared: plan resumes the committed x=8 stone"},
		},
	}
}
