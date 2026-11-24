package com.civquest.mod.periphery;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.motor.AgentMotorFSM;
import com.civquest.mod.actuator.motor.AgentWatchdogFSM;
import com.civquest.mod.actuator.motor.MotorState;
import com.civquest.mod.actuator.motor.WatchdogState;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.HorizonSurveyReport;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.SurveyHorizonIntent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.test.MinecraftTestBootstrap;
import net.minecraft.core.BlockPos;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.tags.BlockTags;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.MoverType;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BedPart;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyFloat;
import static org.mockito.Mockito.*;

/**
 * Closed-loop Periphery integration tests exercising {@link PeripherySimulationHarness},
 * {@link com.civquest.mod.actuator.IntentActuator}, {@link AgentMotorFSM},
 * {@link AgentWatchdogFSM}, {@link AgentNavigator}, and {@link com.civquest.mod.sensory.SensoryFrustumCollector}.
 */
public class PeripheryHorizonSurveyIntegrationTest {

    private static final String AGENT_ID = "agent-explorer-periphery";
    private static final String DISPLAY_NAME = "Dave";

    private PeripherySimulationHarness harness;
    private ServerLevel mockLevel;
    private AgentEntity agent;
    private AgentWatchdogFSM watchdogFSM;
    private AgentNavigator navigator;

    private AtomicLong gameTime;
    private AtomicBoolean sleeping;
    private AtomicReference<Vec3> positionRef;
    private AtomicReference<Vec3> deltaMovementRef;
    private AtomicReference<Float> yawRef;
    private AtomicReference<Float> headYawRef;
    private AtomicReference<Float> pitchRef;
    private AtomicReference<HorizonSurveyReport> pendingSurvey;
    private List<HorizonSurveyReport> queuedSurveyReports;
    private List<WatchdogState> observedWatchdogStates;

    @BeforeAll
    static void initMinecraft() {
        MinecraftTestBootstrap.ensureBootstrapped();
    }

    @BeforeEach
    void setUp() {
        harness = new PeripherySimulationHarness();
        mockLevel = harness.getMockLevel();

        gameTime = new AtomicLong(1000L);
        sleeping = new AtomicBoolean(false);
        positionRef = new AtomicReference<>(new Vec3(0.5, 86.0, 0.5));
        deltaMovementRef = new AtomicReference<>(Vec3.ZERO);
        yawRef = new AtomicReference<>(0.0f);
        headYawRef = new AtomicReference<>(0.0f);
        pitchRef = new AtomicReference<>(0.0f);
        pendingSurvey = new AtomicReference<>();
        queuedSurveyReports = new CopyOnWriteArrayList<>();
        observedWatchdogStates = new ArrayList<>();

        // PeripherySimulationHarness stubs server.submit(...), while CivQuestBridgeServiceImpl
        // dispatches WatchdogFSM callbacks via server.execute(...).
        doAnswer(invoc -> {
            Runnable r = invoc.getArgument(0);
            r.run();
            return null;
        }).when(harness.getMockServer()).execute(any(Runnable.class));
        doAnswer(invoc -> (int) gameTime.get()).when(harness.getMockServer()).getTickCount();
        when(mockLevel.getGameTime()).thenAnswer(invoc -> gameTime.get());
        when(mockLevel.getDayTime()).thenAnswer(invoc -> gameTime.get());

        agent = harness.createMockAgent(AGENT_ID, DISPLAY_NAME, new BlockPos(0, 86, 0));

        // Wire dynamic pose/physics/sleep/survey hooks onto mock AgentEntity
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
        when(agent.getBoundingBox()).thenAnswer(invoc -> {
            Vec3 p = positionRef.get();
            return new AABB(p.x - 0.3, p.y, p.z - 0.3, p.x + 0.3, p.y + 1.8, p.z + 0.3);
        });
        when(agent.onGround()).thenAnswer(invoc -> {
            Vec3 p = positionRef.get();
            BlockPos below = BlockPos.containing(p.x, p.y - 0.1, p.z);
            BlockState belowState = harness.getBlock(below);
            return !belowState.isAir() && !belowState.getCollisionShape(mockLevel, below).isEmpty();
        });
        when(agent.getDeltaMovement()).thenAnswer(invoc -> deltaMovementRef.get());
        doAnswer(invoc -> {
            deltaMovementRef.set(invoc.getArgument(0));
            return null;
        }).when(agent).setDeltaMovement(any(Vec3.class));
        doAnswer(invoc -> {
            double x = invoc.getArgument(0);
            double y = invoc.getArgument(1);
            double z = invoc.getArgument(2);
            deltaMovementRef.set(new Vec3(x, y, z));
            return null;
        }).when(agent).setDeltaMovement(anyDouble(), anyDouble(), anyDouble());
        doAnswer(invoc -> {
            double x = invoc.getArgument(0);
            double y = invoc.getArgument(1);
            double z = invoc.getArgument(2);
            positionRef.set(new Vec3(x, y, z));
            return null;
        }).when(agent).setPos(anyDouble(), anyDouble(), anyDouble());
        doAnswer(invoc -> {
            Vec3 move = invoc.getArgument(1);
            Vec3 cur = positionRef.get();
            Vec3 next = cur.add(move);
            // Clamp against solid ground floor so gravity/fall doesn't sink through solid blocks
            BlockPos feet = BlockPos.containing(next.x, next.y, next.z);
            BlockState feetState = harness.getBlock(feet);
            if (!feetState.isAir() && !feetState.getCollisionShape(mockLevel, feet).isEmpty() && move.y < 0) {
                next = new Vec3(next.x, feet.getY() + 1.0, next.z);
            }
            positionRef.set(next);
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

        when(agent.isSleeping()).thenAnswer(invoc -> sleeping.get());
        doAnswer(invoc -> {
            sleeping.set(true);
            return null;
        }).when(agent).startSleeping(any(BlockPos.class));
        doAnswer(invoc -> {
            sleeping.set(false);
            return null;
        }).when(agent).stopSleeping();

        doAnswer(invoc -> {
            HorizonSurveyReport report = invoc.getArgument(0);
            if (report != null) {
                pendingSurvey.set(report);
                queuedSurveyReports.add(report);
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

    private void buildFlatGround(int y, int radius) {
        for (int x = -radius; x <= radius; x++) {
            for (int z = -radius; z <= radius; z++) {
                harness.setBlock(new BlockPos(x, y, z), Blocks.GRASS_BLOCK.defaultBlockState());
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
            harness.getEntityManager().tickTelemetry(agent.tickCount);
            // Prevent Mockito InvocationContainer from accumulating hundreds of thousands of
            // getBlockState() call records across long multi-second tick loops.
            clearInvocations(mockLevel, agent);
        }
    }

    @Test
    @DisplayName("Full 72-tick 360-degree horizon survey does not trigger WatchdogFSM STREAM_DEGRADED or DISCONNECTED_SAFE_MODE")
    void testFull72TickHorizonSurveyDoesNotTriggerWatchdogDegradedOrSafeMode() {
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState(),
                "Agent must start in CONNECTED_ACTIVE after stream handshake");

        IntentBatch surveyBatch = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(1L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(1L)
                        .setSurveyHorizon(SurveyHorizonIntent.newBuilder()
                                .setNumSectors(36)
                                .setMaxRange(64.0f)
                                .setTicksPerSector(2)
                                .build())
                        .build())
                .build();

        harness.sendIntentBatch(surveyBatch);
        assertEquals(MotorState.SURVEYING, agent.getMotorFSM().getState(),
                "AgentMotorFSM must enter SURVEYING upon SurveyHorizonIntent");

        // Tick 80 ticks (4.0 seconds) with NO additional IntentBatches from Go while survey runs
        tickServer(80);

        assertEquals(MotorState.IDLE, agent.getMotorFSM().getState(),
                "AgentMotorFSM must return to IDLE after completing 72-tick horizon survey");
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED,
                harness.getIntentActuator().getCurrentReceipt(agent).getStatus(),
                "IntentReceipt must transition to RECEIPT_STATUS_COMPLETED after survey completes");

        List<HorizonSurveyReport> telemetryReports = harness.getReceivedFrames().stream()
                .filter(TelemetryFrame::hasHorizonSurvey)
                .map(TelemetryFrame::getHorizonSurvey)
                .toList();
        assertEquals(1, telemetryReports.size(),
                "Exactly 1 HorizonSurveyReport must be delivered on TelemetryFrame");
        assertEquals(36, telemetryReports.get(0).getSectorsCount(),
                "HorizonSurveyReport must contain all 36 sectors");

        assertFalse(observedWatchdogStates.contains(WatchdogState.STREAM_DEGRADED),
                "AgentWatchdogFSM must NEVER enter STREAM_DEGRADED during active 72-tick SURVEYING; observed: "
                        + observedWatchdogStates);
        assertFalse(observedWatchdogStates.contains(WatchdogState.DISCONNECTED_SAFE_MODE),
                "AgentWatchdogFSM must NEVER enter DISCONNECTED_SAFE_MODE during active 72-tick SURVEYING; observed: "
                        + observedWatchdogStates);
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState(),
                "AgentWatchdogFSM must remain CONNECTED_ACTIVE throughout the horizon survey");
    }

    @Test
    @DisplayName("Sleeping in bed for 120 ticks without new Go intents does not trigger WatchdogFSM false disconnect")
    void testSleepingInBedDoesNotTriggerWatchdogFalseDisconnect() {
        assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState());

        BlockPos bedPos = new BlockPos(1, 86, 0);
        when(mockLevel.isNight()).thenReturn(true);
        BlockState headBedState = mock(BlockState.class);
        when(headBedState.is(BlockTags.BEDS)).thenReturn(true);
        when(headBedState.getBlock()).thenReturn(Blocks.WHITE_BED);
        when(headBedState.hasProperty(BedBlock.PART)).thenReturn(true);
        when(headBedState.getValue(BedBlock.PART)).thenReturn(BedPart.HEAD);
        when(headBedState.hasProperty(BedBlock.OCCUPIED)).thenReturn(true);
        when(headBedState.getValue(BedBlock.OCCUPIED)).thenReturn(false);
        harness.setBlock(bedPos, headBedState);

        IntentBatch sleepBatch = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(10L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(10L)
                        .setInteractBlock(InteractBlockIntent.newBuilder()
                                .setPos(BlockPosProto.newBuilder().setX(1).setY(86).setZ(0).build())
                                .setInteractionType(InteractBlockIntent.InteractionType.SLEEP_IN_BED)
                                .build())
                        .build())
                .build();

        harness.sendIntentBatch(sleepBatch);
        assertTrue(agent.isSleeping(), "Agent must be sleeping after SLEEP_IN_BED");
        assertEquals(MotorState.SLEEPING, agent.getMotorFSM().getState(),
                "AgentMotorFSM must enter MotorState.SLEEPING");

        // Tick 120 ticks (6.0 seconds) without Go sending new intents while sleeping
        tickServer(120);

        assertEquals(MotorState.SLEEPING, agent.getMotorFSM().getState(),
                "AgentMotorFSM must remain SLEEPING for the full 120-tick night sleep");
        assertAll(
                () -> assertFalse(observedWatchdogStates.contains(WatchdogState.STREAM_DEGRADED),
                        "AgentWatchdogFSM must NEVER enter STREAM_DEGRADED while sleeping in bed; observed: "
                                + observedWatchdogStates),
                () -> assertFalse(observedWatchdogStates.contains(WatchdogState.DISCONNECTED_SAFE_MODE),
                        "AgentWatchdogFSM must NEVER enter DISCONNECTED_SAFE_MODE while sleeping in bed; observed: "
                                + observedWatchdogStates),
                () -> assertEquals(WatchdogState.CONNECTED_ACTIVE, watchdogFSM.getState(),
                        "AgentWatchdogFSM must remain CONNECTED_ACTIVE while sleeping")
        );
    }

    @Test
    @DisplayName("Duplicate SurveyHorizonIntent batches submitted while SURVEYING are deduplicated, not queued")
    void testDuplicateSurveyHorizonIntentsWhileSurveyingAreDeduplicatedNotQueued() {
        IntentBatch initialSurvey = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(1L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(1L)
                        .setSurveyHorizon(SurveyHorizonIntent.newBuilder()
                                .setNumSectors(36)
                                .setMaxRange(64.0f)
                                .setTicksPerSector(2)
                                .build())
                        .build())
                .build();

        harness.sendIntentBatch(initialSurvey);
        assertEquals(MotorState.SURVEYING, agent.getMotorFSM().getState());

        // Over the next 20 ticks while SURVEYING, submit 10 duplicate SurveyHorizonIntent batches (batch_id=2..11)
        for (int i = 0; i < 20; i++) {
            tickServer(1);
            if (i % 2 == 0) {
                long dupId = 2L + (i / 2);
                IntentBatch duplicateSurvey = IntentBatch.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setBatchId(dupId)
                        .addIntents(AgentIntent.newBuilder()
                                .setAgentId(AGENT_ID)
                                .setSequenceId(dupId)
                                .setSurveyHorizon(SurveyHorizonIntent.newBuilder()
                                        .setNumSectors(36)
                                        .setMaxRange(64.0f)
                                        .setTicksPerSector(2)
                                        .build())
                                .build())
                        .build();
                harness.sendIntentBatch(duplicateSurvey);
            }
        }

        // Tick up to tick 80 total (shortly after the ~72-tick survey finishes)
        tickServer(60);
        MotorState stateAtTick80 = agent.getMotorFSM().getState();
        int queuedAtTick80 = harness.getIntentActuator().getQueuedBatchCount(agent);

        // Tick remainder of 200 ticks total to confirm no extra surveys execute
        tickServer(120);

        assertAll(
                () -> assertEquals(MotorState.IDLE, stateAtTick80,
                        "MotorState must return to IDLE shortly after tick ~74 instead of staying locked in queued surveys"),
                () -> assertEquals(0, queuedAtTick80,
                        "Duplicate SurveyHorizonIntent batches must not accumulate in pendingBatches"),
                () -> assertEquals(1, queuedSurveyReports.size(),
                        "Avatar must execute EXACTLY 1 360-degree horizon survey, not 11 consecutive stationary surveys"),
                () -> assertEquals(MotorState.IDLE, agent.getMotorFSM().getState(),
                        "MotorState must remain IDLE through tick 200 after the single survey completes")
        );
    }

    @Test
    @DisplayName("Elevated unreachable OAK_LOG at (+6, +8, +6) / Y=94 fails cleanly without freezing subsequent valid navigation")
    void testElevatedUnreachableLogAtY94FailsCleanlyWithoutFreezingSubsequentValidNavigation() {
        buildFlatGround(85, 20);
        // Agent stands at (0, 86, 0); place solid OAK_LOG at (+6, +8, +6) -> (6, 94, 6) inside radius 7
        BlockPos unreachableLog = new BlockPos(6, 94, 6);
        harness.setBlock(unreachableLog, Blocks.OAK_LOG.defaultBlockState());

        IntentBatch unreachableNav = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(100L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(100L)
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(com.civquest.mod.grpc.v1.Vec3.newBuilder()
                                        .setX(6.5)
                                        .setY(94.0)
                                        .setZ(6.5)
                                        .build())
                                .setArrivalTolerance(1)
                                .build())
                        .build())
                .build();

        harness.sendIntentBatch(unreachableNav);

        IntentReceipt unreachableReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
        for (int t = 0; t < 120; t++) {
            tickServer(1);
            unreachableReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (unreachableReceipt.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED
                    || unreachableReceipt.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_ABORTED
                    || unreachableReceipt.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED) {
                break;
            }
        }

        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED, unreachableReceipt.getStatus(),
                "Navigating toward unreachable elevated Y=94 OAK_LOG must cleanly report RECEIPT_STATUS_FAILED");
        assertEquals(MotorState.IDLE, agent.getMotorFSM().getState(),
                "MotorState must return to IDLE after failing unreachable navigation");

        // Immediately submit NavigateIntent to reachable flat ground target (-10, 0, 0) relative -> (-10, 86, 0)
        IntentBatch reachableNav = IntentBatch.newBuilder()
                .setAgentId(AGENT_ID)
                .setBatchId(101L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId(AGENT_ID)
                        .setSequenceId(101L)
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(com.civquest.mod.grpc.v1.Vec3.newBuilder()
                                        .setX(-9.5)
                                        .setY(86.0)
                                        .setZ(0.5)
                                        .build())
                                .setArrivalTolerance(1)
                                .build())
                        .build())
                .build();

        harness.sendIntentBatch(reachableNav);

        IntentReceipt reachableReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
        for (int t = 0; t < 200; t++) {
            tickServer(1);
            reachableReceipt = harness.getIntentActuator().getCurrentReceipt(agent);
            if (reachableReceipt.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED
                    || reachableReceipt.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED
                    || reachableReceipt.getStatus() == IntentReceipt.ReceiptStatus.RECEIPT_STATUS_ABORTED) {
                break;
            }
        }

        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, reachableReceipt.getStatus(),
                "Subsequent valid NavigateIntent to flat ground (-10, 0, 0) must succeed without lingering stuck state");
        assertEquals(MotorState.IDLE, agent.getMotorFSM().getState(),
                "MotorState must return to IDLE upon arrival");
        assertTrue(positionRef.get().distanceTo(new Vec3(-9.5, 86.0, 0.5)) <= 1.5,
                "Agent must physically arrive near (-9.5, 86.0, 0.5); actual pos=" + positionRef.get());
    }
}
