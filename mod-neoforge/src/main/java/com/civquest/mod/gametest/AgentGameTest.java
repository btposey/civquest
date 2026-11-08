package com.civquest.mod.gametest;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.AgentEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.actuator.LookSmoothing;
import com.civquest.mod.sensory.SensoryFrustumCollector;
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
import net.minecraft.world.level.ChunkPos;
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

        ChunkPos forcedChunk = agent.getForcedChunkPos();
        helper.assertTrue(forcedChunk != null, "Spawn chunk must be forced");
        helper.assertTrue(level.getForcedChunks().contains(forcedChunk.toLong()), "Level must force spawn chunk");

        // Tick agent entity in server level
        agent.tick();
        helper.assertTrue(agent.isAlive(), "Agent must remain alive after tick");

        // Clean despawn
        boolean despawned = manager.despawnAgent(server, "agent-gt-spawn").join();
        helper.assertTrue(despawned, "Agent must despawn cleanly");
        helper.assertTrue(manager.getAgent("agent-gt-spawn") == null, "Manager must clear despawned agent");
        helper.assertTrue(agent.getForcedChunkPos() == null, "Forced chunk must be released after despawn");
        helper.assertFalse(level.getForcedChunks().contains(forcedChunk.toLong()), "Level must release forced chunk");
        helper.succeed();
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
        ChunkPos forcedChunk = agent.getForcedChunkPos();
        helper.assertTrue(forcedChunk != null, "Forced chunk must be set");

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
        helper.assertTrue(agent.getForcedChunkPos() == null, "Forced chunk must be cleared on agent");
        helper.assertFalse(level.getForcedChunks().contains(forcedChunk.toLong()), "Level forced chunk must be released");
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
        helper.setBlock(localBed, Blocks.RED_BED);
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

        agent.getNavigator().navigate(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5, 1.0f, false, false, false, 1);
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

        agent.getNavigator().navigate(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5, 1.0f, false, false, false, 1);
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
    public static void testMotionEnclosedPitDigOutWithShovel(GameTestHelper helper) {
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

        // Put iron shovel in non-selected slot 4 to verify tool selection equips it into mainhand
        agent.getInventory().setItem(4, new ItemStack(Items.IRON_SHOVEL));
        agent.getNavigator().navigate(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5, 1.0f, false, true, false, 1);

        boolean equippedShovel = false;
        for (int t = 0; t < 40; t++) {
            agent.tick();
            if (agent.getMainHandItem().is(Items.IRON_SHOVEL)) {
                equippedShovel = true;
            }
            appendMotionTrace("enclosed_pit_digout", t, agent);
            if (helper.getBlockState(new BlockPos(2, 2, 1)).isAir()) {
                break;
            }
        }

        helper.assertTrue(equippedShovel, "Agent must equip the iron shovel when digging dirt");
        helper.assertBlockNotPresent(Blocks.DIRT, new BlockPos(2, 2, 1));
        manager.despawnAgent(server, "agent-gt-digout").join();
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

        // 1. Default sprint=false walks and preserves full stamina
        agent.getNavigator().navigate(spawnPos.getX() + 20.0, spawnPos.getY(), spawnPos.getZ() + 0.5, 1.0f, false, false, false, 1);
        agent.tick();
        helper.assertFalse(agent.isSprinting(), "Default navigation must walk, not sprint");
        helper.assertTrue(agent.getStamina() == AgentNavigator.STAMINA_MAX, "Walking must not drain stamina");

        // 2. Sprint=true drains stamina until exhausted
        agent.getNavigator().navigate(spawnPos.getX() + 500.0, spawnPos.getY(), spawnPos.getZ() + 0.5, 1.0f, true, false, false, 1);
        for (int t = 0; t < 200 && !agent.getNavigator().isExhausted(); t++) {
            agent.getNavigator().setPosition(spawnPos.getX() + 0.5, spawnPos.getY(), spawnPos.getZ() + 0.5);
            agent.getNavigator().setStuckTicks(0);
            agent.tick();
        }
        helper.assertTrue(agent.getNavigator().isExhausted(), "Sustained sprinting must exhaust stamina");

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
        agent.getNavigator().navigate(spawnPos.getX() + 15.0, spawnPos.getY(), spawnPos.getZ() - 15.0, 1.0f, false, false, false, 1);

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

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        AgentEntity agent = manager.spawnAgent(server, level, SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-shelter")
                .setDisplayName("ShelterAgent")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder().setX(spawnPos.getX() + 0.5).setY(spawnPos.getY()).setZ(spawnPos.getZ() + 0.5).build())
                .build()).join();

        // Give agent 4 oak logs at dusk
        agent.getInventory().add(new ItemStack(Items.OAK_LOG, 4));

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
}
