package com.civquest.mod.gametest;

import com.civquest.mod.SettlerEntity;
import com.civquest.mod.SettlerEntityManager;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.Vec3;
import com.google.protobuf.ByteString;
import net.minecraft.core.BlockPos;
import net.minecraft.gametest.framework.GameTest;
import net.minecraft.gametest.framework.GameTestHelper;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
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

        // Tick settler entity in server level
        settler.tick();
        helper.assertTrue(settler.isAlive(), "Settler must remain alive after tick");

        // Clean despawn
        boolean despawned = manager.despawnSettler(server, "agent-gt-spawn").join();
        helper.assertTrue(despawned, "Settler must despawn cleanly");
        helper.assertTrue(manager.getSettler("agent-gt-spawn") == null, "Manager must clear despawned settler");
        helper.succeed();
    }

    @GameTest(template = "empty3x3x3")
    public static void testSettlerPermadeathLifecycle(GameTestHelper helper) {
        ServerLevel level = helper.getLevel();
        MinecraftServer server = level.getServer();
        SettlerEntityManager manager = new SettlerEntityManager();

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

        // Kill settler with generic damage
        settler.die(level.damageSources().generic());

        // Settler should be discarded and automatically unregistered via deathListener
        helper.assertTrue(settler.isRemoved(), "Settler entity must be removed from level upon death");
        helper.assertTrue(manager.getSettler("agent-gt-death") == null, "Settler must be unregistered upon death");
        helper.succeed();
    }
}
