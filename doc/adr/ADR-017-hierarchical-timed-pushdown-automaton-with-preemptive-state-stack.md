# ADR-017: Hierarchical Timed Pushdown Automaton (HTPDA) with Preemptive State Stack

## Status
Accepted

## Context
CivQuest autonomous agents (`civquest-agent`) operate under a Two-Tier cognitive model: Tier 1 defines macro strategic settlement stages (`StageBootstrap`, `StageSettlement`, `StageColonize`), while Tier 2 defines discrete circadian micro-routines (`RoutineDawnPrep`, `RoutinePrimaryDuty`, `RoutineTavernSocial`, `RoutineSleep`).

In early implementations, when asynchronous, critical interrupts occurred (such as hostile mob attacks, creeper hisses, sudden physical damage, player chat dialogue, or critical hunger), the FSM executed direct transitions into `StageEmergency`. When the threat cleared, the agent was forced to guess its return state using static heuristics (e.g. checking if tools were in inventory). This produced severe behavioral amnesia: an agent interrupted while paving a road, conversing with a player, or constructing an outpost shelter lost their active state context and frequently reverted to default baseline routines.

Furthermore, death in Minecraft is permadeath under CivQuest's invariants (ADR-008). A dead agent must never resume prior tasks or loop back into the state machine.

## Decision

### 1. Mathematical Classification: Hierarchical Timed Pushdown Automaton (HTPDA)
We formalize System 1 as a **Hierarchical Timed Pushdown Automaton (HTPDA)**:
1. **Hierarchical**: Decouples civilizational epoch (Macro Stage) from daily diurnal rhythm (Micro Routine).
2. **Timed**: Transitions within Tier 2 are driven by discrete clock invariants ($\tau = t_{\text{world}} \pmod{24000}$) evaluated synchronously at 5 Hz.
3. **Pushdown**: Asynchronous interruptions push active state frames onto a bounded Last-In, First-Out (LIFO) stack; threat resolution pops the stack to resume prior operational focus with zero amnesia.
4. **Terminal Sink**: Fatal health or `DeathEvent` immediately purges the stack and forces a transition to an absorbing terminal state (`StageDead` / `RoutineDead`).

### 2. State Stack Specification
The state stack is integrated directly into `AgentFSM` in `internal/system1/fsm.go`:

```go
type StateStackFrame struct {
    Stage       Stage             `json:"stage"`
    Routine     Routine           `json:"routine"`
    EnteredTick int64             `json:"entered_tick"`
    Reason      string            `json:"reason"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}
```

- **Preemption (`Push`)**:
  When transitioning into `StageEmergency` (or any interrupt state such as `StateDialogue`) from a non-emergency state, the current `(Stage, Routine, EnteredTick, Reason, Metadata)` is atomically pushed onto the stack.
- **Bounded Capacity & Deduplication**:
  Maximum stack depth is bounded to 8 frames. Pushing a frame with identical `(Stage, Routine)` to the current stack top is suppressed to prevent oscillation overflow. If capacity is exceeded, the oldest frame is evicted.
- **Context Resumption (`Pop`)**:
  When `threat.threats_cleared` evaluates to true, `fsm.Pop()` is evaluated first. If a frame exists, the FSM transitions directly back to `frame.Stage` and restores `frame.Routine`, bypassing heuristic fallbacks.
- **Multi-Level Interrupts**:
  Supports nested preemption (e.g., Working $\to$ Player Dialogue $\to$ Skeleton Attack $\to$ Resume Dialogue $\to$ Resume Work).
- **Terminal Death Handling**:
  When `Health <= 0` or a `DeathEvent` is detected, `fsm.ClearStack()` wipes all frames, transitions permanently to `StageDead` / `RoutineDead`, generates memorial records, and exits with code 0.

## Consequences

### Positive
- **Zero Behavioral Amnesia**: Agents resume complex tasks (paving, masonry, timber harvesting, trading) immediately after neutralizing threats.
- **Formal Theoretical Soundness**: Eliminates ad-hoc state-flag coupling; replaces imperative overrides with classical pushdown automata transitions.
- **Sub-5ms Zero-Token SLA**: State stack operations (`Push`, `Pop`, `Peek`) execute in $<1\text{ µs}$ without heap thrashing or allocations.

### Negative / Trade-offs
- Stale stack frames: If the world context changes significantly during an extended combat encounter (e.g., night fell or shelter was destroyed), popping back to an obsolete routine could trigger immediate secondary transitions. Mitigated by FSM clock-invariant re-evaluation following stack pops.
