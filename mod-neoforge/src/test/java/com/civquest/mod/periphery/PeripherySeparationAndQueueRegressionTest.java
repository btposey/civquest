package com.civquest.mod.periphery;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.actuator.motor.AgentWatchdogFSM;
import com.civquest.mod.actuator.motor.MotorState;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.test.MinecraftTestBootstrap;
import com.mojang.authlib.GameProfile;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Holder;
import net.minecraft.core.MappedRegistry;
import net.minecraft.core.Registry;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.core.registries.Registries;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.PlayerAdvancements;
import net.minecraft.server.ReloadableServerRegistries;
import net.minecraft.server.ServerScoreboard;
import net.minecraft.server.level.ServerChunkCache;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.server.level.ServerPlayerGameMode;
import net.minecraft.server.network.TextFilter;
import net.minecraft.server.players.PlayerList;
import net.minecraft.stats.ServerStatsCounter;
import net.minecraft.tags.FluidTags;
import net.minecraft.util.Mth;
import net.minecraft.util.RandomSource;
import net.minecraft.util.profiling.InactiveProfiler;
import net.minecraft.world.Difficulty;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.TickRateManager;
import net.minecraft.world.damagesource.DamageScaling;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.damagesource.DamageSources;
import net.minecraft.world.damagesource.DamageType;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.MoverType;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.enchantment.Enchantment;
import net.minecraft.world.level.GameRules;
import net.minecraft.world.level.GameType;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.biome.Biome;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.border.WorldBorder;
import net.minecraft.world.level.dimension.DimensionType;
import net.minecraft.world.level.material.FluidState;
import net.minecraft.world.level.material.Fluids;
import net.minecraft.world.level.storage.WorldData;
import net.minecraft.world.level.storage.loot.LootTable;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.VoxelShape;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.lang.reflect.Field;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyDouble;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.Mockito.*;

/**
 * RED-phase regression test suite for:
 * 1) Horizontal personal-space separation among overlapping {@link AgentEntity} peers
 *    without vertical stacking or hazard/cliff drift (cq-otm.2 / cq-otm.3).
 * 2) {@link IntentActuator} NavigateIntent-only batch coalescing during {@link MotorState#STUCK_RECOVERY}
 *    to prevent rapid-fire 1-tick arrival bursts while preserving non-navigate queued intents.
 */
public class PeripherySeparationAndQueueRegressionTest {

    private static final double HALF_WIDTH = 0.3;
    private static final double HEIGHT = 1.8;

    @BeforeAll
    static void initMinecraft() {
        MinecraftTestBootstrap.ensureBootstrapped();
    }

    // -----------------------------------------------------------------------------------------
    // Shared ServerLevel + World Wiring Helpers (concrete ServerLevel.class only)
    // -----------------------------------------------------------------------------------------

    private static void wireConcreteServerLevelForRealAgents(
            PeripherySimulationHarness harness, AtomicLong gameTime) {
        MinecraftServer mockServer = mock(MinecraftServer.class, withSettings().mockMaker(org.mockito.MockMakers.SUBCLASS));
        ServerLevel mockLevel = harness.getMockLevel();
        PlayerList mockPlayerList = mock(PlayerList.class, withSettings().mockMaker(org.mockito.MockMakers.SUBCLASS));

        WorldData worldData = mock(WorldData.class, withSettings().mockMaker(org.mockito.MockMakers.SUBCLASS));
        ServerPlayerGameMode gameMode = mock(ServerPlayerGameMode.class);
        ServerStatsCounter statsCounter = mock(ServerStatsCounter.class);
        PlayerAdvancements advancements = mock(PlayerAdvancements.class);
        DimensionType dimensionType = mock(DimensionType.class);

        DamageSources damageSources = mock(DamageSources.class);
        DamageSource genericSource = new DamageSource(
                Holder.direct(new DamageType("generic", DamageScaling.NEVER, 0.0F)));
        when(damageSources.generic()).thenReturn(genericSource);
        when(damageSources.inWall()).thenReturn(genericSource);
        when(damageSources.drown()).thenReturn(genericSource);
        when(damageSources.fellOutOfWorld()).thenReturn(genericSource);
        when(damageSources.onFire()).thenReturn(genericSource);
        when(damageSources.lava()).thenReturn(genericSource);

        GameRules gameRules = new GameRules();
        gameRules.getRule(GameRules.RULE_DOMOBLOOT).set(false, mockServer);
        gameRules.getRule(GameRules.RULE_NATURAL_REGENERATION).set(false, mockServer);

        when(mockServer.getPlayerList()).thenReturn(mockPlayerList);
        when(mockServer.isPvpAllowed()).thenReturn(true);
        when(mockServer.isRunning()).thenReturn(true);
        when(mockServer.getRecipeManager()).thenReturn(harness.getRecipeManager());
        when(mockServer.createTextFilterForPlayer(any())).thenReturn(TextFilter.DUMMY);
        when(mockServer.createGameModeForPlayer(any())).thenReturn(gameMode);
        when(mockServer.getWorldData()).thenReturn(worldData);
        when(mockServer.isDedicatedServer()).thenReturn(false);
        doAnswer(invoc -> {
            Runnable r = invoc.getArgument(0);
            r.run();
            return null;
        }).when(mockServer).execute(any(Runnable.class));
        when(mockServer.getTickCount()).thenAnswer(invoc -> (int) gameTime.get());

        ReloadableServerRegistries.Holder reloadableRegistries = mock(ReloadableServerRegistries.Holder.class);
        when(reloadableRegistries.getLootTable(any())).thenReturn(LootTable.EMPTY);
        when(mockServer.reloadableRegistries()).thenReturn(reloadableRegistries);

        when(worldData.getGameType()).thenReturn(GameType.SURVIVAL);
        when(gameMode.getGameModeForPlayer()).thenReturn(GameType.SURVIVAL);
        when(mockPlayerList.getPlayers()).thenReturn(Collections.emptyList());
        when(mockPlayerList.getPlayerStats(any())).thenReturn(statsCounter);
        when(mockPlayerList.getPlayerAdvancements(any())).thenReturn(advancements);

        // Include Registries.ENCHANTMENT for ServerPlayer/LivingEntity initialization
        MappedRegistry<Enchantment> enchantmentRegistry = new MappedRegistry<>(
                Registries.ENCHANTMENT, com.mojang.serialization.Lifecycle.stable());
        enchantmentRegistry.freeze();
        List<Registry<?>> allRegs = new ArrayList<>();
        BuiltInRegistries.REGISTRY.forEach(allRegs::add);
        allRegs.add(enchantmentRegistry);
        RegistryAccess.Frozen fullRegistryAccess = new RegistryAccess.ImmutableRegistryAccess(allRegs).freeze();
        when(mockServer.registryAccess()).thenReturn(fullRegistryAccess);
        when(mockLevel.getServer()).thenReturn(mockServer);
        when(mockLevel.registryAccess()).thenReturn(fullRegistryAccess);

        when(mockLevel.getSharedSpawnPos()).thenReturn(new BlockPos(0, 64, 0));
        when(mockLevel.getSharedSpawnAngle()).thenReturn(0.0F);
        when(mockLevel.dimensionType()).thenReturn(dimensionType);
        when(mockLevel.dimension()).thenReturn(Level.OVERWORLD);
        when(mockLevel.getWorldBorder()).thenReturn(new WorldBorder());
        when(mockLevel.getGameTime()).thenAnswer(invoc -> gameTime.get());
        when(mockLevel.getDayTime()).thenAnswer(invoc -> gameTime.get());

        RandomSource randomSource = RandomSource.create(42L);
        when(mockLevel.getRandom()).thenReturn(randomSource);
        try {
            Field randomField = Level.class.getDeclaredField("random");
            randomField.setAccessible(true);
            randomField.set(mockLevel, randomSource);
        } catch (Throwable t) {
            throw new RuntimeException("Failed to initialize Level.random on concrete ServerLevel mock", t);
        }

        when(mockLevel.getGameRules()).thenReturn(gameRules);
        when(mockLevel.getDifficulty()).thenReturn(Difficulty.NORMAL);
        when(mockLevel.damageSources()).thenReturn(damageSources);
        when(mockLevel.getProfiler()).thenReturn(InactiveProfiler.INSTANCE);
        when(mockLevel.getChunkSource()).thenReturn(mock(ServerChunkCache.class));
        when(mockLevel.getScoreboard()).thenReturn(new ServerScoreboard(mockServer));
        when(mockLevel.tickRateManager()).thenReturn(new TickRateManager());
        when(mockLevel.players()).thenReturn(Collections.emptyList());

        @SuppressWarnings("unchecked")
        Holder<Biome> mockBiome = mock(Holder.class, withSettings().mockMaker(org.mockito.MockMakers.SUBCLASS));
        when(mockLevel.getBiome(any(BlockPos.class))).thenReturn(mockBiome);

        when(mockLevel.getFluidState(any(BlockPos.class))).thenAnswer(invoc -> {
            BlockPos p = invoc.getArgument(0);
            BlockState s = harness.getBlock(p);
            return s != null ? s.getFluidState() : Fluids.EMPTY.defaultFluidState();
        });

        when(mockLevel.getBlockCollisions(any(), any(AABB.class))).thenAnswer(invoc -> {
            AABB box = invoc.getArgument(1);
            List<VoxelShape> shapes = new ArrayList<>();
            int minX = Mth.floor(box.minX + 1e-6);
            int maxX = Mth.floor(box.maxX - 1e-6);
            int minY = Mth.floor(box.minY + 1e-6);
            int maxY = Mth.floor(box.maxY - 1e-6);
            int minZ = Mth.floor(box.minZ + 1e-6);
            int maxZ = Mth.floor(box.maxZ - 1e-6);
            for (int x = minX; x <= maxX; x++) {
                for (int y = minY; y <= maxY; y++) {
                    for (int z = minZ; z <= maxZ; z++) {
                        BlockPos bp = new BlockPos(x, y, z);
                        BlockState st = harness.getBlock(bp);
                        if (isSolidBlock(mockLevel, bp, st)) {
                            shapes.add(st.getCollisionShape(mockLevel, bp).move(x, y, z));
                        }
                    }
                }
            }
            return shapes;
        });

        when(mockLevel.getEntityCollisions(any(), any(AABB.class))).thenReturn(Collections.emptyList());

        when(mockLevel.noCollision(any(), any(AABB.class))).thenAnswer(invoc -> {
            AABB box = invoc.getArgument(1);
            return !collidesWithSolid(harness, mockLevel, box);
        });
    }

    private static AgentEntity spawnRealAgent(
            PeripherySimulationHarness harness, String agentId, String displayName, double x, double y, double z) {
        GameProfile profile = new GameProfile(AgentEntity.createOfflineUUID(displayName), displayName);
        AgentEntity agent = new AgentEntity(harness.getMockLevel(), profile, agentId, "clan-regression");
        agent.setIntentActuator(harness.getIntentActuator());
        agent.setTelemetryContext(harness.getStreamRegistry(), harness.getSensoryCollector());
        agent.setPos(x, y, z);
        agent.setOnGround(true);
        harness.spawnEntity(agent);
        harness.getEntityManager().registerAgent(agent);
        return agent;
    }

    private static boolean isSolidBlock(ServerLevel level, BlockPos pos, BlockState state) {
        if (state == null || state.isAir()) {
            return false;
        }
        VoxelShape shape = state.getCollisionShape(level, pos);
        return shape != null && !shape.isEmpty();
    }

    private static boolean collidesWithSolid(PeripherySimulationHarness harness, ServerLevel level, AABB box) {
        int minX = Mth.floor(box.minX + 1e-6);
        int maxX = Mth.floor(box.maxX - 1e-6);
        int minY = Mth.floor(box.minY + 1e-6);
        int maxY = Mth.floor(box.maxY - 1e-6);
        int minZ = Mth.floor(box.minZ + 1e-6);
        int maxZ = Mth.floor(box.maxZ - 1e-6);
        for (int x = minX; x <= maxX; x++) {
            for (int y = minY; y <= maxY; y++) {
                for (int z = minZ; z <= maxZ; z++) {
                    BlockPos bp = new BlockPos(x, y, z);
                    BlockState st = harness.getBlock(bp);
                    if (isSolidBlock(level, bp, st)) {
                        AABB blockBox = new AABB(bp);
                        if (blockBox.intersects(box.deflate(1e-6))) {
                            return true;
                        }
                    }
                }
            }
        }
        return false;
    }

    private static boolean isLavaAt(PeripherySimulationHarness harness, BlockPos pos) {
        BlockState state = harness.getBlock(pos);
        if (state == null) {
            return false;
        }
        FluidState fluid = state.getFluidState();
        return state.is(Blocks.LAVA) || (fluid != null && fluid.is(FluidTags.LAVA));
    }

    private static void setFlatFloor(PeripherySimulationHarness harness, int y, int minX, int maxX, int minZ, int maxZ) {
        for (int x = minX; x <= maxX; x++) {
            for (int z = minZ; z <= maxZ; z++) {
                harness.setBlock(new BlockPos(x, y, z), Blocks.GRASS_BLOCK.defaultBlockState());
            }
        }
    }

    // -----------------------------------------------------------------------------------------
    // Scenario 1: Horizontal Peer Separation & Hazard/Cliff Guard (RED Phase)
    // -----------------------------------------------------------------------------------------

    @Test
    @DisplayName("1A & 1B. Overlapping AgentEntity peers separate >= 1.0m horizontally without vertical stacking or hazard/cliff drift")
    void testOverlappingPeersSeparateHorizontallyWithoutVerticalStackingOrHazardDrift() {
        // --- Subtest A: Flat safe plateau with 3 overlapping real AgentEntity instances ---
        PeripherySimulationHarness plateauHarness = new PeripherySimulationHarness();
        AtomicLong plateauGameTime = new AtomicLong(1000L);
        wireConcreteServerLevelForRealAgents(plateauHarness, plateauGameTime);
        setFlatFloor(plateauHarness, 63, -10, 10, -10, 10);

        List<AgentEntity> peers = List.of(
                spawnRealAgent(plateauHarness, "peer-a", "PeerAlpha", 0.5, 64.0, 0.5),
                spawnRealAgent(plateauHarness, "peer-b", "PeerBeta", 0.5, 64.0, 0.5),
                spawnRealAgent(plateauHarness, "peer-c", "PeerGamma", 0.5, 64.0, 0.5)
        );

        // Sanity check: concrete ServerLevel.getEntitiesOfClass returns all 3 peers
        List<AgentEntity> foundPeers = plateauHarness.getMockLevel().getEntitiesOfClass(
                AgentEntity.class,
                peers.get(0).getBoundingBox().inflate(2.0),
                Entity::isAlive);
        assertEquals(3, foundPeers.size(),
                "Concrete ServerLevel.getEntitiesOfClass must return all 3 overlapping AgentEntity instances");

        for (int tick = 0; tick < 25; tick++) {
            plateauGameTime.incrementAndGet();
            for (AgentEntity peer : peers) {
                peer.tickCount++;
                peer.tick();
            }
            clearInvocations(plateauHarness.getMockLevel());
        }

        // Assert zero vertical stacking and >= 1.0 block pairwise horizontal separation
        List<org.junit.jupiter.api.function.Executable> assertions = new ArrayList<>();
        for (AgentEntity peer : peers) {
            assertions.add(() -> assertEquals(64.0, peer.getY(), 1e-3,
                    "Peer " + peer.getAgentId() + " must remain on y=64.0 with zero vertical stacking"));
        }
        for (int i = 0; i < peers.size(); i++) {
            for (int j = i + 1; j < peers.size(); j++) {
                AgentEntity a = peers.get(i);
                AgentEntity b = peers.get(j);
                double horizDist = Math.hypot(a.getX() - b.getX(), a.getZ() - b.getZ());
                assertions.add(() -> assertTrue(horizDist >= 1.0,
                        String.format("Subtest A: Overlapping peers %s (%.3f, %.3f) and %s (%.3f, %.3f) must separate >= 1.0m horizontally; actual=%.3f",
                                a.getAgentId(), a.getX(), a.getZ(), b.getAgentId(), b.getX(), b.getZ(), horizDist)));
            }
        }

        // Assert ADR-033 invariants: physical collision restored & push is strictly horizontal (dy == 0.0)
        AgentEntity agentA = peers.get(0);
        AgentEntity agentB = peers.get(1);
        assertions.add(() -> assertTrue(agentA.canCollideWith(agentB), "ADR-033: canCollideWith(AgentEntity) must return true"));
        assertions.add(() -> assertTrue(agentA.canBeCollidedWith(), "ADR-033: canBeCollidedWith() must return true"));
        agentA.setDeltaMovement(Vec3.ZERO);
        agentB.setDeltaMovement(Vec3.ZERO);
        agentA.push(agentB);
        assertions.add(() -> assertEquals(0.0, agentA.getDeltaMovement().y, 1e-6, "ADR-033: push(AgentEntity) must not launch agentA vertically"));
        assertions.add(() -> assertEquals(0.0, agentB.getDeltaMovement().y, 1e-6, "ADR-033: push(AgentEntity) must not launch agentB vertically"));
        assertions.add(() -> assertTrue(agentA.getDeltaMovement().horizontalDistanceSqr() > 0.0, "ADR-033: push(AgentEntity) must impart horizontal separation"));

        // --- Subtest B: Hazard & Cliff Guard ---
        PeripherySimulationHarness hazardHarness = new PeripherySimulationHarness();
        AtomicLong hazardGameTime = new AtomicLong(2000L);
        wireConcreteServerLevelForRealAgents(hazardHarness, hazardGameTime);

        // Safe North/South walkway at x=0, y=63, z=-10..10
        setFlatFloor(hazardHarness, 63, 0, 0, -10, 10);
        // East (x=1..3): Blocks.LAVA at y=63 (stone basin at y=62)
        for (int x = 1; x <= 3; x++) {
            for (int z = -10; z <= 10; z++) {
                hazardHarness.setBlock(new BlockPos(x, 62, z), Blocks.STONE.defaultBlockState());
                hazardHarness.setBlock(new BlockPos(x, 63, z), Blocks.LAVA.defaultBlockState());
            }
        }
        // West (x=-1..-3): 5-block drop (air down to y=59, solid floor at y=58)
        for (int x = -3; x <= -1; x++) {
            for (int z = -10; z <= 10; z++) {
                hazardHarness.setBlock(new BlockPos(x, 58, z), Blocks.STONE.defaultBlockState());
            }
        }

        AgentEntity hazardPeer1 = spawnRealAgent(hazardHarness, "hazard-peer-1", "HazardOne", 0.5, 64.0, 0.5);
        AgentEntity hazardPeer2 = spawnRealAgent(hazardHarness, "hazard-peer-2", "HazardTwo", 0.5, 64.0, 0.5);

        int hazardOrCliffContacts = 0;
        for (int tick = 0; tick < 20; tick++) {
            hazardGameTime.incrementAndGet();
            hazardPeer1.tickCount++;
            hazardPeer1.tick();
            hazardPeer2.tickCount++;
            hazardPeer2.tick();

            for (AgentEntity p : List.of(hazardPeer1, hazardPeer2)) {
                BlockPos feet = BlockPos.containing(p.getX(), p.getY() + 0.05, p.getZ());
                BlockPos below = BlockPos.containing(p.getX(), p.getY() - 0.1, p.getZ());
                if (isLavaAt(hazardHarness, feet) || isLavaAt(hazardHarness, below)
                        || p.getX() >= 1.0 || p.getX() < 0.0 || p.getY() < 63.9) {
                    hazardOrCliffContacts++;
                }
            }
            clearInvocations(hazardHarness.getMockLevel());
        }

        final int observedHazardContacts = hazardOrCliffContacts;
        double safeAxisSep = Math.hypot(
                hazardPeer1.getX() - hazardPeer2.getX(),
                hazardPeer1.getZ() - hazardPeer2.getZ());
        assertions.add(() -> assertEquals(0, observedHazardContacts,
                "Subtest B: Overlapping peers must NEVER step onto East lava (x=1) or West 5-block cliff drop (x=-1)"));
        assertions.add(() -> assertEquals(64.0, hazardPeer1.getY(), 1e-3));
        assertions.add(() -> assertEquals(64.0, hazardPeer2.getY(), 1e-3));
        assertions.add(() -> assertTrue(safeAxisSep >= 1.0,
                String.format("Subtest B: Peers must separate >= 1.0m along safe North/South axes; p1=(%.3f,%.3f), p2=(%.3f,%.3f), dist=%.3f",
                        hazardPeer1.getX(), hazardPeer1.getZ(), hazardPeer2.getX(), hazardPeer2.getZ(), safeAxisSep)));

        assertAll(assertions);
    }

    @Test
    @DisplayName("1C. External ServerPlayer and Mob overlapping a stationary AgentEntity trigger safe horizontal depenetration")
    void testExternalPlayerAndMobOverlapTriggersSafeHorizontalDepenetration() {
        PeripherySimulationHarness harness = new PeripherySimulationHarness();
        AtomicLong gameTime = new AtomicLong(2500L);
        wireConcreteServerLevelForRealAgents(harness, gameTime);

        // Safe North/South walkway at x=0, y=63, z=-10..10; East (x=1) is lava; West (x=-1) is 5-block cliff
        setFlatFloor(harness, 63, 0, 0, -10, 10);
        for (int z = -10; z <= 10; z++) {
            harness.setBlock(new BlockPos(1, 62, z), Blocks.STONE.defaultBlockState());
            harness.setBlock(new BlockPos(1, 63, z), Blocks.LAVA.defaultBlockState());
            harness.setBlock(new BlockPos(-1, 58, z), Blocks.STONE.defaultBlockState());
        }

        AgentEntity agent = spawnRealAgent(harness, "depen-agent", "DepenAgent", 0.5, 64.0, 0.5);

        // 1. Human ServerPlayer walks directly onto the stationary agent at (0.5, 64.0, 0.6)
        ServerPlayer humanPlayer = mock(ServerPlayer.class);
        when(humanPlayer.isAlive()).thenReturn(true);
        when(humanPlayer.isRemoved()).thenReturn(false);
        when(humanPlayer.isSpectator()).thenReturn(false);
        when(humanPlayer.isSleeping()).thenReturn(false);
        when(humanPlayer.getX()).thenReturn(0.5);
        when(humanPlayer.getY()).thenReturn(64.0);
        when(humanPlayer.getZ()).thenReturn(0.6);
        when(humanPlayer.getBbWidth()).thenReturn(0.6F);
        when(humanPlayer.getBbHeight()).thenReturn(1.8F);
        when(humanPlayer.getBoundingBox()).thenReturn(new AABB(0.2, 64.0, 0.3, 0.8, 65.8, 0.9));
        when(humanPlayer.getUUID()).thenReturn(UUID.randomUUID());
        harness.spawnEntity(humanPlayer);

        for (int tick = 0; tick < 18; tick++) {
            gameTime.incrementAndGet();
            agent.tickCount++;
            agent.tick();
            assertEquals(64.0, agent.getY(), 1e-3, "Depenetration must stay strictly horizontal");
            assertTrue(agent.getX() >= 0.0 && agent.getX() < 1.0,
                    "Depenetration from ServerPlayer must never enter East lava or West cliff");
            clearInvocations(harness.getMockLevel());
        }
        double distFromPlayer = Math.hypot(agent.getX() - 0.5, agent.getZ() - 0.6);
        assertTrue(distFromPlayer >= 1.0,
                "Agent must depenetrate >= 1.0m from overlapping ServerPlayer along safe axis; actual=" + distFromPlayer);

        // 2. Mob (Zombie) walks onto the agent's new position
        harness.removeEntity(humanPlayer);
        double zombieX = agent.getX();
        double zombieZ = agent.getZ() + 0.1;
        net.minecraft.world.entity.monster.Zombie zombie = mock(net.minecraft.world.entity.monster.Zombie.class);
        when(zombie.isAlive()).thenReturn(true);
        when(zombie.isRemoved()).thenReturn(false);
        when(zombie.isSpectator()).thenReturn(false);
        when(zombie.isSleeping()).thenReturn(false);
        when(zombie.getX()).thenReturn(zombieX);
        when(zombie.getY()).thenReturn(64.0);
        when(zombie.getZ()).thenReturn(zombieZ);
        when(zombie.getBbWidth()).thenReturn(0.6F);
        when(zombie.getBbHeight()).thenReturn(1.95F);
        when(zombie.getBoundingBox()).thenReturn(
                new AABB(zombieX - 0.3, 64.0, zombieZ - 0.3, zombieX + 0.3, 65.95, zombieZ + 0.3));
        when(zombie.getUUID()).thenReturn(UUID.randomUUID());
        harness.spawnEntity(zombie);

        for (int tick = 0; tick < 18; tick++) {
            gameTime.incrementAndGet();
            agent.tickCount++;
            agent.tick();
            assertEquals(64.0, agent.getY(), 1e-3);
            assertTrue(agent.getX() >= 0.0 && agent.getX() < 1.0,
                    "Depenetration from Mob must never enter East lava or West cliff");
            clearInvocations(harness.getMockLevel());
        }
        double distFromZombie = Math.hypot(agent.getX() - zombieX, agent.getZ() - zombieZ);
        assertTrue(distFromZombie >= 1.0,
                "Agent must depenetrate >= 1.0m from overlapping Mob along safe axis; actual=" + distFromZombie);
    }

    // -----------------------------------------------------------------------------------------
    // Scenario 2: NavigateIntent-only Batches During STUCK_RECOVERY Coalescing (RED Phase)
    // -----------------------------------------------------------------------------------------

    @Test
    @DisplayName("2A. Navigate-only batches during 20-tick STUCK_RECOVERY coalesce instead of queuing 5 rapid-fire arrivals")
    void testNavigateOnlyBatchesDuringStuckRecoveryCoalesceInsteadOfQueuingFiveRapidArrivals() {
        PeripherySimulationHarness harness = new PeripherySimulationHarness();
        ServerLevel mockLevel = harness.getMockLevel();
        AtomicLong gameTime = new AtomicLong(3000L);
        AtomicReference<Vec3> positionRef = new AtomicReference<>(new Vec3(0.5, 64.0, 0.5));
        AtomicReference<Vec3> deltaMovementRef = new AtomicReference<>(Vec3.ZERO);
        AtomicReference<Float> yawRef = new AtomicReference<>(-90.0f);
        AtomicReference<Float> headYawRef = new AtomicReference<>(-90.0f);
        AtomicReference<Float> pitchRef = new AtomicReference<>(0.0f);

        setupMockServerAndCollisionWorld(harness, mockLevel, gameTime);
        setFlatFloor(harness, 63, -15, 15, -15, 15);
        // Sheer unbreakable bedrock wall at x=2 across z=-12..12 blocking path to (10.5, 64.0, 0.5)
        for (int z = -12; z <= 12; z++) {
            for (int y = 64; y <= 68; y++) {
                harness.setBlock(new BlockPos(2, y, z), Blocks.BEDROCK.defaultBlockState());
            }
        }

        AgentEntity agent = createDynamicPhysicsMockAgent(
                harness, mockLevel, "agent-stuck-coalesce", "StuckAgent",
                positionRef, deltaMovementRef, yawRef, headYawRef, pitchRef);

        AtomicInteger navigationArrivedTransitions = new AtomicInteger(0);
        AgentMotorFSM spyFsm = spy(new AgentMotorFSM(agent));
        doAnswer(invoc -> {
            MotorState prev = spyFsm.getState();
            boolean changed = (boolean) invoc.callRealMethod();
            MotorState target = invoc.getArgument(0);
            String reason = invoc.getArgument(1);
            if (changed && prev == MotorState.MOVING && target == MotorState.IDLE
                    && ("navigation_arrived".equals(reason) || "navigate_already_arrived".equals(reason))) {
                navigationArrivedTransitions.incrementAndGet();
            }
            return changed;
        }).when(spyFsm).transitionTo(any(), any());
        when(agent.getMotorFSM()).thenReturn(spyFsm);

        AgentWatchdogFSM watchdogFSM = new AgentWatchdogFSM(agent);
        when(agent.getWatchdogFSM()).thenReturn(watchdogFSM);

        harness.connectAgentStream("agent-stuck-coalesce");
        harness.clearReceivedFrames();

        // 1. Dispatch initial NavigateIntent toward (10.5, 64.0, 0.5) blocked by sheer wall
        harness.sendIntentBatch(navigateBatch("agent-stuck-coalesce", 1L, 10.5, 64.0, 0.5, 1));
        assertEquals(MotorState.MOVING, spyFsm.getState());

        // Advance until AgentNavigator hits stuckTicks >= 16 and MotorFSM enters STUCK_RECOVERY
        for (int t = 0; t < 40 && spyFsm.getState() != MotorState.STUCK_RECOVERY; t++) {
            tickMockAgentServer(harness, mockLevel, agent, watchdogFSM, gameTime);
        }
        assertEquals(MotorState.STUCK_RECOVERY, spyFsm.getState(),
                "Blocked navigation against sheer wall must enter MotorState.STUCK_RECOVERY");

        // 2. While in 20-tick STUCK_RECOVERY, submit 5 consecutive NavigateIntent-only batches (5 Hz EvaluateUnstuck)
        IntentActuator actuator = harness.getIntentActuator();
        int maxQueuedDuringStuckRecovery = actuator.getQueuedBatchCount(agent);
        int submittedDuringRecovery = 0;

        for (int t = 0; t < AgentMotorFSM.STUCK_RECOVERY_MAX_TICKS + 2; t++) {
            if (spyFsm.getState() == MotorState.STUCK_RECOVERY && submittedDuringRecovery < 5 && (t % 4 == 0)) {
                double targetZ = (submittedDuringRecovery % 2 == 0) ? 0.5 : -0.5;
                long batchId = 100L + submittedDuringRecovery;
                harness.sendIntentBatch(navigateBatch("agent-stuck-coalesce", batchId, 1.5, 64.0, targetZ, 1));
                submittedDuringRecovery++;
            }
            if (spyFsm.getState() == MotorState.STUCK_RECOVERY) {
                maxQueuedDuringStuckRecovery = Math.max(maxQueuedDuringStuckRecovery, actuator.getQueuedBatchCount(agent));
            }
            tickMockAgentServer(harness, mockLevel, agent, watchdogFSM, gameTime);
            if (spyFsm.getState() != MotorState.STUCK_RECOVERY && submittedDuringRecovery == 5) {
                break;
            }
        }

        assertEquals(5, submittedDuringRecovery, "All 5 NavigateIntent-only batches must be submitted during STUCK_RECOVERY");

        // 3. Observe the 20 ticks after STUCK_RECOVERY exits: must NOT burst 4-5 rapid-fire arrivals
        navigationArrivedTransitions.set(0);
        for (int t = 0; t < 20; t++) {
            tickMockAgentServer(harness, mockLevel, agent, watchdogFSM, gameTime);
        }

        final int observedMaxQueued = maxQueuedDuringStuckRecovery;
        final int observedArrivals = navigationArrivedTransitions.get();
        assertAll(
                () -> assertTrue(observedMaxQueued <= 1,
                        "NavigateIntent-only batches during STUCK_RECOVERY must coalesce so queued batch count stays <= 1; actual="
                                + observedMaxQueued),
                () -> assertTrue(observedArrivals <= 1,
                        "After STUCK_RECOVERY exits, MOVING -> IDLE (navigation_arrived) bursts over next 20 ticks must be <= 1; actual="
                                + observedArrivals)
        );
    }

    @Test
    @DisplayName("2B. Non-navigate CraftItemIntent and InteractBlockIntent queued behind MOVING are preserved cleanly")
    void testNonNavigateIntentsQueuedBehindMovingArePreservedCleanly() {
        PeripherySimulationHarness harness = new PeripherySimulationHarness();
        ServerLevel mockLevel = harness.getMockLevel();
        AtomicLong gameTime = new AtomicLong(4000L);
        AtomicReference<Vec3> positionRef = new AtomicReference<>(new Vec3(0.5, 64.0, 0.5));
        AtomicReference<Vec3> deltaMovementRef = new AtomicReference<>(Vec3.ZERO);
        AtomicReference<Float> yawRef = new AtomicReference<>(-90.0f);
        AtomicReference<Float> headYawRef = new AtomicReference<>(-90.0f);
        AtomicReference<Float> pitchRef = new AtomicReference<>(0.0f);

        setupMockServerAndCollisionWorld(harness, mockLevel, gameTime);
        setFlatFloor(harness, 63, -15, 15, -15, 15);
        // Crafting table adjacent to destination (2.5, 64.0, 0.5) so 3x3 chest craft succeeds after arrival
        harness.setBlock(new BlockPos(3, 64, 1), Blocks.CRAFTING_TABLE.defaultBlockState());

        AgentEntity agent = createDynamicPhysicsMockAgent(
                harness, mockLevel, "agent-preserve-queue", "QueueKeeper",
                positionRef, deltaMovementRef, yawRef, headYawRef, pitchRef);
        agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 8));
        agent.getInventory().setItem(1, new ItemStack(Items.COBBLESTONE, 4));

        AgentWatchdogFSM watchdogFSM = new AgentWatchdogFSM(agent);
        when(agent.getWatchdogFSM()).thenReturn(watchdogFSM);

        harness.connectAgentStream("agent-preserve-queue");
        harness.clearReceivedFrames();

        // 1. Start MOVING toward (2.5, 64.0, 0.5)
        harness.sendIntentBatch(navigateBatch("agent-preserve-queue", 1L, 2.5, 64.0, 0.5, 1));
        assertEquals(MotorState.MOVING, agent.getMotorFSM().getState());

        // 2. Queue a 3x3 CraftItemIntent (chest) and an InteractBlockIntent (PLACE_BLOCK) behind MOVING
        IntentBatch craftBatch = IntentBatch.newBuilder()
                .setAgentId("agent-preserve-queue")
                .setBatchId(2L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("agent-preserve-queue")
                        .setSequenceId(2L)
                        .setCraftItem(CraftItemIntent.newBuilder()
                                .setRecipeId("minecraft:chest")
                                .build())
                        .build())
                .build();
        IntentBatch placeBatch = IntentBatch.newBuilder()
                .setAgentId("agent-preserve-queue")
                .setBatchId(3L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("agent-preserve-queue")
                        .setSequenceId(3L)
                        .setInteractBlock(InteractBlockIntent.newBuilder()
                                .setPos(BlockPosProto.newBuilder().setX(2).setY(64).setZ(-1).build())
                                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                                .setBlockOrItemId("minecraft:cobblestone")
                                .build())
                        .build())
                .build();

        harness.sendIntentBatch(craftBatch);
        harness.sendIntentBatch(placeBatch);

        assertEquals(2, harness.getIntentActuator().getQueuedBatchCount(agent),
                "Both non-navigate batches (CraftItemIntent & InteractBlockIntent) must be preserved in pendingBatches behind MOVING");

        // 3. Tick until navigation arrives and queued batches drain and complete
        for (int t = 0; t < 40; t++) {
            tickMockAgentServer(harness, mockLevel, agent, watchdogFSM, gameTime);
            if (harness.getIntentActuator().getQueuedBatchCount(agent) == 0
                    && agent.getMotorFSM().getState() == MotorState.IDLE
                    && harness.getIntentActuator().getCurrentReceipt(agent).getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED) {
                break;
            }
        }

        assertEquals(0, harness.getIntentActuator().getQueuedBatchCount(agent));
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED,
                harness.getIntentActuator().getCurrentReceipt(agent).getStatus());
        assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.CHEST)),
                "Queued CraftItemIntent (minecraft:chest) must execute after MOVING completes");
        assertEquals(Blocks.COBBLESTONE.defaultBlockState(), harness.getBlock(new BlockPos(2, 64, -1)),
                "Queued InteractBlockIntent (PLACE_BLOCK) must execute after MOVING completes");
    }

    // -----------------------------------------------------------------------------------------
    // Mock-Agent Voxel Physics Helpers for Scenario 2
    // -----------------------------------------------------------------------------------------

    private static void setupMockServerAndCollisionWorld(
            PeripherySimulationHarness harness, ServerLevel mockLevel, AtomicLong gameTime) {
        doAnswer(invoc -> {
            Runnable r = invoc.getArgument(0);
            r.run();
            return null;
        }).when(harness.getMockServer()).execute(any(Runnable.class));
        doAnswer(invoc -> (int) gameTime.get()).when(harness.getMockServer()).getTickCount();
        when(mockLevel.getGameTime()).thenAnswer(invoc -> gameTime.get());
        when(mockLevel.getDayTime()).thenAnswer(invoc -> gameTime.get());
        when(mockLevel.getFluidState(any(BlockPos.class))).thenAnswer(invoc -> {
            BlockPos p = invoc.getArgument(0);
            BlockState s = harness.getBlock(p);
            return s != null ? s.getFluidState() : Fluids.EMPTY.defaultFluidState();
        });
        when(mockLevel.getBlockCollisions(any(), any(AABB.class))).thenAnswer(invoc -> {
            AABB box = invoc.getArgument(1);
            List<VoxelShape> shapes = new ArrayList<>();
            int minX = Mth.floor(box.minX + 1e-6);
            int maxX = Mth.floor(box.maxX - 1e-6);
            int minY = Mth.floor(box.minY + 1e-6);
            int maxY = Mth.floor(box.maxY - 1e-6);
            int minZ = Mth.floor(box.minZ + 1e-6);
            int maxZ = Mth.floor(box.maxZ - 1e-6);
            for (int x = minX; x <= maxX; x++) {
                for (int y = minY; y <= maxY; y++) {
                    for (int z = minZ; z <= maxZ; z++) {
                        BlockPos bp = new BlockPos(x, y, z);
                        BlockState st = harness.getBlock(bp);
                        if (isSolidBlock(mockLevel, bp, st)) {
                            shapes.add(st.getCollisionShape(mockLevel, bp).move(x, y, z));
                        }
                    }
                }
            }
            return shapes;
        });
    }

    private static AgentEntity createDynamicPhysicsMockAgent(
            PeripherySimulationHarness harness,
            ServerLevel mockLevel,
            String agentId,
            String displayName,
            AtomicReference<Vec3> positionRef,
            AtomicReference<Vec3> deltaMovementRef,
            AtomicReference<Float> yawRef,
            AtomicReference<Float> headYawRef,
            AtomicReference<Float> pitchRef) {
        AgentEntity agent = harness.createMockAgent(agentId, displayName, new BlockPos(0, 64, 0));

        when(agent.position()).thenAnswer(invoc -> positionRef.get());
        when(agent.getX()).thenAnswer(invoc -> positionRef.get().x);
        when(agent.getY()).thenAnswer(invoc -> positionRef.get().y);
        when(agent.getZ()).thenAnswer(invoc -> positionRef.get().z);
        when(agent.getEyeY()).thenAnswer(invoc -> positionRef.get().y + 1.62);
        when(agent.getEyePosition()).thenAnswer(invoc -> {
            Vec3 p = positionRef.get();
            return new Vec3(p.x, p.y + 1.62, p.z);
        });
        when(agent.blockPosition()).thenAnswer(invoc -> BlockPos.containing(positionRef.get()));
        when(agent.getBoundingBox()).thenAnswer(invoc -> aabbAt(positionRef.get()));
        when(agent.onGround()).thenAnswer(invoc ->
                collidesWithSolid(harness, mockLevel, aabbAt(positionRef.get()).move(0.0, -0.05, 0.0)));
        when(agent.getDeltaMovement()).thenAnswer(invoc -> deltaMovementRef.get());
        doAnswer(invoc -> {
            deltaMovementRef.set(invoc.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));
        doAnswer(invoc -> {
            deltaMovementRef.set(new Vec3(invoc.getArgument(0), invoc.getArgument(1), invoc.getArgument(2)));
            return null;
        }).when(agent).setDeltaMovement(anyDouble(), anyDouble(), anyDouble());
        doAnswer(invoc -> {
            positionRef.set(new Vec3(invoc.getArgument(0), invoc.getArgument(1), invoc.getArgument(2)));
            return null;
        }).when(agent).setPos(anyDouble(), anyDouble(), anyDouble());
        doAnswer(invoc -> {
            performVoxelMove(harness, mockLevel, agent, positionRef, deltaMovementRef, invoc.getArgument(1));
            return null;
        }).when(agent).move(any(MoverType.class), any(Vec3.class));

        when(agent.getYRot()).thenAnswer(invoc -> yawRef.get());
        doAnswer(invoc -> {
            yawRef.set(invoc.getArgument(0));
            return null;
        }).when(agent).setYRot(anyFloat());
        when(agent.getYHeadRot()).thenAnswer(invoc -> headYawRef.get());
        doAnswer(invoc -> {
            headYawRef.set(invoc.getArgument(0));
            return null;
        }).when(agent).setYHeadRot(anyFloat());
        when(agent.getXRot()).thenAnswer(invoc -> pitchRef.get());
        doAnswer(invoc -> {
            pitchRef.set(invoc.getArgument(0));
            return null;
        }).when(agent).setXRot(anyFloat());

        when(agent.getAirSupply()).thenReturn(300);
        when(agent.getItemInHand(any(InteractionHand.class))).thenReturn(ItemStack.EMPTY);
        when(agent.getOffhandItem()).thenReturn(ItemStack.EMPTY);
        when(agent.getUseItem()).thenReturn(ItemStack.EMPTY);
        when(agent.isUsingItem()).thenReturn(false);

        AgentNavigator navigator = new AgentNavigator(agent);
        when(agent.getNavigator()).thenReturn(navigator);
        return agent;
    }

    private static void performVoxelMove(
            PeripherySimulationHarness harness,
            ServerLevel level,
            AgentEntity agent,
            AtomicReference<Vec3> positionRef,
            AtomicReference<Vec3> deltaMovementRef,
            Vec3 move) {
        Vec3 cur = positionRef.get();
        double x = cur.x;
        double y = cur.y;
        double z = cur.z;

        double dy = move.y;
        if (Math.abs(dy) > 1e-9) {
            int steps = Math.max(1, (int) Math.ceil(Math.abs(dy) / 0.05));
            double stepY = dy / steps;
            for (int i = 0; i < steps; i++) {
                double candY = y + stepY;
                if (collidesWithSolid(harness, level, aabbAt(new Vec3(x, candY, z)))) {
                    if (stepY < 0) {
                        y = Math.floor(y + 1e-6);
                    }
                    Vec3 dm = deltaMovementRef.get();
                    deltaMovementRef.set(new Vec3(dm.x, 0.0, dm.z));
                    break;
                }
                y = candY;
            }
        }

        boolean horizCollided = false;
        double dx = move.x;
        if (Math.abs(dx) > 1e-9) {
            int steps = Math.max(1, (int) Math.ceil(Math.abs(dx) / 0.05));
            double stepX = dx / steps;
            for (int i = 0; i < steps; i++) {
                double candX = x + stepX;
                if (collidesWithSolid(harness, level, aabbAt(new Vec3(candX, y, z)))) {
                    horizCollided = true;
                    break;
                }
                x = candX;
            }
        }

        double dz = move.z;
        if (Math.abs(dz) > 1e-9) {
            int steps = Math.max(1, (int) Math.ceil(Math.abs(dz) / 0.05));
            double stepZ = dz / steps;
            for (int i = 0; i < steps; i++) {
                double candZ = z + stepZ;
                if (collidesWithSolid(harness, level, aabbAt(new Vec3(x, y, candZ)))) {
                    horizCollided = true;
                    break;
                }
                z = candZ;
            }
        }

        agent.horizontalCollision = horizCollided;
        positionRef.set(new Vec3(x, y, z));
    }

    private static AABB aabbAt(Vec3 pos) {
        return new AABB(
                pos.x - HALF_WIDTH, pos.y, pos.z - HALF_WIDTH,
                pos.x + HALF_WIDTH, pos.y + HEIGHT, pos.z + HALF_WIDTH
        );
    }

    private static void tickMockAgentServer(
            PeripherySimulationHarness harness,
            ServerLevel mockLevel,
            AgentEntity agent,
            AgentWatchdogFSM watchdogFSM,
            AtomicLong gameTime) {
        agent.tickCount++;
        gameTime.incrementAndGet();
        watchdogFSM.tick();
        agent.getMotorFSM().tick();
        harness.getIntentActuator().tick(agent);
        harness.getEntityManager().tickTelemetry(agent.tickCount);
        clearInvocations(mockLevel, agent);
    }

    private static IntentBatch navigateBatch(
            String agentId, long batchId, double x, double y, double z, int tolerance) {
        return IntentBatch.newBuilder()
                .setAgentId(agentId)
                .setBatchId(batchId)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(agentId)
                        .setSequenceId(batchId)
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(com.civquest.mod.grpc.v1.Vec3.newBuilder()
                                        .setX(x).setY(y).setZ(z).build())
                                .setArrivalTolerance(tolerance)
                                .build())
                        .build())
                .build();
    }
}
