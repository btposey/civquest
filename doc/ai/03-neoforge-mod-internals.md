# Progressive Discovery: NeoForge Mod Internals

## 1. Engine & Setup
- **Target Platform**: Minecraft 1.21.1 / NeoForge 21.1.265.
- **Java Version**: OpenJDK 21.
- **Build System**: Gradle with NeoForge Gradle Plugin (`net.neoforged.gradle.userdev`).

## 2. WorldGen Integration & Co-existence
CivQuest preserves vanilla villages and villager entities so players and settlers can interact with them. Rather than suppressing vanilla world generation, the mod introduces custom clan starter structures:
1. **Pioneer Camp Spawner**:
   A custom jigsaw feature (`civquest:pioneer_campfire`) creates an initial starter camp (campfire, supply crates, clan banner) where initial agents can be spawned and establish their settlements.
2. **Vanilla Co-existence**:
   Vanilla villages, villagers, and wandering traders spawn naturally, giving autonomous avatars additional in-game entities to encounter, barter with, and protect.

## 3. `SettlerEntity` (`FakePlayer`) Lifecycle
`SettlerEntity` extends `net.neoforged.neoforge.common.util.FakePlayer` (or `ServerPlayer` attached to a headless virtual `ServerGamePacketListenerImpl`):
- **Server Tick Integration**: Executed at 20 Hz inside `ServerLevel`.
  - Ticks `FoodData` for hunger depletion during sprinting and health regeneration.
  - Ticks active `LivingEntity` item usage (food consumption, bow drawing, shield block).
  - Ticks Baritone path execution.
- **Offline UUIDs**: Generated via `UUIDUtil.createOfflinePlayerUUID(displayName)` for deterministic identity and consistent entity state tracking across restarts.
- **Custom Skin Rendering**:
  - Attached to the entity profile or broadcast to human clients using `ClientboundSettlerSkinPacket` carrying raw 64x64 PNG bytes.

## 4. Baritone Integration
Physical navigation delegates to an embedded Baritone API wrapper:
- Translates `NavigateIntent` into Baritone `GoalNear` or `GoalBlock`.
- Settings such as `allowBreak`, `allowPlace`, and `sprint` are configured per intent.
- Provides real-time path execution status back to the telemetry collector: `IDLE`, `PATHING`, `STUCK`, `ARRIVED`, `BUILDING`.

## 5. Mod Lifecycle, Jar-in-Jar Packaging & Client Isolation

### Client vs. Server Physical Lifecycle Isolation
`CivQuestMod` operates in both dedicated server environments and client game instances (for receiving `ClientboundSettlerSkinPacket`):
- **Deferred Bridge Server Initialization**: `CivQuestBridgeServer` must **never** be instantiated inside `CivQuestMod.<init>`. Instantiating it in `<init>` eagerly triggers classloading of Netty gRPC dependencies on physical game clients during launcher startup, crashing with `NoClassDefFoundError: io/grpc/BindableService`.
- **`ServerStartingEvent` Gate**: `CivQuestBridgeServer` is lazily instantiated only upon receiving `ServerStartingEvent`. Because `ServerStartingEvent` is strictly published by logical server runtimes (dedicated servers or integrated singleplayer), client title screens and multiplayer join routines remain completely decoupled from server IPC sockets.

### Jar-in-Jar (`jarJar`) Dependency Packaging
NeoForge's ModDev plugin requires explicit `jarJar` configuration in `build.gradle` to embed dependencies within `META-INF/jarjar/`:
- Standard `implementation` dependencies are only present on the classpath during development Gradle runs. Standalone mod jars placed in `~/.minecraft/mods/` or standalone server environments require Jar-in-Jar bundling for external non-vanilla libraries.
- Bundled libraries include: `grpc-api`, `grpc-core`, `grpc-netty-shaded`, `grpc-protobuf`, `grpc-stub`, `grpc-services`, `grpc-context`, `grpc-util`, `grpc-protobuf-lite`, `proto-google-common-protos`, `perfmark-api`, and `protobuf-java`.
- Automated verification via `:mod-neoforge:verifyJarJarPackaging` executes during `./gradlew check` to ensure all required nested jars and `META-INF/jarjar/metadata.json` are present in every built artifact.

### Optional Network Channel Negotiation
Custom payloads registered via `RegisterPayloadHandlersEvent` must configure optionality:
```java
PayloadRegistrar registrar = event.registrar("1").optional();
```
Calling `.optional()` ensures that clients without CivQuest installed can freely connect to a CivQuest server without the connection handshake failing with "Incompatible mod list". Modded clients negotiate channel "1" and receive dynamic settler skins.
