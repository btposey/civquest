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
