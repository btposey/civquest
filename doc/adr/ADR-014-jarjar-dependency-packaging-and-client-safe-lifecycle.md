# ADR-014: Jar-in-Jar Dependency Packaging & Client-Safe Mod Lifecycle Isolation

## Status
Accepted

## Context
CivQuest operates a dual-layer architecture where the Java NeoForge modification (`mod-neoforge`) serves two distinct runtime environments:
1. **Dedicated Game Server**: Hosts the Minecraft world, runs headless `SettlerEntity` (`FakePlayer`) avatars, and runs an embedded Netty gRPC server (`CivQuestBridgeServer`) communicating with external Go orchestrators.
2. **Player Client**: Connects to the dedicated server, renders avatars, and receives dynamic agent skin updates via custom network payloads (`ClientboundSettlerSkinPacket`).

During playtesting, two critical failure modes were discovered when launching the game through the standard Minecraft client launcher with `mod-neoforge` installed in `~/.minecraft/mods/`:
1. **Eager Bridge Class Loading**: The mod main entrypoint (`CivQuestMod.<init>`) eagerly constructed `CivQuestBridgeServer`. On a physical game client launching into the title screen, the server IPC bridge was initialized unnecessarily, attempting to link server socket and gRPC dependencies before any world or multiplayer session was selected.
2. **Classloader Dependency Deficiencies (`NoClassDefFoundError: io/grpc/BindableService`)**: In standard NeoForge mod distribution, dependencies declared under Gradle `implementation` are not included in the compiled mod jar. While Gradle's `:mod-neoforge:runServer` run task passes all cache jars on the Java classpath flag (`-cp`), standard Minecraft launchers load only the standalone mod JAR. Because `io.grpc.*` and `com.google.protobuf.*` were not embedded, the client classloader failed with `NoClassDefFoundError`.

## Decision

We establish three mandatory architectural standards for NeoForge mod distribution and lifecycle management:

1. **Lazy Server Lifecycle Initialization**:
   - `CivQuestBridgeServer` must never be instantiated during mod construction (`CivQuestMod.<init>`).
   - Construction and binding are deferred to `ServerStartingEvent`. Because `ServerStartingEvent` is only published by logical servers (dedicated servers or integrated singleplayer worlds), physical clients launching to the title screen or joining dedicated multiplayer servers will never load, link, or instantiate gRPC bridge classes.

2. **Jar-in-Jar (`jarJar`) Dependency Bundling**:
   - All third-party libraries not provided by the base Minecraft/NeoForge runtime (specifically `io.grpc:*` and `com.google.protobuf:*`) must be packaged into the distribution JAR using NeoForge's native **Jar-in-Jar (`jarJar`)** mechanism via `ModDevGradle`.
   - Bundled dependencies are placed under `META-INF/jarjar/` with an auto-generated `metadata.json` manifest, allowing NeoForge's runtime `JarJarSelector` to isolate and load these nested jars without classpath collision or class relocation issues.

3. **Optional Network Channel Negotiation**:
   - All client-bound custom payloads (`ClientboundSettlerSkinPacket`) registered via `RegisterPayloadHandlersEvent` must mark their channel registrar as optional (`event.registrar("1").optional()`).
   - This ensures full forward and backward compatibility: vanilla or unmodded NeoForge clients can connect to a CivQuest server without being rejected with "Incompatible mod list", while modded clients negotiate the channel and receive dynamic skin packets.

4. **Automated Verification in the Test Harness**:
   - A Gradle verification task (`:mod-neoforge:verifyJarJarPackaging`) is registered and wired directly into `:mod-neoforge:check`.
   - The task programmatically inspects the built JAR zip entries to confirm the presence of `META-INF/jarjar/metadata.json` and all required gRPC/Protobuf jars, failing the build immediately if any dependency is omitted.

## Consequences

### Positive
- **Client Stability**: Physical Minecraft clients launch cleanly into the main menu without classloader errors.
- **Single-Artifact Simplicity**: A single built JAR (`mod-neoforge-0.1.0-SNAPSHOT.jar`) functions reliably across all deployments: standalone dedicated servers, developer Gradle runs, and player client mod folders.
- **No Incompatible Mod Rejection**: Servers running CivQuest do not require connected players to have matching mod versions to join basic gameplay.
- **CI / Build Regression Gate**: Automated build verification prevents future dependency additions or refactorings from accidentally stripping embedded Jar-in-Jar components.

### Negative / Trade-offs
- Mod JAR file size increases from $\approx 500\text{ KB}$ to $\approx 16\text{ MB}$ to account for embedded gRPC Netty Shaded and Protobuf runtimes.
