# ADR-015: In-Engine Peripheral Reflexes and Afferent Telemetry

## Status
Accepted

## Context
In CivQuest's two-tier architecture, the external Go orchestrator ingests sensory telemetry (`TelemetryFrame`) at 5 Hz and emits action directives (`AgentIntent`). However, physical survival in Minecraft presents sub-tick hazards (instant lava contact, point-blank projectile hits, falling blocks, vacuuming nearby dropped item entities) where waiting for an external gRPC IPC round-trip ($5\text{--}20\text{ ms}$) results in fatal avatar destruction or erratic kinetic behavior.

Conversely, executing autonomous reflexes in-engine (`mod-neoforge`) without notifying the external orchestrator causes cognitive desynchronization, where the Go-side Hierarchical Finite State Machine (HFSM) pursues obsolete targets unaware that an emergency reflex has altered avatar state or inventory.

## Decision
We establish a dedicated **Layer 1A (In-Engine Peripheral Reflex Tier)** within `mod-neoforge` coupled with an **Afferent Reporting Channel**:

1. **Local Server-Tick Reflex Execution**:
   - Immediate deterministic survival routines execute natively in `AgentEntity.tick()` at 20 Hz ($<0.1\text{ ms}$, zero IPC delay).
   - In-engine reflexes handle instant shield raising on projectile trajectory intersection, vacuuming `ItemEntity` within $2.5\text{ blocks}$, step-height collision climbing, and real-time social gaze interpolation via `LookSmoothing`.
   - Reflexes are strictly prohibited from generating synthetic `AgentIntent` messages or waiting for Go orchestrator confirmation.

2. **Afferent Reflex Telemetry (`ReflexNotice`)**:
   - Whenever an in-engine reflex fires, `mod-neoforge` appends a lightweight `ReflexNotice` into the next outbound `TelemetryFrame`.
   - The Go orchestrator ingests `ReflexNotice` events to dynamically adjust survival confidence scores, trigger HFSM state transitions (e.g., forcing a transition from `STATE_WORK` to `STATE_EMERGENCY`), and record salient episodic memory in the local database.

## Consequences
### Positive
- Guarantees instant sub-tick physical survival against environmental and projectile hazards with zero network latency penalty.
- Eliminates network channel saturation by preventing micro-kinematic adjustments from being transmitted as gRPC intents.
- Closes the perception-action loop by providing ascending afferent feedback to the Go harness for tactical replanning.

### Negative / Trade-offs
- Requires maintaining tight synchronization between in-engine state representations and Go-side inventory/state models.
- Reflex routines in Java must remain computationally lean ($<0.1\text{ ms}$) to prevent server tick-rate degradation (TPS drops).
