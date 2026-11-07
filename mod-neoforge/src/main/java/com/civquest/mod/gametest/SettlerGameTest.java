package com.civquest.mod.gametest;

import com.civquest.mod.SettlerEntity;
import com.civquest.mod.SettlerEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.Vec3;
import com.google.protobuf.ByteString;
import io.grpc.stub.StreamObserver;
import net.minecraft.core.BlockPos;
import net.minecraft.gametest.framework.GameTest;
import net.minecraft.gametest.framework.GameTestHelper;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.world.level.ChunkPos;
import net.minecraft.world.level.block.Blocks;
import net.neoforged.neoforge.gametest.GameTestHolder;
import net.neoforged.neoforge.gametest.PrefixGameTestTemplate;

import java.nio.charset.StandardCharsets;

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

        // Register active telemetry stream for agent
        streamRegistry.register("agent-gt-death", new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {}
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
}
