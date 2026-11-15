# ADR-016: Dynamic Clan Governance, Emergent Councils, Benevolent Monarchy, and Warlord Hegemony

## Status
Accepted

## Context
In early settlement phases, CivQuest agents execute autonomous System 1 utility curves and daily schedules. However, when active blueprints or immediate personal goals are exhausted, settlements lack an autonomous, collective governance apparatus to formulate communal direction.

Hardcoded periodic elections or centralized omniscient planners violate CivQuest's core philosophy of emergent dynastic realism. Collective direction must emerge from the settlers themselves—through social discourse, crisis consolidation, trait-driven leadership, and fluid political relations with human players. Furthermore, authoritarian governance is not uniformly oppressive; in harsh frontier conditions, decisive autocratic leadership can manifest as either exploitative martial tyranny or beloved, paternalistic benevolent monarchy.

## Decision

### 1. Two-Axis Governance Model
Clan political structures are governed along two continuous axes:
- **Decision Structure**: Egalitarian Collective Consensus $\longleftrightarrow$ Unilateral Autocracy.
- **Sovereign Orientation / Morale Impact**: Paternalistic/Civic (Communal Welfare) $\longleftrightarrow$ Exploitative/Martial (Coercive Might).

This establishes four emergent regime types:
1. **Democratic Council (Egalitarian Assembly)**:
   - Dominant Traits: High `Sociability` ($\ge 0.65$), balanced `Bravery`, low `Aggressiveness` ($< 0.40$).
   - Operation: Settlers assemble at civic landmarks (Town Hall, Bell, or Hearth) to conduct paced System 2 conversational debates broadcasted to local in-game chat. Proposals are decided by affinity- and prestige-weighted voting.
2. **Benevolent Monarchy (Paternalistic Sovereign)**:
   - Dominant Traits: High `Industriousness` ($\ge 0.75$), high `Sociability` ($\ge 0.60$), low `Aggressiveness` ($< 0.35$).
   - Legitimacy: Earned through dynastic lineage (oldest living generation) or proven civic salvation (guiding the clan out of famine or completing monumental civic works).
   - Operation: Issues decisive, unilateral edicts focused on communal welfare (granary expansion, paved roadways, housing, bakeries, nurseries). High public approval; increases interpersonal affinity and communal morale.
3. **Protector-Warlord / Martial Hegemony**:
   - Dominant Traits: High `Aggressiveness` ($\ge 0.75$), high `Bravery` ($\ge 0.70$), low `Sociability` ($< 0.40$).
   - Legitimacy: Earned strictly through **heroic martial feats in moments of mortal peril** (e.g., neutralizing lethal mob breaches, killing raid champions, defending the nursery/granary). The populace consolidates behind the warlord out of survival necessity and gratitude.
   - Operation: Issues unilateral edicts prioritizing palisades, watchtowers, armories, and sentry patrols. Overbearing rule or neglected famine breeds resentment in `reputation.db` and subversive conspiracy whispers in `rumors.db`.
4. **Oligarchic Elders Council**:
   - Dominant Traits: Senior generation, high cumulative `Prestige`. Traditionalist, defensive, cautious expansion.

### 2. Event-Driven Assemblies ("Axes of Change")
Assemblies and political challenges do **not** follow an arbitrary fixed calendar. They are triggered strictly by material, demographic, and security inflection points:
1. **Material Shocks**: Granary reserves fall below famine threshold ($< 3\text{ days}$ rations) or sudden windfall of rare resources (iron, diamonds, redstone).
2. **Security Crises**: Villager casualty (`DeathEvent`), hostile breach of the inner settlement perimeter, or night monster sieges.
3. **Dynastic Succession & New Blood**: An adolescent comes of age into adulthood (ADR-008 reproduction governor) possessing traits or ideologies clashing with the incumbent regime.
4. **Goal Vacuums**: All active blueprints in `civic_registry.db` completed.

### 3. Fluid Human Player Integration
The human player is an organic actor within the geopolitical simulation:
- **High Prestige ($\ge 85$)**: Respected players are invited to cast deciding tie-breaker votes in democratic councils, or can be elected titular Clan Chieftain/Protector.
- **Conversational Lobbying**: System 2 dialogue (`ProcessChat`) allows players to lobby agents, planting ideas that agents subsequently propose at assembly.
- **Warlord Interaction**: A warlord calculates relative player strength; they may demand tribute from weak players, challenge formidable rivals to duels for clan dominance, or recruit martial peers as vanguard champions.

### 4. Persistence & Data Tier
Clan governance state is persisted in `civic_registry.db`:
- `clan_governance`: Tracks `regime_type`, `leader_agent_id`, `mandate_goal`, `legitimacy_score`, and `term_start_tick`.
- `civic_assemblies`: Records assembly trigger reasons, active speakers, and timestamps.
- `civic_votes`: Records individual agent ballots, weights, and rationale.
- `civic_edicts`: Records unilateral sovereign decrees and tracks community compliance vs. underground dissent.

## Consequences

### Positive
- Transforms static task execution into an emergent, living political society.
- Rich narrative drama: heroic warlord ascents, beloved monarchies, dynastic succession crises upon permadeath, and tavern conspiracies.
- Players have meaningful political agency through chat, trade, and combat rather than artificial admin menus.

### Negative / Trade-offs
- Multi-agent System 2 deliberation requires strict turn caps (3–4 conversational turns) and sentence pacing to prevent token budget exhaustion.
- Authoritarian edicts can cause temporary productivity dips if dissenting agents grumble or resist unpopular orders.
