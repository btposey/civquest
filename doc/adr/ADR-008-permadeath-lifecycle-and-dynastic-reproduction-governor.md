# ADR-008: Permadeath Lifecycle & Dynastic Reproduction Governor

## Status
Accepted

## Context
CivQuest simulations create high-stakes emotional investment and emergent generational storytelling. If agents simply respawned infinitely upon death, survival reflexes and defensive planning would lose significance. Conversely, unbounded agent reproduction would exhaust local hardware (CPU, RAM, GPU inference tokens).

## Decision
1. **Strict Permadeath Lifecycle**:
   - Agents never respawn upon receiving fatal damage.
   - Upon `DeathEvent`:
     - High-saliency memorial records are logged into the clan chronicler queue.
     - Spouses transition to `WIDOWED` status; player companions suffer prestige penalties (or outlaw status if murder occurred).
     - gRPC streams and UNIX sockets are severed.
     - The agent's directory in `$XDG_DATA_HOME/civquest/agents/{name}` is moved to `$XDG_DATA_HOME/civquest/graveyard/{name}_{uuid}_tick{N}` along with a `death_certificate.json` and epitaph.
     - The Go worker process exits with code 0, freeing its slot.
2. **Dynastic Reproduction Governor**:
   - Checked daily by the supervisor daemon under strict constraints:
     - Total active agents must be strictly less than `max_active_agents`.
     - The clan must possess a minimum food surplus reserve (`min_surplus_food_days >= 7`) and an unclaimed bed.
     - Two adult agents must have mutual `disposition_score >= 80.0` and `kinship_role == 'SPOUSE'`.
   - Offspring inherit blended personality traits from parents via Gaussian perturbation:
     $$\text{Trait}_{\text{child}} = \text{clamp}\left(\frac{\text{Trait}_A + \text{Trait}_B}{2} + \mathcal{N}(0, 0.08^2),\ 0.0,\ 1.0\right)$$

## Consequences
### Positive
- Deep emergent stakes: individual agents are irreplaceable, driving genuine generational history.
- Built-in resource capping prevents simulation sprawl and out-of-memory or inference throttling failures.

### Negative / Trade-offs
- An unexpected agent death irrevocably tears down its active process and live state.
- Reproduction requires sustained player or agent agricultural management.
