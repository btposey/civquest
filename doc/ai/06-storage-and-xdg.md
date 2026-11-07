# Progressive Discovery: Storage, Databases & XDG Directory Specification

## 1. XDG Base Directory Layout

All CivQuest binaries dynamically resolve paths using standard XDG Base Directory environment variables with automatic fallbacks:

```text
$XDG_CONFIG_HOME/civquest/ (default: ~/.config/civquest)
├── global.yaml                         # Global inference governor, gRPC host, timeouts
├── agents/
│   └── {agent_name}.yaml               # Identity, clan, professions, personality traits
└── blueprints/
    ├── official/                       # Tier 1 & Tier 2 official blueprints
    ├── player_imported/                # Blueprints saved via drafting quill
    └── llm_generated/                  # Procedural designs synthesized by System 2

$XDG_DATA_HOME/civquest/ (default: ~/.local/share/civquest)
├── agents/
│   └── {agent_name}/
│       ├── memory.db                   # Episodic memory (sqlite-vec), relationships, skills
│       ├── map_graph.db                # Discovered topological navigation nodes & edges
│       ├── inventory_cache.json        # Last known inventory & position snapshot
│       └── textures/
│           └── skin.png                # 64x64 Minecraft skin texture
├── clans/
│   └── {clan_id}/
│       ├── civic_registry.db           # Granary ledger, active build projects, functional zones
│       ├── reputation.db               # Human Hero Prestige scores & ledger
│       ├── diplomacy.db                # Inter-clan affinity matrix & treaties
│       ├── landmarks.db                # Named landmarks, coordinates, and lore
│       ├── rumors.db                   # Tavern intelligence & hazard reports
│       └── contracts.db                # Player-agent sharecropping tenancy agreements
└── graveyard/
    └── {agent_name}_{uuid}_tick{N}/    # Cold archive moved upon permadeath
        ├── memory.db                   # Preserved read-only for Chronicler queries
        ├── death_certificate.json      # Cause, killer, coords, final inventory
        └── epitaph.txt

$XDG_STATE_HOME/civquest/ (default: ~/.local/state/civquest)
├── supervisor.pid                      # civctl daemon PID
└── agents/
    └── {agent_name}/
        ├── {agent_name}.log            # Structured JSONL runtime logs
        └── {agent_name}.sock           # UNIX domain socket for live civctl queries
```

## 2. Database Schemas

### 2.1 Per-Agent Episodic Memory (`memory.db`)
Combines SQLite relational tables with the `sqlite-vec` extension for vector RAG:
```sql
CREATE VIRTUAL TABLE IF NOT EXISTS episodic_memories_vec USING vec0(
    memory_id INTEGER PRIMARY KEY,
    embedding FLOAT[768]
);

CREATE TABLE IF NOT EXISTS episodic_memories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    world_tick INTEGER NOT NULL,
    event_category TEXT NOT NULL, -- 'COMBAT', 'TRADE', 'CHAT', 'DEATH', 'DISCOVERY', 'BETRAYAL'
    saliency_score REAL NOT NULL, -- [0.0 - 1.0]
    actors_json TEXT NOT NULL,
    location_coords TEXT NOT NULL,
    summary_text TEXT NOT NULL,
    chronicled BOOLEAN DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS relationships (
    entity_uuid TEXT PRIMARY KEY,
    entity_name TEXT NOT NULL,
    entity_type TEXT CHECK(entity_type IN ('PLAYER', 'SETTLER')),
    disposition_score REAL DEFAULT 0.0, -- [-100.0 to +100.0]
    familiarity REAL DEFAULT 0.0,       -- [0.0 to 1.0]
    kinship_role TEXT DEFAULT 'NONE',   -- 'NONE', 'SPOUSE', 'PARENT', 'CHILD', 'WIDOWED'
    last_interaction_tick INTEGER
);

CREATE TABLE IF NOT EXISTS agent_skills (
    skill_name TEXT PRIMARY KEY,
    category TEXT CHECK(category IN ('PRIMARY', 'SECONDARY_HOBBY')),
    xp INTEGER DEFAULT 0,
    level INTEGER DEFAULT 1,
    workstation_pos TEXT
);
```

### 2.2 Shared Clan Databases (`clans/{clan_id}/*.db`)
- `civic_registry.db`: Tracks granary stock (`settlement_granary`), active construction projects (`construction_projects`), and declared functional zones (`functional_zones`).
- `reputation.db`: Human hero prestige scores (`hero_prestige`) and detailed change ledger (`prestige_ledger`).
- `diplomacy.db`: Inter-clan affinities (`clan_affinities`), active treaties, and caravan trade volumes.
- `landmarks.db`: Cultural toponyms, coordinates, and chronicle lore.
- `contracts.db`: Sharecropping agreements between players and agents with split percentages and chest locations.
