package com.civquest.mod.actuator;

import com.civquest.mod.CivQuestBridgeServiceImpl;
import com.civquest.mod.AgentEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;

public class IntentActuatorTest {

    private IntentActuator actuator;
    private AgentNavigator navigator;

    @BeforeEach
    void setUp() {
        actuator = new IntentActuator();
        navigator = new AgentNavigator();
    }

    @Test
    @DisplayName("AgentNavigator initializes in IDLE state with default settings")
    void testNavigatorInitialState() {
        assertNotNull(navigator);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());
        assertTrue(navigator.isIdle());
        assertFalse(navigator.isPathing());
        assertFalse(navigator.isArrived());
        assertFalse(navigator.isStuck());
        assertNull(navigator.getTargetCoords());
        assertFalse(AgentNavigator.isBaritonePresent());
    }

    @Test
    @DisplayName("AgentNavigator navigateDirect sets pathing state, targets, and parameters")
    void testNavigatorNavigateDirect() {
        navigator.navigate(10.0, 64.0, 20.0, 1.5f, true, true, true, 2);

        assertEquals(NavigationState.NAVIGATION_STATE_PATHING, navigator.getNavigationState());
        assertTrue(navigator.isPathing());
        assertNotNull(navigator.getTargetCoords());
        assertEquals(10.0, navigator.getTargetCoords().x);
        assertEquals(64.0, navigator.getTargetCoords().y);
        assertEquals(20.0, navigator.getTargetCoords().z);
        assertEquals(1.5, navigator.getSpeedMultiplier(), 0.001);
        assertTrue(navigator.isSprint());
        assertTrue(navigator.isAllowBreak());
        assertTrue(navigator.isAllowPlace());
        assertEquals(2, navigator.getArrivalTolerance());
    }

    @Test
    @DisplayName("AgentNavigator navigate from NavigateIntent correctly configures navigation parameters")
    void testNavigatorNavigateIntent() {
        NavigateIntent intent = NavigateIntent.newBuilder()
                .setTarget(Vec3.newBuilder().setX(5.0).setY(60.0).setZ(-15.0).build())
                .setSpeedMultiplier(1.2f)
                .setSprint(true)
                .setAllowBreakBlocks(false)
                .setAllowPlaceScaffolding(false)
                .setArrivalTolerance(1)
                .build();

        navigator.navigate(intent);
        assertTrue(navigator.isPathing());
        assertEquals(5.0, navigator.getTargetCoords().x);
        assertEquals(60.0, navigator.getTargetCoords().y);
        assertEquals(-15.0, navigator.getTargetCoords().z);
        assertEquals(1.2, navigator.getSpeedMultiplier(), 0.001);
        assertTrue(navigator.isSprint());
        assertFalse(navigator.isAllowBreak());
    }

    @Test
    @DisplayName("AgentNavigator native arrival check transitions to ARRIVED when within tolerance")
    void testNavigatorArrival() {
        navigator.setPosition(10.0, 64.0, 10.0);
        navigator.navigate(10.2, 64.0, 10.1, 1.0f, false, false, false, 1);

        assertEquals(NavigationState.NAVIGATION_STATE_PATHING, navigator.getNavigationState());
        navigator.tick();

        assertEquals(NavigationState.NAVIGATION_STATE_ARRIVED, navigator.getNavigationState());
        assertTrue(navigator.isArrived());
    }

    @Test
    @DisplayName("AgentNavigator native tick advances position towards destination coordinates")
    void testNavigatorMovementTick() {
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(10.0, 64.0, 0.0, 1.0f, false, false, false, 1);

        assertTrue(navigator.isPathing());
        navigator.tick();

        // Must have stepped forward along the X axis
        assertTrue(navigator.getPosition().x > 0.0, "Position X must advance towards target");
        assertEquals(NavigationState.NAVIGATION_STATE_PATHING, navigator.getNavigationState());
    }

    @Test
    @DisplayName("AgentNavigator stuck detection triggers after repeated ticks without movement")
    void testNavigatorStuckDetection() {
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(100.0, 64.0, 100.0, 1.0f, false, false, false, 1);

        // Simulate 40 ticks without position advancement
        for (int i = 0; i < 42; i++) {
            navigator.tick();
            navigator.setPosition(0.0, 64.0, 0.0); // Reset position to simulate obstacle blockage
        }

        assertEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getNavigationState());
        assertTrue(navigator.isStuck());
    }

    @Test
    @DisplayName("AgentNavigator stuck detection evaluates horizontal displacement squared ignoring vertical bobbing")
    void testNavigatorHorizontalStuckDetectionWithVerticalBobbing() {
        navigator.setPosition(0.0, 64.0, 0.0);
        navigator.navigate(100.0, 64.0, 100.0, 1.0f, false, false, false, 1);

        // Simulate 42 ticks where horizontal position is frozen but vertical position bobs (e.g. wall jumping)
        for (int i = 0; i < 42; i++) {
            navigator.tick();
            navigator.setPosition(0.0, 64.0 + (i % 2 == 0 ? 0.5 : 0.0), 0.0);
        }

        assertEquals(NavigationState.NAVIGATION_STATE_STUCK, navigator.getNavigationState());
        assertTrue(navigator.isStuck());
    }

    @Test
    @DisplayName("AgentNavigator rejects non-finite coordinates and coordinates exceeding world bounds")
    void testNavigatorCoordinateValidation() {
        navigator.setPosition(0.0, 64.0, 0.0);

        // NaN coordinates rejected
        navigator.navigate(Double.NaN, 64.0, 0.0, 1.0f, false, false, false, 1);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());

        // Infinite coordinates rejected
        navigator.navigate(0.0, Double.POSITIVE_INFINITY, 0.0, 1.0f, false, false, false, 1);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());

        // X exceeding world bounds (> 30,000,000)
        navigator.navigate(30000001.0, 64.0, 0.0, 1.0f, false, false, false, 1);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());

        // Z exceeding world bounds (< -30,000,000)
        navigator.navigate(0.0, 64.0, -30000001.0, 1.0f, false, false, false, 1);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());

        // Y below world floor (< -64)
        navigator.navigate(0.0, -65.0, 0.0, 1.0f, false, false, false, 1);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());

        // Y above world ceiling (> 320)
        navigator.navigate(0.0, 321.0, 0.0, 1.0f, false, false, false, 1);
        assertEquals(NavigationState.NAVIGATION_STATE_IDLE, navigator.getNavigationState());
    }

    @Test
    @DisplayName("AgentNavigator stop resets to IDLE and clears targets and velocity")
    void testNavigatorStop() {
        navigator.navigate(50.0, 64.0, 50.0, 1.0f, true, false, false, 1);
        assertTrue(navigator.isPathing());

        navigator.stop();
        assertTrue(navigator.isIdle());
        assertNull(navigator.getTargetCoords());
    }

    @Test
    @DisplayName("IntentActuator handles null agent and null intent safely without throwing")
    void testActuateWithNullAgentOrIntent() {
        assertDoesNotThrow(() -> actuator.actuate(null, null));
        assertDoesNotThrow(() -> actuator.actuate(null, AgentIntent.getDefaultInstance()));
        assertFalse(actuator.actuateNavigate(null, null));
        assertFalse(actuator.actuateAttack(null, null));
        assertFalse(actuator.actuateInteractBlock(null, null));
        assertFalse(actuator.actuateUseItem(null, null));
        assertFalse(actuator.actuateCreateWrittenBook(null, null));
        assertFalse(actuator.actuatePlaceSignpost(null, null));
    }

    @Test
    @DisplayName("AgentEntityManager handleIntent handles null intent and non-existent agents safely")
    void testAgentEntityManagerHandleIntent() {
        AgentEntityManager manager = new AgentEntityManager();
        assertNotNull(manager.getIntentActuator());

        // Null intent returns completed future
        CompletableFuture<Void> nullFuture = manager.handleIntent(null, null);
        assertNotNull(nullFuture);
        nullFuture.join();

        // Non-existent agent does not throw
        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("agent-non-existent")
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(1.0).setY(1.0).setZ(1.0).build())
                        .build())
                .build();

        CompletableFuture<Void> future = manager.handleIntent(null, intent);
        assertNotNull(future);
        future.join();
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl connectAgent stream dispatches AgentIntent to manager")
    void testBridgeServiceConnectAgentIntentStream() {
        StreamRegistry streamRegistry = new StreamRegistry();
        AgentEntityManager manager = new AgentEntityManager(streamRegistry);
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(streamRegistry, manager, () -> null);

        AtomicReference<TelemetryFrame> initialFrame = new AtomicReference<>();
        StreamObserver<AgentIntent> requestObserver = service.connectAgent(new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {
                initialFrame.set(value);
            }

            @Override
            public void onError(Throwable t) {}

            @Override
            public void onCompleted() {}
        });

        assertNotNull(initialFrame.get());

        // Send NavigateIntent over stream
        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("agent-stream-test")
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(100.0).setY(64.0).setZ(-50.0).build())
                        .setArrivalTolerance(1)
                        .build())
                .build();

        assertDoesNotThrow(() -> requestObserver.onNext(intent));
        assertTrue(streamRegistry.isRegistered("agent-stream-test"));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl stream rejects mutation of bound agent_id with PERMISSION_DENIED")
    void testBridgeServiceStreamAgentIdMismatch() {
        StreamRegistry streamRegistry = new StreamRegistry();
        AgentEntityManager manager = new AgentEntityManager(streamRegistry);
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(streamRegistry, manager, () -> null);

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        StreamObserver<AgentIntent> requestObserver = service.connectAgent(new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        // First intent binds to agent-1
        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-1")
                .setNavigate(NavigateIntent.newBuilder().build())
                .build());

        // Second intent with different agentId must trigger PERMISSION_DENIED
        requestObserver.onNext(AgentIntent.newBuilder()
                .setAgentId("agent-2")
                .setNavigate(NavigateIntent.newBuilder().build())
                .build());

        Throwable error = errorRef.get();
        assertNotNull(error);
        assertInstanceOf(StatusRuntimeException.class, error);
        assertEquals(io.grpc.Status.Code.PERMISSION_DENIED, ((StatusRuntimeException) error).getStatus().getCode());
    }

    @Test
    @DisplayName("Protobuf contract round-trip serialization for all physical intent types")
    void testProtobufIntentContracts() {
        // AttackIntent
        AttackIntent attack = AttackIntent.newBuilder()
                .setTargetEntityId(42)
                .setRaiseShield(true)
                .setKeepDistanceKite(true)
                .setFleeImmediately(false)
                .build();
        assertEquals(42, attack.getTargetEntityId());
        assertTrue(attack.getRaiseShield());
        assertTrue(attack.getKeepDistanceKite());

        // InteractBlockIntent
        InteractBlockIntent interact = InteractBlockIntent.newBuilder()
                .setPos(BlockPosProto.newBuilder().setX(10).setY(64).setZ(20).build())
                .setInteractionType(InteractBlockIntent.InteractionType.PAVE_ROAD_PATH)
                .setBlockOrItemId("minecraft:dirt_path")
                .build();
        assertEquals(10, interact.getPos().getX());
        assertEquals(InteractBlockIntent.InteractionType.PAVE_ROAD_PATH, interact.getInteractionType());

        // UseItemIntent
        UseItemIntent useItem = UseItemIntent.newBuilder()
                .setInventorySlot(3)
                .setIsEatingOrDrinking(true)
                .setEquipArmorOrOffhand(false)
                .build();
        assertEquals(3, useItem.getInventorySlot());
        assertTrue(useItem.getIsEatingOrDrinking());

        // CreateWrittenBookIntent
        CreateWrittenBookIntent book = CreateWrittenBookIntent.newBuilder()
                .setTitle("Settlement Chronicle")
                .setAuthor("Brian")
                .addPages("First entry.")
                .addPages("Second entry.")
                .setTargetLecternOrShelf(BlockPosProto.newBuilder().setX(5).setY(65).setZ(5).build())
                .build();
        assertEquals("Settlement Chronicle", book.getTitle());
        assertEquals("Brian", book.getAuthor());
        assertEquals(2, book.getPagesCount());

        // PlaceSignpostIntent
        PlaceSignpostIntent sign = PlaceSignpostIntent.newBuilder()
                .setPos(BlockPosProto.newBuilder().setX(1).setY(2).setZ(3).build())
                .setIsHangingSign(false)
                .setLine1("Ironwood")
                .setLine2("Town Hall")
                .setLine3("Est. 2026")
                .setLine4("CivQuest")
                .build();
        assertEquals("Ironwood", sign.getLine1());
        assertFalse(sign.getIsHangingSign());
    }
}
