# Project Specification: Autonomous Settlers (`settlers-mc`)

## 0. Agent Directives & Execution Context

You are an autonomous software engineering agent tasked with planning, scaffolding, and implementing **Autonomous Settlers (`settlers-mc`)**—a dual-layer system consisting of a **Minecraft Java Edition (NeoForge 1.21.1+) Server Mod** and an **External Multi-Process Go Agent Orchestrator**.

### Core Objectives
1. **Total Villager & Village Replacement**: Suppress all vanilla `minecraft:village_*` world generation and `minecraft:villager` entity spawns. Replace them with autonomous, humanoid `FakePlayer` avatars driven by external Go processes over bidirectional gRPC streams.
2. **Server-Side FakePlayer Avatars**: Operate using server-side `FakePlayer` instances with offline UUIDs and custom skin textures injected directly into client player-info packets.
3. **Process-Per-Agent Isolation & XDG Compliance**: Never loop through agents on a single thread. Each settler runs as an independent OS process (`settler-agent`) with dedicated state, logs, UNIX domain sockets, and SQLite databases organized strictly under XDG Base Directory specifications.
4. **Dual-Process Cognitive Architecture**:
   - **System 1 (Reactive / Utility AI in Go)**: Sub-$5\text{ ms}$ deterministic execution for survival reflexes, shield blocking, eating, pathing triggers, routine farming, road paving, and idle hobbies. Uses zero LLM tokens.
   - **System 2 (Deliberative LLM Inference)**: Asynchronous calls to an OpenAI-compatible local inference gateway (Ollama / vLLM) using dual-temperature sampling ($T = 0.1$ for structured JSON intents; $T = 0.85 \text{ to } 1.05$ for stochastic dialogue, barter negotiation, diplomatic summits, landmark naming, and book writing).
5. **Permadeath & Generational Dynasties**: Agents never respawn. Self-preservation overrides all routine tasks. Agents reproduce only during village surplus under a strict global inference capacity governor, passing blended personality traits to offspring.

---

## 1. Repository & Monorepo Layout

Initialize the workspace with the following monorepo structure:

```text
settlers-mc/
├── proto/
│   └── settler/
│       └── v1/
│           └── settler.proto           # Master gRPC & Protobuf contract
├── mod-neoforge/                       # Java 21 Gradle Project (NeoForge 1.21.1+)
│   ├── build.gradle
│   ├── gradle.properties
│   └── src/main/
│       ├── java/com/settlers/mod/
│       │   ├── SettlersMod.java
│       │   ├── grpc/                   # gRPC Netty Server & Stream Registry
│       │   ├── entity/                 # SettlerEntity (FakePlayer) & Skin Injector
│       │   ├── worldgen/               # Village Suppressor & Starter Camp Spawner
│       │   ├── navigation/             # Baritone API Wrapper & Caravan Leash Controller
│       │   ├── actions/                # Block Interaction, Builder, Signpost & Book NBT
│       │   ├── recipes/                # Modded Recipe DAG Serializer (JEI/RecipeManager)
│       │   └── client/gui/             # Custom Two-Sided Barter Screen
│       └── resources/
│           ├── META-INF/neoforge.mods.toml
│           └── data/settlers/          # Datapack tags suppressing vanilla villages
├── orchestrator-go/                    # Go 1.23+ Workspace
│   ├── go.mod
│   ├── cmd/
│   │   ├── settlerctl/                 # Management CLI & Supervisor Daemon
│   │   └── settler-agent/              # Single-Agent Autonomous Worker Binary
│   └── internal/
│       ├── xdg/                        # XDG path resolver & cold-archive mover
│       ├── ipc/                        # UNIX domain socket server/client for CLI status
│       ├── db/                         # SQLite + sqlite-vec schemas & queries
│       ├── system1/                    # Behavior Trees, Utility AI, Combat & Needs FSM
│       ├── system2/                    # LLM Client, Prompt Templates & JSON Validators
│       ├── roles/                      # Architect, Chronicler, Tavernkeeper, Caravan, Agrarian
│       └── blueprints/                 # .nbt/.schem parser, YAML compiler & Quadrant Builder
└── configs/
    ├── global.example.yaml
    └── blueprints/
        └── official/                   # Tier 1 & Tier 2 baseline YAML/NBT blueprints
```

---

## 2. XDG Base Directory File System Specification

Every Go binary must resolve paths dynamically via `$XDG_CONFIG_HOME` (default `~/.config`), `$XDG_DATA_HOME` (default `~/.local/share`), and `$XDG_STATE_HOME` (default `~/.local/state`).

```text
$XDG_CONFIG_HOME/settlers/
├── global.yaml                         # Inference governor, MC server gRPC address, defaults
├── agents/
│   └── {agent_name}.yaml               # Identity, clan, starting role, personality weights
└── blueprints/
    ├── official/                       # Built-in Tier 1 & Tier 2 structure definitions
    ├── player_imported/                # Saved in-game via Architect's Drafting Quill
    └── llm_generated/                  # Procedurally synthesized JSON/YAML recipes

$XDG_DATA_HOME/settlers/
├── agents/
│   └── {agent_name}/
│       ├── memory.db                   # Episodic memory (sqlite-vec), relationships, skills
│       ├── map_graph.db                # Discovered topological nodes & edges
│       ├── inventory_cache.json        # Last known inventory, vitals, and position
│       └── textures/
│           └── skin.png                # 64x64 Minecraft player skin texture
├── clans/
│   └── {clan_id}/
│       ├── civic_registry.db           # Granary ledger, active build sites, functional zones
│       ├── reputation.db               # Human Hero Prestige ledger & kinship tree
│       ├── diplomacy.db                # Inter-clan affinity matrix & grievances
│       ├── landmarks.db                # Named regions, battlefields, structures & signposts
│       ├── rumors.db                   # Tavernkeeper intelligence & hazard reports
│       └── contracts.db                # Player-agent sharecropping tenancy agreements
└── graveyard/
    └── {agent_name}_{uuid}_tick{N}/    # Cold archive moved upon permadeath
        ├── memory.db                   # Preserved read-only for Chronicler queries
        ├── death_certificate.json      # Cause, killer, coords, final inventory
        └── epitaph.txt

$XDG_STATE_HOME/settlers/
├── supervisor.pid
└── agents/
    └── {agent_name}/
        ├── {agent_name}.log            # Structured JSONL runtime logs
        └── {agent_name}.sock           # UNIX domain socket for live `settlerctl` inspection
```

### Configuration File Schemas

#### `$XDG_CONFIG_HOME/settlers/global.yaml`
```yaml
minecraft_server:
  grpc_address: "127.0.0.1:50051"
  reconnect_backoff_ms: 2000

inference_gateway:
  provider: "openai_compatible" # Ollama or vLLM
  base_url: "http://127.0.0.1:11434/v1"
  default_model: "qwen2.5:7b-instruct-q4_K_M"
  embedding_model: "nomic-embed-text"
  timeout_seconds: 30

inference_governor:
  max_active_agents: 16
  max_concurrent_llm_requests: 4
  reserve_questor_slots: 4
  allow_reproduction: true
  min_surplus_food_days: 7
```

#### `$XDG_CONFIG_HOME/settlers/agents/{agent_name}.yaml`
```yaml
id: "settler_01j7k9m2p"
name: "Kaelen"
clan_id: "ironvein"
skin_path: "textures/skin.png"
professions:
  primary: "ARCHITECT"         # FARMER, BLACKSMITH, GUARD, MINER, CHRONICLER, ARCHITECT, TAVERNKEEPER, CARAVAN_MASTER, VINTNER, RANCHER, CHEF
  secondary: "JAM_MAKER"       # APIARIST, JAM_MAKER, CIDER_BREWER, CHANDLER, MYCOLOGIST (or "UNASSIGNED" for emergent discovery)
personality:
  bravery: 0.75                # [0.0 - 1.0] Influences fight-or-flight confidence
  cooperativeness: 0.80        # [0.0 - 1.0] Influences trade generosity & party aid
  curiosity: 0.65              # [0.0 - 1.0] Influences cartography & recipe experimentation
  frugality: 0.40              # [0.0 - 1.0] Influences barter pricing
  humor: 0.50                  # [0.0 - 1.0] Influences dialogue tone
  quirks: ["SUPERSTITIOUS", "SENTIMENTAL"]
  system_prompt: |
    You are Kaelen, Master Architect of Clan Ironvein. You take immense pride in stone masonry
    and secretly love stewing sweet berry jams during quiet afternoons.
```

---

## 3. Master gRPC & Protobuf Contract (`settler.proto`)

Compile this unified protocol definition for both Java (`io.grpc`) and Go (`google.golang.org/grpc`).

```protobuf
syntax = "proto3";

package settler.v1;
option go_package = "github.com/settlers-mc/orchestrator-go/internal/proto/v1;settlerv1";
option java_package = "com.settlers.mod.grpc.v1";
option java_multiple_files = true;

service SettlerBridgeService {
  // Primary per-agent bidirectional control and telemetry stream
  rpc ConnectAgent(stream AgentIntent) returns (stream TelemetryFrame);

  // Administrative RPCs called by `settlerctl` supervisor
  rpc SpawnAgentEntity(SpawnAgentRequest) returns (SpawnAgentResponse);
  rpc DespawnAgentEntity(DespawnAgentRequest) returns (DespawnAgentResponse);
  rpc ExportRecipeGraph(ExportRecipeRequest) returns (ExportRecipeResponse);
}

// ==================== COMMON PRIMITIVES ====================

message Vec3 {
  double x = 1;
  double y = 2;
  double z = 3;
}

message BlockPosProto {
  int32 x = 1;
  int32 y = 2;
  int32 z = 3;
}

message BoundingBoxProto {
  BlockPosProto min = 1;
  BlockPosProto max = 2;
}

message ItemStackProto {
  string item_id = 1;           // e.g., "minecraft:iron_ingot", "farmersdelight:tomato_soup"
  int32 count = 2;
  int32 slot = 3;
  int32 durability = 4;
  int32 max_durability = 5;
  string custom_name = 6;
}

// ==================== ADMINISTRATIVE RPCS ====================

message SpawnAgentRequest {
  string agent_id = 1;
  string display_name = 2;
  string clan_id = 3;
  Vec3 spawn_coords = 4;
  bytes skin_png_bytes = 5;
  string skin_ texture_value = 6;
  string skin_signature = 7;
  bool is_child = 8;
}

message SpawnAgentResponse {
  bool success = 1;
  int32 minecraft_entity_id = 2;
  string offline_uuid = 3;
  string error_message = 4;
}

message DespawnAgentRequest {
  string agent_id = 1;
  string reason = 2;
}

message DespawnAgentResponse {
  bool success = 1;
}

message ExportRecipeRequest {}

message RecipeNode {
  string recipe_id = 1;
  string recipe_type = 2;       // e.g., "minecraft:crafting_shaped", "farmersdelight:cooking", "vinery:fermentation"
  string workstation_block = 3; // e.g., "minecraft:crafting_table", "farmersdelight:cooking_pot"
  repeated ItemStackProto ingredients = 4;
  ItemStackProto result = 5;
}

message ExportRecipeResponse {
  repeated RecipeNode recipes = 1;
}

// ==================== TELEMETRY (Server -> Agent @ 5 Hz) ====================

message TelemetryFrame {
  int64 world_tick = 1;
  int64 time_of_day = 2;        // 0 - 24000
  string weather = 3;           // "CLEAR", "RAIN", "THUNDER"
  string biome_id = 4;
  AgentStatus status = 5;
  repeated VisibleEntity visible_entities = 6;
  repeated VisibleBlock interesting_blocks = 7;
  repeated ChatEvent incoming_chats = 8;
  DamageEvent last_damage_taken = 9;
  TradeRequest pending_trade = 10;
  FarmlandSurveyEvent farmland_survey = 11;
  LandmarkContext current_landmark = 12;
  DeathEvent death_event = 13;  // Terminal event triggering permadeath teardown
}

message AgentStatus {
  string agent_id = 1;
  int32 entity_id = 2;
  Vec3 position = 3;
  float yaw = 4;
  float pitch = 5;
  float health = 6;
  float max_health = 7;
  int32 food_level = 8;         // 0 - 20
  float saturation = 9;
  bool is_sleeping = 10;
  bool is_in_water = 11;
  bool is_on_fire = 12;
  string navigation_state = 13; // "IDLE", "PATHING", "STUCK", "ARRIVED", "BUILDING"
  repeated ItemStackProto inventory = 14;
  repeated int32 leashed_entity_ids = 15;
}

message VisibleEntity {
  int32 entity_id = 1;
  string uuid = 2;
  string entity_type = 3;       // e.g., "minecraft:player", "minecraft:zombie", "minecraft:mule"
  string display_name = 4;
  Vec3 position = 5;
  float distance = 6;
  float health = 7;
  bool is_hostile = 8;
  bool is_tamed = 9;
  string held_item_id = 10;
  int32 fuse_ticks = 11;        // > 0 if Creeper is hissing
}

message VisibleBlock {
  BlockPosProto pos = 1;
  string block_id = 2;
  map<string, string> block_state_props = 3; // e.g., {"age": "7", "honey_level": "5"}
}

message ChatEvent {
  string sender_uuid = 1;
  string sender_name = 2;
  string message = 3;
  float distance = 4;
}

message DamageEvent {
  string source_type = 1;       // "mob", "player", "arrow", "fall", "explosion", "lava"
  int32 attacker_entity_id = 2;
  string attacker_uuid = 3;
  string attacker_name = 4;
  float damage_amount = 5;
}

message TradeRequest {
  int32 player_entity_id = 1;
  string player_uuid = 2;
  string player_name = 3;
  repeated ItemStackProto player_offered_items = 4;
  string spoken_proposal = 5;
}

message FarmlandSurveyEvent {
  BoundingBoxProto bounds = 1;
  string primary_crop_id = 2;
  int32 mature_crop_count = 3;
  int32 total_farmland_blocks = 4;
  string inferred_owner_uuid = 5;
}

message LandmarkContext {
  string landmark_id = 1;
  string display_name = 2;
  string category = 3;
}

message DeathEvent {
  string agent_id = 1;
  int64 world_tick = 2;
  Vec3 death_location = 3;
  string damage_source = 4;
  int32 killer_entity_id = 5;
  string killer_uuid = 6;
  string killer_display_name = 7;
  repeated ItemStackProto dropped_loot = 8;
}

// ==================== INTENTS (Agent -> Server) ====================

message AgentIntent {
  string agent_id = 1;
  int64 sequence_id = 2;
  oneof action {
    NavigateIntent navigate = 3;
    AttackIntent attack = 4;
    InteractBlockIntent interact_block = 5;
    UseItemIntent use_item = 6;
    CraftItemIntent craft_item = 7;
    SendChatIntent chat = 8;
    RespondTradeIntent trade_response = 9;
    CaravanIntent caravan = 10;
    BuildBlockIntent build_block = 11;
    CreateWrittenBookIntent create_book = 12;
    PlaceSignpostIntent place_signpost = 13;
    TransferContainerIntent transfer_container = 14;
  }
}

message NavigateIntent {
  Vec3 target = 1;
  float speed_multiplier = 2;
  bool sprint = 3;
  bool allow_break_blocks = 4;
  bool allow_place_scaffolding = 5;
  int32 arrival_tolerance = 6;
}

message AttackIntent {
  int32 target_entity_id = 1;
  bool raise_shield = 2;
  bool keep_distance_kite = 3;
  bool flee_immediately = 4;
}

message InteractBlockIntent {
  BlockPosProto pos = 1;
  enum InteractionType {
    HARVEST_BREAK = 0;
    PLACE_BLOCK = 1;
    SLEEP_IN_BED = 2;
    WAKE_UP = 3;
    USE_WORKSTATION = 4;
    RING_BELL = 5;
    SHEAR_OR_BOTTLE_BEEHIVE = 6;
    PAVE_ROAD_PATH = 7;
  }
  InteractionType interaction_type = 2;
  string block_or_item_id = 3;
}

message UseItemIntent {
  int32 inventory_slot = 1;
  bool is_eating_or_drinking = 2;
  bool equip_armor_or_offhand = 3;
}

message CraftItemIntent {
  string recipe_id = 1;
  BlockPosProto workstation_pos = 2;
  int32 batch_count = 3;
}

message SendChatIntent {
  string message = 1;
  string target_channel = 2;    // "LOCAL", "CLAN_BROADCAST"
}

message RespondTradeIntent {
  int32 player_entity_id = 1;
  bool accept_deal = 2;
  repeated ItemStackProto agent_counter_offer = 3;
  repeated ItemStackProto demanded_from_player = 4;
  string spoken_response = 5;
}

message CaravanIntent {
  enum CaravanAction {
    LEASH_BEAST = 0;
    UNLEASH_BEAST = 1;
    HITCH_TO_FENCE = 2;
    LOAD_BEAST_CHEST = 3;
    UNLOAD_BEAST_CHEST = 4;
  }
  CaravanAction action = 1;
  int32 beast_entity_id = 2;
  BlockPosProto fence_pos = 3;
  repeated ItemStackProto cargo_items = 4;
}

message BuildBlockIntent {
  BlockPosProto target_voxel = 1;
  string block_state_string = 2; // e.g., "minecraft:stone_brick_stairs[facing=north,half=bottom]"
  bool is_temporary_scaffold = 3;
}

message CreateWrittenBookIntent {
  string title = 1;             // Max 32 chars
  string author = 2;
  repeated string pages = 3;    // Max 100 pages, <= 250 chars & <= 14 lines per page
  BlockPosProto target_lectern_or_shelf = 4;
  int32 bookshelf_slot = 5;     // 0 - 5 for chiseled bookshelf
}

message PlaceSignpostIntent {
  BlockPosProto pos = 1;
  bool is_hanging_sign = 2;
  string line1 = 3;             // Max 15 chars
  string line2 = 4;
  string line3 = 5;
  string line4 = 6;
}

message TransferContainerIntent {
  BlockPosProto container_pos = 1;
  bool deposit_into_container = 2; // true = deposit, false = withdraw
  repeated ItemStackProto items = 3;
}
```

---

## 4. Relational & Vector Database Schemas (SQLite + `sqlite-vec`)

Implement automatic migration runners in `orchestrator-go/internal/db/migrations.go`.

### 4.1 Per-Agent Database (`$XDG_DATA_HOME/settlers/agents/{name}/memory.db`)

```sql
-- Requires sqlite-vec extension loaded for episodic vector RAG
CREATE VIRTUAL TABLE IF NOT EXISTS episodic_memories_vec USING vec0(
    memory_id INTEGER PRIMARY KEY,
    embedding FLOAT[768]
);

CREATE TABLE IF NOT EXISTS episodic_memories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    world_tick INTEGER NOT NULL,
    event_category TEXT NOT NULL, -- 'COMBAT', 'TRADE', 'CHAT', 'DEATH', 'DISCOVERY', 'BETRAYAL'
    saliency_score REAL NOT NULL, -- [0.0 - 1.0]
    actors_json TEXT NOT NULL,    -- JSON array of involved names/UUIDs
    location_coords TEXT NOT NULL,-- JSON [x, y, z]
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
    workstation_pos TEXT                -- JSON [x, y, z]
);
```

### 4.2 Per-Agent Cartography (`$XDG_DATA_HOME/settlers/agents/{name}/map_graph.db`)

```sql
CREATE TABLE IF NOT EXISTS map_nodes (
    node_id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    node_type TEXT CHECK(node_type IN ('SETTLEMENT_CORE', 'GATE', 'TAVERN', 'RESOURCE_PATCH', 'FORD', 'HAZARD', 'FOREIGN_CLAN')),
    x INTEGER NOT NULL,
    y INTEGER NOT NULL,
    z INTEGER NOT NULL,
    safety_score REAL DEFAULT 1.0,      -- [-1.0 (Deadly) to +1.0 (Safe)]
    last_visited_tick INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS map_edges (
    from_node_id TEXT NOT NULL,
    to_node_id TEXT NOT NULL,
    distance REAL NOT NULL,
    is_paved BOOLEAN DEFAULT FALSE,
    pack_beast_traversable BOOLEAN DEFAULT TRUE,
    hazard_notes TEXT,
    PRIMARY KEY (from_node_id, to_node_id)
);
```

### 4.3 Shared Clan Databases (`$XDG_DATA_HOME/settlers/clans/{clan_id}/*.db`)

```sql
-- civic_registry.db
CREATE TABLE IF NOT EXISTS settlement_granary (
    item_id TEXT PRIMARY KEY,
    quantity INTEGER NOT NULL DEFAULT 0,
    daily_burn_rate REAL DEFAULT 1.0,
    category TEXT CHECK(category IN ('STAPLE_FOOD', 'LUXURY_FOOD', 'BEVERAGE', 'RAW_MATERIAL', 'TOOL', 'WEAPON'))
);

CREATE TABLE IF NOT EXISTS construction_projects (
    project_id TEXT PRIMARY KEY,
    blueprint_id TEXT NOT NULL,
    foreman_agent_id TEXT NOT NULL,
    origin_x INTEGER NOT NULL,
    origin_y INTEGER NOT NULL,
    origin_z INTEGER NOT NULL,
    rotation INTEGER DEFAULT 0,
    total_voxels INTEGER NOT NULL,
    placed_voxels INTEGER DEFAULT 0,
    remaining_bom_json TEXT NOT NULL,   -- Map of item_id -> missing count
    status TEXT CHECK(status IN ('SURVEYING', 'AWAITING_BOM', 'CLEARING', 'BUILDING', 'COMPLETED'))
);

CREATE TABLE IF NOT EXISTS functional_zones (
    zone_id TEXT PRIMARY KEY,
    zone_type TEXT CHECK(zone_type IN ('TAVERN', 'GRANARY', 'MARKET', 'STABLES', 'DOCKS', 'WAREHOUSE', 'MONUMENT', 'CEMETERY')),
    min_x INTEGER, min_y INTEGER, min_z INTEGER,
    max_x INTEGER, max_y INTEGER, max_z INTEGER,
    tier INTEGER DEFAULT 1,
    metadata_json TEXT
);

-- reputation.db
CREATE TABLE IF NOT EXISTS hero_prestige (
    player_uuid TEXT PRIMARY KEY,
    player_name TEXT NOT NULL,
    prestige_score REAL DEFAULT 0.0,    -- [-100.0 (KOS Outlaw) to +100.0 (Exalted Folk Hero)]
    title TEXT DEFAULT 'Outlander',     -- 'Outlaw', 'Outlander', 'Trusted', 'Champion', 'Folk Hero'
    last_seen_tick INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS prestige_ledger (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    player_uuid TEXT NOT NULL,
    delta REAL NOT NULL,
    reason TEXT NOT NULL,               -- 'GIFT_HIGH_UTILITY', 'BOM_CONTRIBUTION', 'COMBAT_RESCUE', 'COMPANION_DEATH', 'THEFT'
    world_tick INTEGER NOT NULL
);

-- diplomacy.db
CREATE TABLE IF NOT EXISTS clan_affinities (
    target_clan_id TEXT PRIMARY KEY,
    affinity_score REAL DEFAULT 0.0,    -- [-100.0 (Blood War) to +100.0 (Allied Guild)]
    trade_volume_count INTEGER DEFAULT 0,
    active_treaty TEXT DEFAULT 'NONE',
    last_caravan_tick INTEGER
);

-- landmarks.db
CREATE TABLE IF NOT EXISTS landmarks (
    landmark_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    cultural_epithet TEXT,
    category TEXT CHECK(category IN ('TAVERN', 'RIVER', 'PEAK', 'RAVINE', 'BATTLEFIELD', 'BRIDGE', 'MEMORIAL')),
    center_x INTEGER NOT NULL,
    center_y INTEGER NOT NULL,
    center_z INTEGER NOT NULL,
    radius REAL DEFAULT 24.0,
    chronicle_lore TEXT NOT NULL,
    signpost_coords TEXT                -- JSON [x, y, z]
);

-- contracts.db
CREATE TABLE IF NOT EXISTS sharecropping_contracts (
    contract_id TEXT PRIMARY KEY,
    landowner_player_uuid TEXT NOT NULL,
    tenant_agent_id TEXT NOT NULL,
    crop_or_livestock_focus TEXT NOT NULL,
    owner_split_pct INTEGER DEFAULT 50,
    tenant_split_pct INTEGER DEFAULT 50,
    owner_chest_coords TEXT NOT NULL,   -- JSON [x, y, z]
    tenant_chest_coords TEXT NOT NULL,  -- JSON [x, y, z]
    bounds_json TEXT NOT NULL,          -- JSON [min_x, min_y, min_z, max_x, max_y, max_z]
    active BOOLEAN DEFAULT TRUE
);
```

---

## 5. Component 1: NeoForge Server Mod (`mod-neoforge`)

### 5.1 WorldGen Suppression & Entity Interception
1. **Datapack Override**: Place a structure set override in `src/main/resources/data/minecraft/worldgen/structure_set/villages.json` with `"structures": []` so vanilla villages never generate.
2. **Event Interceptor**: Subscribe to `EntityJoinLevelEvent`. If `event.getEntity() instanceof Villager` or `WanderingTrader`, call `event.setCanceled(true)`.
3. **Starter Camp Generation**: Register a custom Jigsaw structure (`settlers:pioneer_campfire`) that spawns basic campfires, supply crates, and a clan banner where initial agents can be spawned via `settlerctl`.

### 5.2 `SettlerEntity` (`FakePlayer` Subclass) & Offline Skin Rendering
1. Extend `net.neoforged.neoforge.common.util.FakePlayer` (or `ServerPlayer` with a dummy `ServerGamePacketListenerImpl` attached to an embedded Netty `Connection` so physics, knockback, hunger, and item use tick identically to real players).
2. **Tick Lifecycle**: Override `ServerLevel` entity ticking so `SettlerEntity.tick()` executes every server tick (20 Hz):
   - Tick `FoodData` (hunger decay during movement/combat).
   - Tick active `ItemUse` (eating food, drawing bows, raising shields).
   - Tick Baritone pathing controller.
3. **Skin Injection**:
   - Create an offline `UUID` via `UUIDUtil.createOfflinePlayerUUID(displayName)`.
   - Attach signed texture properties (`new Property("textures", value, signature)`) or serve custom PNG skins via a tiny client-side mixin on `PlayerSkin` that loads raw PNG bytes broadcast in a custom NeoForge payload `ClientboundSettlerSkinPacket`.
   - Broadcast `ClientboundPlayerInfoUpdatePacket` (`ADD_PLAYER`) to all connected human players.

### 5.3 Sensory Frustum & Telemetry Collector (5 Hz)
Every 4 server ticks (5 Hz), or immediately upon `LivingDamageEvent` or `PlayerDeathEvent`, build and stream `TelemetryFrame`:
- Scan blocks in a $16\text{-block}$ radius around the agent matching tag filters (`#settlers:interesting_blocks`: mature crops, ores, beds, beehives, chests, lecterns, workstations, fire/lava).
- Scan entities within $24\text{ blocks}$ with line-of-sight checks (`ClipContext`).
- Detect cultivated player farmland (hydrated `FarmlandBlock` + fences) and resolve the likely owner from a chunk block-placement tracker.

### 5.4 Action Execution & Baritone Integration
- **Movement (`NavigateIntent`)**: Delegate to Baritone's `ICustomGoalProcess` (`GoalNear` or `GoalBlock`). Enable `allowBreak` and `allowPlace` settings dynamically based on the intent flags.
- **Road Paving (`PAVE_ROAD_PATH`)**: When traversing unpaved grass/dirt along a frequent topological edge, replace `minecraft:grass_block` beneath the agent's feet with `minecraft:dirt_path` or `minecraft:gravel`.
- **Written Books (`CreateWrittenBookIntent`)**: Construct an `ItemStack(Items.WRITTEN_BOOK)`, populate `DataComponents.WRITTEN_BOOK_CONTENT` with resolved `Component.literal(page)` entries, and insert into the target `LecternBlockEntity` or `ChiseledBookShelfBlockEntity`.
- **Signposts (`PlaceSignpostIntent`)**: Place `Blocks.OAK_SIGN` or `Blocks.OAK_HANGING_SIGN` and apply `SignText` lines $0\text{--}3$.

---

## 6. Component 2: Go Agent Runtime (`orchestrator-go`)

### 6.1 System 1: Reactive Utility & Survival Engine (`internal/system1/`)
System 1 executes on every incoming `TelemetryFrame` ($<5\text{ ms}$) without invoking LLM APIs.

#### Fight-or-Flight Confidence Formula
When hostile entities are within $R = 16$ or `last_damage_taken != nil`, compute combat confidence:

$$\text{Confidence} = \frac{\text{HP}_{\text{self}} + \sum_{i \in \text{Allies}} \text{HP}_i}{\sum_{j \in \text{Hostiles}} \text{Threat}_j} \times M_{\text{gear}} \times (0.5 + \text{Bravery}_{\text{trait}})$$

Where:
- $\text{Threat}_j$: Zombie $= 15$, Skeleton $= 20$, Creeper $= 35$, Pillager $= 25$, Warden $= 500$.
- $M_{\text{gear}} \in [0.5, 2.5]$ based on equipped weapon damage, armor toughness, and off-hand shield presence.

**Permadeath Override Rules**:
1. If $\text{HP}_{\text{self}} \le 0.35 \times \text{HP}_{\text{max}}$ OR $\text{Confidence} < 1.0$ OR a Creeper has $\text{fuse\_ticks} > 0$ within $5\text{ blocks}$:
   - Enter `SURVIVAL_PANIC` immediately.
   - Raise shield, sprint-flee toward the highest-safety node in `map_graph.db`, eat highest-saturation food or Golden Apple on the run, and broadcast a distress message on `CLAN_BROADCAST`.
   - If currently escorting a human player on a quest, **abandon the quest immediately** and shout a warning in local chat.
2. If $\text{Confidence} \ge 1.0$ and $\text{HP}_{\text{self}} > 0.35 \times \text{HP}_{\text{max}}$:
   - Coordinate target focus (attack the hostile targeting the lowest-HP ally). Melee units close distance; ranged units kite.

#### Daily Routine & Dual-Career State Machine
Evaluate utility scores $U \in [0.0, 1.0]$ across four time-of-day brackets:
- **Emergency ($U = 1.0$)**: Combat panic, extinguishing fire, eating when `food_level <= 6`.
- **Primary Duty (Ticks $1000\text{--}8000$)**: Execute primary role tasks. If all primary tasks are blocked (e.g., Architect waiting on BOM materials, Guard on off-shift with no threats), transition immediately to Secondary Hobby.
- **Secondary Skill / Hobby (Ticks $8000\text{--}11000$ or Idle Standby)**:
  - Scan `interesting_blocks` within $R = 48$ for hobby feedstocks (e.g., `honey_level == 5` beehives for `APIARIST`, mature berry bushes for `JAM_MAKER`, apples/barrels for `CIDER_BREWER`).
  - If Secondary Skill is `UNASSIGNED`, evaluate abundant local feedstocks against personality traits and permanently unlock an emergent hobby.
- **Tavern Social & Sleep (Ticks $11000\text{--}24000$)**:
  - Navigate to the settlement Tavern (`functional_zones WHERE zone_type = 'TAVERN'`).
  - Deposit surplus hobby crafts (jams, mead, candles), exchange map nodes (`map_graph.db` merge), share rumors, and sleep in assigned bed before midnight.

---

### 6.2 System 2: Deliberative LLM Pipelines (`internal/system2/`)
All System 2 invocations run asynchronously in a worker goroutine pool bounded by `max_concurrent_llm_requests`. Every call uses structured JSON schemas (`response_format: { type: "json_object" }`).

#### Pipeline A: Dialogue, Trade Barter & Hero Delegation ($T = 0.85$)
Triggered by `IncomingChats` or `PendingTrade`.
- **Context Injected**: Agent persona, mood quirks, current health/hunger, Village Economic State (`DEFICIT`, `STABLE`, `SURPLUS`), speaker's `prestige_score`, recent episodic memories retrieved via `sqlite-vec` cosine similarity, and marginal utility of offered items.
- **Stochastic Disobedience Check**: If a human player with `prestige_score >= 50.0` requests an escort or task, check:
  1. Village state MUST be `PROSPEROUS SURPLUS` and agent MUST NOT be in an inelastic sustainment role (sole farmer/baker).
  2. If the command involves lethal danger (e.g., Nether, Warden, low HP/gear) and a stochastic roll $\text{Rand}(0,1) > \text{Bravery}$, the agent refuses in-character.
- **JSON Output Schema**:
  ```json
  {
    "spoken_reply": "string",
    "trade_decision": "ACCEPT | COUNTER | REJECT | NONE",
    "counter_offer_items": [{"item_id": "string", "count": 0}],
    "delegated_task": {
      "task_type": "NONE | ESCORT_PLAYER | GATHER_RESOURCE | SHARE_CROP_PROPOSAL",
      "target_block_or_item": "string",
      "quota": 0
    },
    "prestige_delta": 0.0,
    "memory_summary": "string"
  }
  ```

#### Pipeline B: The Clan Chronicler & Book Materializer ($T = 0.90$)
Triggered at dusk when the `CHRONICLER` agent is seated at a library desk and `episodic_memories` contains unchronicled events with `saliency_score >= 0.70` (or `1.0` for any permadeath event).
- **Pagination Enforcer**: Pass LLM prose through `PaginateProse(title, author, rawText)` enforcing strict Minecraft limits:
  - Title: $\le 32\text{ characters}$.
  - Per Page: $\le 14\text{ lines}$, $\le 19\text{ characters per line}$, $\le 250\text{ characters total}$.
  - Dispatch `CreateWrittenBookIntent` targeting an empty Lectern or Chiseled Bookshelf slot in the library.

#### Pipeline C: Toponym & Landmark Naming Engine ($T = 0.85$)
Triggered when a scout discovers a major geographical feature, a Tier 2 civic building is consecrated, or a clan member dies in battle.
- Generates `display_name` ($\le 24\text{ chars}$), `sign_line1` and `sign_line2` ($\le 15\text{ chars}$ each), `cultural_epithet`, and `chronicle_lore`.
- Writes to `landmarks.db` and dispatches `PlaceSignpostIntent` to erect a physical sign at the coordinates.

#### Pipeline D: Architect & Collaborative Quadrant Construction ($T = 0.20$)
1. **Tier 1 (Universal Settler Builds)**: Any settler can construct dirt/gravel roads, $5 \times 5$ cabins, $9 \times 9$ fenced crop plots, and simple workstation lean-tos without an Architect.
2. **Tier 2 (Architect-Engineered)**:
   - Requires Village State `== PROSPEROUS SURPLUS`.
   - Architect parses blueprint from `$XDG_CONFIG_HOME/settlers/blueprints/` (or synthesizes a new semantic layout via LLM grammar compilation).
   - Computes Bill of Materials (BOM) against `settlement_granary`. Missing items are posted as gathering tasks for settlers and Hero Prestige contracts for players.
   - **Spatial Quadrant Partitioning**: Subdivides the footprint into 4 quadrants ($NW, NE, SW, SE$) and builds strictly bottom-up ($Y_{\text{min}} \to Y_{\text{max}}$) so assigned builders never collide or trap each other in scaffolding.
   - Upon completion, registers the bounding box in `functional_zones` (unlocking Tavern beds/rumors, Granary spoilage reduction, Market stalls, Stables, or Docks).

---

### 6.3 Inter-Clan Caravans, Agrarian Specialization & Sharecropping

#### Emergent Food Crafting & Sharecropping
- On startup, the Go orchestrator calls `ExportRecipeGraph` to load all vanilla and modded recipes (*Farmer's Delight*, *Vinery*, *Brewin' and Chewin'*).
- When a settler discovers wild seeds (e.g., grapes, tomatoes, rice) or untamed cattle, they evaluate recipe chains and can specialize into a `VINTNER`, `RANCHER`, or `CHEF`.
- If an agent encounters player farmland (`FarmlandSurveyEvent`), they approach the player and propose a sharecropping tenancy in chat. If accepted, they record the agreement in `contracts.db`, harvest/replant/cook modded meals daily, and split outputs into `owner_chest_coords` and `tenant_chest_coords`.

#### Beast-of-Burden Trade Caravans
- When Clan A has $>10\text{ days}$ surplus of an item and `diplomacy.db` shows Clan B has a deficit and `affinity_score >= -20.0`, the `CARAVAN_MASTER` leashes $1\text{--}2$ Mules/Donkeys/Llamas (`CaravanIntent: LEASH_BEAST`), loads their chests, recruits $1\text{--}2$ Guard agents (or hires a Hero Player), and traverses paved edges in `map_graph.db` to Clan B's Tavern/Market.
- Upon arrival, System 2 negotiates the exchange, updates `clan_affinities` ($+3.0$ on fair trade), exchanges Tavern rumors (`rumors.db`), and journeys home.

---

### 6.4 Permadeath Teardown & Dynastic Reproduction

#### Permadeath Teardown Sequence
When `TelemetryFrame.death_event != nil` is received:
1. Log the death details and write a high-saliency ($1.0$) entry into the clan's shared chronicler queue and surviving family members' `memory.db` (updating spouse status to `WIDOWED`).
2. If the agent died while escorting a player, deduct $-50.0$ from the player's `hero_prestige`. If murdered directly by a player, lock the player's `hero_prestige` to $-100.0$ (`Outlaw` / Kill-on-Sight across the clan and allied clans).
3. Close the gRPC stream and UNIX socket.
4. Move `$XDG_DATA_HOME/settlers/agents/{name}/` to `$XDG_DATA_HOME/settlers/graveyard/{name}_{uuid}_tick{N}/` with `death_certificate.json`.
5. Exit the Go process with code `0` so `settlerctl` reclaims the inference slot.

#### Dynastic Reproduction Governor
Checked once per in-game day by the `settlerctl` supervisor:
1. Verify `active_agents < max_active_agents` in `global.yaml`.
2. Verify clan is in `PROSPEROUS SURPLUS` ($>7\text{ days}$ food reserve) and an unclaimed bed exists in the settlement.
3. Find two adult agents with mutual `disposition_score >= 80.0` and `kinship_role == 'SPOUSE'`.
4. Generate a new child agent YAML config blending parent personality weights with Gaussian noise:

$$\text{Trait}_{\text{child}} = \text{clamp}\left(\frac{\text{Trait}_A + \text{Trait}_B}{2} + \mathcal{N}(0, 0.08^2),\ 0.0,\ 1.0\right)$$

5. Composite parent skin PNGs (or pick a clan child skin), call `SpawnAgentEntity(is_child: true)`, and launch a new `settler-agent` process.

---

## 7. Component 3: Management CLI (`settlerctl`)

Implement `settlerctl` using `spf13/cobra` with the following command tree:

```text
settlerctl
├── daemon start | stop | status            # Starts supervisor managing agent OS processes & reproduction
├── agent
│   ├── create --name <n> --clan <c> --role <r> --skin <path>
│   ├── start <name> [--coords <x,y,z>]     # Spawns entity in MC & forks `settler-agent` process
│   ├── start-clan <clan_id> --count <N>    # Batch spawns a pioneer settlement
│   ├── stop <name>                         # Despawns entity & gracefully stops process
│   ├── status [<name>]                     # Queries $XDG_STATE_HOME/settlers/agents/{name}.sock
│   └── logs <name> [--follow]
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

---

## 8. Phased Implementation Plan & Acceptance Criteria

Execute the implementation in the following sequential phases. Do not advance to the next phase until all verification criteria pass.

### Phase 1: Protobuf Contract, XDG Scaffolding & Database Layer
- **Tasks**:
  1. Generate Go and Java gRPC/Protobuf code from `proto/settler/v1/settler.proto`.
  2. Implement `internal/xdg` path resolution and SQLite connection pools (with `sqlite-vec` integration) in `internal/db`.
  3. Build `settlerctl agent create` and `settlerctl gateway test`.
- **Verification**: Run `go test ./internal/...` to verify XDG directory creation, SQLite table migrations, vector insert/query roundtrips, and YAML serialization.

### Phase 2: NeoForge Bridge Mod, WorldGen Override & FakePlayer Puppet
- **Tasks**:
  1. Scaffold `mod-neoforge` for Minecraft 1.21.1+ with embedded Netty gRPC server on `:50051`.
  2. Implement vanilla village datapack suppression and `Villager` spawn cancellation.
  3. Implement `SettlerEntity` (`FakePlayer`), offline UUID generation, custom skin packet broadcasting, 5 Hz `TelemetryFrame` streaming, and Baritone navigation integration.
- **Verification**: Start NeoForge server, run `settlerctl agent start kaelen --coords "0,70,0"`, verify entity spawns in-game with custom skin, receives 5 Hz telemetry frames, and walks to a target coordinate via `NavigateIntent`.

### Phase 3: System 1 Reflexes, Biological Needs, Combat & Permadeath
- **Tasks**:
  1. Implement `internal/system1` utility evaluator: hunger consumption, bed finding/sleeping, road paving, and the Fight-or-Flight confidence formula.
  2. Implement the Permadeath teardown handler (`DeathEvent` -> graveyard cold archive + tombstone signpost placement + process exit).
- **Verification**: Spawn an agent with low food; verify it harvests and eats bread. Spawn 3 Zombies near an unarmored agent; verify $\text{Confidence} < 1.0$ triggers sprint-flee and shield raise. Kill an agent; verify it never respawns, its folder moves to `$XDG_DATA_HOME/settlers/graveyard/`, and its process exits cleanly.

### Phase 4: System 2 LLM Integration, Barter GUI, Hero Prestige & Chronicler Books
- **Tasks**:
  1. Implement `internal/system2` OpenAI-compatible client with JSON schema validation and dual-temperature sampling.
  2. Implement in-game natural chat routing, the custom two-sided Barter GUI in NeoForge, and `reputation.db` Hero Prestige calculations.
  3. Implement the `CHRONICLER` role (`PaginateProse` + `CreateWrittenBookIntent`) and `PlaceSignpostIntent` Toponym engine.
- **Verification**: Chat with an agent in-game and complete a barter trade. Trigger a high-saliency combat event; verify the Chronicler writes a multi-page `minecraft:written_book` and places it on a Lectern with valid formatting.

### Phase 5: Blueprints, Architect Quadrant Builder, Tavern, Hobbies & Caravans
- **Tasks**:
  1. Implement Tier 1 settler self-builds and Tier 2 Architect quadrant construction with BOM tracking and functional zone registration.
  2. Implement the `TAVERNKEEPER` behavior tree, rumor exchange (`rumors.db`), Dual-Career idle hobby switching (`APIARIST`, `JAM_MAKER`, etc.), modded recipe DAG export, and player sharecropping contracts (`contracts.db`).
  3. Implement `CARAVAN_MASTER` pack-beast leashing, topological `map_graph.db` routing, inter-clan diplomacy, and the `settlerctl` Dynastic Reproduction governor.
- **Verification**: Verify multiple builders construct a Tier 2 Tavern bottom-up without colliding. Verify idle guards tend beehives, caravans lead leashed mules between two clan outposts, and two high-affinity settlers in a surplus village spawn an offspring agent within the inference governor cap.