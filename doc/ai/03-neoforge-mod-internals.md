# Progressive Discovery: NeoForge Mod Internals

## 1. Engine & Setup
- **Target Platform**: Minecraft 1.21.1 / NeoForge 21.1.265.
- **Java Version**: OpenJDK 21.
- **Build System**: Gradle with NeoForge Gradle Plugin (`net.neoforged.gradle.userdev`).

## 2. WorldGen Suppression & Interception
To ensure complete replacement of vanilla villagers:
1. **Datapack Override**:
   File `src/main/resources/data/minecraft/worldgen/structure_set/villages.json` defines `"structures": []`. This prevents the vanilla world generator from placing village structures during chunk creation.
2. **Spawn Cancellation**:
   Event listener on `EntityJoinLevelEvent`:
   ```java
   @SubscribeEvent
   public void onEntityJoin(EntityJoinLevelEvent event) {
       if (event.getEntity() instanceof Villager || event.getEntity() instanceof WanderingTrader) {
           event.setCanceled(true);
       }
   }
   ```
3. **Pioneer Camp Spawner**:
   A custom jigsaw feature (`civquest:pioneer_campfire`) creates an initial starter camp (campfire, supply crates, clan banner) where initial agents can be spawned.

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
