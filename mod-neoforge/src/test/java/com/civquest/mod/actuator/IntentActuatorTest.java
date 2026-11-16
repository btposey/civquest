package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.CivQuestBridgeServiceImpl;
import com.civquest.mod.AgentEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.CraftItemIntent;
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
import net.minecraft.core.BlockPos;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.shapes.Shapes;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.Collections;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

public class IntentActuatorTest {

    private IntentActuator actuator;
    private AgentNavigator navigator;

    @BeforeAll
    static void initMinecraft() {
        try {
            net.neoforged.fml.loading.LoadingModList.of(
                    java.util.List.of(), java.util.List.of(), java.util.List.of(), java.util.List.of(), java.util.Map.of()
            );
        } catch (Throwable ignored) {}
        try {
            java.lang.reflect.Field unsafeField = sun.misc.Unsafe.class.getDeclaredField("theUnsafe");
            unsafeField.setAccessible(true);
            sun.misc.Unsafe unsafe = (sun.misc.Unsafe) unsafeField.get(null);

            java.lang.reflect.Field fmlLoaderDist = net.neoforged.fml.loading.FMLLoader.class.getDeclaredField("dist");
            long offset1 = unsafe.staticFieldOffset(fmlLoaderDist);
            unsafe.putObject(net.neoforged.fml.loading.FMLLoader.class, offset1, net.neoforged.api.distmarker.Dist.DEDICATED_SERVER);

            java.lang.reflect.Field fmlEnvDist = net.neoforged.fml.loading.FMLEnvironment.class.getDeclaredField("dist");
            long offset2 = unsafe.staticFieldOffset(fmlEnvDist);
            unsafe.putObject(net.neoforged.fml.loading.FMLEnvironment.class, offset2, net.neoforged.api.distmarker.Dist.DEDICATED_SERVER);
        } catch (Throwable ignored) {}
        try {
            net.minecraft.SharedConstants.tryDetectVersion();
            net.minecraft.server.Bootstrap.bootStrap();
        } catch (Throwable ignored) {}
    }

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

    @Test
    @DisplayName("IntentActuator rejects PLACE_BLOCK when agent AABB collides with target block")
    void testPlaceBlockRejectsCollisionWithAgentAABB() {
        BlockState solidBlockState = mock(BlockState.class);
        when(solidBlockState.blocksMotion()).thenReturn(true);
        when(solidBlockState.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        IntentActuator testActuator = new IntentActuator() {
            @Override
            protected BlockState resolveBlockState(AgentEntity a, String id) {
                return solidBlockState;
            }
        };

        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.isCreative()).thenReturn(true);
        // Agent positioned at (0.5, 64.0, 0.5), bounding box [0.2, 64.0, 0.2] to [0.8, 65.8, 0.8]
        AABB agentBox = new AABB(0.2, 64.0, 0.2, 0.8, 65.8, 0.8);
        when(agent.getBoundingBox()).thenReturn(agentBox);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(1.0);

        BlockPos targetPos = new BlockPos(0, 64, 0);
        when(level.hasChunkAt(targetPos)).thenReturn(true);
        BlockState replaceableState = mock(BlockState.class);
        when(replaceableState.canBeReplaced()).thenReturn(true);
        when(level.getBlockState(targetPos)).thenReturn(replaceableState);

        InteractBlockIntent intent = InteractBlockIntent.newBuilder()
                .setPos(BlockPosProto.newBuilder().setX(0).setY(64).setZ(0).build())
                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                .setBlockOrItemId("minecraft:cobblestone")
                .build();

        boolean result = testActuator.actuateInteractBlock(agent, intent);
        assertFalse(result, "PLACE_BLOCK must be rejected when agent bounding box intersects target block");
        verify(level, never()).setBlockAndUpdate(any(), any());
    }

    @Test
    @DisplayName("IntentActuator rejects PLACE_BLOCK when another player or living entity AABB collides with target block")
    void testPlaceBlockRejectsCollisionWithOtherPlayerAABB() {
        BlockState solidBlockState = mock(BlockState.class);
        when(solidBlockState.blocksMotion()).thenReturn(true);
        when(solidBlockState.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        IntentActuator testActuator = new IntentActuator() {
            @Override
            protected BlockState resolveBlockState(AgentEntity a, String id) {
                return solidBlockState;
            }
        };

        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.isCreative()).thenReturn(true);
        // Agent positioned far away at (10.0, 64.0, 10.0)
        AABB agentBox = new AABB(9.7, 64.0, 9.7, 10.3, 65.8, 10.3);
        when(agent.getBoundingBox()).thenReturn(agentBox);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(4.0);

        BlockPos targetPos = new BlockPos(0, 64, 0);
        when(level.hasChunkAt(targetPos)).thenReturn(true);
        BlockState replaceableState = mock(BlockState.class);
        when(replaceableState.canBeReplaced()).thenReturn(true);
        when(level.getBlockState(targetPos)).thenReturn(replaceableState);

        // Another player is standing inside pos (0, 64, 0)
        Player otherPlayer = mock(Player.class);
        when(otherPlayer.isAlive()).thenReturn(true);
        AABB otherBox = new AABB(0.2, 64.0, 0.2, 0.8, 65.8, 0.8);
        when(otherPlayer.getBoundingBox()).thenReturn(otherBox);

        AABB blockBox = new AABB(targetPos);
        when(level.getEntities(eq(agent), eq(blockBox), any())).thenReturn(List.of(otherPlayer));

        InteractBlockIntent intent = InteractBlockIntent.newBuilder()
                .setPos(BlockPosProto.newBuilder().setX(0).setY(64).setZ(0).build())
                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                .setBlockOrItemId("minecraft:cobblestone")
                .build();

        boolean result = testActuator.actuateInteractBlock(agent, intent);
        assertFalse(result, "PLACE_BLOCK must be rejected when another player's bounding box intersects target block");
        verify(level, never()).setBlockAndUpdate(any(), any());
    }

    @Test
    @DisplayName("IntentActuator succeeds with PLACE_BLOCK when no entity collides with target block")
    void testPlaceBlockSucceedsWhenNoCollision() {
        BlockState solidBlockState = mock(BlockState.class);
        when(solidBlockState.blocksMotion()).thenReturn(true);
        when(solidBlockState.getCollisionShape(any(), any())).thenReturn(Shapes.block());

        IntentActuator testActuator = new IntentActuator() {
            @Override
            protected BlockState resolveBlockState(AgentEntity a, String id) {
                return solidBlockState;
            }
        };

        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.isCreative()).thenReturn(true);
        // Agent positioned safely outside target block at (2.0, 64.0, 2.0)
        AABB agentBox = new AABB(1.7, 64.0, 1.7, 2.3, 65.8, 2.3);
        when(agent.getBoundingBox()).thenReturn(agentBox);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(4.0);

        BlockPos targetPos = new BlockPos(0, 64, 0);
        when(level.hasChunkAt(targetPos)).thenReturn(true);
        BlockState replaceableState = mock(BlockState.class);
        when(replaceableState.canBeReplaced()).thenReturn(true);
        when(level.getBlockState(targetPos)).thenReturn(replaceableState);
        when(level.setBlockAndUpdate(eq(targetPos), any())).thenReturn(true);

        AABB blockBox = new AABB(targetPos);
        when(level.getEntities(eq(agent), eq(blockBox), any())).thenReturn(Collections.emptyList());

        InteractBlockIntent intent = InteractBlockIntent.newBuilder()
                .setPos(BlockPosProto.newBuilder().setX(0).setY(64).setZ(0).build())
                .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                .setBlockOrItemId("minecraft:cobblestone")
                .build();

        boolean result = testActuator.actuateInteractBlock(agent, intent);
        assertTrue(result, "PLACE_BLOCK must succeed when no collision occurs");
        verify(level, times(1)).setBlockAndUpdate(eq(targetPos), any());
    }

    @Test
    @DisplayName("3x3 tool and structure recipes fail immediately when no crafting table is within reach")
    void test3x3RecipesFailWithoutCraftingTable() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.blockPosition()).thenReturn(new BlockPos(0, 64, 0));
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(100.0);
        BlockState air = mock(BlockState.class);
        when(level.getBlockState(any(BlockPos.class))).thenReturn(air);

        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_pickaxe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_sword").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_axe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_pickaxe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_sword").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:door").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:chest").build()));
    }

    @Test
    @DisplayName("isCraftingTableWithinReach detects crafting table within interaction reach (<= 4.5 blocks / 20.25 reach_sq)")
    void testCraftingTableProximityDetection() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.blockPosition()).thenReturn(new BlockPos(0, 64, 0));

        // When surrounded by air, reach check returns false
        BlockState air = mock(BlockState.class);
        when(level.getBlockState(any(BlockPos.class))).thenReturn(air);
        CraftItemIntent axeIntent = CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build();
        assertFalse(actuator.isCraftingTableWithinReach(agent, axeIntent));

        // When crafting table is placed at (1, 64, 0) (dist_sq = 1.0 <= 20.25)
        BlockState tableState = mock(BlockState.class);
        when(tableState.toString()).thenReturn("Block{minecraft:crafting_table}");
        BlockPos tablePos = new BlockPos(1, 64, 0);
        when(agent.distanceToSqr(tablePos.getX() + 0.5, tablePos.getY() + 0.5, tablePos.getZ() + 0.5)).thenReturn(1.0);
        when(level.getBlockState(tablePos)).thenReturn(tableState);

        assertTrue(actuator.isCraftingTableWithinReach(agent, axeIntent));

        // When crafting table is provided explicitly via workstation_pos
        CraftItemIntent intentWithWorkstation = CraftItemIntent.newBuilder()
                .setRecipeId("minecraft:stone_sword")
                .setWorkstationPos(BlockPosProto.newBuilder().setX(1).setY(64).setZ(0).build())
                .build();
        assertTrue(actuator.isCraftingTableWithinReach(agent, intentWithWorkstation));
    }

    @Test
    @DisplayName("isCraftingTableWithinReach rejects workstations farther than 4.5 blocks (20.25 reach_sq)")
    void testCraftingTableProximityThreshold() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.blockPosition()).thenReturn(new BlockPos(0, 64, 0));

        // Crafting table at (5, 64, 0) -> distance squared is 25.0 > 20.25
        BlockState tableState = mock(BlockState.class);
        when(tableState.toString()).thenReturn("Block{minecraft:crafting_table}");

        BlockPos distantTable = new BlockPos(5, 64, 0);
        when(agent.distanceToSqr(distantTable.getX() + 0.5, distantTable.getY() + 0.5, distantTable.getZ() + 0.5)).thenReturn(25.0);
        when(level.getBlockState(distantTable)).thenReturn(tableState);
        when(level.getBlockState(argThat(p -> p != null && !p.equals(distantTable)))).thenReturn(mock(BlockState.class));

        // Test with explicit workstation_pos that is distant (> 20.25)
        CraftItemIntent distantIntent = CraftItemIntent.newBuilder()
                .setRecipeId("minecraft:wooden_axe")
                .setWorkstationPos(BlockPosProto.newBuilder().setX(5).setY(64).setZ(0).build())
                .build();
        assertFalse(actuator.isCraftingTableWithinReach(agent, distantIntent), "Crafting table at dist_sq=25 > 20.25 must be out of reach");
    }

    @Test
    @DisplayName("resolveBlockState parses directional block state properties correctly")
    void testResolveBlockState_DirectionalProperties() {
        if (Blocks.OAK_STAIRS != null && Blocks.OAK_STAIRS.defaultBlockState() != null) {
            // 1. Horizontal facing + half on stairs
            BlockState stairState = actuator.resolveBlockState(null, "minecraft:oak_stairs[facing=south,half=top]");
            assertNotNull(stairState);
            assertEquals(Blocks.OAK_STAIRS, stairState.getBlock());
            assertEquals("south", stairState.getValue(net.minecraft.world.level.block.StairBlock.FACING).getSerializedName());
            assertEquals("top", stairState.getValue(net.minecraft.world.level.block.StairBlock.HALF).getSerializedName());

            // 2. Axis on log
            BlockState logState = actuator.resolveBlockState(null, "minecraft:oak_log[axis=x]");
            assertNotNull(logState);
            assertEquals(Blocks.OAK_LOG, logState.getBlock());
            assertEquals("x", logState.getValue(net.minecraft.world.level.block.RotatedPillarBlock.AXIS).getSerializedName());

            // 3. Simple block without properties
            BlockState stoneState = actuator.resolveBlockState(null, "minecraft:stone");
            assertNotNull(stoneState);
            assertEquals(Blocks.STONE, stoneState.getBlock());
        }
    }
}
