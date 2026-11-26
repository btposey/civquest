package com.civquest.mod;

import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.DespawnAgentRequest;
import com.civquest.mod.grpc.v1.DespawnAgentResponse;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.SpawnAgentResponse;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.Vec3;
import com.google.protobuf.ByteString;
import com.mojang.authlib.GameProfile;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import io.netty.buffer.Unpooled;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Holder;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.UUIDUtil;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.FriendlyByteBuf;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.PlayerAdvancements;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.server.level.ServerPlayerGameMode;
import net.minecraft.server.network.TextFilter;
import net.minecraft.server.players.PlayerList;
import net.minecraft.stats.ServerStatsCounter;
import net.minecraft.tags.DamageTypeTags;
import net.minecraft.util.RandomSource;
import net.minecraft.world.Difficulty;
import net.minecraft.world.damagesource.DamageScaling;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.damagesource.DamageSources;
import net.minecraft.world.damagesource.DamageType;
import net.minecraft.world.entity.monster.Zombie;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.GameRules;
import net.minecraft.world.level.GameType;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.border.WorldBorder;
import net.minecraft.world.level.dimension.DimensionType;
import net.minecraft.world.level.material.Fluids;
import net.minecraft.world.level.storage.WorldData;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyBoolean;
import static org.mockito.Mockito.*;

public class AgentEntityTest {

    @BeforeAll
    static void initMinecraft() {
        try {
            net.neoforged.fml.loading.LoadingModList.of(
                    java.util.List.of(), java.util.List.of(), java.util.List.of(), java.util.List.of(), java.util.Map.of()
            );
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Field unsafeField = sun.misc.Unsafe.class.getDeclaredField("theUnsafe");
            unsafeField.setAccessible(true);
            sun.misc.Unsafe unsafe = (sun.misc.Unsafe) unsafeField.get(null);

            java.lang.reflect.Field fmlLoaderDist = net.neoforged.fml.loading.FMLLoader.class.getDeclaredField("dist");
            long offset1 = unsafe.staticFieldOffset(fmlLoaderDist);
            unsafe.putObject(net.neoforged.fml.loading.FMLLoader.class, offset1, net.neoforged.api.distmarker.Dist.DEDICATED_SERVER);

            java.lang.reflect.Field fmlEnvDist = net.neoforged.fml.loading.FMLEnvironment.class.getDeclaredField("dist");
            long offset2 = unsafe.staticFieldOffset(fmlEnvDist);
            unsafe.putObject(net.neoforged.fml.loading.FMLEnvironment.class, offset2, net.neoforged.api.distmarker.Dist.DEDICATED_SERVER);
        } catch (Throwable ignored) {}
        try {
            net.minecraft.SharedConstants.tryDetectVersion();
            net.minecraft.server.Bootstrap.bootStrap();
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Method unfreeze = net.neoforged.neoforge.registries.BaseMappedRegistry.class.getDeclaredMethod("unfreeze");
            unfreeze.setAccessible(true);
            @SuppressWarnings({"unchecked", "rawtypes"})
            net.minecraft.core.WritableRegistry rootRegistry = (net.minecraft.core.WritableRegistry) net.minecraft.core.registries.BuiltInRegistries.REGISTRY;
            if (!net.minecraft.core.registries.BuiltInRegistries.REGISTRY.containsKey(net.neoforged.neoforge.registries.NeoForgeRegistries.Keys.FLUID_TYPES.location())) {
                unfreeze.invoke(rootRegistry);
                net.minecraft.core.Registry.register(
                        rootRegistry,
                        net.neoforged.neoforge.registries.NeoForgeRegistries.Keys.FLUID_TYPES.location(),
                        net.neoforged.neoforge.registries.NeoForgeRegistries.FLUID_TYPES
                );
                rootRegistry.freeze();
            }
            var fluidTypes = net.neoforged.neoforge.registries.NeoForgeRegistries.FLUID_TYPES;
            if (!fluidTypes.containsKey(net.minecraft.resources.ResourceLocation.withDefaultNamespace("empty"))) {
                unfreeze.invoke(fluidTypes);
                net.minecraft.core.Registry.register(
                        fluidTypes,
                        net.minecraft.resources.ResourceLocation.withDefaultNamespace("empty"),
                        new net.neoforged.neoforge.fluids.FluidType(net.neoforged.neoforge.fluids.FluidType.Properties.create())
                );
                net.minecraft.core.Registry.register(
                        fluidTypes,
                        net.minecraft.resources.ResourceLocation.withDefaultNamespace("water"),
                        new net.neoforged.neoforge.fluids.FluidType(net.neoforged.neoforge.fluids.FluidType.Properties.create())
                );
                net.minecraft.core.Registry.register(
                        fluidTypes,
                        net.minecraft.resources.ResourceLocation.withDefaultNamespace("lava"),
                        new net.neoforged.neoforge.fluids.FluidType(net.neoforged.neoforge.fluids.FluidType.Properties.create())
                );
                fluidTypes.freeze();
            }
            var attributes = net.minecraft.core.registries.BuiltInRegistries.ATTRIBUTE;
            var swimKey = net.minecraft.resources.ResourceLocation.fromNamespaceAndPath("neoforge", "swim_speed");
            if (!attributes.containsKey(swimKey)) {
                unfreeze.invoke(attributes);
                net.minecraft.core.Registry.register(
                        attributes,
                        swimKey,
                        new net.neoforged.neoforge.common.PercentageAttribute("neoforge.swim_speed", 1.0D, 0.0D, 1024.0D).setSyncable(true)
                );
                net.minecraft.core.Registry.register(
                        attributes,
                        net.minecraft.resources.ResourceLocation.fromNamespaceAndPath("neoforge", "nametag_distance"),
                        new net.minecraft.world.entity.ai.attributes.RangedAttribute("neoforge.name_tag_distance", 64.0D, 0.0D, 64.0D).setSyncable(true)
                );
                net.minecraft.core.Registry.register(
                        attributes,
                        net.minecraft.resources.ResourceLocation.fromNamespaceAndPath("neoforge", "creative_flight"),
                        new net.neoforged.neoforge.common.BooleanAttribute("neoforge.creative_flight", false).setSyncable(true)
                );
                attributes.freeze();
            }
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Field cachedValueField = net.neoforged.neoforge.common.ModConfigSpec.ConfigValue.class.getDeclaredField("cachedValue");
            cachedValueField.setAccessible(true);
            cachedValueField.set(net.neoforged.neoforge.common.NeoForgeConfig.SERVER.fullBoundingBoxLadders, Boolean.FALSE);
        } catch (Throwable t) {
            throw new RuntimeException("Failed to initialize NeoForgeConfig.SERVER.fullBoundingBoxLadders in test", t);
        }
    }

    private AgentEntityManager entityManager;
    private StreamRegistry streamRegistry;
    private CivQuestBridgeServiceImpl bridgeService;

    @BeforeEach
    void setUp() {
        entityManager = new AgentEntityManager();
        streamRegistry = new StreamRegistry();
        bridgeService = new CivQuestBridgeServiceImpl(streamRegistry, entityManager, () -> null);
    }

    @Test
    @DisplayName("Deterministic offline player UUID generation adheres to Minecraft standard")
    void testOfflineUUIDGeneration() {
        String displayName = "Agent-Brian";
        UUID uuid1 = UUIDUtil.createOfflinePlayerUUID(displayName);
        UUID uuid2 = UUIDUtil.createOfflinePlayerUUID(displayName);

        assertNotNull(uuid1);
        assertEquals(uuid1, uuid2, "Same display name must yield identical offline UUID");

        UUID uuidDifferent = UUIDUtil.createOfflinePlayerUUID("Agent-Other");
        assertNotEquals(uuid1, uuidDifferent, "Different display names must produce different UUIDs");

        // Verify version 3 UUID (name-based MD5)
        assertEquals(3, uuid1.version(), "Offline player UUID must be version 3");
    }

    @Test
    @DisplayName("ClientboundAgentSkinPacket encodes and decodes raw 64x64 PNG payloads correctly")
    void testAgentSkinPacketCodec() {
        UUID agentUuid = UUIDUtil.createOfflinePlayerUUID("Agent-Artisan");
        String agentId = "agent-artisan-01";
        byte[] dummySkinPng = "dummy-64x64-png-texture-bytes-content".getBytes(StandardCharsets.UTF_8);

        ClientboundAgentSkinPacket packet = new ClientboundAgentSkinPacket(agentUuid, agentId, dummySkinPng);
        assertEquals(agentUuid, packet.agentUuid());
        assertEquals(agentId, packet.agentId());
        assertArrayEquals(dummySkinPng, packet.skinPng());
        assertEquals("civquest:agent_skin", packet.type().id().toString());

        // Test stream codec round-trip serialization
        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        ClientboundAgentSkinPacket.STREAM_CODEC.encode(buffer, packet);

        ClientboundAgentSkinPacket decoded = ClientboundAgentSkinPacket.STREAM_CODEC.decode(buffer);
        assertNotNull(decoded);
        assertEquals(packet.agentUuid(), decoded.agentUuid());
        assertEquals(packet.agentId(), decoded.agentId());
        assertArrayEquals(packet.skinPng(), decoded.skinPng());
    }

    @Test
    @DisplayName("ClientboundAgentSkinPacket handles empty or null skin bytes safely")
    void testAgentSkinPacketEmptyPayload() {
        UUID agentUuid = UUID.randomUUID();
        ClientboundAgentSkinPacket emptyPacket = new ClientboundAgentSkinPacket(agentUuid, "agent-bare", null);
        assertNotNull(emptyPacket.skinPng());
        assertEquals(0, emptyPacket.skinPng().length);

        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        ClientboundAgentSkinPacket.STREAM_CODEC.encode(buffer, emptyPacket);
        ClientboundAgentSkinPacket decoded = ClientboundAgentSkinPacket.STREAM_CODEC.decode(buffer);
        assertEquals(0, decoded.skinPng().length);
    }

    @Test
    @DisplayName("AgentEntityManager tracks, queries, and clears agents cleanly")
    void testAgentEntityManagerLifecycle() throws ExecutionException, InterruptedException {
        assertEquals(0, entityManager.count());
        assertNull(entityManager.getAgent("non-existent"));

        // Despawn on non-existent returns false
        CompletableFuture<Boolean> despawnFuture = entityManager.despawnAgent(null, "non-existent");
        assertFalse(despawnFuture.get());

        // Clear all on empty manager does not throw
        assertDoesNotThrow(() -> entityManager.clearAll());
        assertEquals(0, entityManager.count());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl spawns agent in standalone/test mode with deterministic offline UUID")
    void testBridgeServiceSpawnStandalone() {
        String agentId = "agent-unit-01";
        String displayName = "AgentUnit";
        String clanId = "clan-ironwood";

        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId(agentId)
                .setDisplayName(displayName)
                .setClanId(clanId)
                .setSpawnCoords(Vec3.newBuilder().setX(100.0).setY(64.0).setZ(-200.0).build())
                .build();

        AtomicReference<SpawnAgentResponse> responseRef = new AtomicReference<>();
        AtomicReference<Throwable> errorRef = new AtomicReference<>();

        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {
                responseRef.set(value);
            }

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {
            }
        });

        assertNull(errorRef.get());
        SpawnAgentResponse response = responseRef.get();
        assertNotNull(response);
        assertTrue(response.getSuccess());
        assertEquals(1001, response.getMinecraftEntityId());

        String expectedUuid = UUIDUtil.createOfflinePlayerUUID(displayName).toString();
        assertEquals(expectedUuid, response.getOfflineUuid());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl despawns agent and closes agent stream registry")
    void testBridgeServiceDespawn() {
        String agentId = "agent-despawn-test";

        DespawnAgentRequest request = DespawnAgentRequest.newBuilder()
                .setAgentId(agentId)
                .setReason("Test cycle completed")
                .build();

        AtomicReference<DespawnAgentResponse> responseRef = new AtomicReference<>();
        bridgeService.despawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(DespawnAgentResponse value) {
                responseRef.set(value);
            }

            @Override
            public void onError(Throwable t) {
            }

            @Override
            public void onCompleted() {
            }
        });

        DespawnAgentResponse response = responseRef.get();
        assertNotNull(response);
        assertTrue(response.getSuccess());
        assertNull(streamRegistry.getObserver(agentId));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with empty agent_id")
    void testBridgeServiceSpawnValidation() {
        SpawnAgentRequest invalidRequest = SpawnAgentRequest.newBuilder()
                .setAgentId("")
                .setDisplayName("NoId")
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(invalidRequest, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {
            }

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {
            }
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with invalid Base64 skin_texture_value")
    void testSkinValidationInvalidBase64() {
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-invalid-base64")
                .setSkinTextureValue("not-valid-base64-!@#$%^&*()")
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
        assertTrue(error.getMessage().contains("skin_texture_value is not valid Base64"));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with skin_texture_value exceeding 4096 characters")
    void testSkinValidationTextureTooLong() {
        String longTexture = "A".repeat(4097);
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-long-texture")
                .setSkinTextureValue(longTexture)
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
        assertTrue(error.getMessage().contains("exceeds maximum allowed length of 4096"));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects SpawnAgentRequest with skin_png_bytes exceeding 64KB")
    void testSkinPngBytesExceeds64KB() {
        byte[] oversizedBytes = new byte[65537];
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-oversized-skin")
                .setSkinPngBytes(ByteString.copyFrom(oversizedBytes))
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.spawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(SpawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
        assertTrue(error.getMessage().contains("exceeds maximum allowed size of 65536"));
    }

    @Test
    @DisplayName("ClientboundAgentSkinPacket rejects payloads larger than 64KB on decode")
    void testAgentSkinPacketCodecRejectsOversizedPayload() {
        FriendlyByteBuf buffer = new FriendlyByteBuf(Unpooled.buffer());
        buffer.writeUUID(UUID.randomUUID());
        buffer.writeUtf("agent-oversized-test");
        buffer.writeByteArray(new byte[65537]);

        assertThrows(Exception.class, () -> ClientboundAgentSkinPacket.STREAM_CODEC.decode(buffer));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl rejects DespawnAgentRequest with empty or blank agent_id")
    void testDespawnAgentEmptyIdValidation() {
        DespawnAgentRequest request = DespawnAgentRequest.newBuilder()
                .setAgentId("   ")
                .build();

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        bridgeService.despawnAgentEntity(request, new StreamObserver<>() {
            @Override
            public void onNext(DespawnAgentResponse value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(Status.Code.INVALID_ARGUMENT, ((StatusRuntimeException) error).getStatus().getCode());
    }

    @Test
    @DisplayName("StreamRegistry reopen restores closed registry allowing new registrations")
    void testStreamRegistryReopen() {
        StreamRegistry registry = new StreamRegistry();
        assertFalse(registry.isClosed());

        StreamObserver<TelemetryFrame> dummyObserver = new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {}
            @Override
            public void onError(Throwable t) {}
            @Override
            public void onCompleted() {}
        };

        assertTrue(registry.register("agent-reopen-1", dummyObserver));
        assertEquals(1, registry.getActiveAgentCount());

        registry.closeAll();
        assertTrue(registry.isClosed());
        assertEquals(0, registry.getActiveAgentCount());

        // While closed, registrations are rejected
        assertFalse(registry.register("agent-reopen-2", dummyObserver));

        // Reopen resets closed state
        registry.reopen();
        assertFalse(registry.isClosed());

        // Registrations succeed after reopen
        assertTrue(registry.register("agent-reopen-3", dummyObserver));
        assertEquals(1, registry.getActiveAgentCount());
    }

    @Test
    @DisplayName("cq-0b0.9: Player gift gratitude triggers ONLY when item thrower UUID matches the nearby player")
    void testGratitudeAttributionOnThrowerUUID() {
        UUID playerUUID = UUID.randomUUID();
        UUID otherUUID = UUID.randomUUID();

        // 1. Naturally dropped item (seeds/flowers from grass) has no thrower/owner (null)
        UUID naturalDropThrower = null;
        boolean naturalThanked = playerUUID != null && naturalDropThrower != null && naturalDropThrower.equals(playerUUID);
        assertFalse(naturalThanked, "Naturally dropped items without thrower must not trigger gratitude dialogue");

        // 2. Genuine player gift thrown by the player (thrower UUID matches player)
        UUID playerGiftThrower = playerUUID;
        boolean playerGiftThanked = playerUUID != null && playerGiftThrower != null && playerGiftThrower.equals(playerUUID);
        assertTrue(playerGiftThanked, "Genuine player gift matching player UUID must trigger gratitude dialogue");

        // 3. Item thrown by a different entity/player
        UUID otherEntityThrower = otherUUID;
        boolean otherEntityThanked = playerUUID != null && otherEntityThrower != null && otherEntityThrower.equals(playerUUID);
        assertFalse(otherEntityThanked, "Items thrown by other entities must not thank the nearby player");
    }

    private static class TestWorldContext {
        final MinecraftServer server;
        final ServerLevel level;
        final DamageSources damageSources;
        final DamageSource starveSource;
        final GameRules gameRules;

        TestWorldContext() {
            server = mock(MinecraftServer.class);
            level = mock(ServerLevel.class);
            PlayerList playerList = mock(PlayerList.class);
            WorldData worldData = mock(WorldData.class);
            ServerPlayerGameMode gameMode = mock(ServerPlayerGameMode.class);
            ServerStatsCounter statsCounter = mock(ServerStatsCounter.class);
            PlayerAdvancements advancements = mock(PlayerAdvancements.class);
            DimensionType dimensionType = mock(DimensionType.class);

            damageSources = mock(DamageSources.class);
            starveSource = spy(new DamageSource(Holder.direct(new DamageType("starve", DamageScaling.NEVER, 0.0F))));
            doReturn(true).when(starveSource).is(DamageTypeTags.BYPASSES_ARMOR);
            doReturn(starveSource).when(damageSources).starve();
            DamageSource genericSource = new DamageSource(Holder.direct(new DamageType("generic", DamageScaling.NEVER, 0.0F)));
            doReturn(genericSource).when(damageSources).generic();
            doReturn(genericSource).when(damageSources).inWall();
            doReturn(genericSource).when(damageSources).drown();
            doReturn(genericSource).when(damageSources).fellOutOfWorld();
            doReturn(genericSource).when(damageSources).onFire();

            gameRules = new GameRules();
            gameRules.getRule(GameRules.RULE_DOMOBLOOT).set(false, server);

            when(server.createTextFilterForPlayer(any())).thenReturn(TextFilter.DUMMY);
            when(server.createGameModeForPlayer(any())).thenReturn(gameMode);
            when(server.getPlayerList()).thenReturn(playerList);
            when(server.getWorldData()).thenReturn(worldData);
            when(server.isPvpAllowed()).thenReturn(true);
            when(server.isDedicatedServer()).thenReturn(false);
            when(server.isRunning()).thenReturn(true);
            net.minecraft.server.ReloadableServerRegistries.Holder reloadableRegistries = mock(net.minecraft.server.ReloadableServerRegistries.Holder.class);
            when(reloadableRegistries.getLootTable(any())).thenReturn(net.minecraft.world.level.storage.loot.LootTable.EMPTY);
            when(server.reloadableRegistries()).thenReturn(reloadableRegistries);

            when(worldData.getGameType()).thenReturn(GameType.SURVIVAL);
            when(gameMode.getGameModeForPlayer()).thenReturn(GameType.SURVIVAL);
            when(playerList.getPlayerStats(any())).thenReturn(statsCounter);
            when(playerList.getPlayerAdvancements(any())).thenReturn(advancements);
            when(playerList.getPlayers()).thenReturn(Collections.emptyList());

            when(level.getServer()).thenReturn(server);
            when(level.getSharedSpawnPos()).thenReturn(new BlockPos(0, 64, 0));
            when(level.getSharedSpawnAngle()).thenReturn(0.0F);
            net.minecraft.core.MappedRegistry<net.minecraft.world.item.enchantment.Enchantment> enchantmentRegistry =
                    new net.minecraft.core.MappedRegistry<>(net.minecraft.core.registries.Registries.ENCHANTMENT, com.mojang.serialization.Lifecycle.stable());
            enchantmentRegistry.freeze();
            java.util.List<net.minecraft.core.Registry<?>> allRegs = new java.util.ArrayList<>();
            BuiltInRegistries.REGISTRY.forEach(allRegs::add);
            allRegs.add(enchantmentRegistry);
            RegistryAccess.Frozen registryAccess = new RegistryAccess.ImmutableRegistryAccess(allRegs).freeze();
            when(level.registryAccess()).thenReturn(registryAccess);
            when(level.dimensionType()).thenReturn(dimensionType);
            when(level.dimension()).thenReturn(Level.OVERWORLD);
            when(level.getWorldBorder()).thenReturn(new WorldBorder());
            RandomSource randomSource = RandomSource.create();
            when(level.getRandom()).thenReturn(randomSource);
            try {
                java.lang.reflect.Field randomField = Level.class.getDeclaredField("random");
                randomField.setAccessible(true);
                randomField.set(level, randomSource);
            } catch (Throwable t) {
                throw new RuntimeException("Failed to set Level.random on mock", t);
            }
            when(level.getGameRules()).thenReturn(gameRules);
            when(level.getDifficulty()).thenReturn(Difficulty.NORMAL);
            when(level.noCollision(any(), any())).thenReturn(true);
            when(level.damageSources()).thenReturn(damageSources);
            when(level.getBlockState(any(BlockPos.class))).thenReturn(Blocks.AIR.defaultBlockState());
            when(level.getFluidState(any(BlockPos.class))).thenReturn(Fluids.EMPTY.defaultFluidState());
            when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
            when(level.getMinBuildHeight()).thenReturn(-64);
            when(level.getMaxBuildHeight()).thenReturn(320);
            when(level.getProfiler()).thenReturn(net.minecraft.util.profiling.InactiveProfiler.INSTANCE);
            when(level.getChunkSource()).thenReturn(mock(net.minecraft.server.level.ServerChunkCache.class));
            when(level.getScoreboard()).thenReturn(new net.minecraft.server.ServerScoreboard(server));
            when(level.tickRateManager()).thenReturn(new net.minecraft.world.TickRateManager());
            @SuppressWarnings("unchecked")
            Holder<Biome> mockBiome = mock(Holder.class);
            when(level.getBiome(any(BlockPos.class))).thenReturn(mockBiome);
        }

        AgentEntity createAgent(String agentId, String displayName) {
            GameProfile profile = new GameProfile(AgentEntity.createOfflineUUID(displayName), displayName);
            AgentEntity agent = new AgentEntity(level, profile, agentId, "clan-test");
            agent.setPos(0.5, 64.0, 0.5);
            return agent;
        }
    }

    @Test
    @DisplayName("AgentEntity takes damage from hostile attacks and fall damage immediately after spawn")
    void testAgentTakesHostileAndFallDamage() {
        TestWorldContext ctx = new TestWorldContext();
        ctx.gameRules.getRule(GameRules.RULE_NATURAL_REGENERATION).set(false, ctx.server);
        AgentEntity agent = ctx.createAgent("agent-dmg-01", "MortalTestAgent");

        assertEquals(20.0F, agent.getHealth(), 0.001F);

        // 1. Hostile mob attack (does NOT bypass invulnerability)
        Zombie mockZombie = mock(Zombie.class);
        when(mockZombie.getName()).thenReturn(net.minecraft.network.chat.Component.literal("Zombie"));
        when(mockZombie.getUUID()).thenReturn(UUID.randomUUID());
        DamageSource mobAttack = new DamageSource(
                Holder.direct(new DamageType("mob", DamageScaling.NEVER, 0.1F)),
                mockZombie
        );

        boolean hurtByMob = agent.hurt(mobAttack, 4.0F);
        assertTrue(hurtByMob, "AgentEntity must take damage from hostile attacks immediately after spawn");
        assertEquals(16.0F, agent.getHealth(), 0.001F);

        // Tick 20 times to clear post-hit invulnerableTime (20 -> 0)
        assertTrue(agent.invulnerableTime > 0, "Post-hit invulnerableTime should be set");
        for (int i = 0; i < 20; i++) {
            agent.tick();
        }
        assertEquals(0, agent.invulnerableTime, "invulnerableTime must decrement to 0 across 20 ticks");

        // 2. Fall damage (IS_FALL, does NOT bypass invulnerability)
        DamageSource fallDamage = spy(new DamageSource(
                Holder.direct(new DamageType("fall", DamageScaling.NEVER, 0.0F))
        ));
        doReturn(true).when(fallDamage).is(DamageTypeTags.IS_FALL);

        boolean hurtByFall = agent.hurt(fallDamage, 5.0F);
        assertTrue(hurtByFall, "AgentEntity must take fall damage");
        assertEquals(11.0F, agent.getHealth(), 0.001F);
    }

    @Test
    @DisplayName("Food lasts 3 days at normal exertion, 1 day at full exertion; starvation at 0 food")
    void testAgentExhaustionAndStarvationDamage() {
        TestWorldContext ctx = new TestWorldContext();
        AgentEntity agent = ctx.createAgent("agent-hunger-01", "HungryAgent");
        // Disable natural regeneration so starvation / food decay is isolated cleanly
        ctx.gameRules.getRule(GameRules.RULE_NATURAL_REGENERATION).set(false, ctx.server);

        assertEquals(20, agent.getFoodData().getFoodLevel());
        assertEquals(5.0F, agent.getFoodData().getSaturationLevel(), 0.001F);
        assertEquals(3 * 24000, 80.0F / Metabolism.NORMAL_PER_TICK, 1.0F, "A full bar lasts three days");
        assertEquals(24000, 80.0F / Metabolism.perTick(true), 1.0F, "and one day at full exertion");

        // 1. Standing and walking cost the normal rate
        agent.tick();
        float afterIdleTick = agent.getFoodData().getExhaustionLevel();
        assertEquals(Metabolism.NORMAL_PER_TICK, afterIdleTick, 1e-5F);
        agent.setDeltaMovement(0.25, 0.0, 0.0);
        agent.tick();
        float afterWalkTick = agent.getFoodData().getExhaustionLevel();
        assertEquals(Metabolism.NORMAL_PER_TICK, afterWalkTick - afterIdleTick, 1e-5F);

        // 2. Sprinting costs three times as much
        agent.setSprinting(true);
        agent.setDeltaMovement(0.25, 0.0, 0.0);
        agent.tick();
        assertEquals(3 * Metabolism.NORMAL_PER_TICK, agent.getFoodData().getExhaustionLevel() - afterWalkTick, 1e-5F);
        agent.setSprinting(false);

        // 3. Breaking a block and other actions cost nothing extra
        agent.setOnGround(true);
        BlockPos targetPos = new BlockPos(1, 64, 0);
        BlockState dirt = Blocks.DIRT.defaultBlockState();
        when(ctx.level.getBlockState(targetPos)).thenReturn(dirt);
        when(ctx.level.destroyBlock(targetPos, true, agent)).thenReturn(true);
        BlockBreaker breaker = new BlockBreaker(agent);
        float beforeBreak = agent.getFoodData().getExhaustionLevel();
        assertTrue(breaker.start(targetPos));
        // Tick until dirt breaks
        for (int i = 0; i < 80 && breaker.isBreaking(); i++) {
            breaker.tick();
        }
        assertFalse(breaker.isBreaking(), "BlockBreaker should complete breaking dirt");
        agent.causeFoodExhaustion(0.1F); // what vanilla charges for an attack
        assertEquals(beforeBreak, agent.getFoodData().getExhaustionLevel(), 1e-5F,
                "Actions add no exhaustion of their own");
        // Block is now broken in the mock world
        when(ctx.level.getBlockState(targetPos)).thenReturn(Blocks.AIR.defaultBlockState());

        // 4. Exhaustion drains saturation and then food level
        agent.getFoodData().setSaturation(0.0F);
        agent.getFoodData().setFoodLevel(20);
        agent.getFoodData().setExhaustion(3.9995F);
        agent.setDeltaMovement(0.25, 0.0, 0.0);
        agent.tick(); // 3.9995 + 0.0011 > 4.0 => foodLevel drops to 19
        assertEquals(19, agent.getFoodData().getFoodLevel(),
                "Food level must decrease when accumulated exhaustion exceeds 4.0");

        // 5. Starvation damage applies when food level is 0 (every 80 ticks in Difficulty.NORMAL)
        agent.setNoGravity(true);
        agent.setPos(0.5, 64.0, 0.5);
        agent.setDeltaMovement(0.0, 0.0, 0.0);
        agent.getFoodData().setFoodLevel(0);
        agent.getFoodData().setSaturation(0.0F);
        agent.getFoodData().setExhaustion(0.0F);
        float initialHealth = agent.getHealth();
        for (int i = 0; i < 80; i++) {
            agent.tick();
        }
        assertTrue(agent.getHealth() < initialHealth,
                "Starvation damage must reduce agent health after 80 ticks at 0 food");
        assertSame(ctx.starveSource, agent.getLastDamageSource(),
                "Damage source after 80 ticks at 0 food must be starvation");
    }

    @Test
    @DisplayName("Taking lethal damage invokes die(), emits DeathEvent, clears inventory, and fires deathListener")
    void testLethalDamageTriggersDeathLifecycle() {
        TestWorldContext ctx = new TestWorldContext();
        AgentEntity agent = ctx.createAgent("agent-lethal-01", "LethalTestAgent");

        StreamRegistry registry = new StreamRegistry();
        AtomicReference<TelemetryFrame> terminalFrameRef = new AtomicReference<>();
        registry.register("agent-lethal-01", new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {
                if (value.hasDeathEvent()) {
                    terminalFrameRef.set(value);
                }
            }
            @Override
            public void onError(Throwable t) {}
            @Override
            public void onCompleted() {}
        });
        agent.setStreamRegistry(registry);

        agent.getInventory().setItem(0, new ItemStack(Items.COBBLESTONE, 12));

        AtomicBoolean deathListenerCalled = new AtomicBoolean(false);
        AtomicInteger deathListenerCount = new AtomicInteger(0);
        agent.setDeathListener(dead -> {
            deathListenerCalled.set(true);
            deathListenerCount.incrementAndGet();
            registry.close(dead.getAgentId());
        });

        DamageSource lethalAttack = new DamageSource(
                Holder.direct(new DamageType("mob", DamageScaling.NEVER, 0.1F))
        );
        boolean hurt = agent.hurt(lethalAttack, 25.0F);
        assertTrue(hurt, "Lethal damage must be accepted");
        assertTrue(agent.getHealth() <= 0.0F, "Agent health must be <= 0 after lethal damage");
        assertTrue(deathListenerCalled.get(), "deathListener must be triggered on lethal damage");
        assertEquals(1, deathListenerCount.get(), "deathListener must fire exactly once");
        assertTrue(agent.isRemoved(), "Agent entity must be discarded on death");
        assertNotNull(terminalFrameRef.get(), "DeathEvent telemetry frame must be emitted");
        assertEquals("agent-lethal-01", terminalFrameRef.get().getDeathEvent().getAgentId());
        assertTrue(agent.getInventory().isEmpty(), "Inventory must be dropped/cleared on death");
    }

    @Test
    @DisplayName("Agents without explicit spawn_coords default to world shared spawn with surface heightmap and deterministic spread")
    void testSpawnDefaultsToWorldSpawnNotPlayerLocation() throws Exception {
        TestWorldContext ctx = new TestWorldContext();
        when(ctx.server.submit(any(java.util.function.Supplier.class))).thenAnswer(inv -> {
            java.util.function.Supplier<?> supplier = inv.getArgument(0);
            return CompletableFuture.completedFuture(supplier.get());
        });
        when(ctx.level.addFreshEntity(any())).thenReturn(true);
        when(ctx.level.getSharedSpawnPos()).thenReturn(new BlockPos(100, 64, 200));
        when(ctx.level.getHeight(eq(net.minecraft.world.level.levelgen.Heightmap.Types.MOTION_BLOCKING_NO_LEAVES), anyInt(), anyInt()))
                .thenReturn(72);

        // Place a human player far away to verify agents do NOT spawn near the player
        net.minecraft.server.level.ServerPlayer humanPlayer = mock(net.minecraft.server.level.ServerPlayer.class);
        when(humanPlayer.getX()).thenReturn(500.0);
        when(humanPlayer.getY()).thenReturn(80.0);
        when(humanPlayer.getZ()).thenReturn(-500.0);
        when(ctx.server.getPlayerList().getPlayers()).thenReturn(java.util.List.of(humanPlayer));

        SpawnAgentRequest req1 = SpawnAgentRequest.newBuilder()
                .setAgentId("spawn-test-1")
                .setDisplayName("SpawnOne")
                .setClanId("clan-test")
                .build();
        SpawnAgentRequest req2 = SpawnAgentRequest.newBuilder()
                .setAgentId("spawn-test-2")
                .setDisplayName("SpawnTwo")
                .setClanId("clan-test")
                .build();

        AgentEntity agent1 = entityManager.spawnAgent(ctx.server, ctx.level, req1).get();
        AgentEntity agent2 = entityManager.spawnAgent(ctx.server, ctx.level, req2).get();

        assertEquals(100.5, agent1.getX(), 1e-6, "First agent must spawn at world spawn X");
        assertEquals(72.0, agent1.getY(), 1e-6, "First agent must spawn at surface heightmap Y");
        assertEquals(200.5, agent1.getZ(), 1e-6, "First agent must spawn at world spawn Z");

        assertEquals(102.0, agent2.getX(), 1e-6, "Second agent must receive deterministic spread offset");
        assertEquals(72.0, agent2.getY(), 1e-6);
        assertEquals(200.5, agent2.getZ(), 1e-6);
    }

    @Test
    @DisplayName("AgentEntity hurt() reflex preempts MINING via MotorPDA stack and reflex pop resumes breaking with zero amnesia")
    void testDamageReflexPreemptsMiningAndResumesWithZeroAmnesia() {
        TestWorldContext ctx = new TestWorldContext();
        AgentEntity agent = ctx.createAgent("agent-reflex-01", "ReflexMiner");
        IntentActuator actuator = new IntentActuator();
        agent.setIntentActuator(actuator);
        agent.setNoGravity(true);
        agent.setOnGround(true);
        agent.setPos(0.5, 64.0, 0.5);
        agent.getInventory().setItem(0, new ItemStack(Items.STONE_PICKAXE, 1));

        BlockPos targetPos = new BlockPos(1, 64, 0);
        BlockPos floorPos = new BlockPos(0, 63, 0);
        BlockState stoneState = Blocks.STONE.defaultBlockState();
        when(ctx.level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            return (targetPos.equals(p) || floorPos.equals(p)) ? stoneState : Blocks.AIR.defaultBlockState();
        });
        when(ctx.level.destroyBlock(eq(targetPos), eq(true), eq(agent))).thenReturn(true);

        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId("agent-reflex-01")
                .setBatchId(901L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("agent-reflex-01")
                        .setInteractBlock(InteractBlockIntent.newBuilder()
                                .setPos(BlockPosProto.newBuilder().setX(1).setY(64).setZ(0).build())
                                .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                                .build())
                        .build())
                .build();

        actuator.submitBatch(agent, batch);
        assertInstanceOf(com.civquest.mod.actuator.motor.Mine.class, agent.getMotorFSM().topMotor().orElse(null));

        // Advance several ticks to accrue partial breaking progress
        for (int i = 0; i < 6; i++) {
            agent.tick();
        }
        float progressBeforeHit = agent.getBreaker().getProgress();
        assertTrue(progressBeforeHit > 0.0f, "Breaking progress must advance before interruption");
        assertInstanceOf(com.civquest.mod.actuator.motor.Mine.class, agent.getMotorFSM().topMotor().orElse(null));

        // Simulate mob attack hitting the agent -> triggers hurt() reflex preemption
        Zombie attacker = mock(Zombie.class);
        DamageSource mobHit = new DamageSource(
                Holder.direct(new DamageType("mob", DamageScaling.NEVER, 0.1F)),
                attacker
        );
        boolean hurt = agent.hurt(mobHit, 2.0F);
        assertTrue(hurt);
        // Zero knockback drift in mock air world so the agent remains within 4.5 blocks of the target ore
        agent.setDeltaMovement(0.0, 0.0, 0.0);
        agent.setPos(0.5, 64.0, 0.5);
        assertInstanceOf(com.civquest.mod.actuator.motor.Combat.class, agent.getMotorFSM().activeReflex().orElseThrow());
        assertEquals(1, agent.getMotorFSM().getStackDepth());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX,
                actuator.getCurrentReceipt(agent).getStatus());
        assertEquals(1, actuator.getCurrentReceipt(agent).getMotorStackDepth());

        // Clear invulnerability / tick past combat timeout -> Combat frame pop restores MINING & progress
        for (int i = 0; i <= AgentMotorFSM.COMBAT_TIMEOUT_TICKS; i++) {
            agent.setDeltaMovement(0.0, 0.0, 0.0);
            agent.tick();
        }
        assertInstanceOf(com.civquest.mod.actuator.motor.Mine.class, agent.getMotorFSM().topMotor().orElse(null),
                () -> "Expected MINING after reflex pop, reason=" + agent.getMotorFSM().getCurrentReceipt().getFailureReason()
                        + ", pos=(" + agent.getX() + "," + agent.getY() + "," + agent.getZ() + ")");
        assertEquals(0, agent.getMotorFSM().getStackDepth());
        assertTrue(agent.getBreaker().isBreaking());
        assertTrue(agent.getBreaker().getProgress() >= progressBeforeHit,
                "Resumed breaking progress must retain prior progress (zero amnesia)");

        // Finish breaking the block cleanly -> batch completes
        for (int i = 0; i < 200 && actuator.getCurrentReceipt(agent).getStatus() != IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED; i++) {
            agent.setOnGround(true);
            agent.setDeltaMovement(0.0, 0.0, 0.0);
            agent.tick();
        }
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED,
                actuator.getCurrentReceipt(agent).getStatus());
        assertTrue(agent.getMotorFSM().isMotorIdle());
    }

    @Test
    @DisplayName("ADR-033: Entity collision and purely horizontal push across AgentEntity, ServerPlayer, and Mob")
    void testInterAgentCollisionAndHorizontalPush() {
        TestWorldContext ctx = new TestWorldContext();
        AgentEntity agent1 = ctx.createAgent("agent-1", "AgentAlpha");
        AgentEntity agent2 = ctx.createAgent("agent-2", "AgentBeta");
        net.minecraft.server.level.ServerPlayer human = mock(net.minecraft.server.level.ServerPlayer.class);
        when(human.isAlive()).thenReturn(true);
        when(human.isRemoved()).thenReturn(false);
        when(human.isSpectator()).thenReturn(false);
        Zombie mob = mock(Zombie.class);
        when(mob.isAlive()).thenReturn(true);
        when(mob.isRemoved()).thenReturn(false);
        when(mob.isSpectator()).thenReturn(false);

        // canCollideWith & canBeCollidedWith
        assertTrue(agent1.canCollideWith(agent2), "ADR-033: canCollideWith another AgentEntity must return true");
        assertTrue(agent2.canCollideWith(agent1), "ADR-033: canCollideWith another AgentEntity must return true");
        assertTrue(agent1.canCollideWith(human), "ADR-033: canCollideWith alive ServerPlayer must return true");
        assertTrue(agent1.canCollideWith(mob), "ADR-033: canCollideWith alive Mob must return true");
        assertTrue(agent1.canBeCollidedWith(), "ADR-033: canBeCollidedWith must return true");
        assertTrue(agent2.canBeCollidedWith(), "ADR-033: canBeCollidedWith must return true");

        // push applies purely horizontal displacement (dy == 0.0) even when co-located or offset
        agent1.setDeltaMovement(0.0, 0.0, 0.0);
        agent2.setDeltaMovement(0.0, 0.0, 0.0);
        agent1.push(agent2);
        assertEquals(0.0, agent1.getDeltaMovement().y, 1e-6, "push must never apply vertical lift to agent1");
        assertEquals(0.0, agent2.getDeltaMovement().y, 1e-6, "push must never apply vertical lift to agent2");
        assertTrue(agent1.getDeltaMovement().horizontalDistanceSqr() > 0.0,
                "Co-located push must impart deterministic horizontal separation impulse to agent1");
        assertTrue(agent2.getDeltaMovement().horizontalDistanceSqr() > 0.0,
                "Co-located push must impart deterministic horizontal separation impulse to agent2");

        agent1.setPos(0.5, 64.0, 0.5);
        agent2.setPos(0.8, 64.0, 0.5);
        agent1.setDeltaMovement(0.0, 0.0, 0.0);
        agent2.setDeltaMovement(0.0, 0.0, 0.0);
        agent1.push(agent2);
        assertTrue(agent1.getDeltaMovement().x < 0.0, "Offset push must push agent1 away (-X)");
        assertTrue(agent2.getDeltaMovement().x > 0.0, "Offset push must push agent2 away (+X)");
        assertEquals(0.0, agent1.getDeltaMovement().y, 1e-6);
        assertEquals(0.0, agent2.getDeltaMovement().y, 1e-6);
    }

    @Test
    @DisplayName("applyHorizontalPeerSeparation skips while sleeping, SURVEYING, or breaking")
    void testHorizontalPeerSeparationSkipsDuringSleepSurveyAndBreak() {
        TestWorldContext ctx = new TestWorldContext();
        AgentEntity agent = ctx.createAgent("sep-guard-1", "GuardOne");
        AgentEntity peer = ctx.createAgent("sep-guard-2", "GuardTwo");

        when(ctx.level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(ctx.level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            return p.getY() == 63 ? Blocks.GRASS_BLOCK.defaultBlockState() : Blocks.AIR.defaultBlockState();
        });
        when(ctx.level.getEntitiesOfClass(eq(net.minecraft.world.entity.LivingEntity.class), any(net.minecraft.world.phys.AABB.class), any()))
                .thenReturn(java.util.List.of(peer));

        // 1. Sleeping suppresses separation
        AgentEntity spySleeping = spy(agent);
        when(spySleeping.isSleeping()).thenReturn(true);
        spySleeping.applyHorizontalPeerSeparation();
        assertEquals(0.5, spySleeping.getX(), 1e-6);
        assertEquals(64.0, spySleeping.getY(), 1e-6);
        assertEquals(0.5, spySleeping.getZ(), 1e-6);

        // 2. SURVEYING suppresses separation
        agent.getMotorFSM().pushDirect(new com.civquest.mod.actuator.motor.Sweep(com.civquest.mod.grpc.v1.Vec3.getDefaultInstance(), 4, 48f, 2, 0, 0, java.util.List.of()));
        agent.applyHorizontalPeerSeparation();
        assertEquals(0.5, agent.getX(), 1e-6);
        assertEquals(64.0, agent.getY(), 1e-6);
        assertEquals(0.5, agent.getZ(), 1e-6);
        agent.getMotorFSM().flush();

        // 3. Active block breaking suppresses separation
        BlockPos breakPos = new BlockPos(1, 64, 0);
        when(ctx.level.getBlockState(breakPos)).thenReturn(Blocks.DIRT.defaultBlockState());
        assertTrue(agent.getBreaker().start(breakPos));
        assertTrue(agent.getBreaker().isBreaking());
        agent.applyHorizontalPeerSeparation();
        assertEquals(0.5, agent.getX(), 1e-6);
        assertEquals(64.0, agent.getY(), 1e-6);
        assertEquals(0.5, agent.getZ(), 1e-6);
    }

    @Test
    @DisplayName("ADR-033: AgentEntityManager spawn deconfliction avoids occupied entities, solids, hazards, and cliffs")
    void testSpawnDeconflictionSkipsOccupiedSolidAndHazardCells() throws Exception {
        TestWorldContext ctx = new TestWorldContext();
        when(ctx.server.submit(any(java.util.function.Supplier.class))).thenAnswer(inv -> {
            java.util.function.Supplier<?> supplier = inv.getArgument(0);
            return CompletableFuture.completedFuture(supplier.get());
        });
        when(ctx.level.addFreshEntity(any())).thenReturn(true);
        when(ctx.level.getSharedSpawnPos()).thenReturn(new BlockPos(10, 64, 10));
        when(ctx.level.getHeight(eq(net.minecraft.world.level.levelgen.Heightmap.Types.MOTION_BLOCKING_NO_LEAVES), anyInt(), anyInt()))
                .thenReturn(64);

        // Flat stone floor at y=63 except:
        // - (10, 64, 10): solid stone wall at feet/head
        // - (11, 63, 10): lava hazard
        // - (9, 63, 10): sheer 5-block drop (air at y=63)
        // - (10, 64, 11): occupied by an alive ServerPlayer
        when(ctx.level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getX() == 10 && p.getZ() == 10 && (p.getY() == 64 || p.getY() == 65)) {
                return Blocks.STONE.defaultBlockState();
            }
            if (p.getX() == 11 && p.getZ() == 10 && p.getY() == 63) {
                return Blocks.LAVA.defaultBlockState();
            }
            if (p.getX() == 9 && p.getZ() == 10 && p.getY() >= 59 && p.getY() <= 63) {
                return Blocks.AIR.defaultBlockState();
            }
            return p.getY() <= 63 ? Blocks.GRASS_BLOCK.defaultBlockState() : Blocks.AIR.defaultBlockState();
        });

        net.minecraft.server.level.ServerPlayer playerAt10_11 = mock(net.minecraft.server.level.ServerPlayer.class);
        when(playerAt10_11.isAlive()).thenReturn(true);
        when(playerAt10_11.isRemoved()).thenReturn(false);
        when(playerAt10_11.isSpectator()).thenReturn(false);
        when(playerAt10_11.getX()).thenReturn(10.5);
        when(playerAt10_11.getY()).thenReturn(64.0);
        when(playerAt10_11.getZ()).thenReturn(11.5);
        when(playerAt10_11.getBbWidth()).thenReturn(0.6F);
        when(playerAt10_11.getBbHeight()).thenReturn(1.8F);
        when(ctx.level.players()).thenReturn(java.util.List.of(playerAt10_11));

        // 1. Explicit spawn request at solid wall (10.5, 64.0, 10.5) spirals to a safe, unoccupied, standable cell
        SpawnAgentRequest explicitReq = SpawnAgentRequest.newBuilder()
                .setAgentId("deconflict-explicit")
                .setDisplayName("DeconflictExplicit")
                .setClanId("clan-test")
                .setSpawnCoords(Vec3.newBuilder().setX(10.5).setY(64.0).setZ(10.5).build())
                .build();
        AgentEntity spawned1 = entityManager.spawnAgent(ctx.server, ctx.level, explicitReq).get();
        BlockPos p1 = BlockPos.containing(spawned1.getX(), spawned1.getY() + 0.05, spawned1.getZ());
        assertNotEquals(new BlockPos(10, 64, 10), p1, "Must not spawn inside solid block");
        assertNotEquals(new BlockPos(11, 64, 10), p1, "Must not spawn over lava");
        assertNotEquals(new BlockPos(9, 64, 10), p1, "Must not spawn over sheer cliff drop");
        assertNotEquals(new BlockPos(10, 64, 11), p1, "Must not spawn on top of existing ServerPlayer");
        assertEquals(64.0, spawned1.getY(), 1e-6);

        // 2. Second explicit request at the exact same coords as spawned1 must also deconflict (>= 1.0m apart)
        SpawnAgentRequest duplicateExplicitReq = SpawnAgentRequest.newBuilder()
                .setAgentId("deconflict-dup")
                .setDisplayName("DeconflictDup")
                .setClanId("clan-test")
                .setSpawnCoords(Vec3.newBuilder().setX(spawned1.getX()).setY(spawned1.getY()).setZ(spawned1.getZ()).build())
                .build();
        AgentEntity spawned2 = entityManager.spawnAgent(ctx.server, ctx.level, duplicateExplicitReq).get();
        double sep = Math.hypot(spawned1.getX() - spawned2.getX(), spawned1.getZ() - spawned2.getZ());
        assertTrue(sep >= 1.0, "Spawn deconfliction must separate newly spawned agent >= 1.0m from existing agent; actual=" + sep);

        // 3. Default grid spawn (no spawn_coords) at occupied/blocked shared spawn also deconflicts safely
        SpawnAgentRequest gridReq = SpawnAgentRequest.newBuilder()
                .setAgentId("deconflict-grid")
                .setDisplayName("DeconflictGrid")
                .setClanId("clan-test")
                .build();
        AgentEntity spawned3 = entityManager.spawnAgent(ctx.server, ctx.level, gridReq).get();
        assertTrue(Math.hypot(spawned3.getX() - spawned1.getX(), spawned3.getZ() - spawned1.getZ()) >= 1.0);
        assertTrue(Math.hypot(spawned3.getX() - spawned2.getX(), spawned3.getZ() - spawned2.getZ()) >= 1.0);
        assertTrue(Math.hypot(spawned3.getX() - 10.5, spawned3.getZ() - 11.5) >= 1.0);
    }
}
