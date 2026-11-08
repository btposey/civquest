# Progressive Discovery: Supervisor & Management CLI (`cq`)

## 1. Overview
The `cq` command-line tool (built with `spf13/cobra` in Go) serves as the primary management interface and supervisor daemon for CivQuest.

## 2. Command Tree

```text
cq
├── daemon start | stop | status            # Starts supervisor managing agent OS processes & reproduction
├── agent
│   ├── create --name <n> --clan <c> --role <r> --skin <path>
│   ├── start <name> [--coords <x,y,z>]     # Spawns entity in MC & forks `civquest-agent` process
│   ├── start-clan <clan_id> --count <N>    # Batch spawns a pioneer settlement
│   ├── stop <name>                         # Despawns entity & gracefully stops process
│   ├── status [<name>]                     # Queries $XDG_STATE_HOME/civquest/agents/{name}.sock
│   └── logs <name> [--follow]
├── client
│   └── install [--minecraft-dir <d>] [--mod-jar <j>]   # Installs NeoForge client, stages mod in $XDG_DATA_HOME/civquest/client, adds "CivQuest" launcher profile
├── clan
│   ├── inspect <clan_id>                   # Displays granary surplus, functional zones & active BOMs
│   ├── prestige <clan_id>                  # Prints human Hero Prestige leaderboard
│   └── diplomacy <clan_id>                 # Prints inter-clan affinity matrix & active caravans
├── blueprint
│   ├── list                                # Lists official, player-imported & LLM-generated blueprints
│   ├── validate <file.yaml>
│   └── generate --type <t> --style <s>     # Invokes LLM procedural blueprint generator
└── gateway
    └── test                                # Validates LLM & embedding latency against global.yaml
```

## 3. Supervisor Daemon Responsibilities
1. **Process Supervision**:
   - Spawns independent `civquest-agent` processes.
   - Monitors process health, captures stdout/stderr into structured JSONL logs, and detects unhandled panics.
   - Cleans up dead sockets and stale PID files.
2. **Dynastic Reproduction Governor**:
   - Executes a daily check:
     - Verifies `active_agents < max_active_agents`.
     - Confirms the clan is in `PROSPEROUS SURPLUS` ($>7\text{ days}$ food reserves) with an available bed.
     - Identifies two adult spouses with mutual disposition $\ge 80.0$.
     - Blends parent personality traits with Gaussian perturbation:
       $$\text{Trait}_{\text{child}} = \text{clamp}\left(\frac{\text{Trait}_A + \text{Trait}_B}{2} + \mathcal{N}(0, 0.08^2),\ 0.0,\ 1.0\right)$$
     - Composites child skin, invokes `SpawnAgentEntity(is_child: true)`, and forks the new child agent process.
3. **UNIX Socket IPC**:
   - Each agent process hosts a UNIX domain socket at `$XDG_STATE_HOME/civquest/agents/{name}.sock`.
   - `cq agent status <name>` queries this socket directly to retrieve live memory usage, current utility scores, active Baritone goals, and recent gRPC frame latencies.
