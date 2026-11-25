package com.civquest.mod.periphery;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.LocalPathPlanner;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.actuator.motor.AgentWatchdogFSM;
import com.civquest.mod.actuator.motor.WatchdogState;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.HorizonSector;
import com.civquest.mod.grpc.v1.HorizonSurveyReport;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.SurveyHorizonIntent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.sensory.HorizonSurveyScanner;
import com.civquest.mod.test.MinecraftTestBootstrap;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.tags.FluidTags;
import net.minecraft.util.Mth;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.EntityType;
import net.minecraft.world.entity.MoverType;
import net.minecraft.world.entity.monster.Creeper;
import net.minecraft.world.entity.projectile.Arrow;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.ClipContext;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.material.FluidState;
import net.minecraft.world.level.material.Fluids;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.BlockHitResult;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.Shapes;
import net.minecraft.world.phys.shapes.VoxelShape;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyDouble;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.*;

/**
 * Multi-terrain and hazard periphery integration test suite exercising
 * {@link LocalPathPlanner}, {@link AgentNavigator}, {@link AgentMotorFSM},
 * {@link com.civquest.mod.actuator.IntentActuator}, {@link AgentWatchdogFSM},
 * and {@link HorizonSurveyScanner} end-to-end inside {@link PeripherySimulationHarness}.
 */
public class PeripheryTerrainHazardIntegrationTest {

    private static final String AGENT_ID = "agent-terrain-hazard";
    private static final String DISPLAY_NAME = "Pioneer";
    private static final double HALF_WIDTH = 0.3;
    private static final double HEIGHT = 1.8;

    private PeripherySimulationHarness harness;
    private ServerLevel mockLevel;
    private AgentEntity agent;
    private AgentNavigator navigator;
    private AgentWatchdogFSM watchdogFSM;

    private AtomicLong gameTime;
    private AtomicReference<Vec3> positionRef;
    private AtomicReference<Vec3> deltaMovementRef;
    private AtomicReference<Float> yawRef;
    private AtomicReference<Float> headYawRef;
    private AtomicReference<Float> pitchRef;
    private AtomicReference<HorizonSurveyReport> pendingSurvey;
    private int ceilingCollisionCount;
    private List<HorizonSurveyReport> completedSurveys;
    private List<String> observedMotorStates;
    private int observedStuckFailures;
    private List<WatchdogState> observedWatchdogStates;

    @BeforeAll
    static void initMinecraft() {
        MinecraftTestBootstrap.ensureBootstrapped();
    }

    @BeforeEach
    void setUp() {
        harness = new PeripherySimulationHarness();
        mockLevel = harness.getMockLevel();

        gameTime = new AtomicLong(2000L);
        positionRef = new AtomicReference<>(new Vec3(0.5, 64.0, 0.5));
        deltaMovementRef = new AtomicReference<>(Vec3.ZERO);
        yawRef = new AtomicReference<>(-90.0f); // Facing East (+X)
        headYawRef = new AtomicReference<>(-90.0f);
        pitchRef = new AtomicReference<>(0.0f);
        pendingSurvey = new AtomicReference<>();
        completedSurveys = new CopyOnWriteArrayList<>();
        observedMotorStates = new ArrayList<>();
        observedStuckFailures = 0;
        observedWatchdogStates = new ArrayList<>();

        doAnswer(invoc -> {
            Runnable r = invoc.getArgument(0);
            r.run();
            return null;
        }).when(harness.getMockServer()).execute(any(Runnable.class));
        doAnswer(invoc -> (int) gameTime.get()).when(harness.getMockServer()).getTickCount();
        when(mockLevel.getGameTime()).thenAnswer(invoc -> gameTime.get());
        when(mockLevel.getDayTime()).thenAnswer(invoc -> gameTime.get());

        // Authentic voxel fluid & collision queries backed by harness blockWorld
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
                        if (isSolidBlock(bp, st)) {
                            shapes.add(st.getCollisionShape(mockLevel, bp).move(x, y, z));
                        }
                    }
                }
            }
            return shapes;
        });

        // Authentic voxel raycast for HorizonSurveyScanner (respecting ClipContext.Block & ClipContext.Fluid.ANY)
        when(mockLevel.clip(any(ClipContext.class))).thenAnswer(invoc -> raycastVoxelWorld(invoc.getArgument(0)));

        agent = harness.createMockAgent(AGENT_ID, DISPLAY_NAME, new BlockPos(0, 64, 0));

        // Dynamic pose, AABB, physics, and fluid sensors
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

        when(agent.onGround()).thenAnswer(invoc -> {
            Vec3 p = positionRef.get();
            AABB footProbe = aabbAt(p).move(0.0, -0.05, 0.0);
            return collidesWithSolid(footProbe);
        });

        when(agent.isInWater()).thenAnswer(invoc -> {
            Vec3 p = positionRef.get();
            BlockPos feet = BlockPos.containing(p.x, p.y + 0.1, p.z);
            BlockPos below = BlockPos.containing(p.x, p.y - 0.05, p.z);
            return isWaterAt(feet) || isWaterAt(below);
        });

        when(agent.isEyeInFluid(FluidTags.WATER)).thenAnswer(invoc -> {
            Vec3 p = positionRef.get();
            BlockPos eye = BlockPos.containing(p.x, p.y + 1.62, p.z);
            return isWaterAt(eye);
        });

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
            Vec3 move = invoc.getArgument(1);
            performVoxelMove(move);
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

        doAnswer(invoc -> {
            float yawDeg = invoc.getArgument(0);
            yawRef.set(yawDeg);
            headYawRef.set(yawDeg);
            agent.yBodyRot = yawDeg;
            pitchRef.set(0.0f);
            return null;
        }).when(agent).faceBearingAndSync(anyFloat());

        when(agent.getAirSupply()).thenReturn(300);
        when(agent.getItemInHand(any(InteractionHand.class))).thenReturn(ItemStack.EMPTY);
        when(agent.getOffhandItem()).thenReturn(ItemStack.EMPTY);
        when(agent.getUseItem()).thenReturn(ItemStack.EMPTY);
        when(agent.isUsingItem()).thenReturn(false);

        doAnswer(invoc -> {
            HorizonSurveyReport report = invoc.getArgument(0);
            if (report != null) {
                pendingSurvey.set(report);
                completedSurveys.add(report);
            }
            return null;
        }).when(agent).queueHorizonSurveyReport(any(HorizonSurveyReport.class));
        when(agent.pollHorizonSurveyReport()).thenAnswer(invoc -> pendingSurvey.getAndSet(null));

        navigator = new AgentNavigator(agent);
        when(agent.getNavigator()).thenReturn(navigator);

        watchdogFSM = new AgentWatchdogFSM(agent);
        when(agent.getWatchdogFSM()).thenReturn(watchdogFSM);

        harness.connectAgentStream(AGENT_ID);
        harness.clearReceivedFrames();
    }

    private AABB aabbAt(Vec3 pos) {
        return new AABB(
                pos.x - HALF_WIDTH, pos.y, pos.z - HALF_WIDTH,
                pos.x + HALF_WIDTH, pos.y + HEIGHT, pos.z + HALF_WIDTH
        );
    }

    private boolean isSolidBlock(BlockPos pos, BlockState state) {
        if (state == null || state.isAir()) {
            return false;
        }
        VoxelShape shape = state.getCollisionShape(mockLevel, pos);
        return shape != null && !shape.isEmpty();
    }

    private boolean isWaterAt(BlockPos pos) {
        BlockState state = harness.getBlock(pos);
        if (state == null) {
            return false;
        }
        FluidState fluid = state.getFluidState();
        return state.is(Blocks.WATER) || (fluid != null && fluid.is(FluidTags.WATER));
    }

    private boolean isLavaAt(BlockPos pos) {
        BlockState state = harness.getBlock(pos);
        if (state == null) {
            return false;
        }
        FluidState fluid = state.getFluidState();
        return state.is(Blocks.LAVA) || (fluid != null && fluid.is(FluidTags.LAVA));
    }

    private boolean collidesWithSolid(AABB box) {
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
                    if (isSolidBlock(bp, st)) {
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

    /**
     * Authentic axis-separated voxel AABB collision move simulation (no free teleport step-up;
     * 1-block step-ups must actually clear via jump impulse or swimming buoyancy).
     */
    private void performVoxelMove(Vec3 move) {
        Vec3 cur = positionRef.get();
        double x = cur.x;
        double y = cur.y;
        double z = cur.z;

        // 1. Resolve Y axis first
        double dy = move.y;
        if (Math.abs(dy) > 1e-9) {
            int steps = Math.max(1, (int) Math.ceil(Math.abs(dy) / 0.05));
            double stepY = dy / steps;
            for (int i = 0; i < steps; i++) {
                double candY = y + stepY;
                if (collidesWithSolid(aabbAt(new Vec3(x, candY, z)))) {
                    if (stepY < 0) {
                        y = Math.floor(y + 1e-6);
                    } else {
                        // Ceiling contact: clamp below solid block and kill upward velocity
                        y = Math.floor(candY + HEIGHT) - HEIGHT;
                        ceilingCollisionCount++;
                    }
                    Vec3 dm = deltaMovementRef.get();
                    deltaMovementRef.set(new Vec3(dm.x, 0.0, dm.z));
                    break;
                }
                y = candY;
            }
        }

        // 2. Resolve X axis
        boolean horizCollided = false;
        double dx = move.x;
        if (Math.abs(dx) > 1e-9) {
            int steps = Math.max(1, (int) Math.ceil(Math.abs(dx) / 0.05));
            double stepX = dx / steps;
            for (int i = 0; i < steps; i++) {
                double candX = x + stepX;
                if (collidesWithSolid(aabbAt(new Vec3(candX, y, z)))) {
                    horizCollided = true;
                    break;
                }
                x = candX;
            }
        }

        // 3. Resolve Z axis
        double dz = move.z;
        if (Math.abs(dz) > 1e-9) {
            int steps = Math.max(1, (int) Math.ceil(Math.abs(dz) / 0.05));
            double stepZ = dz / steps;
            for (int i = 0; i < steps; i++) {
                double candZ = z + stepZ;
                if (collidesWithSolid(aabbAt(new Vec3(x, y, candZ)))) {
                    horizCollided = true;
                    break;
                }
                z = candZ;
            }
        }

        agent.horizontalCollision = horizCollided;
        positionRef.set(new Vec3(x, y, z));
    }

    /**
     * Voxel raycaster for ClipContext over harness.blockWorld, honoring ClipContext.Fluid.ANY.
     */
    private BlockHitResult raycastVoxelWorld(ClipContext ctx) {
        Vec3 from = ctx.getFrom();
        Vec3 to = ctx.getTo();
        Vec3 delta = to.subtract(from);
        double len = delta.length();
        if (len < 1e-6) {
            return BlockHitResult.miss(to, Direction.UP, BlockPos.containing(to));
        }

        int steps = Math.max(1, (int) Math.ceil(len / 0.1));
        Vec3 prev = from;
        for (int i = 1; i <= steps; i++) {
            double t = (double) i / steps;
            Vec3 sample = from.add(delta.scale(t));
            BlockPos pos = BlockPos.containing(sample);
            BlockState state = harness.getBlock(pos);
            if (state != null && !state.isAir()) {
                VoxelShape blockShape = ctx.getBlockShape(state, mockLevel, pos);
                VoxelShape fluidShape = ctx.getFluidShape(state.getFluidState(), mockLevel, pos);
                if (!blockShape.isEmpty()) {
                    BlockHitResult hit = blockShape.clip(prev, sample, pos);
                    if (hit != null) {
                        return hit;
                    }
                    return new BlockHitResult(sample, Direction.getNearest(from.x - sample.x, from.y - sample.y, from.z - sample.z), pos, false);
                }
                if (!fluidShape.isEmpty()) {
                    AABB fluidBox = new AABB(
                            pos.getX(), pos.getY(), pos.getZ(),
                            pos.getX() + 1.0, pos.getY() + 0.9, pos.getZ() + 1.0
                    );
                    if (fluidBox.contains(sample) || fluidBox.clip(prev, sample).isPresent()) {
                        Vec3 hitVec = fluidBox.clip(prev, sample).orElse(sample);
                        return new BlockHitResult(hitVec, Direction.UP, pos, false);
                    }
                }
            }
            prev = sample;
        }
        return BlockHitResult.miss(to, Direction.UP, BlockPos.containing(to));
    }

    private void buildFlatGround(int floorY, int minX, int maxX, int minZ, int maxZ) {
        for (int x = minX; x <= maxX; x++) {
            for (int z = minZ; z <= maxZ; z++) {
                for (int y = floorY - 2; y <= floorY; y++) {
                    harness.setBlock(new BlockPos(x, y, z),
                            y == floorY ? Blocks.GRASS_BLOCK.defaultBlockState() : Blocks.STONE.defaultBlockState());
                }
            }
        }
    }

    private void setBox(int minX, int maxX, int minY, int maxY, int minZ, int maxZ, BlockState state) {
        for (int x = minX; x <= maxX; x++) {
            for (int y = minY; y <= maxY; y++) {
                for (int z = minZ; z <= maxZ; z++) {
                    harness.setBlock(new BlockPos(x, y, z), state);
                }
            }
        }
    }

    private void tickServer(int ticks) {
        for (int i = 0; i < ticks; i++) {
            agent.tickCount++;
            gameTime.incrementAndGet();
            watchdogFSM.tick();
            observedWatchdogStates.add(watchdogFSM.getState());
            agent.tick();
            observedMotorStates.add(agent.getMotorFSM().topMotor().map(m -> m.getClass().getSimpleName()).orElse("Idle"));
            com.civquest.mod.grpc.v1.IntentReceipt stuckReceipt = agent.getMotorFSM().getCurrentReceipt();
            if (stuckReceipt.getStatus() == com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED
                    && "Navigation stuck".equals(stuckReceipt.getFailureReason())) {
                observedStuckFailures++;
            }
            harness.getEntityManager().tickTelemetry(agent.tickCount);
            clearInvocations(mockLevel, agent);
        }
    }

    private LocalPathPlanner.World plannerWorldView() {
        return new LocalPathPlanner.World() {
            @Override
            public boolean solid(int x, int y, int z) {
                BlockPos p = new BlockPos(x, y, z);
                return isSolidBlock(p, harness.getBlock(p));
            }

            @Override
            public boolean hazard(int x, int y, int z) {
                return isLavaAt(new BlockPos(x, y, z));
            }

            @Override
            public boolean water(int x, int y, int z) {
                return isWaterAt(new BlockPos(x, y, z));
            }
        };
    }

    private void sendNavigate(long seqId, double x, double y, double z, int tolerance) {
        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(seqId)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(seqId)
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(com.civquest.mod.grpc.v1.Vec3.newBuilder()
                                        .setX(x).setY(y).setZ(z).build())
                                .setArrivalTolerance(tolerance)
                                .build())
                        .build())
                .build();
        harness.sendIntentBatch(batch);
    }

    private void sendSurvey(long seqId, int numSectors, float maxRange, int ticksPerSector) {
        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(seqId)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(seqId)
                        .setSurveyHorizon(SurveyHorizonIntent.newBuilder()
                                .setNumSectors(numSectors)
                                .setMaxRange(maxRange)
                                .setTicksPerSector(ticksPerSector)
                                .build())
                        .build())
                .build();
        harness.sendIntentBatch(batch);
    }

    private static boolean isTerminal(IntentReceipt receipt) {
        IntentReceipt.ReceiptStatus st = receipt.getStatus();
        return st == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED
                || st == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED
                || st == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_ABORTED;
    }

    // -----------------------------------------------------------------------------------------
    // Test 1: Surface Lava Pool Hazard
    // -----------------------------------------------------------------------------------------
    @Test
    @DisplayName("1. Surface 3x3 lava pool on straight line triggers clean detour to (10, 64, 0) with zero lava contacts")
    void testLavaPoolAvoidanceAndDetour() {
        buildFlatGround(63, -15, 20, -15, 15);
        // 3x3 surface lava pool directly on line from (0,64,0) to (10,64,0) at x=4..6, z=-1..1
        setBox(4, 6, 62, 62, -1, 1, Blocks.STONE.defaultBlockState());
        setBox(4, 6, 63, 63, -1, 1, Blocks.LAVA.defaultBlockState());

        sendNavigate(1L, 10.5, 64.0, 0.5, 1);

        int lavaContacts = 0;
        boolean detouredOutsideZCorridor = false;
        IntentReceipt receipt = harness.getIntentActuator().getCurrentReceipt(agent);
        for (int t = 0; t < 250; t++) {
            tickServer(1);
            Vec3 pos = positionRef.get();
            BlockPos feet = BlockPos.containing(pos.x, pos.y + 0.05, pos.z);
            BlockPos below = BlockPos.containing(pos.x, pos.y - 0.1, pos.z);
            if (isLavaAt(feet) || isLavaAt(below)) {
                lavaContacts++;
            }
            if (pos.x >= 3.5 && pos.x <= 6.5 && Math.abs(pos.z - 0.5) >= 1.5) {
                detouredOutsideZCorridor = true;
            }
            receipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (isTerminal(receipt)) {
                break;
            }
        }

        assertEquals(0, lavaContacts, "Agent must NEVER step into or onto any lava cell");
        assertTrue(detouredOutsideZCorridor, "Agent must detour around the 3x3 lava pool at x=4..6, z=-1..1");
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, receipt.getStatus(),
                "Navigation around lava pool must complete cleanly; pos=" + positionRef.get());
        assertTrue(positionRef.get().distanceTo(new Vec3(10.5, 64.0, 0.5)) <= 1.5,
                "Agent must arrive near (10.5, 64.0, 0.5); actual=" + positionRef.get());
    }

    // -----------------------------------------------------------------------------------------
    // Test 2: River / Water Crossing
    // -----------------------------------------------------------------------------------------
    @Test
    @DisplayName("2. 3-wide, 2-deep river channel is detected as WATER by HorizonSurveyScanner and crossed without a stuck failure")
    void testRiverWaterCrossingWithoutStalling() {
        buildFlatGround(63, -15, 20, -20, 20);
        // 3-block-wide river channel at x=4..6, 2 blocks deep (y=62..63 water, riverbed y=61), banks at y=64
        setBox(4, 6, 61, 61, -20, 20, Blocks.SAND.defaultBlockState());
        setBox(4, 6, 62, 63, -20, 20, Blocks.WATER.defaultBlockState());

        // 1. HorizonSurveyScanner must detect WATER via ClipContext.Fluid.ANY along East bearing (270 deg)
        HorizonSector eastSector = HorizonSurveyScanner.scanSector(
                mockLevel, agent, new Vec3(0.5, 65.62, 0.5), 27, 270.0f, 64.0f);
        assertEquals(HorizonSurveyScanner.FEATURE_WATER, eastSector.getFeatureType(),
                "HorizonSurveyScanner must classify the river bearing as WATER");
        assertTrue(eastSector.getHitBlockId().contains("water"),
                "Water hit block id must identify water; got=" + eastSector.getHitBlockId());

        // 2. Navigate across the 2-block-deep river from (0,64,0) to (10,64,0)
        sendNavigate(2L, 10.5, 64.0, 0.5, 1);

        boolean enteredWater = false;
        IntentReceipt receipt = harness.getIntentActuator().getCurrentReceipt(agent);
        for (int t = 0; t < 300; t++) {
            tickServer(1);
            if (agent.isInWater()) {
                enteredWater = true;
            }
            receipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (isTerminal(receipt)) {
                break;
            }
        }

        assertTrue(enteredWater, "Agent must enter and swim/wade across the river channel");
        assertEquals(0, observedStuckFailures,
                "Crossing 2-block-deep river and stepping onto bank must NOT trigger a stuck failure (ADR-037 facts); states="
                        + observedMotorStates);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, receipt.getStatus(),
                "Navigation across river must complete; status=" + receipt.getStatus()
                        + " reason=" + receipt.getFailureReason() + " pos=" + positionRef.get());
        assertTrue(positionRef.get().x >= 9.0 && positionRef.get().y >= 63.9,
                "Agent must climb out onto the far bank at y=64; actual=" + positionRef.get());
    }

    // -----------------------------------------------------------------------------------------
    // Test 3: Aggro Mob Cluster Ambush Mid-Survey
    // -----------------------------------------------------------------------------------------
    @Test
    @DisplayName("3. Hostile ambush mid-72-tick survey preempts the Sweep with a Dodge reflex frame without Watchdog disconnect, then fresh survey completes")
    void testAggroMobClusterMidSurveyPreemptsAndResumesCleanly() {
        buildFlatGround(63, -15, 15, -15, 15);

        sendSurvey(10L, 36, 64.0f, 2);
        assertTrue(agent.getMotorFSM().isSweeping());

        // Run first 20 ticks of the 72-tick survey
        tickServer(20);
        assertTrue(agent.getMotorFSM().isSweeping());
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        // Tick 20: Inject hostile mob cluster ambush (hissing Creeper + incoming projectile + damage event)
        Creeper creeper = mock(Creeper.class);
        when(creeper.getId()).thenReturn(201);
        when(creeper.getUUID()).thenReturn(UUID.randomUUID());
        when(creeper.isAlive()).thenReturn(true);
        when(creeper.isRemoved()).thenReturn(false);
        when(creeper.getX()).thenReturn(3.0);
        when(creeper.getY()).thenReturn(64.0);
        when(creeper.getZ()).thenReturn(0.5);
        when(creeper.getEyePosition()).thenReturn(new Vec3(3.0, 65.5, 0.5));
        when(creeper.getBoundingBox()).thenReturn(new AABB(2.7, 64.0, 0.2, 3.3, 65.7, 0.8));
        when(creeper.getName()).thenReturn(net.minecraft.network.chat.Component.literal("Creeper"));
        doReturn(EntityType.CREEPER).when(creeper).getType();
        when(creeper.isIgnited()).thenReturn(true);
        harness.spawnEntity(creeper);

        Arrow arrow = mock(Arrow.class);
        when(arrow.isAlive()).thenReturn(true);
        when(arrow.getOwner()).thenReturn(creeper);
        when(arrow.position()).thenReturn(new Vec3(3.5, 64.9, 0.5));
        when(arrow.getDeltaMovement()).thenReturn(new Vec3(-1.2, 0.0, 0.0)); // Heading straight toward agent at (0.5, 64.9, 0.5)
        when(arrow.getBoundingBox()).thenReturn(new AABB(3.3, 64.7, 0.3, 3.7, 65.1, 0.7));
        harness.spawnEntity(arrow);

        DamageSource ambushDamage = mock(DamageSource.class);
        when(ambushDamage.getEntity()).thenReturn(creeper);
        harness.simulateDamage(agent, ambushDamage, 4.0f);

        // Next tick detects incoming projectile trajectory via checkIncomingProjectileHazard -> preemptWithDodgeReflex
        tickServer(1);

        assertTrue(agent.getMotorFSM().activeReflex().isPresent(),
                "Ambush mid-survey must immediately preempt the Sweep with a reflex frame");
        assertTrue(agent.getMotorFSM().hasSweepFrame(), "Sweep stays suspended beneath the reflex");
        assertEquals(1, agent.getMotorFSM().getStackDepth(),
                "reflex frame must be pushed over the Sweep");
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX,
                harness.getIntentActuator().getCurrentReceipt(agent).getStatus(),
                "Receipt must report PREEMPTED_BY_REFLEX while a reflex frame holds the stack");

        // Clear threats and let combat disengage cleanly (COMBAT_TIMEOUT_TICKS = 60)
        harness.removeEntity(arrow);
        harness.removeEntity(creeper);
        tickServer(AgentMotorFSM.COMBAT_TIMEOUT_TICKS + 2);

        // Reflex must have popped back to the Sweep (or already completed remaining sectors); finish it cleanly
        for (int t = 0; t < 80 && agent.getMotorFSM().isSweeping(); t++) {
            tickServer(1);
        }

        assertTrue(agent.getMotorFSM().isMotorIdle(),
                "MotorState must return to IDLE after popped survey finishes");
        assertEquals(0, agent.getMotorFSM().getStackDepth());
        assertFalse(observedWatchdogStates.contains(WatchdogState.DISCONNECTED_SAFE_MODE),
                "AgentWatchdogFSM must NEVER falsely enter DISCONNECTED_SAFE_MODE during ambush preemption; observed="
                        + observedWatchdogStates);

        // Issue a fresh 72-tick survey and verify no corrupted survey state remains
        completedSurveys.clear();
        harness.clearReceivedFrames();
        sendSurvey(11L, 36, 64.0f, 2);
        assertTrue(agent.getMotorFSM().isSweeping());
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        tickServer(80);

        assertTrue(agent.getMotorFSM().isMotorIdle(),
                "Fresh post-combat survey must return cleanly to IDLE");
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED,
                harness.getIntentActuator().getCurrentReceipt(agent).getStatus());
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());
        assertEquals(1, completedSurveys.size(), "Fresh survey must emit exactly 1 clean HorizonSurveyReport");
        assertEquals(36, completedSurveys.get(0).getSectorsCount(), "Fresh survey must contain all 36 sectors");
    }

    // -----------------------------------------------------------------------------------------
    // Test 4: Steep Alpine / Mountain Pass
    // -----------------------------------------------------------------------------------------
    @Test
    @DisplayName("4. Sheer stone ridge at x=5 with stepped pass at z=3 (64->65->66->67->66->65->64) is detected and traversed without in-place hopping")
    void testMountainRidgePassAndStepUp() {
        buildFlatGround(63, -15, 20, -15, 15);
        // Sheer stone ridge blocking direct path at x=5 (y=64..74 across z=-15..15, except z=3)
        for (int z = -15; z <= 15; z++) {
            if (z != 3) {
                setBox(5, 5, 64, 74, z, z, Blocks.STONE.defaultBlockState());
            }
        }
        // Stepped 1-block-at-a-time mountain pass at z=3:
        // x=2: y=64 (floor 63), x=3: y=65 (floor 64), x=4: y=66 (floor 65),
        // x=5: y=67 (floor 66), x=6: y=66 (floor 65), x=7: y=65 (floor 64), x=8: y=64 (floor 63)
        setBox(3, 3, 64, 64, 3, 3, Blocks.STONE.defaultBlockState());
        setBox(4, 4, 64, 65, 3, 3, Blocks.STONE.defaultBlockState());
        setBox(5, 5, 64, 66, 3, 3, Blocks.STONE.defaultBlockState());
        setBox(6, 6, 64, 65, 3, 3, Blocks.STONE.defaultBlockState());
        setBox(7, 7, 64, 64, 3, 3, Blocks.STONE.defaultBlockState());

        // 1. HorizonSurveyScanner: sheer wall bearing is RIDGE_OBSTRUCTION (occluded=true), pass bearing is non-occluded
        HorizonSector wallSector = HorizonSurveyScanner.scanSector(
                mockLevel, agent, new Vec3(0.5, 65.62, 0.5), 27, 270.0f, 64.0f);
        assertEquals(HorizonSurveyScanner.FEATURE_RIDGE_OBSTRUCTION, wallSector.getFeatureType(),
                "Sheer stone ridge bearing must classify as RIDGE_OBSTRUCTION");
        assertTrue(wallSector.getOccluded(), "Sheer stone ridge must be occluded=true");

        HorizonSector passSector = HorizonSurveyScanner.scanSector(
                mockLevel, agent, new Vec3(3.5, 67.62, 3.5), 27, 270.0f, 64.0f);
        assertFalse(passSector.getOccluded(),
                "Open mountain pass bearing must be non-occluded; got feature=" + passSector.getFeatureType());

        // 2. Navigate from (0,64,0) across the ridge to (10,64,0)
        sendNavigate(3L, 10.5, 64.0, 0.5, 1);

        int zeroVelHops = 0;
        double maxYReached = 64.0;
        IntentReceipt receipt = harness.getIntentActuator().getCurrentReceipt(agent);
        for (int t = 0; t < 350; t++) {
            Vec3 before = positionRef.get();
            tickServer(1);
            Vec3 after = positionRef.get();
            Vec3 dm = deltaMovementRef.get();
            maxYReached = Math.max(maxYReached, after.y);
            if (dm.y > 0.40 && Math.hypot(after.x - before.x, after.z - before.z) < 0.005) {
                zeroVelHops++;
            }
            receipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (isTerminal(receipt)) {
                break;
            }
        }

        assertTrue(maxYReached >= 66.8,
                "Agent must climb over the y=67 mountain pass crest; maxY=" + maxYReached);
        assertEquals(0, zeroVelHops,
                "Agent must not perform zero-velocity in-place hops during ridge pass traversal");
        assertEquals(0, observedStuckFailures,
                "Mountain pass traversal must not stall into a stuck failure (ADR-037 facts); states=" + observedMotorStates);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, receipt.getStatus(),
                "Agent must reach far side of ridge; status=" + receipt.getStatus()
                        + " reason=" + receipt.getFailureReason() + " pos=" + positionRef.get());
        assertTrue(positionRef.get().distanceTo(new Vec3(10.5, 64.0, 0.5)) <= 1.5,
                "Agent must arrive near (10.5, 64.0, 0.5); actual=" + positionRef.get());
    }

    // -----------------------------------------------------------------------------------------
    // Test 5: Cave / Underground Chamber Navigation & Exit
    // -----------------------------------------------------------------------------------------
    @Test
    @DisplayName("5. Underground cave chamber: sealed alcove detects enclosed=true without ceiling hopping; stepped tunnel exits cleanly")
    void testCaveChamberNavigationAndSlopedTunnelExit() {
        // Solid stone massif encapsulating cave & surface plateau above (floor y=43 -> stand y=44 at x>=8)
        setBox(-8, 15, 38, 46, -8, 8, Blocks.STONE.defaultBlockState());
        // Clear open sky above surface y=44+
        setBox(-8, 15, 44, 52, -8, 8, Blocks.AIR.defaultBlockState());
        // Keep underground roof solid above the chamber and alcove up to y=43 (2-block ceiling at y=42)
        setBox(-6, 6, 42, 43, -6, 6, Blocks.STONE.defaultBlockState());

        // Sealed dead-end alcove: x in [-4..-3], y in [40..41], z in [-1..1] (2-block ceiling y=42 solid)
        setBox(-4, -3, 40, 41, -1, 1, Blocks.AIR.defaultBlockState());

        // Main cave chamber: x in [0..3], y in [40..41], z in [-2..2] (2-block ceiling y=42 solid)
        setBox(0, 3, 40, 41, -2, 2, Blocks.AIR.defaultBlockState());

        // 1-block-stepped upward tunnel from chamber (x=3, y=40) to surface (x=7, y=44) along z=0
        // Each step has 2-block stair headroom (open y_step..y_step+2 so agent hitbox clears the ceiling lip)
        setBox(3, 3, 40, 42, 0, 0, Blocks.AIR.defaultBlockState());
        setBox(4, 4, 41, 43, 0, 0, Blocks.AIR.defaultBlockState());
        setBox(5, 5, 42, 44, 0, 0, Blocks.AIR.defaultBlockState());
        setBox(6, 6, 43, 45, 0, 0, Blocks.AIR.defaultBlockState());

        // --- Part A: Sealed dead-end alcove ---
        LocalPathPlanner.Result alcovePlan = LocalPathPlanner.plan(
                plannerWorldView(),
                new LocalPathPlanner.Cell(-3, 40, 0),
                10.5, 44.0, 0.5,
                LocalPathPlanner.DEFAULT_RADIUS,
                null
        );
        assertTrue(alcovePlan.enclosed(), "Completely sealed dead-end alcove must report enclosed=true");
        assertFalse(alcovePlan.hasPath(), "Sealed alcove must not emit a path through solid walls");

        // Place agent inside sealed alcove and verify AgentNavigator detects enclosed=true and does NOT hop into the 2-block stone ceiling
        positionRef.set(new Vec3(-3.5, 40.0, 0.5));
        sendNavigate(4L, 10.5, 44.0, 0.5, 1);

        boolean sawNavigatorEnclosed = false;
        int ceilingHops = 0;
        for (int t = 0; t < 40; t++) {
            tickServer(1);
            if (navigator.isEnclosed()) {
                sawNavigatorEnclosed = true;
            }
            // ADR-037: the enclosed Move now ends FAILED at once and reports enclosed as a receipt fact
            IntentReceipt factsReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (factsReceipt.getFacts().getEnclosed()) {
                sawNavigatorEnclosed = true;
                assertFalse(factsReceipt.getFacts().getBlockedByEntity(), "sealed alcove is terrain");
            }
            if (deltaMovementRef.get().y > 0.35 || positionRef.get().y > 40.2) {
                ceilingHops++;
            }
        }
        assertTrue(sawNavigatorEnclosed, "AgentNavigator must detect enclosed=true inside sealed alcove");
        assertEquals(0, ceilingHops, "Agent must NEVER hop into the 2-block stone ceiling inside sealed alcove");

        // --- Part B: Cave chamber exit up stepped tunnel ---
        harness.getIntentActuator().flush(agent);
        observedMotorStates.clear();
        observedStuckFailures = 0;
        ceilingCollisionCount = 0;
        positionRef.set(new Vec3(1.5, 40.0, 0.5));
        deltaMovementRef.set(Vec3.ZERO);

        sendNavigate(5L, 9.5, 44.0, 0.5, 1);

        int totalJumps = 0;
        int sameCellRepeatedJumps = 0;
        BlockPos lastJumpCell = null;
        IntentReceipt exitReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
        for (int t = 0; t < 350; t++) {
            tickServer(1);
            if (deltaMovementRef.get().y > 0.40) {
                totalJumps++;
                BlockPos jumpCell = BlockPos.containing(positionRef.get());
                if (jumpCell.equals(lastJumpCell)) {
                    sameCellRepeatedJumps++;
                }
                lastJumpCell = jumpCell;
            }
            exitReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (isTerminal(exitReceipt)) {
                break;
            }
        }

        assertTrue(ceilingCollisionCount <= 4,
                "Stepped cave exit must not repeatedly bonk head into stone ceiling; count=" + ceilingCollisionCount);
        assertEquals(0, sameCellRepeatedJumps,
                "Stepped cave exit must not trigger repeated jump loops in the same cell");
        assertTrue(totalJumps <= 6,
                "4-step cave exit must require only ~1 jump per step (<=6 total); got " + totalJumps);
        assertEquals(0, observedStuckFailures,
                "Cave tunnel exit must complete without a stuck failure (ADR-037 facts); states=" + observedMotorStates);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, exitReceipt.getStatus(),
                "Agent must exit cave chamber up stepped tunnel to (9.5, 44.0, 0.5); status=" + exitReceipt.getStatus()
                        + " reason=" + exitReceipt.getFailureReason() + " pos=" + positionRef.get());
        assertTrue(positionRef.get().y >= 43.9 && positionRef.get().x >= 8.0,
                "Agent must stand on surface outside cave at y=44; actual=" + positionRef.get());
    }
}
