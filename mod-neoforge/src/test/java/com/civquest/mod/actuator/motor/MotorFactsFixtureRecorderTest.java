package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.MotorFacts;
import com.civquest.mod.grpc.v1.Vec3;
import net.minecraft.core.BlockPos;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.shapes.Shapes;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyDouble;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

/**
 * cq-2sr.23 contract fixtures. Drives the REAL {@link AgentNavigator} through the REAL
 * {@link AgentMotorFSM} (Move motor) into stuck / enclosed / congestion, and records the FAILED
 * {@link IntentReceipt} the mod would send as protojson under
 * {@code orchestrator-go/test/integration/testdata/motorfacts/}. Go tests load these files and feed
 * them to Recover, so the Go simulators cannot silently drift from what Java really reports.
 *
 * <p>Default mode ASSERTS the freshly recorded receipt equals the committed fixture (drift in the
 * mod fails here). Regenerate with {@code CIVQUEST_RECORD_FIXTURES=1 ./gradlew :mod-neoforge:test
 * --tests '*MotorFactsFixtureRecorderTest'}.
 */
public class MotorFactsFixtureRecorderTest {

    private static final Path DIR = Path.of("..", "orchestrator-go", "test", "integration", "testdata", "motorfacts");

    @BeforeAll
    static void initMinecraft() {
        com.civquest.mod.test.MinecraftTestBootstrap.ensureBootstrapped();
    }

    // ---- fixtures ----

    @Test
    @DisplayName("Terrain wall: real navigator with no displacement fails the Move with terrain facts")
    void recordTerrainStuck() throws IOException {
        AgentEntity agent = mock(AgentEntity.class);
        AgentNavigator nav = new AgentNavigator();
        nav.setPosition(0.0, 64.0, 0.0);
        when(agent.getNavigator()).thenReturn(nav);
        AgentMotorFSM fsm = new AgentMotorFSM(agent);
        pushMove(fsm, 50.0, 64.0, 50.0);

        IntentReceipt failed = null;
        for (int i = 0; i < 80 && failed == null; i++) {
            fsm.tick();
            nav.setPosition(0.0, 64.0, 0.0); // pinned: walled in
            failed = settledFailure(fsm);
        }
        assertNotNull(failed, "Move must fail once the navigator reaches its stuck threshold");
        MotorFacts f = failed.getFacts();
        assertTrue(f.getStuck());
        assertFalse(f.getBlockedByEntity());
        assertFalse(f.getEnclosed());
        compareOrRecord("terrain_stuck.json", failed);
    }

    @Test
    @DisplayName("Enclosed 1x1x2 pit: real navigator reports enclosed and the Move fails with enclosed facts")
    void recordEnclosedPit() throws IOException {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        BlockBreaker breaker = new BlockBreaker(agent);
        when(agent.level()).thenReturn(level);
        when(agent.position()).thenReturn(new net.minecraft.world.phys.Vec3(0.5, 62.0, 0.5));
        when(agent.getX()).thenReturn(0.5);
        when(agent.getY()).thenReturn(62.0);
        when(agent.getZ()).thenReturn(0.5);
        when(agent.getEyeY()).thenReturn(63.62);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(1.0);
        when(agent.getDeltaMovement()).thenReturn(net.minecraft.world.phys.Vec3.ZERO);
        when(agent.onGround()).thenReturn(true);
        when(agent.getBreaker()).thenReturn(breaker);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(level.getMinBuildHeight()).thenReturn(-64);

        BlockState solid = mock(BlockState.class);
        when(solid.isAir()).thenReturn(false);
        when(solid.getDestroySpeed(any(), any())).thenReturn(0.5f);
        when(solid.getDestroyProgress(any(), any(), any())).thenReturn(0.25f);
        when(solid.getCollisionShape(any(), any())).thenReturn(Shapes.block());
        BlockState air = mock(BlockState.class);
        when(air.isAir()).thenReturn(true);
        when(air.getCollisionShape(any(), any())).thenReturn(Shapes.empty());
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getY() <= 61) return solid;
            if (p.getX() == 0 && p.getZ() == 0) return air;
            if (p.getY() <= 63) return solid;
            return air;
        });

        AgentNavigator nav = new AgentNavigator(agent);
        when(agent.getNavigator()).thenReturn(nav);
        AgentMotorFSM fsm = new AgentMotorFSM(agent);
        pushMove(fsm, 10.5, 64.0, 0.5);

        IntentReceipt failed = null;
        for (int i = 0; i < 40 && failed == null; i++) {
            fsm.tick();
            failed = settledFailure(fsm);
        }
        assertNotNull(failed, "an enclosed agent is reported stuck, so the Move must fail");
        assertTrue(failed.getFacts().getEnclosed(), "pit must be reported enclosed");
        assertFalse(failed.getFacts().getBlockedByEntity());
        compareOrRecord("enclosed_pit.json", failed);
    }

    @Test
    @DisplayName("Congestion: a peer's AABB sealing a 1-wide corridor fails the Move with blocked_by_entity")
    void recordCongestion() throws IOException {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        BlockState solid = mock(BlockState.class);
        when(solid.isAir()).thenReturn(false);
        when(solid.getCollisionShape(any(), any())).thenReturn(Shapes.block());
        BlockState air = mock(BlockState.class);
        when(air.isAir()).thenReturn(true);
        when(air.getCollisionShape(any(), any())).thenReturn(Shapes.empty());
        when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
            BlockPos p = inv.getArgument(0);
            if (p.getY() <= 63) return solid;
            if (p.getZ() != 0 && p.getY() >= 64 && p.getY() <= 66) return solid;
            return air;
        });

        net.minecraft.server.level.ServerPlayer blocker = mock(net.minecraft.server.level.ServerPlayer.class);
        when(blocker.isAlive()).thenReturn(true);
        when(blocker.isRemoved()).thenReturn(false);
        when(blocker.isSpectator()).thenReturn(false);
        AABB blockerBox = new AABB(0.85, 64.0, 0.2, 1.45, 65.8, 0.8);
        when(blocker.getBoundingBox()).thenReturn(blockerBox);
        when(level.getEntities(eq(agent), any(AABB.class), any())).thenAnswer(inv -> {
            AABB query = inv.getArgument(1);
            return blockerBox.intersects(query) ? List.of(blocker) : List.of();
        });

        AtomicReference<net.minecraft.world.phys.Vec3> posRef =
                new AtomicReference<>(new net.minecraft.world.phys.Vec3(0.5, 64.0, 0.5));
        AtomicReference<net.minecraft.world.phys.Vec3> deltaRef = new AtomicReference<>(net.minecraft.world.phys.Vec3.ZERO);
        when(agent.position()).thenAnswer(inv -> posRef.get());
        when(agent.getBoundingBox()).thenAnswer(inv -> {
            net.minecraft.world.phys.Vec3 p = posRef.get();
            return new AABB(p.x - 0.3, p.y, p.z - 0.3, p.x + 0.3, p.y + 1.8, p.z + 0.3);
        });
        when(agent.getDeltaMovement()).thenAnswer(inv -> deltaRef.get());
        when(agent.onGround()).thenReturn(true);
        when(agent.getYRot()).thenReturn(-90.0f);
        when(agent.getUUID()).thenReturn(java.util.UUID.randomUUID());
        doAnswer(inv -> {
            deltaRef.set(inv.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(net.minecraft.world.phys.Vec3.class));
        doAnswer(inv -> {
            net.minecraft.world.phys.Vec3 move = inv.getArgument(1);
            net.minecraft.world.phys.Vec3 cur = posRef.get();
            posRef.set(new net.minecraft.world.phys.Vec3(cur.x + move.x, cur.y, cur.z + move.z));
            return null;
        }).when(agent).move(any(), any(net.minecraft.world.phys.Vec3.class));

        AgentNavigator nav = new AgentNavigator(agent);
        when(agent.getNavigator()).thenReturn(nav);
        AgentMotorFSM fsm = new AgentMotorFSM(agent);
        pushMove(fsm, 4.5, 64.0, 0.5);

        IntentReceipt failed = null;
        for (int i = 0; i < 80 && failed == null; i++) {
            fsm.tick();
            failed = settledFailure(fsm);
        }
        assertNotNull(failed, "agent held by a peer must reach the stuck threshold");
        assertTrue(failed.getFacts().getStuck());
        assertTrue(failed.getFacts().getBlockedByEntity(), "entity AABB on the heading must set blocked_by_entity");
        compareOrRecord("congestion_peer.json", failed);
    }

    // ---- helpers ----

    private static void pushMove(AgentMotorFSM fsm, double x, double y, double z) {
        Motor move = new Move(Vec3.newBuilder().setX(x).setY(y).setZ(z).build(), 1f, false, true, false, 1);
        assertTrue(fsm.pushBatch(List.of(new Frame(7L, 70L, 0, move, Lease.none(0))), 1));
    }

    private static IntentReceipt settledFailure(AgentMotorFSM fsm) {
        IntentReceipt r = fsm.getCurrentReceipt();
        return r.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED ? r : null;
    }

    /** protojson-compatible (lowerCamelCase, 64-bit ints as strings, zero values omitted). */
    static String toProtoJson(IntentReceipt r) {
        MotorFacts f = r.getFacts();
        StringBuilder sb = new StringBuilder("{\n");
        sb.append("  \"batchId\": \"").append(r.getBatchId()).append("\",\n");
        sb.append("  \"status\": \"").append(r.getStatus().name()).append("\",\n");
        sb.append("  \"failureReason\": \"").append(r.getFailureReason()).append("\",\n");
        sb.append("  \"frameId\": \"").append(r.getFrameId()).append("\",\n");
        sb.append("  \"facts\": {\n");
        sb.append("    \"stuck\": ").append(f.getStuck()).append(",\n");
        sb.append("    \"enclosed\": ").append(f.getEnclosed()).append(",\n");
        sb.append("    \"blockedBearingDeg\": ").append(f.getBlockedBearingDeg()).append(",\n");
        sb.append("    \"stuckTicks\": ").append(f.getStuckTicks()).append(",\n");
        sb.append("    \"progress\": ").append(f.getProgress()).append(",\n");
        sb.append("    \"blockedByEntity\": ").append(f.getBlockedByEntity()).append("\n");
        sb.append("  }\n}\n");
        return sb.toString();
    }

    private static void compareOrRecord(String name, IntentReceipt receipt) throws IOException {
        String json = toProtoJson(receipt);
        Path file = DIR.resolve(name);
        if (System.getenv("CIVQUEST_RECORD_FIXTURES") != null) {
            Files.createDirectories(DIR);
            Files.writeString(file, json, StandardCharsets.UTF_8);
            return;
        }
        assertTrue(Files.exists(file), "missing fixture " + file + " (record with CIVQUEST_RECORD_FIXTURES=1)");
        assertEquals(Files.readString(file, StandardCharsets.UTF_8), json,
                "mod behaviour drifted from committed MotorFacts fixture " + name
                        + "; if intended, re-record with CIVQUEST_RECORD_FIXTURES=1 and re-run the Go contract tests");
    }
}
