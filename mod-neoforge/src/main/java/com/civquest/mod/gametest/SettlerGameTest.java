package com.civquest.mod.gametest;

import com.civquest.mod.SettlerEntity;
import com.civquest.mod.SettlerEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.TelemetryFrame;
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
import net.neoforged.neoforge.common.damagesource.DamageContainer;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;
import net.neoforged.neoforge.gametest.GameTestHolder;
import net.neoforged.neoforge.gametest.PrefixGameTestTemplate;

import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

@GameTestHolder("civquest")
@PrefixGameTestTemplate(false)
public class SettlerGameTest {

    @GameTest(template = "empty3x3x3")
    public static void testSettlerEnvironment(GameTestHelper helper) {
        BlockPos pos = new BlockPos(1, 1, 1);
        helper.setBlock(pos, Blocks.STONE);
        helper.assertBlockPresent(Blocks.STONE, pos);
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSettlerSpawnAndTick(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-spawn")
                .setDisplayName("SettlerGameTest")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .setSkinPngBytes(ByteString.copyFrom("test-skin-bytes", StandardCharsets.UTF_8))
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "SettlerEntity must not be null");
        helper.assertTrue(settler == manager.getSettler("agent-gt-spawn"), "Manager must track active settler");
        helper.assertTrue("agent-gt-spawn".equals(settler.getAgentId()), "Agent ID must match request");
        helper.assertTrue("clan-ironwood".equals(settler.getClanId()), "Clan ID must match request");
        helper.assertTrue(settler.getFoodData().getFoodLevel() == 20, "Food level must initialize to 20");
        helper.assertTrue(settler.getHealth() == settler.getMaxHealth(), "Health must initialize to maximum");
        helper.assertFalse(settler.canUsePortal(false), "Settler cannot use portals");

        ChunkPos forcedChunk = settler.getForcedChunkPos();
        helper.assertTrue(forcedChunk != null, "Spawn chunk must be forced");
        helper.assertTrue(level.getForcedChunks().contains(forcedChunk.toLong()), "Level must force spawn chunk");

        // Tick settler entity in server level
        settler.tick();
        helper.assertTrue(settler.isAlive(), "Settler must remain alive after tick");

        // Clean despawn
        boolean despawned = manager.despawnSettler(server, "agent-gt-spawn").join();
        helper.assertTrue(despawned, "Settler must despawn cleanly");
        helper.assertTrue(manager.getSettler("agent-gt-spawn") == null, "Manager must clear despawned settler");
        helper.assertTrue(settler.getForcedChunkPos() == null, "Forced chunk must be released after despawn");
        helper.assertFalse(level.getForcedChunks().contains(forcedChunk.toLong()), "Level must release forced chunk");
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSettlerPermadeathLifecycle(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        StreamRegistry streamRegistry = new StreamRegistry();
        SettlerEntityManager manager = new SettlerEntityManager(streamRegistry);

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
                .setDisplayName("MortalSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");
        helper.assertTrue(manager.getSettler("agent-gt-death") != null, "Settler must be registered");
        ChunkPos forcedChunk = settler.getForcedChunkPos();
        helper.assertTrue(forcedChunk != null, "Forced chunk must be set");

        // Kill settler with generic damage
        settler.die(level.damageSources().generic());

        // Settler should be discarded and automatically unregistered via deathListener
        helper.assertTrue(settler.isRemoved(), "Settler entity must be removed from level upon death");
        helper.assertTrue(manager.getSettler("agent-gt-death") == null, "Settler must be unregistered upon death");
        helper.assertFalse(streamRegistry.isRegistered("agent-gt-death"), "Stream must be cleanly closed on death (ADR-008)");
        helper.assertTrue(deathFrameRef.get() != null, "Terminal death telemetry frame must be sent prior to stream closure");
        helper.assertTrue(deathFrameRef.get().hasDeathEvent(), "Terminal frame must contain death_event");
        helper.assertTrue("agent-gt-death".equals(deathFrameRef.get().getDeathEvent().getAgentId()), "DeathEvent must have matching agent_id");
        helper.assertTrue(streamOpenWhenDeathReceived.get(), "Death frame must be delivered while stream was still registered");
        helper.assertTrue(deathFrameRef.get().getInterestingBlocksCount() == 0,
                "Terminal death frame must skip block scanning");
        helper.assertTrue(deathFrameRef.get().getVisibleEntitiesCount() == 0,
                "Terminal death frame must skip entity scanning");
        helper.assertTrue(settler.getForcedChunkPos() == null, "Forced chunk must be cleared on settler");
        helper.assertFalse(level.getForcedChunks().contains(forcedChunk.toLong()), "Level forced chunk must be released");
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testDuplicateAgentSpawn(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request1 = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-dup")
                .setDisplayName("FirstSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler1 = manager.spawnSettler(server, level, request1).join();
        helper.assertTrue(settler1 != null, "First settler must spawn");
        helper.assertTrue(manager.count() == 1, "Manager must have 1 settler");
        helper.assertTrue("FirstSettler".equals(settler1.getGameProfile().getName()), "Name must match first");

        // Spawn second settler with same agentId
        SpawnAgentRequest request2 = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-dup")
                .setDisplayName("SecondSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler2 = manager.spawnSettler(server, level, request2).join();
        helper.assertTrue(settler2 != null, "Second settler must spawn");
        helper.assertTrue(manager.count() == 1, "Manager must still have 1 settler");
        helper.assertTrue(settler1.isRemoved(), "First settler must be discarded/removed");
        helper.assertTrue(settler2 == manager.getSettler("agent-gt-dup"), "Manager must track second settler");
        helper.assertTrue("SecondSettler".equals(settler2.getGameProfile().getName()), "Name must match second");

        // Clean up
        manager.despawnSettler(server, "agent-gt-dup").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSensoryFrustumBlockScanning(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

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
                .setDisplayName("BlockScannerSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

        TelemetryFrame frame = manager.getSensoryCollector().collectFrame(settler);
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
        TelemetryFrame frameFarmlandOnly = manager.getSensoryCollector().collectFrame(settler);
        helper.assertTrue(frameFarmlandOnly.hasFarmlandSurvey(),
                "Must populate FarmlandSurveyEvent for farmland even without mature crops");
        helper.assertTrue("".equals(frameFarmlandOnly.getFarmlandSurvey().getPrimaryCropId()),
                "Primary crop ID must be empty string when mature crops are 0");
        helper.assertTrue(frameFarmlandOnly.getFarmlandSurvey().getMatureCropCount() == 0,
                "Mature crop count must be 0");
        helper.assertTrue(frameFarmlandOnly.getFarmlandSurvey().getTotalFarmlandBlocks() >= 1,
                "Total farmland blocks must be at least 1");

        manager.despawnSettler(server, "agent-gt-blocks").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSensoryFrustumEntityScanningAndFuseTicks(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-entity")
                .setDisplayName("EntityScannerSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

        // Spawn Creeper adjacent to settler within line of sight
        Creeper creeper = helper.spawn(EntityType.CREEPER, new BlockPos(1, 1, 2));
        helper.assertTrue(creeper != null, "Creeper must spawn");
        creeper.ignite();

        TelemetryFrame frame = manager.getSensoryCollector().collectFrame(settler);
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
        manager.despawnSettler(server, "agent-gt-entity").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSensory5HzTelemetryStreaming(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        StreamRegistry streamRegistry = new StreamRegistry();
        SettlerEntityManager manager = new SettlerEntityManager(streamRegistry);

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
                .setDisplayName("StreamSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

        // Manually trigger telemetry tick
        manager.tickTelemetry();
        helper.assertTrue(receivedFrames.size() == 1,
                "Must receive exactly 1 telemetry frame on tick, got: " + receivedFrames.size());

        TelemetryFrame frame = receivedFrames.get(0);
        helper.assertTrue(frame.hasStatus(), "Frame must have status");
        helper.assertTrue("agent-gt-stream".equals(frame.getStatus().getAgentId()), "Agent ID must match");
        helper.assertTrue(frame.getStatus().getHealth() == settler.getMaxHealth(), "Health must match max health");

        helper.assertTrue(streamRegistry.isRegistered("agent-gt-stream"), "Agent stream must be registered prior to despawn");
        manager.despawnSettler(server, "agent-gt-stream").join();
        helper.assertFalse(streamRegistry.isRegistered("agent-gt-stream"),
                "Agent stream must be cleanly closed via removalListener upon non-death despawn");
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testImmediateDamageEventStreaming(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        StreamRegistry streamRegistry = new StreamRegistry();
        SettlerEntityManager manager = new SettlerEntityManager(streamRegistry);

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
                .setDisplayName("DamageSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

        // Emit LivingDamageEvent.Post
        DamageSource damageSource = level.damageSources().magic();
        DamageContainer container = new DamageContainer(damageSource, 4.5f);
        LivingDamageEvent.Post postEvent = new LivingDamageEvent.Post(settler, container);
        manager.onLivingDamage(postEvent);

        helper.assertTrue(receivedFrames.size() == 1,
                "Must receive immediate telemetry frame upon damage event, received: " + receivedFrames.size());

        TelemetryFrame frame = receivedFrames.get(0);
        helper.assertTrue(frame.hasLastDamageTaken(), "Frame must contain last_damage_taken");
        helper.assertTrue(frame.getLastDamageTaken().getDamageAmount() == 4.5f,
                "Damage amount must match 4.5f, was: " + frame.getLastDamageTaken().getDamageAmount());

        // Emit damage event when settler is dying / dead (health <= 0) - should be skipped
        settler.setHealth(0.0f);
        manager.onLivingDamage(postEvent);
        helper.assertTrue(receivedFrames.size() == 1,
                "Must not emit damage frame when settler is dead/dying, received count: " + receivedFrames.size());

        manager.despawnSettler(server, "agent-gt-damage-event").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testNavigateIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        // Prepare flat walking floor at Y=0
        helper.setBlock(new BlockPos(1, 0, 1), Blocks.STONE);
        helper.setBlock(new BlockPos(1, 0, 2), Blocks.STONE);
        helper.setBlock(new BlockPos(1, 1, 1), Blocks.AIR);
        helper.setBlock(new BlockPos(1, 1, 2), Blocks.AIR);

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-nav")
                .setDisplayName("NavSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

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
        helper.assertTrue(settler.getNavigator().isPathing(), "Settler navigator must be PATHING");

        double startDistSq = settler.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5);
        for (int i = 0; i < 5; i++) {
            settler.tick();
        }
        double endDistSq = settler.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY(), targetPos.getZ() + 0.5);
        helper.assertTrue(endDistSq <= startDistSq, "Settler must move closer towards target destination");

        manager.despawnSettler(server, "agent-gt-nav").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testAttackIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-attack")
                .setDisplayName("AttackSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");
        settler.setItemInHand(InteractionHand.MAIN_HAND, new ItemStack(Items.IRON_SWORD));

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

        // Verify shield raised in offhand
        helper.assertTrue(settler.getItemInHand(InteractionHand.OFF_HAND).is(Items.SHIELD),
                "Settler must hold shield in offhand");
        helper.assertTrue(settler.isUsingItem(), "Settler must be using offhand shield item");
        helper.assertTrue(settler.getUsedItemHand() == InteractionHand.OFF_HAND,
                "Settler used hand must be OFF_HAND");

        // Verify target entity took damage
        helper.assertTrue(zombie.getHealth() < initialHealth,
                "Target zombie must have taken damage, health was: " + zombie.getHealth());

        zombie.discard();
        manager.despawnSettler(server, "agent-gt-attack").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testInteractBlockIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-interact")
                .setDisplayName("InteractSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

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

        // 2. PAVE_ROAD_PATH on grass block
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

        // 3. SLEEP_IN_BED and WAKE_UP
        BlockPos localBed = new BlockPos(1, 1, 0);
        BlockPos absBed = helper.absolutePos(localBed);
        helper.setBlock(localBed, Blocks.RED_BED);

        AgentIntent sleepIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(absBed.getX()).setY(absBed.getY()).setZ(absBed.getZ()).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.SLEEP_IN_BED)
                        .build())
                .build();
        manager.handleIntent(server, sleepIntent).join();
        helper.assertTrue(settler.isSleeping(), "Settler must be sleeping in bed");

        AgentIntent wakeIntent = AgentIntent.newBuilder()
                .setAgentId("agent-gt-interact")
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setInteractionType(InteractBlockIntent.InteractionType.WAKE_UP)
                        .build())
                .build();
        manager.handleIntent(server, wakeIntent).join();
        helper.assertFalse(settler.isSleeping(), "Settler must wake up from bed");

        manager.despawnSettler(server, "agent-gt-interact").join();
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testCreateWrittenBookIntent(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

        BlockPos spawnPos = helper.absolutePos(new BlockPos(1, 1, 1));
        SpawnAgentRequest request = SpawnAgentRequest.newBuilder()
                .setAgentId("agent-gt-book")
                .setDisplayName("ChroniclerSettler")
                .setClanId("clan-ironwood")
                .setSpawnCoords(Vec3.newBuilder()
                        .setX(spawnPos.getX() + 0.5)
                        .setY(spawnPos.getY())
                        .setZ(spawnPos.getZ() + 0.5)
                        .build())
                .build();

        SettlerEntity settler = manager.spawnSettler(server, level, request).join();
        helper.assertTrue(settler != null, "Settler must spawn");

        BlockPos localLectern = new BlockPos(1, 1, 2);
        BlockPos absLectern = helper.absolutePos(localLectern);
        helper.setBlock(localLectern, Blocks.LECTERN);
        helper.assertBlockPresent(Blocks.LECTERN, localLectern);

        CreateWrittenBookIntent bookIntent = CreateWrittenBookIntent.newBuilder()
                .setTitle("Settler Annals")
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
        helper.assertTrue("Settler Annals".equals(content.title().raw()), "Book title must match intent");
        helper.assertTrue("Chronicler".equals(content.author()), "Book author must match intent");
        helper.assertTrue(content.pages().size() == 2, "Page count must match intent (2)");

        manager.despawnSettler(server, "agent-gt-book").join();
        helper.succeed();
    }
}
