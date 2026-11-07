# Progressive Discovery: IPC & Protobuf Contract

## 1. Master Service Definition

The communication backbone of CivQuest is defined in `proto/civquest/v1/civquest.proto`.

```protobuf
syntax = "proto3";

package civquest.v1;
option go_package = "github.com/btposey/civquest/orchestrator-go/internal/proto/v1;civquestv1";
option java_package = "com.civquest.mod.grpc.v1";
option java_multiple_files = true;

service CivQuestBridgeService {
  // Primary per-agent bidirectional control and telemetry stream
  rpc ConnectAgent(stream AgentIntent) returns (stream TelemetryFrame);

  // Administrative RPCs called by civctl supervisor
  rpc SpawnAgentEntity(SpawnAgentRequest) returns (SpawnAgentResponse);
  rpc DespawnAgentEntity(DespawnAgentRequest) returns (DespawnAgentResponse);
  rpc ExportRecipeGraph(ExportRecipeRequest) returns (ExportRecipeResponse);
}
```

## 2. Telemetry Streaming (Server -> Agent @ 5 Hz)

Every 4 Minecraft server ticks (5 Hz), or immediately upon an urgent event (such as `LivingDamageEvent` or `DeathEvent`), the Minecraft mod constructs and sends a `TelemetryFrame` containing:
- `world_tick`: Absolute server tick counter.
- `time_of_day`: 0 to 24000 tick cycle (1000 = morning, 6000 = noon, 13000 = dusk, 18000 = midnight).
- `weather`: "CLEAR", "RAIN", "THUNDER".
- `status`: Comprehensive agent status (health, food, saturation, position, yaw/pitch, navigation state, complete inventory, leashed entities).
- `visible_entities`: Mobs, players, animals within $R = 24$ blocks with line-of-sight checks. Includes hostility flags, held items, and Creeper `fuse_ticks`.
- `interesting_blocks`: Blocks within $R = 16$ matching `#civquest:interesting_blocks` (mature crops, beds, workstations, beehives, chests, lecterns, hazards).
- `incoming_chats`: Local chat messages from nearby players or clan members.
- `last_damage_taken`: Attacker ID, damage amount, and damage source ("mob", "arrow", "fall", "lava", etc.).
- `pending_trade`: In-game player trade proposals and offered items.
- `death_event`: Sent upon fatal damage, triggering the permanent teardown sequence.

## 3. Intents (Agent -> Server)

The Go agent sends continuous or discrete `AgentIntent` messages containing a sequence ID and one of the following action payloads:
1. `NavigateIntent`: Target coordinates, sprint flag, speed multiplier, scaffolding/break allowances, arrival tolerance.
2. `AttackIntent`: Target entity ID, shield raising flag, kiting flag, flee flag.
3. `InteractBlockIntent`: Coordinate, interaction type (`HARVEST_BREAK`, `PLACE_BLOCK`, `SLEEP_IN_BED`, `WAKE_UP`, `USE_WORKSTATION`, `RING_BELL`, `SHEAR_OR_BOTTLE_BEEHIVE`, `PAVE_ROAD_PATH`).
4. `UseItemIntent`: Inventory slot, eating/drinking flag, equip armor/offhand flag.
5. `CraftItemIntent`: Recipe ID, workstation position, batch count.
6. `SendChatIntent`: Message string, target channel (`LOCAL`, `CLAN_BROADCAST`).
7. `RespondTradeIntent`: Deal acceptance flag, counter offer items, spoken reply.
8. `CaravanIntent`: Leashing, unleashing, hitching to fence, chest loading/unloading.
9. `BuildBlockIntent`: Voxel coordinate, target blockstate string (e.g. `minecraft:stone_brick_stairs[facing=north]`), scaffold marker.
10. `CreateWrittenBookIntent`: Book title ($\le 32$ chars), author, paginated strings, target lectern/bookshelf.
11. `PlaceSignpostIntent`: Coordinate, hanging sign flag, 4 text lines ($\le 15$ chars each).
12. `TransferContainerIntent`: Container coordinate, deposit/withdraw flag, item stacks.
