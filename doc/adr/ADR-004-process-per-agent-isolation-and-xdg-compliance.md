# ADR-004: Process-Per-Agent Isolation & XDG Base Directory Compliance

## Status
Accepted

## Context
Running dozens of autonomous agents inside a single OS process or single threaded loop presents severe reliability, performance, and failure isolation risks:
- A panic, memory leak, or CPU-intensive thread in one agent would degrade or crash the entire village simulation.
- Agents require dedicated local databases (SQLite), log streams, and live inspection endpoints.
- Filesystem paths hardcoded to arbitrary directories violate UNIX standards and break containerization/multi-user deployments.

## Decision
1. **Process-Per-Agent Architecture**:
   - Each active agent runs as an independent operating system process (`civquest-agent`).
   - A supervisor daemon (`cq daemon`) manages process lifecycles, spawns, clean shutdowns, and restarts.
   - Each agent process exposes a dedicated UNIX domain socket (`$XDG_STATE_HOME/civquest/agents/{name}.sock`) for live status inspections and diagnostics.
2. **XDG Base Directory Compliance**:
   - All paths are resolved strictly through XDG environment variables with standard fallbacks:
     - `$XDG_CONFIG_HOME/civquest/` (`~/.config/civquest`): Identity YAML, global configs, blueprints.
     - `$XDG_DATA_HOME/civquest/` (`~/.local/share/civquest`): SQLite databases, vector stores, textures, cold graveyard archives.
     - `$XDG_STATE_HOME/civquest/` (`~/.local/state/civquest`): PID files, JSONL log streams, UNIX domain sockets.

## Consequences
### Positive
- Total fault isolation: an individual agent crash never affects sibling agents or the Minecraft server.
- Operating system-level resource accounting, cgroup isolation, and CPU core affinity.
- Standardized, predictable filesystem layout adhering to Linux/UNIX desktop and server standards.

### Negative / Trade-offs
- Slight OS process overhead (~15-25MB RSS per Go process) compared to goroutines in a single monolithic binary.
- Requires robust supervisor process monitoring, PID tracking, and dead-worker cleanup.
