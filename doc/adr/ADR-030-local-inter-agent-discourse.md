# ADR-030: Local Inter-Agent Acoustic Perception & Community Awareness

## Status
Accepted

## Context
In previous iterations of CivQuest, agents were socially isolated:
1. **Unconstrained Player Chat Broadcast**: Agent chat messages (`ChatIntent` / `IntentActuator.actuateSendChat()`) were delivered to all human players on the server within a wide $48\text{ m}$ radius, spamming players with distant chatter regardless of whether they were in earshot.
2. **Deaf & Mute Agents**: `actuateSendChat()` sent chat messages exclusively to `server.getPlayerList().getPlayers()`. It never delivered incoming chat packets to nearby `AgentEntity` instances, leaving agents completely deaf to the speech and intents of their peers.
3. **Lack of Community Awareness**: Agent System 2 deliberation (LLM planning) operated in vacuum isolation. Agents had no awareness of what other clan members were saying, planning, or requesting, leading to duplicated labor (e.g. multiple agents chopping the same tree grove or crafting redundant workstations) and failure to collaborate.

## Decision

### 1. Dual-Radius Acoustic Model
We establish a realistic dual-radius local acoustic propagation model in `IntentActuator`:
- **Human Player Earshot ($\le 16\text{ m}$)**: Spoken chat from an agent is only delivered as a system message to human players (`ServerPlayer`) within a $16\text{ m}$ spherical radius ($\text{distSq} \le 256.0$). Distant players will not hear or see agent chatter.
- **Inter-Agent Acoustic Horizon ($\le 20\text{ m}$)**: Spoken chat is actively broadcast to all peer `AgentEntity` instances within a $20\text{ m}$ spherical radius ($\text{distSq} \le 400.0$) using `AgentEntityManager`. The speaking agent is excluded from receiving its own voice.

### 2. In-Engine Telemetry Delivery (`ChatEvent`)
When an agent speaks, each peer `AgentEntity` within $20\text{ m}$ receives a sensory `ChatEvent` queued onto its sensory stream:
- `sender_name`: avatar name of the speaker.
- `sender_uuid`: UUID of the speaker.
- `message`: raw text string spoken.
- `distance`: Euclidean distance between the speaker and listener.
- `timestamp`: in-game tick or epoch millisecond.

### 3. Episodic Memory & Dialogue Ingestion in Go Orchestrator
In the Go orchestrator (`civquest-agent`):
- Telemetry dispatcher receives `ChatEvent` and records it into the agent's episodic memory store (`rumors.db` / `dialogue_log`).
- Dialogue entries are tagged with sender identity, timestamp, clan affiliation, and spatial coordinate of origin.

### 4. System 2 Community Context Injection
During System 2 Strategic Contemplation (LLM prompt synthesis):
- The agent queries recent peer dialogue ($\le 10\text{ minutes}$ old, or top-$k$ most recent statements) from episodic memory.
- Injects a formatted `[COMMUNITY DIALOGUE & RECENT PEER STATEMENTS]` section into the LLM system/user prompt.
- Allows the LLM to take peer plans, requests, and warnings into account when formulating daily goals, construction assignments, and resource gathering priorities.

### 5. Turn-Taking, Anti-Looping, and Conversational Cooldowns
To prevent infinite LLM ping-pong chat storms between adjacent agents:
- **Pairwise Cooldown**: Agents enforce a minimum $90\text{ s}$ conversational cooldown before initiating another direct dialogue with the same peer, unless responding to an explicit question or emergency hazard.
- **Context-Driven Discourse**: Autonomous inter-agent chats are stimulated primarily by shared environmental contexts:
  - Evening campfire / tavern social synchronization ([ADR-018](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-018-circadian-shift-work-and-tavern-social-synchronization.md)).
  - Proximity encounters at workstations, clan borders, or construction quadrants.
  - Sighting of high-threat hostiles or scarce resource discoveries.

## Consequences

### Positive
- Human players are no longer spammed by distant agent conversations beyond conversational proximity ($16\text{ m}$).
- Agents gain organic acoustic awareness of their peers within earshot ($20\text{ m}$).
- System 2 planning incorporates real peer intentions and clan needs, eliminating redundant tasks and enabling emergent cooperation.
- Anti-loop cooldowns prevent token exhaustion and repetitive dialogue loops.

### Negative
- Agents beyond $20\text{ m}$ cannot hear verbal chatter without long-range signaling (e.g. bells, horn blasts, or clan runner couriers).
- Slight increase in SQLite episodic memory writes per spoke message.
