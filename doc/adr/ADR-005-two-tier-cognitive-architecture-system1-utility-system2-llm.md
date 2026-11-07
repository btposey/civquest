# ADR-005: Two-Tier Cognitive Architecture (System 1 Utility vs. System 2 LLM)

## Status
Accepted

## Context
Autonomous agents in an active survival Minecraft environment face two fundamentally different operational challenges:
1. **Real-time Survival & Routine Reflexes**: Avoiding Creepers, raising shields, eating food, following paved paths, and farming crops require deterministic execution in $<5\text{ ms}$. Calling an LLM for these actions is prohibitively slow ($500\text{--}3000\text{ ms}$), expensive, and susceptible to hallucination during critical survival moments.
2. **Emergent Social & Creative Reasoning**: In-character dialogue, barter price negotiation, chronicling lore books, naming landmarks, and architectural layout planning require nuanced contextual reasoning and stochastic creativity.

## Decision
We implement a strict two-tier cognitive architecture:
- **System 1 (Reactive Utility AI Engine)**:
  - Runs synchronously on every incoming 5 Hz `TelemetryFrame` in pure Go.
  - Computes fight-or-flight confidence formulas, pathing triggers, hunger consumption, and career state transitions.
  - Consumes **0 LLM tokens** and completes in $<5\text{ ms}$.
- **System 2 (Deliberative LLM Inference Engine)**:
  - Executes asynchronously in background worker pools.
  - Bounded by a global inference governor (`max_concurrent_llm_requests`).
  - Calls OpenAI-compatible endpoints (Ollama / vLLM) with strict JSON schema validation.
  - Uses temperature splitting: low temperature ($T = 0.1\text{--}0.2$) for structured intents and high temperature ($T = 0.85\text{--}1.05$) for dialogue, books, and diplomacy.

## Consequences
### Positive
- Sub-millisecond survival reflexes guarantee agent survival against hostile mobs without lag.
- Token consumption and inference costs are tightly bounded, preventing GPU overload or provider quota exhaustion.
- Highly expressive natural language generation without compromising real-time game physics.

### Negative / Trade-offs
- Increased architectural complexity maintaining two distinct reasoning paradigms.
- State synchronization between background LLM tasks and real-time System 1 game loop must prevent stale actions.
