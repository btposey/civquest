package com.civquest.mod.gametest;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.AgentEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.actuator.LookSmoothing;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.actuator.intent.AttackHandler;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import com.civquest.mod.grpc.v1.VisibleBlock;
import com.civquest.mod.grpc.v1.VisibleEntity;
import com.google.protobuf.ByteString;
import io.grpc.stub.StreamObserver;
import net.minecraft.core.BlockPos;
import net.minecraft.core.component.DataComponents;
import net.minecraft.gametest.framework.GameTest;
import net.minecraft.gametest.framework.GameTestHelper;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.entity.EntityType;
import net.minecraft.world.entity.monster.Creeper;
import net.minecraft.world.entity.monster.Zombie;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.component.WrittenBookContent;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.CropBlock;
import net.minecraft.world.level.block.LecternBlock;
import net.minecraft.world.level.block.entity.LecternBlockEntity;
import net.minecraft.world.level.block.entity.SignBlockEntity;
import net.neoforged.neoforge.common.damagesource.DamageContainer;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;
import net.neoforged.neoforge.gametest.GameTestHolder;
import net.neoforged.neoforge.gametest.PrefixGameTestTemplate;

import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

@GameTestHolder("civquest")
@PrefixGameTestTemplate(false)
public class AgentGameTest {

    @GameTest(template = "empty3x3x3")
    public static void testAgentEnvironment(GameTestHelper helper) {
        BlockPos pos = new BlockPos(1, 1, 1);
        helper.setBlock(pos, Blocks.STONE);
        helper.assertBlockPresent(Blocks.STONE, pos);
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testAgentSpawnAndTick(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-spawn")
                .setDisplayName("AgentGameTest")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .setSkinPngBytes(ByteString.copyFrom("test-skin-bytes", StandardCharsets.UTF_8))
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "AgentEntity must not be null");
        helper.assertTrue(agent == manager.getAgent("agent-gt-spawn"), "Manager must track active agent");
        helper.assertTrue("agent-gt-spawn".equals(agent.getAgentId()), "Agent ID must match request");
        helper.assertTrue("clan-ironwood".equals(agent.getClanId()), "Clan ID must match request");
        helper.assertTrue(agent.getFoodData().getFoodLevel() == 20, "Food level must initialize to 20");
        helper.assertTrue(agent.getHealth() == agent.getMaxHealth(), "Health must initialize to maximum");
        helper.assertFalse(agent.canUsePortal(false), "Agent cannot use portals");

        helper.assertTrue(agent.chunkPosition().equals(agent.getHeldChunk()), "Agent must hold its spawn chunk");

        // Tick agent entity in server level
        agent.tick();
        helper.assertTrue(agent.isAlive(), "Agent must remain alive after tick");

        // Clean despawn
        boolean despawned = manager.despawnAgent(server, "agent-gt-spawn").join();
        helper.assertTrue(despawned, "Agent must despawn cleanly");
        helper.assertTrue(manager.getAgent("agent-gt-spawn") == null, "Manager must clear despawned agent");
        helper.assertTrue(agent.getHeldChunk() == null, "Held chunk must be released after despawn");
        helper.succeed();
    }

    // cq-gey.4: spruces carry leaves to the ground; an agent must be able to walk in to the trunk.
    @GameTest(template = "empty3x3x3")
    public static void testWalksThroughLeavesToATrunk(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        stoneFloor(helper, -9, 9, LEAF_ARENA_Y);
        BlockPos trunk = new BlockPos(4, LEAF_ARENA_Y + 1, 0);
        for (int x = -3; x <= 3; x++) {
            for (int z = -3; z <= 3; z++) {
                for (int y = 0; y <= 1; y++) {
                    helper.setBlock(trunk.offset(x, y, z), Blocks.SPRUCE_LEAVES);
                }
            }
        }
        for (int y = 0; y <= 2; y++) {
            helper.setBlock(trunk.above(y), Blocks.SPRUCE_LOG);
        }
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-leaves", new BlockPos(-8, LEAF_ARENA_Y + 1, 0));
        BlockPos to = helper.absolutePos(trunk);
        navigateByIntent(manager, server, "agent-gt-leaves", to.getX() + 0.5, to.getY(), to.getZ() + 0.5, false, false);
        for (int t = 0; t < 300 && !agent.getNavigator().isArrived(); t++) {
            agent.tick();
        }
        double remaining = Math.hypot(agent.getX() - (to.getX() + 0.5), agent.getZ() - (to.getZ() + 0.5));
        helper.assertTrue(remaining <= 2.5, "Agent ended " + remaining + " blocks short of the trunk at " + agent.position());
        manager.despawnAgent(server, "agent-gt-leaves").join();
        helper.succeed();
    }

    // cq-gey.1: agents far from any player and from spawn must keep ticking.
    @GameTest(template = "empty3x3x3", timeoutTicks = 200)
    public static void testAgentKeepsTickingFarFromSpawn(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();
        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-wander")
                .setDisplayName("Wanderer")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build()).join();
        AtomicInteger ticksWhenMoved = new AtomicInteger();
        helper.runAfterDelay(5, () -> {
            agent.setPos(spawnPos.getX() + 640.5, spawnPos.getY(), spawnPos.getZ() + 0.5);
            ticksWhenMoved.set(agent.tickCount);
        });
        helper.runAfterDelay(65, () -> {
            int ticked = agent.tickCount - ticksWhenMoved.get();
            boolean ticking = level.isPositionEntityTicking(agent.blockPosition());
            manager.despawnAgent(server, "agent-gt-wander");
            helper.assertTrue(ticking, "The chunk around a distant agent must tick entities");
            helper.assertTrue(ticked > 40, "A distant agent must keep ticking, ticked " + ticked + " of 60");
            helper.succeed();
        });
    }

    @GameTest(template = "empty3x3x3")
    public static void testAgentPermadeathLifecycle(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        StreamRegistry streamRegistry = new StreamRegistry();
        AgentEntityManager manager = new AgentEntityManager(streamRegistry);

        AtomicReference<TelemetryFrame> deathFrameRef = new AtomicReference<>();
        AtomicBoolean streamOpenWhenDeathReceived = new AtomicBoolean(false);

        // Register active telemetry stream for agent
        streamRegistry.register("agent-gt-death", new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {
                if (value.hasDeathEvent()) {
                    deathFrameRef.set(value);
                    streamOpenWhenDeathReceived.set(streamRegistry.isRegistered("agent-gt-death"));
                }
            }
            @Override
            public void onError(Throwable t) {}
            @Override
            public void onCompleted() {}
        });
        helper.assertTrue(streamRegistry.isRegistered("agent-gt-death"), "Agent stream must be registered");

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-death")
                .setDisplayName("MortalAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");
        helper.assertTrue(manager.getAgent("agent-gt-death") != null, "Agent must be registered");
        helper.assertTrue(agent.getHeldChunk() != null, "Agent must hold a chunk");

        // Kill agent with generic damage
        agent.die(level.damageSources().generic());

        // Agent should be discarded and automatically unregistered via deathListener
        helper.assertTrue(agent.isRemoved(), "Agent entity must be removed from level upon death");
        helper.assertTrue(manager.getAgent("agent-gt-death") == null, "Agent must be unregistered upon death");
        helper.assertFalse(streamRegistry.isRegistered("agent-gt-death"), "Stream must be cleanly closed on death (ADR-008)");
        helper.assertTrue(deathFrameRef.get() != null, "Terminal death telemetry frame must be sent prior to stream closure");
        helper.assertTrue(deathFrameRef.get().hasDeathEvent(), "Terminal frame must contain death_event");
        helper.assertTrue("agent-gt-death".equals(deathFrameRef.get().getDeathEvent().getAgentId()), "DeathEvent must have matching agent_id");
        helper.assertTrue(streamOpenWhenDeathReceived.get(), "Death frame must be delivered while stream was still registered");
        helper.assertTrue(deathFrameRef.get().getInterestingBlocksCount() == 0,
                "Terminal death frame must skip block scanning");
        helper.assertTrue(deathFrameRef.get().getVisibleEntitiesCount() == 0,
                "Terminal death frame must skip entity scanning");
        helper.assertTrue(agent.getHeldChunk() == null, "Held chunk must be released on death");
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testDuplicateAgentSpawn(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request1 = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-dup")
                .setDisplayName("FirstAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent1 = manager.spawnAgent(server, level, request1).join();
        helper.assertTrue(agent1 != null, "First agent must spawn");
        helper.assertTrue(manager.count() == 1, "Manager must have 1 agent");
        helper.assertTrue("FirstAgent".equals(agent1.getGameProfile().getName()), "Name must match first");

        // Spawn second agent with same agentId
        SpawnAgentRequest request2 = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-dup")
                .setDisplayName("SecondAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent2 = manager.spawnAgent(server, level, request2).join();
        helper.assertTrue(agent2 != null, "Second agent must spawn");
        helper.assertTrue(manager.count() == 1, "Manager must still have 1 agent");
        helper.assertTrue(agent1.isRemoved(), "First agent must be discarded/removed");
        helper.assertTrue(agent2 == manager.getAgent("agent-gt-dup"), "Manager must track second agent");
        helper.assertTrue("SecondAgent".equals(agent2.getGameProfile().getName()), "Name must match second");

        // Clean up
        manager.despawnAgent(server, "agent-gt-dup").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSensoryFrustumBlockScanning(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        // Place interesting blocks in test boundary
        BlockPos chestPos = new BlockPos(1, 1, 2);
        helper.setBlock(chestPos, Blocks.CHEST);

        BlockPos farmPos = new BlockPos(2, 1, 1);
        helper.setBlock(farmPos, Blocks.FARMLAND);

        BlockPos wheatPos = new BlockPos(2, 2, 1);
        helper.setBlock(wheatPos, Blocks.WHEAT.defaultBlockState().setValue(CropBlock.AGE, 7));

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-blocks")
                .setDisplayName("BlockScannerAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        TelemetryFrame frame = manager.getSensoryCollector().collectFrame(agent);
        helper.assertTrue(frame != null, "Frame must not be null");
        helper.assertTrue(frame.getInterestingBlocksCount() >= 2,
                "Must find at least 2 interesting blocks, found: " + frame.getInterestingBlocksCount());

        boolean foundWheat = false;
        boolean foundChest = false;
        for (VisibleBlock vb : frame.getInterestingBlocksList()) {
            if ("minecraft:wheat".equals(vb.getBlockId())) {
                foundWheat = true;
                helper.assertTrue(vb.getBlockStateString().contains("age=7"),
                        "Wheat block_state_string must contain age=7: " + vb.getBlockStateString());
            } else if ("minecraft:chest".equals(vb.getBlockId())) {
                foundChest = true;
            }
        }
        helper.assertTrue(foundWheat, "Must detect visible wheat crop");
        helper.assertTrue(foundChest, "Must detect visible chest");

        helper.assertTrue(frame.hasFarmlandSurvey(), "Must populate FarmlandSurveyEvent for mature crops");
        helper.assertTrue("minecraft:wheat".equals(frame.getFarmlandSurvey().getPrimaryCropId()),
                "Primary crop ID must be minecraft:wheat");
        helper.assertTrue(frame.getFarmlandSurvey().getMatureCropCount() >= 1,
                "Mature crop count must be at least 1");
        helper.assertTrue(frame.getFarmlandSurvey().getTotalFarmlandBlocks() >= 1,
                "Total farmland blocks must be at least 1");

        // Verify farmland survey when matureCropCount == 0 (only farmland)
        helper.setBlock(wheatPos, Blocks.AIR);
        TelemetryFrame frameFarmlandOnly = manager.getSensoryCollector().collectFrame(agent);
        helper.assertTrue(frameFarmlandOnly.hasFarmlandSurvey(),
                "Must populate FarmlandSurveyEvent for farmland even without mature crops");
        helper.assertTrue("".equals(frameFarmlandOnly.getFarmlandSurvey().getPrimaryCropId()),
                "Primary crop ID must be empty string when mature crops are 0");
        helper.assertTrue(frameFarmlandOnly.getFarmlandSurvey().getMatureCropCount() == 0,
                "Mature crop count must be 0");
        helper.assertTrue(frameFarmlandOnly.getFarmlandSurvey().getTotalFarmlandBlocks() >= 1,
                "Total farmland blocks must be at least 1");

        manager.despawnAgent(server, "agent-gt-blocks").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSensoryFrustumEntityScanningAndFuseTicks(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-entity")
                .setDisplayName("EntityScannerAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        // Spawn Creeper adjacent to agent within line of sight
        Creeper creeper = helper.spawn(EntityType.CREEPER, new BlockPos(1, 1, 2));
        helper.assertTrue(creeper != null, "Creeper must spawn");
        creeper.ignite();

        TelemetryFrame frame = manager.getSensoryCollector().collectFrame(agent);
        helper.assertTrue(frame != null, "Frame must not be null");

        VisibleEntity creeperEntity = null;
        for (VisibleEntity ve : frame.getVisibleEntitiesList()) {
            if ("minecraft:creeper".equals(ve.getEntityType())) {
                creeperEntity = ve;
                break;
            }
        }

        helper.assertTrue(creeperEntity != null, "Creeper must be visible within frustum");
        helper.assertTrue(creeperEntity.getIsHostile(), "Creeper must be hostile");
        helper.assertTrue(creeperEntity.getFuseTicks() == 30,
                "Ignited creeper must report fuse_ticks=30, was: " + creeperEntity.getFuseTicks());
        helper.assertTrue(creeperEntity.getDistance() > 0.0f, "Distance must be positive");
        helper.assertTrue(creeperEntity.getHealth() > 0.0f, "Health must be positive");

        // Test swelling and deflating states via getCreeperFuseTicks
        Creeper swellCreeper = helper.spawn(EntityType.CREEPER, new BlockPos(1, 1, 0));
        swellCreeper.setSwellDir(1);
        int swellingFuse = SensoryFrustumCollector.getCreeperFuseTicks(swellCreeper);
        helper.assertTrue(swellingFuse >= 0 && swellingFuse <= 30, "Swelling creeper fuse ticks must be in [0, 30]");
        swellCreeper.setSwellDir(-1);
        int deflatingFuse = SensoryFrustumCollector.getCreeperFuseTicks(swellCreeper);
        helper.assertTrue(deflatingFuse >= 0 && deflatingFuse <= 30, "Deflating creeper fuse ticks must be in [0, 30]");
        swellCreeper.discard();

        creeper.discard();
        manager.despawnAgent(server, "agent-gt-entity").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSensory5HzTelemetryStreaming(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        StreamRegistry streamRegistry = new StreamRegistry();
        AgentEntityManager manager = new AgentEntityManager(streamRegistry);

        List<TelemetryFrame> receivedFrames = new CopyOnWriteArrayList<>();
        streamRegistry.register("agent-gt-stream", new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {
                receivedFrames.add(value);
            }
            @Override
            public void onError(Throwable t) {}
            @Override
            public void onCompleted() {}
        });

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-stream")
                .setDisplayName("StreamAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        // Manually trigger telemetry tick
        manager.tickTelemetry();
        helper.assertTrue(receivedFrames.size() == 1,
                "Must receive exactly 1 telemetry frame on tick, got: " + receivedFrames.size());

        TelemetryFrame frame = receivedFrames.get(0);
        helper.assertTrue(frame.hasStatus(), "Frame must have status");
        helper.assertTrue("agent-gt-stream".equals(frame.getStatus().getAgentId()), "Agent ID must match");
        helper.assertTrue(frame.getStatus().getHealth() == agent.getMaxHealth(), "Health must match max health");

        helper.assertTrue(streamRegistry.isRegistered("agent-gt-stream"), "Agent stream must be registered prior to despawn");
        manager.despawnAgent(server, "agent-gt-stream").join();
        helper.assertFalse(streamRegistry.isRegistered("agent-gt-stream"),
                "Agent stream must be cleanly closed via removalListener upon non-death despawn");
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testImmediateDamageEventStreaming(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        StreamRegistry streamRegistry = new StreamRegistry();
        AgentEntityManager manager = new AgentEntityManager(streamRegistry);

        List<TelemetryFrame> receivedFrames = new CopyOnWriteArrayList<>();
        streamRegistry.register("agent-gt-damage-event", new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {
                receivedFrames.add(value);
            }
            @Override
            public void onError(Throwable t) {}
            @Override
            public void onCompleted() {}
        });

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-damage-event")
                .setDisplayName("DamageAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        // Emit LivingDamageEvent.Post
        DamageSource damageSource = level.damageSources().magic();
        DamageContainer container = new DamageContainer(damageSource, 4.5f);
        LivingDamageEvent.Post postEvent = new LivingDamageEvent.Post(agent, container);
        manager.onLivingDamage(postEvent);

        helper.assertTrue(receivedFrames.size() == 1,
                "Must receive immediate telemetry frame upon damage event, received: " + receivedFrames.size());

        TelemetryFrame frame = receivedFrames.get(0);
        helper.assertTrue(frame.hasLastDamageTaken(), "Frame must contain last_damage_taken");
        helper.assertTrue(frame.getLastDamageTaken().getDamageAmount() == 4.5f,
                "Damage amount must match 4.5f, was: " + frame.getLastDamageTaken().getDamageAmount());

        // Emit damage event when agent is dying / dead (health <= 0) - should be skipped
        agent.setHealth(0.0f);
        manager.onLivingDamage(postEvent);
        helper.assertTrue(receivedFrames.size() == 1,
                "Must not emit damage frame when agent is dead/dying, received count: " + receivedFrames.size());

        manager.despawnAgent(server, "agent-gt-damage-event").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testNavigateIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        // Prepare flat walking floor at Y=0
        helper.setBlock(new BlockPos(1, 0, 1), Blocks.STONE);
        helper.setBlock(new BlockPos(1, 0, 2), Blocks.STONE);
        helper.setBlock(new BlockPos(1, 1, 1), Blocks.AIR);
        helper.setBlock(new BlockPos(1, 1, 2), Blocks.AIR);

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-nav")
                .setDisplayName("NavAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        BlockPos targetPos = helper.absolutePos(new BlockPos(1, 1, 2));
        NavigateIntent navIntent = NavigateIntent.newBuilder()
                .setTarget(Vec3.newBuilder()
                        .setX(targetPos.getX() + 0.5)
                        .setY(targetPos.getY())
                        .setZ(targetPos.getZ() + 0.5)
                        .build())
                .setSpeedMultiplier(1.0f)
                .setSprint(false)
                .setArrivalTolerance(1)
                .build();

        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-nav")
                .setNavigate(navIntent)
                .build();

        manager.handleIntent(server, intent).join();
        helper.assertTrue(agent.getNavigator().isPathing(), "Agent navigator must be PATHING");

        double startDistSq = agent.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5);
        for (int i = 0; i < 5; i++) {
            agent.tick();
        }
        double endDistSq = agent.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5);
        helper.assertTrue(endDistSq <= startDistSq, "Agent must move closer towards target destination");

        manager.despawnAgent(server, "agent-gt-nav").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testAttackIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-attack")
                .setDisplayName("AttackAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");
        agent.setItemInHand(InteractionHand.MAIN_HAND, new ItemStack(Items.IRON_SWORD));
        agent.getInventory().add(new ItemStack(Items.SHIELD));

        // Advance ticks so attack strength cooldown scale is >= 0.9f
        for (int i = 0; i < 20; i++) {
            agent.tick();
        }

        // Spawn a zombie in adjacent block
        Zombie zombie = helper.spawn(EntityType.ZOMBIE, new BlockPos(1, 1, 2));
        helper.assertTrue(zombie != null, "Zombie must spawn");
        float initialHealth = zombie.getHealth();
        helper.assertTrue(initialHealth > 0.0f, "Zombie initial health must be positive");

        AttackIntent attackIntent = AttackIntent.newBuilder()
                .setTargetEntityId(zombie.getId())
                .setRaiseShield(true)
                .build();

        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-attack")
                .setAttack(attackIntent)
                .build();

        manager.handleIntent(server, intent).join();

        // Verify shield raised in offhand (cleanly swapped from inventory without free item generation)
        helper.assertTrue(agent.getItemInHand(InteractionHand.OFF_HAND).is(Items.SHIELD),
                "Agent must hold shield in offhand");
        helper.assertTrue(agent.isUsingItem(), "Agent must be using offhand shield item");
        helper.assertTrue(agent.getUsedItemHand() == InteractionHand.OFF_HAND,
                "Agent used hand must be OFF_HAND");

        // Verify target entity took damage
        helper.assertTrue(zombie.getHealth() < initialHealth,
                "Target zombie must have taken damage, health was: " + zombie.getHealth());

        // Verify friendly fire rejection for ally with same clan
        BlockPos allyPos = helper.absolutePos(new BlockPos(1, 1, 0));
        SpawnAgentRequest allyRequest = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-ally")
                .setDisplayName("AllyAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(allyPos.getX() + 0.5)
                        .setY(allyPos.getY())
                        .setZ(allyPos.getZ() + 0.5)
                        .build())
                .build();
        AgentEntity ally = manager.spawnAgent(server, level, allyRequest).join();
        float allyInitialHealth = ally.getHealth();

        AgentIntent allyAttackIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-attack")
                .setAttack(AttackIntent.newBuilder()
                        .setTargetEntityId(ally.getId())
                        .build())
                .build();
        manager.handleIntent(server, allyAttackIntent).join();
        helper.assertTrue(ally.getHealth() == allyInitialHealth,
                "Ally agent in same clan must not take friendly fire damage");

        // Verify Flee reflex movement (normalized vector * 10 blocks, tolerance 1)
        AttackIntent fleeIntent = AttackIntent.newBuilder()
                .setTargetEntityId(zombie.getId())
                .setFleeImmediately(true)
                .build();
        AgentIntent fleeAgentIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-attack")
                .setAttack(fleeIntent)
                .build();
        zombie.setPos(agent.getX(), agent.getY(), agent.getZ() + 1.0);
        manager.handleIntent(server, fleeAgentIntent).join();

        helper.assertTrue(agent.getNavigator().isPathing(), "Agent must be pathing away when fleeing");
        net.minecraft.world.phys.Vec3 fleeTarget = agent.getNavigator().getTargetCoords();
        helper.assertTrue(fleeTarget != null, "Flee target coordinates must be set");
        helper.assertTrue(agent.getNavigator().getArrivalTolerance() == 1,
                "Flee navigation arrival tolerance must be 1");
        helper.assertTrue(Math.abs(fleeTarget.z - (agent.getZ() - 10.0)) < 0.01,
                "Flee target Z must be 10 blocks away from zombie opposite to target");

        // Verify Flee reflex when distance < 0.001 (opposite to yaw)
        zombie.setPos(agent.getX(), agent.getY(), agent.getZ());
        agent.setYRot(0.0F); // Facing +Z
        manager.handleIntent(server, fleeAgentIntent).join();
        net.minecraft.world.phys.Vec3 zeroDistFleeTarget = agent.getNavigator().getTargetCoords();
        helper.assertTrue(zeroDistFleeTarget != null && zeroDistFleeTarget.z < agent.getZ(),
                "When distance < 0.001, flee direction must be opposite to agent yaw");

        // Verify thread safety: world-modifying intent with null server must return failed future
        CompletableFuture<Void> nullServerFuture = manager.handleIntent(null, intent);
        helper.assertTrue(nullServerFuture.isCompletedExceptionally(),
                "World-modifying intent without running MinecraftServer must return failed future");

        manager.despawnAgent(server, "agent-gt-ally").join();
        zombie.discard();
        manager.despawnAgent(server, "agent-gt-attack").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testInteractBlockIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-interact")
                .setDisplayName("InteractAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        // 1. HARVEST_BREAK on wheat crop
        BlockPos localWheat = new BlockPos(1, 1, 2);
        BlockPos absWheat = helper.absolutePos(localWheat);
        helper.setBlock(localWheat, Blocks.WHEAT.defaultBlockState().setValue(CropBlock.AGE, 7));
        helper.assertBlockPresent(Blocks.WHEAT, localWheat);

        AgentIntent harvestIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absWheat.getX()).setY(absWheat.getY()).setZ(absWheat.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                        .build())
                .build();
        manager.handleIntent(server, harvestIntent).join();
        helper.assertBlockNotPresent(Blocks.WHEAT, localWheat);

        // 2. HARVEST_BREAK prohibited on indestructible blocks (bedrock destroySpeed < 0)
        BlockPos localBedrock = new BlockPos(1, 1, 2);
        BlockPos absBedrock = helper.absolutePos(localBedrock);
        helper.setBlock(localBedrock, Blocks.BEDROCK);
        AgentIntent breakBedrockIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absBedrock.getX()).setY(absBedrock.getY()).setZ(absBedrock.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                        .build())
                .build();
        manager.handleIntent(server, breakBedrockIntent).join();
        helper.assertBlockPresent(Blocks.BEDROCK, localBedrock);
        helper.setBlock(localBedrock, Blocks.AIR);

        // 3. PAVE_ROAD_PATH on grass block
        BlockPos localGrass = new BlockPos(2, 1, 1);
        BlockPos absGrass = helper.absolutePos(localGrass);
        helper.setBlock(localGrass, Blocks.GRASS_BLOCK);
        helper.assertBlockPresent(Blocks.GRASS_BLOCK, localGrass);

        AgentIntent paveIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absGrass.getX()).setY(absGrass.getY()).setZ(absGrass.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.PAVE_ROAD_PATH)
                        .build())
                .build();
        manager.handleIntent(server, paveIntent).join();
        helper.assertBlockPresent(Blocks.DIRT_PATH, localGrass);

        // 4. PLACE_BLOCK consuming inventory item
        BlockPos localCobble = new BlockPos(2, 1, 2);
        BlockPos absCobble = helper.absolutePos(localCobble);
        helper.setBlock(localCobble, Blocks.AIR);
        agent.getInventory().add(new ItemStack(Items.COBBLESTONE, 2));

        AgentIntent placeIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absCobble.getX()).setY(absCobble.getY()).setZ(absCobble.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                        .setBlockOrItemId("minecraft:cobblestone")
                        .build())
                .build();
        manager.handleIntent(server, placeIntent).join();
        helper.assertBlockPresent(Blocks.COBBLESTONE, localCobble);

        int cobbleCount = 0;
        for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
            ItemStack stack = agent.getInventory().getItem(i);
            if (stack.is(Items.COBBLESTONE)) {
                cobbleCount += stack.getCount();
            }
        }
        helper.assertTrue(cobbleCount == 1, "Cobblestone inventory count must shrink by 1 after placement");

        // 5. SLEEP_IN_BED and WAKE_UP (requires night time or thundering)
        BlockPos localBed = new BlockPos(1, 1, 0);
        BlockPos absBed = helper.absolutePos(localBed);
        helper.setBlock(localBed, Blocks.RED_BED.defaultBlockState()
                .setValue(net.minecraft.world.level.block.BedBlock.FACING, net.minecraft.core.Direction.EAST));
        helper.setBlock(localBed.east(), Blocks.RED_BED.defaultBlockState()
                .setValue(net.minecraft.world.level.block.BedBlock.FACING, net.minecraft.core.Direction.EAST)
                .setValue(net.minecraft.world.level.block.BedBlock.PART, net.minecraft.world.level.block.state.properties.BedPart.HEAD));
        level.setDayTime(13000L); // Set to night
        level.setThunderLevel(1.0F);
        level.setRainLevel(1.0F);

        AgentIntent sleepIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absBed.getX()).setY(absBed.getY()).setZ(absBed.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.SLEEP_IN_BED)
                        .build())
                .build();
        manager.handleIntent(server, sleepIntent).join();
        helper.assertTrue(agent.isSleeping(), "Agent must be sleeping in bed");

        AgentIntent wakeIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setInteractionType(InteractBlockIntent.InteractionType.WAKE_UP)
                        .build())
                .build();
        manager.handleIntent(server, wakeIntent).join();
        helper.assertFalse(agent.isSleeping(), "Agent must wake up from bed");

        manager.despawnAgent(server, "agent-gt-interact").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testCreateWrittenBookIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-book")
                .setDisplayName("ChroniclerAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        BlockPos localLectern = new BlockPos(1, 1, 2);
        BlockPos absLectern = helper.absolutePos(localLectern);
        helper.setBlock(localLectern, Blocks.LECTERN);
        helper.assertBlockPresent(Blocks.LECTERN, localLectern);

        CreateWrittenBookIntent bookIntent = CreateWrittenBookIntent.newBuilder()
                .setTitle("Agent Annals")
                .setAuthor("Chronicler")
                .addPages("The founding of the Ironwood settlement.")
                .addPages("Trade routes established with neighboring lands.")
                .setTargetLecternOrShelf(BlockPosProto.newBuilder()
                        .setX(absLectern.getX())
                        .setY(absLectern.getY())
                        .setZ(absLectern.getZ())
                        .build())
                .build();

        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-book")
                .setCreateBook(bookIntent)
                .build();

        manager.handleIntent(server, intent).join();

        // Verify lectern has book
        helper.assertTrue(helper.getBlockState(localLectern).getValue(LecternBlock.HAS_BOOK),
                "Lectern block state must have HAS_BOOK=true");

        LecternBlockEntity lectern = helper.getBlockEntity(localLectern);
        helper.assertTrue(lectern != null, "Lectern block entity must exist");
        helper.assertTrue(lectern.hasBook(), "Lectern block entity must hold book");
        ItemStack bookStack = lectern.getBook();
        helper.assertTrue(bookStack.is(Items.WRITTEN_BOOK), "Placed book must be WRITTEN_BOOK");

        WrittenBookContent content = bookStack.get(DataComponents.WRITTEN_BOOK_CONTENT);
        helper.assertTrue(content != null, "WrittenBookContent component must not be null");
        helper.assertTrue("Agent Annals".equals(content.title().raw()), "Book title must match intent");
        helper.assertTrue("Chronicler".equals(content.author()), "Book author must match intent");
        helper.assertTrue(content.pages().size() == 2, "Page count must match intent (2)");

        // 2. Placing book on lectern that already has a book must be rejected
        boolean duplicateLectern = manager.getIntentActuator().actuateCreateWrittenBook(agent, bookIntent);
        helper.assertFalse(duplicateLectern, "Placing book on lectern that already holds a book must be rejected");

        // 3. Title length > 32 chars must be rejected
        CreateWrittenBookIntent longTitleIntent = CreateWrittenBookIntent.newBuilder()
                .setTitle("A".repeat(33))
                .addPages("Valid content")
                .build();
        helper.assertFalse(manager.getIntentActuator().actuateCreateWrittenBook(agent, longTitleIntent),
                "Title length > 32 chars must be rejected");

        // 4. Pages count > 100 must be rejected
        CreateWrittenBookIntent.Builder manyPagesBuilder = CreateWrittenBookIntent.newBuilder()
                .setTitle("Valid Title");
        for (int i = 0; i < 101; i++) {
            manyPagesBuilder.addPages("Page " + i);
        }
        helper.assertFalse(manager.getIntentActuator().actuateCreateWrittenBook(agent, manyPagesBuilder.build()),
                "Page count > 100 must be rejected");

        // 5. Page text length > 1024 chars must be rejected
        CreateWrittenBookIntent longPageIntent = CreateWrittenBookIntent.newBuilder()
                .setTitle("Valid Title")
                .addPages("B".repeat(1025))
                .build();
        helper.assertFalse(manager.getIntentActuator().actuateCreateWrittenBook(agent, longPageIntent),
                "Page length > 1024 chars must be rejected");

        // 6. Target lectern out of reach (> 6.0 blocks) must be rejected
        CreateWrittenBookIntent outOfReachLectern = CreateWrittenBookIntent.newBuilder()
                .setTitle("Valid Title")
                .addPages("Content")
                .setTargetLecternOrShelf(BlockPosProto.newBuilder().setX(absLectern.getX() + 20).setY(absLectern.getY()).setZ(absLectern.getZ()).build())
                .build();
        helper.assertFalse(manager.getIntentActuator().actuateCreateWrittenBook(agent, outOfReachLectern),
                "Target lectern out of reach (> 6.0 blocks) must be rejected");

        manager.despawnAgent(server, "agent-gt-book").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testUseItemAndSignpost(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-useitem")
                .setDisplayName("UseItemAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, request).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        // 1. Eating non-food item (e.g. Iron Ingot) must be rejected
        agent.getInventory().setItem(0, new ItemStack(Items.IRON_INGOT, 2));
        agent.getFoodData().setFoodLevel(16);

        AgentIntent eatIngotIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-useitem")
                .setUseItem(UseItemIntent.newBuilder()
                        .setInventorySlot(0)
                        .setIsEatingOrDrinking(true)
                        .build())
                .build();
        manager.handleIntent(server, eatIngotIntent).join();
        helper.assertTrue(agent.getFoodData().getFoodLevel() == 16, "Eating non-food must be rejected");
        helper.assertTrue(agent.getInventory().getItem(0).getCount() == 2, "Non-food item count must not decrease");

        // 2. Eating valid food (Apple) increases food level and shrinks item
        agent.getInventory().setItem(1, new ItemStack(Items.APPLE, 2));
        AgentIntent eatAppleIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-useitem")
                .setUseItem(UseItemIntent.newBuilder()
                        .setInventorySlot(1)
                        .setIsEatingOrDrinking(true)
                        .build())
                .build();
        manager.handleIntent(server, eatAppleIntent).join();
        helper.assertTrue(agent.getFoodData().getFoodLevel() > 16, "Eating valid food must increase food level");
        helper.assertTrue(agent.getInventory().getItem(1).getCount() == 1, "Food item count must shrink by 1");

        // 3. Eating when full (foodLevel=20) for standard food must be rejected
        agent.getFoodData().setFoodLevel(20);
        manager.handleIntent(server, eatAppleIntent).join();
        helper.assertTrue(agent.getInventory().getItem(1).getCount() == 1, "Eating when full must be rejected");

        // 4. Place Signpost with oak sign item consumed and lines clamped to 80 chars
        helper.setBlock(new BlockPos(1, 0, 2), Blocks.STONE);
        helper.setBlock(new BlockPos(1, 1, 2), Blocks.AIR);
        BlockPos absSign = helper.absolutePos(new BlockPos(1, 1, 2));
        agent.getInventory().setItem(2, new ItemStack(Items.OAK_SIGN, 2));

        String longLine = "A".repeat(100);
        AgentIntent signIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-useitem")
                .setPlaceSignpost(PlaceSignpostIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absSign.getX()).setY(absSign.getY()).setZ(absSign.getZ()).build())
                        .setIsHangingSign(false)
                        .setLine1("Ironwood Gate")
                        .setLine2(longLine)
                        .build())
                .build();
        manager.handleIntent(server, signIntent).join();

        helper.assertBlockPresent(Blocks.OAK_SIGN, new BlockPos(1, 1, 2));
        helper.assertTrue(agent.getInventory().getItem(2).getCount() == 1, "Sign item count must shrink by 1");

        net.minecraft.world.level.block.entity.BlockEntity signEntity = helper.getBlockEntity(new BlockPos(1, 1, 2));
        helper.assertTrue(signEntity instanceof SignBlockEntity, "Must be SignBlockEntity");
        SignBlockEntity signBe = (SignBlockEntity) signEntity;
        helper.assertTrue("Ironwood Gate".equals(signBe.getFrontText().getMessage(0, false).getString()), "Sign line 1 must match");
        helper.assertTrue(signBe.getFrontText().getMessage(1, false).getString().length() == 80, "Sign line 2 must be clamped to 80 chars");

        manager.despawnAgent(server, "agent-gt-useitem").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testCreateWrittenBookActuation(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        ServerLevel level = helper.getLevel();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos relPos = new BlockPos(1, 1, 1);
        BlockPos absPos = helper.absolutePos(relPos);

        SpawnAgentRequest spawnReq = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-chronicler")
                .setDisplayName("Chronicler")
                .setClanId("pioneers")
                .setSpawnCoords(Vec3.newBuilder().setX(absPos.getX() + 0.5).setY(absPos.getY()).setZ(absPos.getZ() + 0.5).build())
                .build();

        AgentEntity agent = manager.spawnAgent(server, level, spawnReq).join();
        helper.assertTrue(agent != null, "Agent must spawn");

        // 1. Materialize Written Book into a Lectern
        BlockPos relLectern = new BlockPos(1, 1, 2);
        helper.setBlock(relLectern, Blocks.LECTERN.defaultBlockState().setValue(LecternBlock.HAS_BOOK, false));
        BlockPos absLectern = helper.absolutePos(relLectern);

        AgentIntent bookIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-chronicler")
                .setCreateBook(CreateWrittenBookIntent.newBuilder()
                        .setTitle("Chronicle of Pioneers")
                        .setAuthor("Chronicler")
                        .addPages("Page 1: In the beginning...")
                        .addPages("Page 2: The settlement grew.")
                        .setTargetLecternOrShelf(BlockPosProto.newBuilder()
                                .setX(absLectern.getX())
                                .setY(absLectern.getY())
                                .setZ(absLectern.getZ())
                                .build())
                        .build())
                .build();

        manager.handleIntent(server, bookIntent).join();

        LecternBlockEntity lectern = (LecternBlockEntity) helper.getBlockEntity(relLectern);
        helper.assertTrue(lectern != null, "Lectern block entity must exist");
        helper.assertTrue(lectern.hasBook(), "Lectern must hold the synthesized book");

        ItemStack bookItem = lectern.getBook();
        helper.assertTrue(bookItem.is(Items.WRITTEN_BOOK), "Lectern item must be a written book");
        WrittenBookContent content = bookItem.get(DataComponents.WRITTEN_BOOK_CONTENT);
        helper.assertTrue(content != null, "Book must have written book content component");
        helper.assertTrue("Chronicle of Pioneers".equals(content.title().raw()), "Book title must match");
        helper.assertTrue("Chronicler".equals(content.author()), "Book author must match");
        helper.assertTrue(content.pages().size() == 2, "Book must have 2 pages");

        // 2. Materialize Written Book into a Chiseled Bookshelf
        BlockPos relShelf = new BlockPos(2, 1, 1);
        helper.setBlock(relShelf, Blocks.CHISELED_BOOKSHELF.defaultBlockState());
        BlockPos absShelf = helper.absolutePos(relShelf);

        AgentIntent shelfIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-chronicler")
                .setCreateBook(CreateWrittenBookIntent.newBuilder()
                        .setTitle("Volume of Archives")
                        .setAuthor("Chronicler")
                        .addPages("Archive records in bookshelf")
                        .setTargetLecternOrShelf(BlockPosProto.newBuilder()
                                .setX(absShelf.getX())
                                .setY(absShelf.getY())
                                .setZ(absShelf.getZ())
                                .build())
                        .setBookshelfSlot(2)
                        .build())
                .build();

        manager.handleIntent(server, shelfIntent).join();

        net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity shelf =
                (net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity) helper.getBlockEntity(relShelf);
        helper.assertTrue(shelf != null, "Chiseled bookshelf block entity must exist");
        helper.assertTrue(shelf.getItem(2).is(Items.WRITTEN_BOOK), "Chiseled bookshelf slot 2 must hold written book");

        // 3. Materialize Written Book into inventory when no target block specified
        AgentIntent invBookIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-chronicler")
                .setCreateBook(CreateWrittenBookIntent.newBuilder()
                        .setTitle("Tome of Wisdom")
                        .setAuthor("Chronicler")
                        .addPages("First page in inventory")
                        .build())
                .build();

        int initialInvCount = 0;
        for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
            if (agent.getInventory().getItem(i).is(Items.WRITTEN_BOOK)) {
                initialInvCount++;
            }
        }

        manager.handleIntent(server, invBookIntent).join();

        int afterInvCount = 0;
        for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
            if (agent.getInventory().getItem(i).is(Items.WRITTEN_BOOK)) {
                afterInvCount++;
            }
        }
        helper.assertTrue(afterInvCount == initialInvCount + 1, "Agent inventory must receive written book");

        manager.despawnAgent(server, "agent-gt-chronicler").join();
        helper.succeed();
    }

    private static java.nio.file.Path resolveMotionTracePath() {
        String xdgState = System.getenv("XDG_STATE_HOME");
        java.nio.file.Path base = (xdgState != null && !xdgState.isBlank())
                ? java.nio.file.Paths.get(xdgState)
                : java.nio.file.Paths.get(System.getProperty("user.home", "."), ".local", "state");
        return base.resolve(java.nio.file.Paths.get("civquest", "logs", "motion-integration-trace.jsonl"));
    }

    /**
     * Starts navigation the way a live agent does: a Navigate intent, which pushes the Move
     * frame that ticks the navigator. Calling the navigator directly moves nothing.
     */
    private static void navigateByIntent(AgentEntityManager manager, MinecraftServer server, String agentId,
            double x, double y, double z, boolean sprint, boolean allowBreak) {
        manager.handleIntent(server, AgentIntent.newBuilder().setAgentId(agentId)
                .setNavigate(com.civquest.mod.grpc.v1.NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(x).setY(y).setZ(z).build())
                        .setSpeedMultiplier(1.0f)
                        .setSprint(sprint)
                        .setAllowBreakBlocks(allowBreak)
                        .setArrivalTolerance(1))
                .build()).join();
    }

    private static void appendMotionTrace(String scenario, int tick, AgentEntity agent) {
        if (agent == null || agent.getNavigator() == null) {
            return;
        }
        try {
            java.nio.file.Path path = resolveMotionTracePath();
            java.nio.file.Files.createDirectories(path.getParent());
            AgentNavigator nav = agent.getNavigator();
            net.minecraft.world.phys.Vec3 dm = agent.getDeltaMovement();
            String line = String.format(java.util.Locale.ROOT,
                    "{\"scenario\":\"%s\",\"tick\":%d,\"x\":%.4f,\"y\":%.4f,\"z\":%.4f,\"vx\":%.4f,\"vz\":%.4f,\"yaw\":%.2f,\"head_yaw\":%.2f,\"body_yaw\":%.2f,\"pitch\":%.2f,\"stamina\":%.2f,\"nav_state\":\"%s\"}%n",
                    scenario, tick, agent.getX(), agent.getY(), agent.getZ(), dm.x, dm.z,
                    agent.getYRot(), nav.getHeadYaw(), nav.getBodyYaw(), nav.getNavPitch(),
                    nav.getStamina(), nav.getReportedNavigationState().name());
            java.nio.file.Files.writeString(path, line, StandardCharsets.UTF_8,
                    java.nio.file.StandardOpenOption.CREATE, java.nio.file.StandardOpenOption.APPEND);
        } catch (Throwable ignored) {
        }
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionOneBlockStepUp(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        // Build a 1-block step: start floor at Y=0 (standing Y=1), step at Z=2 (block at Y=1, standing Y=2)
        for (int x = 0; x <= 2; x++) {
            for (int z = 0; z <= 3; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
                helper.setBlock(new BlockPos(x, 1, z), Blocks.AIR);
                helper.setBlock(new BlockPos(x, 2, z), Blocks.AIR);
                helper.setBlock(new BlockPos(x, 3, z), Blocks.AIR);
            }
        }
        helper.setBlock(new BlockPos(1, 1, 2), Blocks.STONE);
        helper.setBlock(new BlockPos(1, 1, 3), Blocks.STONE);

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 0));
        BlockPos targetPos = helper.absolutePos(new BlockPos(1, 2, 3));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-stepup")
                .setDisplayName("StepUpAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        navigateByIntent(manager, server, "agent-gt-stepup",
                targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5, false, false);
        for (int t = 0; t < 40 && !agent.getNavigator().isArrived(); t++) {
            agent.tick();
            appendMotionTrace("one_block_step_up", t, agent);
        }

        helper.assertTrue(agent.getY() >= spawnPos.getY() + 0.9, "Agent must climb the 1-block step, y=" + agent.getY());
        manager.despawnAgent(server, "agent-gt-stepup").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionTwoByThreePitEscape(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        // Build a 2x3 pit (x in 0..1, z in 0..2) surrounded by 2-block-tall walls except a 1-block step exit at (2, 1, 1)
        for (int x = -1; x <= 4; x++) {
            for (int z = -1; z <= 4; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
                for (int y = 1; y <= 4; y++) {
                    helper.setBlock(new BlockPos(x, y, z), Blocks.AIR);
                }
            }
        }
        for (int x = -1; x <= 2; x++) {
            for (int z = -1; z <= 3; z++) {
                boolean interior = x >= 0 && x <= 1 && z >= 0 && z <= 2;
                if (!interior) {
                    if (x == 2 && z == 1) {
                        helper.setBlock(new BlockPos(x, 1, z), Blocks.STONE); // 1-block step exit
                    } else {
                        helper.setBlock(new BlockPos(x, 1, z), Blocks.STONE);
                        helper.setBlock(new BlockPos(x, 2, z), Blocks.STONE);
                    }
                }
            }
        }
        helper.setBlock(new BlockPos(3, 1, 1), Blocks.STONE);

        BlockPos spawnPos = helper.absolutePos(new BlockPos(0, 1, 0));
        BlockPos targetPos = helper.absolutePos(new BlockPos(3, 2, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-pit2x3")
                .setDisplayName("PitEscapeAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        navigateByIntent(manager, server, "agent-gt-pit2x3",
                targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5, false, false);
        for (int t = 0; t < 80 && !agent.getNavigator().isArrived(); t++) {
            agent.tick();
            appendMotionTrace("pit_2x3_escape", t, agent);
        }

        helper.assertFalse(agent.getNavigator().isEnclosed(), "2x3 pit with 1-block step exit must not be marked enclosed");
        helper.assertTrue(agent.getX() > spawnPos.getX() + 1.5, "Agent must exit pit toward target via 1-block step");
        manager.despawnAgent(server, "agent-gt-pit2x3").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionEnclosedPitReportsStuck(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        // Build a fully enclosed 1x1x2-deep dirt pit around (1, 1, 1)
        for (int x = 0; x <= 3; x++) {
            for (int z = 0; z <= 3; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
                for (int y = 1; y <= 4; y++) {
                    helper.setBlock(new BlockPos(x, y, z), Blocks.AIR);
                }
            }
        }
        int[][] ring = {{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}};
        for (int[] d : ring) {
            helper.setBlock(new BlockPos(1 + d[0], 1, 1 + d[1]), Blocks.DIRT);
            helper.setBlock(new BlockPos(1 + d[0], 2, 1 + d[1]), Blocks.DIRT);
        }

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        BlockPos targetPos = helper.absolutePos(new BlockPos(3, 2, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-digout")
                .setDisplayName("DigOutAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        // Drive the navigator itself: a Move frame pops (and stops it) as soon as it reports stuck.
        agent.getNavigator().navigate(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5,
                1.0f, false, true, false, 1);
        agent.getNavigator().tick();

        // ADR-037: the navigator reports the walled-in agent and leaves digging to the brain.
        helper.assertTrue(agent.getNavigator().isEnclosed() && agent.getNavigator().isStuck(),
                "A walled-in agent must be reported as enclosed and stuck");
        helper.assertFalse(agent.getBreaker().isBreaking(), "Navigation must not start a break on its own");
        helper.assertBlockPresent(Blocks.DIRT, new BlockPos(2, 2, 1));
        manager.despawnAgent(server, "agent-gt-digout").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionDugoutStairAwayFromTarget(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        // A trench two deep along +X whose only way out is a single-block stair at its far end,
        // away from the target. Surface standing height is relative y=3.
        for (int x = -6; x <= 14; x++) {
            for (int z = -3; z <= 3; z++) {
                helper.setBlock(new BlockPos(x, DUGOUT_ARENA_Y, z), Blocks.STONE);
                boolean trench = z == 0 && x >= 0 && x <= 10;
                for (int y = 1; y <= 5; y++) {
                    helper.setBlock(new BlockPos(x, DUGOUT_ARENA_Y + y, z), (y <= 2 && !trench) ? Blocks.STONE : Blocks.AIR);
                }
            }
        }
        helper.setBlock(new BlockPos(9, DUGOUT_ARENA_Y + 1, 0), Blocks.STONE);
        helper.setBlock(new BlockPos(10, DUGOUT_ARENA_Y + 1, 0), Blocks.STONE);
        helper.setBlock(new BlockPos(10, DUGOUT_ARENA_Y + 2, 0), Blocks.STONE);

        BlockPos spawnPos = helper.absolutePos(new BlockPos(0, DUGOUT_ARENA_Y + 1, 0));
        BlockPos targetPos = helper.absolutePos(new BlockPos(-5, DUGOUT_ARENA_Y + 3, 0));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-dugout")
                .setDisplayName("DugoutAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        navigateByIntent(manager, server, "agent-gt-dugout",
                targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5, false, false);
        for (int t = 0; t < 400 && !agent.getNavigator().isArrived(); t++) {
            agent.tick();
            appendMotionTrace("dugout_stair", t, agent);
        }

        helper.assertTrue(agent.getY() >= targetPos.getY() - 0.1,
                "Agent must climb out of the dugout, y=" + agent.getY() + " want " + targetPos.getY());
        // Arrival ends the Move, which stops the navigator, so measure where the agent stands.
        double remaining = Math.hypot(agent.getX() - (targetPos.getX() + 0.5), agent.getZ() - (targetPos.getZ() + 0.5));
        helper.assertTrue(remaining <= 1.5, "Agent must reach the target on the surface, " + remaining + " blocks short");
        manager.despawnAgent(server, "agent-gt-dugout").join();
        helper.succeed();
    }

    /**
     * Arenas wider than the 3x3 test template are built this far above it (each at its own
     * height) so they cannot overwrite neighbouring tests.
     */
    private static final int DUGOUT_ARENA_Y = 20;
    private static final int RETREAT_ARENA_Y = 30;
    private static final int SPENT_ARENA_Y = 40;
    private static final int STEP_ARENA_Y = 50;
    private static final int LEAF_ARENA_Y = 130;
    private static final int CAMPFIRE_ARENA_Y = 60;
    private static final int IDLE_POOL_ARENA_Y = 70;
    private static final int SURFACE_POOL_ARENA_Y = 80;
    private static final int RESOURCE_ARENA_Y = 90;
    private static final int FLIGHT_ARENA_Y = 100;
    private static final int POOL_DEPTH = 5;

    /** A walled 3x3 pool, POOL_DEPTH water blocks on a stone floor, with air above. */
    private static void pool(GameTestHelper helper, int floorY) {
        for (int x = -2; x <= 2; x++) {
            for (int z = -2; z <= 2; z++) {
                boolean wall = Math.abs(x) == 2 || Math.abs(z) == 2;
                helper.setBlock(new BlockPos(x, floorY, z), Blocks.STONE);
                for (int y = 1; y <= POOL_DEPTH + 3; y++) {
                    boolean water = !wall && y <= POOL_DEPTH;
                    helper.setBlock(new BlockPos(x, floorY + y, z),
                            water ? Blocks.WATER : wall && y <= POOL_DEPTH ? Blocks.STONE : Blocks.AIR);
                }
            }
        }
    }

    /** Spawning lifts an agent out of water, so sink it afterwards. */
    private static void submerge(GameTestHelper helper, AgentEntity agent, int floorY) {
        BlockPos abs = helper.absolutePos(new BlockPos(0, floorY + 1, 0));
        agent.moveTo(abs.getX() + 0.5, abs.getY(), abs.getZ() + 0.5);
        agent.setDeltaMovement(0.0, 0.0, 0.0);
    }

    // cq-6l3.15: explorers see exposed ore, stone, wild food and cave mouths; buried ore stays hidden.
    @GameTest(template = "empty3x3x3")
    public static void testSensesExposedResourcesAndCaveEntrance(GameTestHelper helper) {
        AgentEntityManager manager = new AgentEntityManager();
        int y = RESOURCE_ARENA_Y;
        stoneFloor(helper, -6, 6, y);
        // A tunnel three wide of solid stone with a one-wide passage, open at its west end.
        for (int x = -2; x <= 4; x++) {
            for (int dy = 1; dy <= 3; dy++) {
                for (int z = 1; z <= 3; z++) {
                    boolean passage = z == 2 && dy <= 2 && x <= 3;
                    helper.setBlock(new BlockPos(x, y + dy, z), passage ? Blocks.AIR : Blocks.STONE);
                }
            }
        }
        BlockPos exposed = new BlockPos(2, y + 1, -3);
        helper.setBlock(exposed, Blocks.IRON_ORE);
        BlockPos buried = new BlockPos(-3, y, -3); // in the floor, covered on top, stone all round
        helper.setBlock(buried.below(), Blocks.STONE);
        helper.setBlock(buried, Blocks.GOLD_ORE);
        helper.setBlock(buried.above(), Blocks.STONE);
        helper.setBlock(new BlockPos(-4, y + 1, -2), Blocks.RED_MUSHROOM);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-resources", new BlockPos(-5, y + 1, 0));
        TelemetryFrame frame = new SensoryFrustumCollector().collectFrame(agent, null, null);
        java.util.Set<String> ids = new java.util.HashSet<>();
        frame.getInterestingBlocksList().forEach(b -> ids.add(b.getBlockId()));
        for (String want : new String[] {"minecraft:iron_ore", "minecraft:stone", "minecraft:red_mushroom",
                SensoryFrustumCollector.CAVE_ENTRANCE_ID}) {
            helper.assertTrue(ids.contains(want), "Explorer must see " + want + ", saw " + ids);
        }
        BlockPos hidden = helper.absolutePos(buried);
        helper.assertTrue(frame.getInterestingBlocksList().stream().noneMatch(b -> b.getPos().getX() == hidden.getX()
                && b.getPos().getY() == hidden.getY() && b.getPos().getZ() == hidden.getZ()
                && b.getBlockId().equals("minecraft:gold_ore")), "Buried ore must stay unseen");
        manager.despawnAgent(helper.getLevel().getServer(), "agent-gt-resources").join();
        helper.succeed();
    }

    private static boolean headingTo(AgentEntity agent, BlockPos cell) {
        net.minecraft.world.phys.Vec3 to = agent.getNavigator().getTargetCoords();
        return to != null && Math.abs(to.x - (cell.getX() + 0.5)) < 0.5 && Math.abs(to.z - (cell.getZ() + 0.5)) < 0.5;
    }

    // cq-6l3.19: a combat order moves the agent at once, a new one redirects it at once, and the
    // attack's own flight never overrides the ordered walk.
    @GameTest(template = "empty3x3x3")
    public static void testCombatOrderWalksAtOnce(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        stoneFloor(helper, -9, 9, FLIGHT_ARENA_Y);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-flight", new BlockPos(0, FLIGHT_ARENA_Y + 1, 0));
        Zombie zombie = helper.spawnWithNoFreeWill(EntityType.ZOMBIE, new BlockPos(-2, FLIGHT_ARENA_Y + 1, 0));
        BlockPos first = helper.absolutePos(new BlockPos(0, FLIGHT_ARENA_Y + 1, 8));
        BlockPos second = helper.absolutePos(new BlockPos(8, FLIGHT_ARENA_Y + 1, 0));
        IntentActuator actuator = new IntentActuator();
        net.minecraft.world.phys.Vec3 start = agent.position();
        actuator.submitBatch(agent, combatOrder(1, zombie.getId(), first));
        helper.assertTrue(headingTo(agent, first), "The ordered walk must start at once, heading "
                + agent.getNavigator().getTargetCoords() + " not " + first);
        for (int t = 0; t < 10; t++) {
            agent.tick();
            actuator.tick(agent);
        }
        helper.assertTrue(agent.position().distanceTo(start) > 0.5, "Agent must be moving, at " + agent.position());
        actuator.submitBatch(agent, combatOrder(2, zombie.getId(), first));
        helper.assertTrue(headingTo(agent, first), "The same order keeps the walk, heading " + agent.getNavigator().getTargetCoords());
        actuator.submitBatch(agent, combatOrder(3, zombie.getId(), second));
        helper.assertTrue(headingTo(agent, second), "A new order redirects at once, heading "
                + agent.getNavigator().getTargetCoords() + " not " + second);
        manager.despawnAgent(server, "agent-gt-flight").join();
        helper.succeed();
    }

    private static IntentBatch combatOrder(long batch, int targetId, BlockPos to) {
        return IntentBatch.newBuilder().setBatchId(batch)
                .addIntents(AgentIntent.newBuilder().setAttack(
                        AttackIntent.newBuilder().setTargetEntityId(targetId).setFleeImmediately(true)))
                .addIntents(AgentIntent.newBuilder().setNavigate(NavigateIntent.newBuilder().setArrivalTolerance(1)
                        .setTarget(Vec3.newBuilder().setX(to.getX() + 0.5).setY(to.getY()).setZ(to.getZ() + 0.5))))
                .build();
    }

    // cq-6l3: an agent with nothing to do in deep water sank and drowned.
    @GameTest(template = "empty3x3x3")
    public static void testIdleAgentStaysAfloat(GameTestHelper helper) {
        AgentEntityManager manager = new AgentEntityManager();
        pool(helper, IDLE_POOL_ARENA_Y);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-float", new BlockPos(0, IDLE_POOL_ARENA_Y + 1, 0));
        submerge(helper, agent, IDLE_POOL_ARENA_Y);
        float full = agent.getHealth();
        for (int t = 0; t < 800; t++) {
            agent.tick();
            helper.assertTrue(agent.getHealth() >= full, "Idle agent is drowning at tick " + t + " at " + agent.position()
                    + " air=" + agent.getAirSupply());
        }
        double surface = helper.absolutePos(new BlockPos(0, IDLE_POOL_ARENA_Y + POOL_DEPTH + 1, 0)).getY();
        helper.assertTrue(agent.getY() >= surface - 2.0, "Idle agent must float near the surface, y=" + agent.getY() + " surface=" + surface);
        manager.despawnAgent(helper.getLevel().getServer(), "agent-gt-float").join();
        helper.succeed();
    }

    // cq-6l3: a surface target overhead counted as reached from the bottom of the pool.
    @GameTest(template = "empty3x3x3")
    public static void testSwimsUpToSurfaceTarget(GameTestHelper helper) {
        AgentEntityManager manager = new AgentEntityManager();
        pool(helper, SURFACE_POOL_ARENA_Y);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-surface", new BlockPos(0, SURFACE_POOL_ARENA_Y + 1, 0));
        submerge(helper, agent, SURFACE_POOL_ARENA_Y);
        agent.tick();
        BlockPos up = helper.absolutePos(new BlockPos(0, SURFACE_POOL_ARENA_Y + POOL_DEPTH + 1, 0));
        double startDepth = up.getY() - agent.getY();
        navigateByIntent(manager, helper.getLevel().getServer(), "agent-gt-surface",
                up.getX() + 0.5, up.getY(), up.getZ() + 0.5, true, false);
        // Arrival ends the Move, so measure where the agent is when it does.
        for (int t = 0; t < 100 && (t == 0 || agent.getMotorFSM().topMotor().isPresent()); t++) {
            agent.tick();
        }
        helper.assertTrue(agent.getMotorFSM().topMotor().isEmpty() && up.getY() - agent.getY() <= 1.1,
                "Agent must swim to the surface target, started " + startDepth + " below, now " + (up.getY() - agent.getY()));
        manager.despawnAgent(helper.getLevel().getServer(), "agent-gt-surface").join();
        helper.succeed();
    }

    // cq-6l3.5: walks that cross a lit campfire must go round it without ever touching it.
    @GameTest(template = "empty3x3x3")
    public static void testMotionWalksRoundCampfire(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        stoneFloor(helper, -7, 7, CAMPFIRE_ARENA_Y);
        helper.setBlock(new BlockPos(0, CAMPFIRE_ARENA_Y + 1, 0), Blocks.CAMPFIRE);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-campfire", new BlockPos(-5, CAMPFIRE_ARENA_Y + 1, -4));
        float full = agent.getHealth();
        // Straight lines through the fire, past its corners and along its edges, at both gaits.
        int[][] legs = {{5, 4}, {-5, -2}, {5, 1}, {-5, 1}, {5, -1}, {-4, 5}, {4, -5}, {-1, 5}, {-1, -5},
                {1, 5}, {1, -5}, {-5, 0}, {5, 0}, {-5, -4}, {5, 5}, {-5, -5}};
        for (int i = 0; i < legs.length * 2; i++) {
            int[] leg = legs[i % legs.length];
            boolean sprint = i >= legs.length;
            BlockPos to = helper.absolutePos(new BlockPos(leg[0], CAMPFIRE_ARENA_Y + 1, leg[1]));
            navigateByIntent(manager, server, "agent-gt-campfire", to.getX() + 0.3, to.getY(), to.getZ() + 0.7, sprint, false);
            for (int t = 0; t < 200 && !agent.getNavigator().isArrived(); t++) {
                agent.tick();
                helper.assertTrue(agent.getHealth() >= full && !agent.isOnFire(), "Agent burned on leg " + i + " tick " + t
                        + " at " + agent.position() + " heading to " + to);
            }
            double remaining = Math.hypot(agent.getX() - (to.getX() + 0.3), agent.getZ() - (to.getZ() + 0.7));
            helper.assertTrue(remaining <= 2.0, "Leg " + i + " ended " + remaining + " blocks short at " + agent.position());
        }
        manager.despawnAgent(server, "agent-gt-campfire").join();
        helper.succeed();
    }

    private static void stoneFloor(GameTestHelper helper, int from, int to, int floorY) {
        for (int x = from; x <= to; x++) {
            for (int z = from; z <= to; z++) {
                helper.setBlock(new BlockPos(x, floorY, z), Blocks.STONE);
                for (int y = 1; y <= 3; y++) {
                    helper.setBlock(new BlockPos(x, floorY + y, z), Blocks.AIR);
                }
            }
        }
    }

    // cq-yn1.5: with its brain silent and no shield, an agent runs from a threat instead of crouching.
    @GameTest(template = "empty3x3x3")
    public static void testSafeModeWithoutShieldRetreats(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        stoneFloor(helper, -14, 6, RETREAT_ARENA_Y);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-retreat", new BlockPos(1, RETREAT_ARENA_Y + 1, 1));
        Zombie zombie = helper.spawn(EntityType.ZOMBIE, new BlockPos(3, RETREAT_ARENA_Y + 1, 3));
        zombie.setNoAi(true);
        double before = agent.distanceTo(zombie);

        for (int t = 0; t < 40; t++) {
            agent.tick();
        }

        helper.assertTrue(agent.distanceTo(zombie) >= before + 3.0,
                "Agent must retreat, distance " + before + " -> " + agent.distanceTo(zombie));
        helper.assertFalse(agent.isShiftKeyDown(), "A retreating agent must not crouch");
        zombie.discard();
        manager.despawnAgent(server, "agent-gt-retreat").join();
        helper.succeed();
    }

    // An arrow lying still is not a threat.
    @GameTest(template = "empty3x3x3")
    public static void testSafeModeIgnoresSpentArrow(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        stoneFloor(helper, -4, 6, SPENT_ARENA_Y);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-spent", new BlockPos(1, SPENT_ARENA_Y + 1, 1));
        net.minecraft.world.entity.projectile.Arrow arrow = helper.spawn(EntityType.ARROW, new BlockPos(3, SPENT_ARENA_Y + 1, 1));
        arrow.setNoGravity(true);
        arrow.setDeltaMovement(net.minecraft.world.phys.Vec3.ZERO);
        net.minecraft.world.phys.Vec3 start = agent.position();

        for (int t = 0; t < 30; t++) {
            agent.tick();
        }

        helper.assertTrue(agent.position().distanceTo(start) < 0.5, "Agent must stay put, moved to " + agent.position());
        helper.assertFalse(agent.isShiftKeyDown(), "Agent must not take a defensive crouch");
        arrow.discard();
        manager.despawnAgent(server, "agent-gt-spent").join();
        helper.succeed();
    }

    // cq-yn1.13: a standable cell one block up is not "arrived at" from the floor beside it.
    @GameTest(template = "empty3x3x3")
    public static void testMotionStepOntoAdjacentBlockWithTolerance(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        stoneFloor(helper, -1, 3, STEP_ARENA_Y);
        helper.setBlock(new BlockPos(1, STEP_ARENA_Y + 1, 1), Blocks.STONE);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-adjacent", new BlockPos(0, STEP_ARENA_Y + 1, 1));
        BlockPos target = helper.absolutePos(new BlockPos(1, STEP_ARENA_Y + 2, 1));
        double startY = agent.getY();

        navigateByIntent(manager, server, "agent-gt-adjacent",
                target.getX() + 0.5, target.getY(), target.getZ() + 0.5, false, false);
        for (int t = 0; t < 40; t++) {
            agent.tick();
        }

        helper.assertTrue(agent.getY() >= startY + 0.9, "Agent must climb onto the block, y=" + agent.getY() + " from " + startY);
        manager.despawnAgent(server, "agent-gt-adjacent").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionWalkDefaultAndStaminaSprint(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-stamina")
                .setDisplayName("StaminaAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        for (int x = 0; x <= 2; x++) {
            for (int z = 0; z <= 2; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
            }
        }

        // 1. Default sprint=false walks and preserves full stamina
        agent.getNavigator().navigate(spawnPos.getX() + 20.0, spawnPos.getY(), spawnPos.getZ() + 0.5,
                1.0f, false, false, false, 1);
        agent.getNavigator().tick();
        helper.assertFalse(agent.isSprinting(), "Default navigation must walk, not sprint");
        helper.assertTrue(agent.getStamina() == AgentNavigator.STAMINA_MAX, "Walking must not drain stamina");

        // 2. Sprint=true drains stamina until exhausted
        agent.getNavigator().navigate(spawnPos.getX() + 500.0, spawnPos.getY(), spawnPos.getZ() + 0.5,
                1.0f, true, false, false, 1);
        for (int t = 0; t < 200 && !agent.getNavigator().isExhausted(); t++) {
            agent.getNavigator().setPosition(spawnPos.getX() + 0.5, spawnPos.getY(), spawnPos.getZ() + 0.5);
            agent.getNavigator().setStuckTicks(0);
            agent.getNavigator().tick();
        }
        helper.assertTrue(agent.getNavigator().isExhausted(), "Sustained sprinting must exhaust stamina, stamina="
                + agent.getStamina() + " state=" + agent.getNavigator().getReportedNavigationState()
                + " receipt=" + agent.getMotorFSM().getCurrentReceipt());

        manager.despawnAgent(server, "agent-gt-stamina").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionFluidHeadBodySmoothing(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-smooth")
                .setDisplayName("SmoothAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        agent.setYRot(0.0f);
        agent.yBodyRot = 0.0f;
        navigateByIntent(manager, server, "agent-gt-smooth",
                spawnPos.getX() + 15.0, spawnPos.getY(), spawnPos.getZ() - 15.0, false, false);

        float prevHead = agent.getNavigator().getHeadYaw();
        float prevPitch = agent.getNavigator().getNavPitch();
        for (int t = 0; t < 20; t++) {
            agent.tick();
            float head = agent.getNavigator().getHeadYaw();
            float body = agent.getNavigator().getBodyYaw();
            float pitch = agent.getNavigator().getNavPitch();
            helper.assertTrue(Math.abs(LookSmoothing.wrap(head - prevHead)) <= 12.01f, "Head yaw delta must respect 12 deg/tick cap");
            helper.assertTrue(Math.abs(pitch - prevPitch) <= 6.01f, "Pitch delta must respect 6 deg/tick cap");
            helper.assertTrue(Math.abs(LookSmoothing.wrap(body - head)) <= 60.01f, "Body yaw must stay within 60 deg of head yaw");
            prevHead = head;
            prevPitch = pitch;
            appendMotionTrace("fluid_head_body_smoothing", t, agent);
        }

        manager.despawnAgent(server, "agent-gt-smooth").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testMotionDuskLogShelterConstruction(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();

        for (int x = 0; x <= 2; x++) {
            for (int z = 0; z <= 2; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
                helper.setBlock(new BlockPos(x, 1, z), Blocks.AIR);
                helper.setBlock(new BlockPos(x, 2, z), Blocks.AIR);
            }
        }
        // Provide physical crafting table within reach for 3x3 campfire recipe (ADR-029)
        helper.setBlock(new BlockPos(0, 1, 0), Blocks.CRAFTING_TABLE);

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setDisplayName("ShelterAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        // Give agent authentic crafting materials for 1x oak_planks (1 log) + 1x campfire (3 logs, 3 sticks, 1 coal)
        agent.getInventory().add(new ItemStack(Items.OAK_LOG, 4));
        agent.getInventory().add(new ItemStack(Items.STICK, 3));
        agent.getInventory().add(new ItemStack(Items.COAL, 1));

        // 1. Craft oak planks and campfire from logs
        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").setBatchCount(1).build())
                .build()).join();
        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").setBatchCount(1).build())
                .build()).join();

        // 2. Place campfire and bottom-up oak_planks wall blocks around (1, 1, 1)
        BlockPos absLight = helper.absolutePos(new BlockPos(2, 1, 2));
        BlockPos absWallBottom = helper.absolutePos(new BlockPos(2, 1, 1));
        BlockPos absWallTop = helper.absolutePos(new BlockPos(2, 2, 1));

        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absLight.getX()).setY(absLight.getY()).setZ(absLight.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                        .setBlockOrItemId("minecraft:campfire")
                        .build())
                .build()).join();
        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absWallBottom.getX()).setY(absWallBottom.getY()).setZ(absWallBottom.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                        .setBlockOrItemId("minecraft:oak_planks")
                        .build())
                .build()).join();
        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absWallTop.getX()).setY(absWallTop.getY()).setZ(absWallTop.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                        .setBlockOrItemId("minecraft:oak_planks")
                        .build())
                .build()).join();

        helper.assertBlockPresent(Blocks.CAMPFIRE, new BlockPos(2, 1, 2));
        helper.assertBlockPresent(Blocks.OAK_PLANKS, new BlockPos(2, 1, 1));
        helper.assertBlockPresent(Blocks.OAK_PLANKS, new BlockPos(2, 2, 1));

        manager.despawnAgent(server, "agent-gt-shelter").join();
        helper.succeed();
    }

    /** cq-3a0.4: place a ladder with FACING on a solid face, break it, and have the agent collect the item. */
    @GameTest(template = "empty3x3x3", timeoutTicks = 200)
    public static void testPlaceAndPickUpLadder(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();
        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-ladder")
                .setDisplayName("LadderAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();
        helper.assertTrue(agent != null, "Agent must spawn");
        agent.getInventory().add(new ItemStack(Items.LADDER, 2));

        BlockPos wall = new BlockPos(2, 1, 2);
        BlockPos rung = new BlockPos(1, 1, 2);
        helper.setBlock(wall, Blocks.STONE);
        // A closed floor and side walls keep the scattered drop within pickup range.
        for (int x = -1; x <= 3; x++) {
            for (int z = -1; z <= 3; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
                if (x == -1 || x == 3 || z == -1 || z == 3) {
                    helper.setBlock(new BlockPos(x, 1, z), Blocks.STONE);
                }
            }
        }
        BlockPos absRung = helper.absolutePos(rung);
        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-ladder")
                .setBuildBlock(com.civquest.mod.grpc.v1.BuildBlockIntent.newBuilder()
                        .setTargetVoxel(BlockPosProto.newBuilder().setX(absRung.getX()).setY(absRung.getY()).setZ(absRung.getZ()).build())
                        .setBlockStateString("minecraft:ladder[facing=west]")
                        .build())
                .build()).join();
        helper.assertBlockState(rung, s -> s.is(Blocks.LADDER)
                && s.getValue(net.minecraft.world.level.block.LadderBlock.FACING) == net.minecraft.core.Direction.WEST,
                () -> "ladder must be placed facing west against the stone wall");

        manager.handleIntent(server, AgentIntent.newBuilder()
                .setAgentId("agent-gt-ladder")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absRung.getX()).setY(absRung.getY()).setZ(absRung.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                        .build())
                .build()).join();
        // The drop spawns with a pickup delay; the agent's periodic pickup collects it once the delay elapses.
        helper.succeedWhen(() -> {
            helper.assertBlockNotPresent(Blocks.LADDER, rung);
            int n = 0;
            for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                ItemStack st = agent.getInventory().getItem(i);
                if (st.is(Items.LADDER)) n += st.getCount();
            }
            helper.assertTrue(n == 2, "ladder item must return to the agent inventory, had " + n);
            manager.despawnAgent(server, "agent-gt-ladder").join();
        });
    }

    // ---- cq-71l.4 coverage smoke tests -------------------------------------------------------

    private static AgentEntity spawnCoverageAgent(GameTestHelper helper, AgentEntityManager manager, String id, BlockPos rel) {
        BlockPos abs = helper.absolutePos(rel);
        return manager.spawnAgent(helper.getLevel().getServer(), helper.getLevel(), SpawnAgentRequest.newBuilder()
                .setAgentId(id)
                .setDisplayName(id)
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(abs.getX() + 0.5).setY(abs.getY()).setZ(abs.getZ() + 0.5).build())
                .build()).join();
    }

    private static int countItem(AgentEntity agent, net.minecraft.world.item.Item item) {
        int n = 0;
        for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
            ItemStack st = agent.getInventory().getItem(i);
            if (st.is(item)) n += st.getCount();
        }
        return n;
    }

    @GameTest(template = "empty3x3x3")
    public static void testCraftIntentPocketAndTable(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-craft", new BlockPos(1, 1, 1));
        agent.getInventory().add(new ItemStack(Items.OAK_PLANKS, 8));

        // 2x2 pocket recipe: planks -> sticks
        manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-craft")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:stick").setBatchCount(1).build())
                .build()).join();
        helper.assertTrue(countItem(agent, Items.STICK) >= 2, "2x2 stick craft must yield sticks");

        // 3x3 recipe without a table must not craft
        manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-craft")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_pickaxe").setBatchCount(1).build())
                .build()).join();
        helper.assertTrue(countItem(agent, Items.WOODEN_PICKAXE) == 0, "3x3 craft must fail without a crafting table");

        // 3x3 recipe with a table in reach succeeds
        helper.setBlock(new BlockPos(2, 1, 1), Blocks.CRAFTING_TABLE);
        manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-craft")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_pickaxe").setBatchCount(1).build())
                .build()).join();
        helper.assertTrue(countItem(agent, Items.WOODEN_PICKAXE) == 1, "3x3 craft must yield a pickaxe beside a crafting table");

        manager.despawnAgent(server, "agent-gt-craft").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testChatIntentDelivery(GameTestHelper helper) {
        MinecraftServer server = helper.getLevel().getServer();
        AgentEntityManager manager = new AgentEntityManager();
        AgentEntity speaker = spawnCoverageAgent(helper, manager, "agent-gt-chat-a", new BlockPos(0, 1, 0));
        AgentEntity listener = spawnCoverageAgent(helper, manager, "agent-gt-chat-b", new BlockPos(2, 1, 2));

        manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-chat-a")
                .setChat(com.civquest.mod.grpc.v1.SendChatIntent.newBuilder().setMessage("hello clan").build())
                .build()).join();

        List<com.civquest.mod.grpc.v1.ChatEvent> heard = listener.pollIncomingChats();
        helper.assertTrue(heard.stream().anyMatch(e -> "hello clan".equals(e.getMessage()) && e.getSenderIsAgent()),
                "Nearby agent must receive the chat event flagged as an agent's, got " + heard.size() + " events");
        helper.assertTrue(speaker.pollIncomingChats().stream().noneMatch(e -> "hello clan".equals(e.getMessage())),
                "Speaker must not hear its own chat");

        manager.despawnAgent(server, "agent-gt-chat-a").join();
        manager.despawnAgent(server, "agent-gt-chat-b").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSleepInBedAndWake(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-sleep", new BlockPos(0, 1, 0));

        net.minecraft.core.Direction facing = net.minecraft.core.Direction.SOUTH;
        BlockPos foot = new BlockPos(1, 1, 0);
        BlockPos head = foot.relative(facing);
        helper.setBlock(foot, Blocks.RED_BED.defaultBlockState()
                .setValue(net.minecraft.world.level.block.BedBlock.FACING, facing)
                .setValue(net.minecraft.world.level.block.BedBlock.PART, net.minecraft.world.level.block.state.properties.BedPart.FOOT));
        helper.setBlock(head, Blocks.RED_BED.defaultBlockState()
                .setValue(net.minecraft.world.level.block.BedBlock.FACING, facing)
                .setValue(net.minecraft.world.level.block.BedBlock.PART, net.minecraft.world.level.block.state.properties.BedPart.HEAD));

        // SleepHandler requires night or thunder; thunder is deterministic regardless of the sky-darken cache.
        level.setDayTime(14000L);
        level.setThunderLevel(1.0f);
        level.setRainLevel(1.0f);
        try {
            BlockPos absFoot = helper.absolutePos(foot);
            manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-sleep")
                    .setInteractBlock(InteractBlockIntent.newBuilder()
                            .setPos(BlockPosProto.newBuilder().setX(absFoot.getX()).setY(absFoot.getY()).setZ(absFoot.getZ()).build())
                            .setInteractionType(InteractBlockIntent.InteractionType.SLEEP_IN_BED).build())
                    .build()).join();
            // No ticking here: the stale sky-darken cache reads as day and vanilla wakes sleepers on a day tick.
            helper.assertTrue(agent.isSleeping(), "Agent must be asleep in the bed at night; night=" + level.isNight()
                    + " thunder=" + level.isThundering() + " depth=" + agent.getMotorFSM().getStackDepth()
                    + " top=" + agent.getMotorFSM().topMotor() + " bed=" + level.getBlockState(absFoot)
                    + " dist=" + agent.distanceToSqr(absFoot.getX() + 0.5, absFoot.getY() + 0.5, absFoot.getZ() + 0.5));

            manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-sleep")
                    .setInteractBlock(InteractBlockIntent.newBuilder()
                            .setPos(BlockPosProto.newBuilder().setX(absFoot.getX()).setY(absFoot.getY()).setZ(absFoot.getZ()).build())
                            .setInteractionType(InteractBlockIntent.InteractionType.WAKE_UP).build())
                    .build()).join();
            helper.assertFalse(agent.isSleeping(), "Agent must wake on WAKE_UP");
        } finally {
            level.setThunderLevel(0.0f);
            level.setRainLevel(0.0f);
        }
        manager.despawnAgent(server, "agent-gt-sleep").join();
        helper.succeed();
    }

    private static void interactAt(GameTestHelper helper, AgentEntityManager manager, String id, BlockPos rel,
                                   InteractBlockIntent.InteractionType type, String item) {
        BlockPos abs = helper.absolutePos(rel);
        manager.handleIntent(helper.getLevel().getServer(), AgentIntent.newBuilder().setAgentId(id)
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(abs.getX()).setY(abs.getY()).setZ(abs.getZ()).build())
                        .setInteractionType(type).setBlockOrItemId(item).build())
                .build()).join();
    }

    private static boolean isBedPart(GameTestHelper helper, BlockPos rel,
                                     net.minecraft.world.level.block.state.properties.BedPart part) {
        var state = helper.getLevel().getBlockState(helper.absolutePos(rel));
        return state.getBlock() instanceof net.minecraft.world.level.block.BedBlock
                && state.getValue(net.minecraft.world.level.block.BedBlock.PART) == part;
    }

    /** A placed bed occupies two blocks and needs room for both (cq-6l3.10). */
    @GameTest(template = "empty3x3x3")
    public static void testBedPlacementPlacesBothHalves(GameTestHelper helper) {
        AgentEntityManager manager = new AgentEntityManager();
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-bed-place", new BlockPos(0, 1, 0));
        agent.getInventory().add(new ItemStack(Items.WHITE_BED));
        agent.getInventory().add(new ItemStack(Items.WHITE_BED));
        var foot = net.minecraft.world.level.block.state.properties.BedPart.FOOT;
        var head = net.minecraft.world.level.block.state.properties.BedPart.HEAD;

        BlockPos boxed = new BlockPos(1, 1, 1);
        for (net.minecraft.core.Direction d : net.minecraft.core.Direction.Plane.HORIZONTAL) {
            helper.setBlock(boxed.relative(d), Blocks.STONE);
        }
        interactAt(helper, manager, "agent-gt-bed-place", boxed, InteractBlockIntent.InteractionType.PLACE_BLOCK, "minecraft:white_bed");
        helper.assertBlockPresent(Blocks.AIR, boxed);
        helper.assertTrue(countItem(agent, Items.WHITE_BED) == 2, "A bed with no room must stay in the inventory");

        helper.setBlock(boxed.east(), Blocks.AIR);
        interactAt(helper, manager, "agent-gt-bed-place", boxed, InteractBlockIntent.InteractionType.PLACE_BLOCK, "minecraft:white_bed");
        helper.assertTrue(isBedPart(helper, boxed, foot), "Foot half expected at the target cell");
        helper.assertTrue(isBedPart(helper, boxed.east(), head), "Head half expected in the only free neighbour");
        helper.assertTrue(countItem(agent, Items.WHITE_BED) == 1, "One bed item makes both halves");

        manager.despawnAgent(helper.getLevel().getServer(), "agent-gt-bed-place").join();
        helper.succeed();
    }

    /** Sleeping in a half bed completes it, or fails when the other half cannot fit (cq-6l3.10). */
    @GameTest(template = "empty3x3x3")
    public static void testSleepCompletesOrRejectsHalfBed(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        AgentEntityManager manager = new AgentEntityManager();
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-halfbed", new BlockPos(0, 1, 0));
        BlockPos foot = new BlockPos(1, 1, 1);
        helper.setBlock(foot.north(), Blocks.STONE);
        helper.setBlock(foot, Blocks.RED_BED);
        level.setDayTime(14000L);
        level.setThunderLevel(1.0f);
        level.setRainLevel(1.0f);
        try {
            // The default state faces north, into the stone.
            interactAt(helper, manager, "agent-gt-halfbed", foot, InteractBlockIntent.InteractionType.SLEEP_IN_BED, "minecraft:bed");
            helper.assertFalse(agent.isSleeping(), "No sleeping in a half bed that cannot be completed");

            helper.setBlock(foot.north(), Blocks.AIR); // the update removes the lone foot, so set it again
            helper.setBlock(foot, Blocks.RED_BED);
            interactAt(helper, manager, "agent-gt-halfbed", foot, InteractBlockIntent.InteractionType.SLEEP_IN_BED, "minecraft:bed");
            helper.assertTrue(isBedPart(helper, foot.north(), net.minecraft.world.level.block.state.properties.BedPart.HEAD),
                    "The missing head must be added");
            helper.assertTrue(agent.isSleeping(), "Agent must be asleep in the completed bed");
            BlockPos at = agent.getSleepingPos().orElse(null);
            helper.assertTrue(at != null && level.getBlockState(at).isBed(level, at, agent),
                    "The sleeping position must hold a bed, or vanilla wakes the sleeper");
        } finally {
            level.setThunderLevel(0.0f);
            level.setRainLevel(0.0f);
        }
        manager.despawnAgent(level.getServer(), "agent-gt-halfbed").join();
        helper.succeed();
    }

    /** An idle agent uses food at the normal rate, a full bar in three days (cq-6l3.8). */
    @GameTest(template = "empty3x3x3")
    public static void testIdleAgentMetabolism(GameTestHelper helper) {
        AgentEntityManager manager = new AgentEntityManager();
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-metabolism", new BlockPos(1, 1, 1));
        helper.runAfterDelay(10, () -> {
            float before = agent.getFoodData().getExhaustionLevel();
            helper.runAfterDelay(40, () -> {
                float used = agent.getFoodData().getExhaustionLevel() - before;
                float perTick = com.civquest.mod.Metabolism.NORMAL_PER_TICK;
                helper.assertTrue(used >= 30 * perTick && used <= 50 * perTick,
                        "40 idle ticks used " + used + " exhaustion, expected about " + 40 * perTick);
                manager.despawnAgent(helper.getLevel().getServer(), "agent-gt-metabolism").join();
                helper.succeed();
            });
        });
    }

    @GameTest(template = "empty3x3x3")
    public static void testFleeReflexPreemptsMove(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();
        for (int x = 0; x <= 2; x++) {
            for (int z = 0; z <= 2; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
            }
        }
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-flee", new BlockPos(0, 1, 0));
        // A connected brain: otherwise the safe-mode watchdog raises its own retreat first.
        agent.getWatchdogFSM().onConnected();
        Zombie zombie = helper.spawn(EntityType.ZOMBIE, new BlockPos(0, 1, 2));
        zombie.setNoAi(true);

        BlockPos far = helper.absolutePos(new BlockPos(2, 1, 2));
        manager.handleIntent(server, AgentIntent.newBuilder().setAgentId("agent-gt-flee")
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(far.getX() + 0.5).setY(far.getY()).setZ(far.getZ() + 0.5).build())
                        .setSpeedMultiplier(1.0f).setArrivalTolerance(1).build())
                .build()).join();
        for (int i = 0; i < 3; i++) agent.tick();
        helper.assertTrue(agent.getMotorFSM().topMotor().isPresent(), "Navigate must leave a motor frame to preempt; nav=" + agent.getNavigator().getNavigationState()
                + " top=" + agent.getMotorFSM().topMotor() + " pos=" + agent.position() + " zombie=" + zombie.position());

        agent.pollReflexNotices();
        boolean pushed = agent.getMotorFSM().preemptWithFleeReflex(
                Vec3.newBuilder().setX(zombie.getX()).setY(zombie.getY()).setZ(zombie.getZ()).build(), "hostile_near");
        helper.assertTrue(pushed, "Flee reflex must preempt the routine motor frame");
        helper.assertTrue(agent.getMotorFSM().activeReflex().filter(m -> m instanceof com.civquest.mod.actuator.motor.Flee).isPresent(),
                "Flee must be the active reflex on top of the stack");
        boolean notice = agent.pollReflexNotices().stream().anyMatch(n ->
                n.getReflexType() == com.civquest.mod.grpc.v1.ReflexNotice.ReflexType.REFLEX_TYPE_FLEE && !n.getPopped());
        helper.assertTrue(notice, "A FLEE ReflexNotice must be emitted");

        zombie.discard();
        manager.despawnAgent(server, "agent-gt-flee").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testPioneerCampStructureGenerates(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        var structureReg = level.registryAccess().registryOrThrow(net.minecraft.core.registries.Registries.STRUCTURE);
        helper.assertTrue(structureReg.containsKey(com.civquest.mod.worldgen.CivQuestStructures.PIONEER_CAMPFIRE_ID),
                "pioneer_campfire structure must be registered via datapack");
        var tpl = level.getStructureManager().get(com.civquest.mod.worldgen.CivQuestStructures.PIONEER_CAMPFIRE_ID);
        helper.assertTrue(tpl.isPresent(), "pioneer_campfire template must load");

        BlockPos origin = helper.absolutePos(new BlockPos(0, 1, 0));
        boolean placed = tpl.get().placeInWorld(level, origin, origin,
                new net.minecraft.world.level.levelgen.structure.templatesystem.StructurePlaceSettings(),
                level.getRandom(), 2);
        helper.assertTrue(placed, "pioneer_campfire template must place in world");

        var size = tpl.get().getSize();
        boolean campfire = false;
        for (BlockPos p : BlockPos.betweenClosed(origin, origin.offset(size.getX() - 1, size.getY() - 1, size.getZ() - 1))) {
            if (level.getBlockState(p).is(Blocks.CAMPFIRE)) {
                campfire = true;
                break;
            }
        }
        helper.assertTrue(campfire, "Placed camp must contain a campfire");
        helper.succeed();
    }

    private static AgentIntent useItem(String agentId, UseItemIntent.Builder b) {
        return AgentIntent.newBuilder().setAgentId(agentId).setUseItem(b).build();
    }

    private static int shearsDamage(AgentEntity agent) {
        for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
            ItemStack st = agent.getInventory().getItem(i);
            if (st.is(Items.SHEARS)) return st.getDamageValue();
        }
        return -1;
    }

    @GameTest(template = "empty3x3x3")
    public static void testShearSheepIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();
        for (int x = 0; x <= 2; x++) {
            for (int z = 0; z <= 2; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
            }
        }
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-shear", new BlockPos(0, 1, 0));
        net.minecraft.world.entity.animal.Sheep sheep = helper.spawn(EntityType.SHEEP, new BlockPos(2, 1, 2));
        sheep.setNoAi(true);

        // Without shears the intent must not shear.
        manager.handleIntent(server, useItem("agent-gt-shear", UseItemIntent.newBuilder()
                .setMode(UseItemIntent.Mode.SHEAR_ENTITY).setTargetEntityId(sheep.getId()))).join();
        helper.assertTrue(!sheep.isSheared(), "A sheep must not be sheared without shears");

        agent.getInventory().add(new ItemStack(Items.SHEARS));
        manager.handleIntent(server, useItem("agent-gt-shear", UseItemIntent.newBuilder()
                .setMode(UseItemIntent.Mode.SHEAR_ENTITY).setTargetEntityId(sheep.getId()))).join();
        helper.assertTrue(sheep.isSheared(), "Shears must shear an adult sheep in reach");

        // Telemetry reports the sheared state.
        TelemetryFrame frame = new SensoryFrustumCollector().collectFrame(agent, null, null);
        VisibleEntity seen = frame.getVisibleEntitiesList().stream()
                .filter(e -> e.getEntityId() == sheep.getId()).findFirst().orElse(null);
        helper.assertTrue(seen != null && seen.getIsSheared() && !seen.getIsBaby(), "Telemetry must report is_sheared on the sheep");

        // An already-sheared sheep is rejected (no wool, shears not worn twice).
        int damage = shearsDamage(agent);
        manager.handleIntent(server, useItem("agent-gt-shear", UseItemIntent.newBuilder()
                .setMode(UseItemIntent.Mode.SHEAR_ENTITY).setTargetEntityId(sheep.getId()))).join();
        helper.assertTrue(shearsDamage(agent) == damage, "A sheared sheep must not wear the shears again");

        sheep.discard();
        manager.despawnAgent(server, "agent-gt-shear").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testFishingRodCastAndBiteTelemetry(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        AgentEntityManager manager = new AgentEntityManager();
        for (int x = 0; x <= 2; x++) {
            for (int z = 0; z <= 2; z++) {
                helper.setBlock(new BlockPos(x, 0, z), Blocks.STONE);
            }
        }
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-fish", new BlockPos(0, 1, 0));

        // No rod: cast must fail and no line is reported.
        manager.handleIntent(server, useItem("agent-gt-fish", UseItemIntent.newBuilder()
                .setMode(UseItemIntent.Mode.CAST_FISHING_ROD))).join();
        helper.assertTrue(agent.fishing == null, "Casting without a rod must not create a hook");

        agent.getInventory().add(new ItemStack(Items.FISHING_ROD));
        manager.handleIntent(server, useItem("agent-gt-fish", UseItemIntent.newBuilder()
                .setMode(UseItemIntent.Mode.CAST_FISHING_ROD))).join();
        helper.assertTrue(agent.fishing != null, "Casting with a rod must create a hook entity");
        TelemetryFrame cast = new SensoryFrustumCollector().collectFrame(agent, null, null);
        helper.assertTrue(cast.getStatus().hasFishing() && cast.getStatus().getFishing().getCast(), "Telemetry must report the cast line");
        helper.assertTrue(!cast.getStatus().getFishing().getBite(), "No bite on a fresh cast");

        // Force the bite flag the way vanilla does when a fish nibbles, then check telemetry and reel.
        try {
            java.lang.reflect.Field f = net.minecraft.world.entity.projectile.FishingHook.class.getDeclaredField("biting");
            f.setAccessible(true);
            f.setBoolean(agent.fishing, true);
        } catch (ReflectiveOperationException e) {
            helper.fail("cannot reach FishingHook.biting: " + e);
            return;
        }
        TelemetryFrame bite = new SensoryFrustumCollector().collectFrame(agent, null, null);
        helper.assertTrue(bite.getStatus().getFishing().getBite(), "Telemetry must report the bite");

        manager.handleIntent(server, useItem("agent-gt-fish", UseItemIntent.newBuilder()
                .setMode(UseItemIntent.Mode.REEL_FISHING_ROD))).join();
        helper.assertTrue(agent.fishing == null, "Reeling must retrieve the line");
        helper.assertTrue(!new SensoryFrustumCollector().collectFrame(agent, null, null).getStatus().hasFishing(),
                "Telemetry must clear fishing state after reeling");

        manager.despawnAgent(server, "agent-gt-fish").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSurfaceWaterSurfacedInTelemetry(GameTestHelper helper) {
        AgentEntityManager manager = new AgentEntityManager();
        helper.setBlock(new BlockPos(0, 0, 0), Blocks.STONE);
        AgentEntity agent = spawnCoverageAgent(helper, manager, "agent-gt-water", new BlockPos(0, 1, 0));
        helper.setBlock(new BlockPos(2, 0, 2), Blocks.WATER);
        helper.setBlock(new BlockPos(2, 1, 2), Blocks.AIR);
        TelemetryFrame frame = new SensoryFrustumCollector().collectFrame(agent, null, null);
        helper.assertTrue(frame.getInterestingBlocksList().stream().anyMatch(b -> "minecraft:water".equals(b.getBlockId())),
                "Surface water within scan radius must be listed as an interesting block");
        manager.despawnAgent(helper.getLevel().getServer(), "agent-gt-water").join();
        helper.succeed();
    }
}
