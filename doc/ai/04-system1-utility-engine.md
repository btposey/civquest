# Progressive Discovery: System 1 Reactive Utility Engine

## 1. Principles & Timing Constraints
System 1 represents the deterministic survival and tactical routine tier. It operates across two complementary sub-tiers:
- **Layer 1A (Spinal / Peripheral Reflexes)**: Executes natively inside `mod-neoforge` (`AgentEntity.tick()`, 20 Hz, $<0.1\text{ ms}$, 0 IPC) for instant physical hazard defense (shield deflection on projectile impact, item vacuuming, step climbing) and emits ascending `ReflexNotice` telemetry.
- **Layer 1B (Subcortical / Tactical Intuition)**: Executes in the Go agent process (`civquest-agent`, 5 Hz, $<5\text{ ms}$, 0 LLM tokens). Evaluates spatial threat matrices, fight-or-flight confidence, and schedule state transitions.
- **Token Cost**: Exactly 0 LLM tokens.
- **Execution Model**: Pure in-memory state evaluation against incoming `TelemetryFrame` data and local SQLite graph caches.
- **Hardware Acceleration (CPU SIMD)**: System 1 leverages host CPU multimedia vector extensions (AVX2 / AVX-512 / ARM NEON) for 3D spatial geometry, threat distance matrices, and utility scoring. This guarantees sub-millisecond execution on CPU threads while preserving 100% of GPU compute and VRAM for System 2 LLM inference.

## 2. Fight-or-Flight Confidence Mathematics

When hostiles are present within $R = 16$ blocks or `last_damage_taken != nil`, System 1 evaluates combat confidence:

$$\text{Confidence} = \frac{\text{HP}_{\text{self}} + \sum_{i \in \text{Allies}} \text{HP}_i}{\sum_{j \in \text{Hostiles}} \text{Threat}_j} \times M_{\text{gear}} \times (0.5 + \text{Bravery}_{\text{trait}})$$

### Parameter Specifications:
- **Mob Threat Constants**:
  - Zombie: $15$
  - Skeleton: $20$
  - Creeper: $35$
  - Pillager: $25$
  - Warden: $500$
- **Gear Multiplier ($M_{\text{gear}} \in [0.5, 2.5]$)**:
  - Computed from armor toughness, defense points, weapon attack damage, and offhand shield presence.
- **Bravery Trait**: Agent personality trait in $[0.0, 1.0]$.

### Panic Override Rules:
System 1 immediately triggers `SURVIVAL_PANIC` if:
1. $\text{HP}_{\text{self}} \le 0.35 \times \text{HP}_{\text{max}}$, OR
2. $\text{Confidence} < 1.0$, OR
3. A Creeper within $5\text{ blocks}$ has `fuse_ticks > 0`.

**Panic Action Sequence**:
- Raise offhand shield (`AttackIntent: raise_shield = true`).
- Flee towards the safest known topological node in `map_graph.db`.
- Consume highest-saturation food or golden apple while sprinting.
- Broadcast an emergency alert on `CLAN_BROADCAST`.
- If on an escort quest with a human player, abandon the quest immediately and shout a warning in local chat.

## 3. Daily Schedule & Career State Machine

System 1 evaluates utility curves across four server time-of-day brackets:
1. **Emergency ($U = 1.0$)**: Extinguishing fire, escaping combat, eating when `food_level <= 6`.
2. **Primary Duty (Ticks 1000 - 8000)**:
   - Farmer: Tending crops, seeding, watering.
   - Blacksmith: Smelting, repairing gear, stocking weapons.
   - Guard: Patrolling gates and perimeter nodes.
   - Miner: Deep tunnel extraction and ore hauling.
   - Architect: Surveying sites and managing quadrant construction.
   - *Fallback*: If primary tasks are blocked (e.g. waiting for materials), transition immediately to Secondary Hobby.
3. **Secondary Hobby (Ticks 8000 - 11000 or Idle Standby)**:
   - Gathers hobby feedstocks: `APIARIST` (beehives), `JAM_MAKER` (sweet berry bushes), `CIDER_BREWER` (apples & barrels).
   - If secondary hobby is unassigned, discovers an emergent hobby based on local abundant resources.
4. **Tavern Social & Sleep (Ticks 11000 - 24000)**:
   - Navigates to the settlement Tavern (`zone_type = 'TAVERN'`).
   - Deposits hobby surplus, shares rumors, updates map graphs with other agents, and sleeps in an assigned bed before midnight.

## 4. Hierarchical Timed Pushdown Automaton (HTPDA) & State Stack (ADR-017)

System 1 models agent cognitive control as a formal **Hierarchical Timed Pushdown Automaton**:
- **Macro Stages (Tier 1)**: `BOOTSTRAP`, `SETTLEMENT`, `COLONIZE`, `DEAD`.
- **Micro Routines (Tier 2)**: Clock-driven circadian phases (`DAWN_PREP`, `PRIMARY_DUTY`, `SECONDARY_HOBBY`, `ARMORY_MAINTENANCE`, `TAVERN_SOCIAL`, `CIVIC_PATROL`, `SLEEP`, `CAMP`, `EMERGENCY`).
- **Pushdown Preemption**: When interrupted by asynchronous threats, player dialogue, or critical starvation, the active `(Stage, Routine)` is pushed onto a bounded LIFO stack (depth $\le 8$).
- **Context Restoration**: Upon `threat.threats_cleared`, `fsm.Pop()` restores the prior task without behavioral amnesia.
- **Terminal Dead State**: On fatal health or `DeathEvent`, the stack is cleared, graveyard memorial records are written, and the process exits with code 0.

## 5. Circadian Shift Work & Tavern Social Synchronization (ADR-018)

Settlers support differentiated work shifts parameterized in `AgentIdentityConfig.Shift`:
- **1st Shift (Day Workers: Farmers, Builders, Artisans)**: Awake and working by day; sleeps through the night ($13000 - 23500$).
- **3rd Shift (Night Watch: Sentinels, Guards, Patrols)**: Awake on perimeter patrol throughout deep night ($13000 - 23500$); sleeps during sunny midday ($2000 - 10000$).
- **Universal Tavern Social Hour ($11000 - 13000$)**: Both shifts converge at the tavern during dusk to coordinate clan consciousness, conduct guard handoffs, and debate governance edicts before nightfall.

## 6. Population-Scaled Militia Armory Maintenance (ADR-019)

For 3rd shift guards during off-watch waking hours ($[10000, 11000)$ & $[1000, 2000)$), System 1 schedules `RoutineArmoryMaintenance`:
- **Militia Sufficiency Formula**: Evaluated dynamically for settlement population $N$:
  $$\text{Armor Sets} \ge N \quad (4N \text{ total pieces}) \qquad \text{Swords} \ge N \qquad \text{Bows} \ge N \qquad \text{Arrows} \ge 10N \qquad \text{TNT} \ge \max(4, N)$$
- **Crafting Reflexes**: If below quota and raw materials exist, guards navigate to crafting tables and craft munitions. Once quotas are satisfied or materials exhausted, guards automatically stand down to `RoutineSecondaryHobby`.

## 7. The FactGraph Blackboard & Modular Skill Engine (ADR-020)

System 1 decouples perception, belief, decision, and action into a pure Go modular engine:
1. **Perception**: Registered `Evaluator` modules (`VitalsEvaluator`, `ThreatEvaluator`, `CircadianEvaluator`, `ArmoryMilitiaEvaluator`, `SurvivalKitEvaluator`, `ShelterEvaluator`, `NavigationEvaluator`, `ColonizeEvaluator`) inspect raw telemetry and emit typed `Fact`s.
2. **Belief (FactGraph)**: A thread-safe blackboard queried via $O(1)$ accessors by System 1 guards and serialized via `ToPromptContext()` into Markdown for System 2 LLM prompts.
3. **Action (Skills)**: Registered `Skill` modules (`CombatDefenseSkill`, `BiologicalNeedsSkill`, `ArmoryCraftingSkill`, `BootstrapGatherSkill`, `BootstrapCraftSkill`, `ShelterConstructionSkill`, `CivicPatrolSkill`, `FrontierScoutingSkill`, `RoadPavingSkill`) evaluate preconditions over facts and emit discrete vectors of `AgentIntent`.
- **Performance**: Entire perception pipeline executes in $<15\text{ µs}$; skill dispatch in $<8\text{ µs}$ ($<0.025\text{ ms}$ total, $>200\times$ faster than the 5ms budget).
