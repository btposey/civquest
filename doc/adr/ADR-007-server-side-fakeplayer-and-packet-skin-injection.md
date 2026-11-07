# ADR-007: Server-Side FakePlayer Architecture & Packet Skin Injection

## Status
Accepted

## Context
CivQuest simulations require autonomous humanoid agents that can physically interact with the game world like players: manipulating off-hand items, dual-wielding shields and weapons, experiencing player hunger decay, wearing armor sets, drawing bows, and sleeping in beds.

Standard entity types (such as `Villager` or `Zombie`) lack player inventory physics, off-hand mechanics, standard food saturation, and player animations. The standard, well-established mechanism within Minecraft server modding (e.g. NeoForge / Forge) for autonomous or automated player-like entities is the `FakePlayer` abstraction.

Furthermore, custom agent skin textures must be delivered to human clients connected to the server so that each agent displays their unique clan attire and appearance.

## Decision
1. **FakePlayer Subclassing**:
   - In `mod-neoforge`, implement `SettlerEntity` extending `net.neoforged.neoforge.common.util.FakePlayer` (or `ServerPlayer` attached to a headless virtual `ServerGamePacketListenerImpl`).
   - Entities tick fully inside `ServerLevel` at 20 Hz, processing hunger (`FoodData`), item usage (eating, blocking, bows), and physics identically to real players.
2. **Deterministic Offline UUIDs & Skin Packet Broadcasting**:
   - Assign deterministic offline UUIDs via `UUIDUtil.createOfflinePlayerUUID(displayName)` for consistent agent identity across world reloads.
   - Deliver custom settler skins to connected clients by broadcasting custom NeoForge network payloads (`ClientboundSettlerSkinPacket`) carrying raw PNG bytes or GameProfile property injections (`textures`) into `ClientboundPlayerInfoUpdatePacket`.

## Consequences
### Positive
- Native player capabilities: dual-wielding, armor toughness, hunger decay, knockback, and sprint mechanics.
- Rich visual diversity with custom player skins rendered dynamically in-game.
- Clean architectural alignment with established NeoForge server-side automation patterns.

### Negative / Trade-offs
- Internal `ServerPlayer` hooks must maintain dummy connection implementations to satisfy server tick assumptions.
- Client players must have the companion client-side mod or packet listener installed to view custom PNG skin payloads.
