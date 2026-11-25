package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.CivQuestBridgeServiceImpl;
import com.civquest.mod.AgentEntityManager;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.ChatChannel;
import com.civquest.mod.grpc.v1.ChatEvent;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.NavigationState;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.NonNullList;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.network.chat.Component;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.server.players.PlayerList;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.crafting.CookingBookCategory;
import net.minecraft.world.item.crafting.CraftingBookCategory;
import net.minecraft.world.item.crafting.Ingredient;
import net.minecraft.world.item.crafting.RecipeHolder;
import net.minecraft.world.item.crafting.RecipeManager;
import net.minecraft.world.item.crafting.ShapedRecipe;
import net.minecraft.world.item.crafting.ShapedRecipePattern;
import net.minecraft.world.item.crafting.ShapelessRecipe;
import net.minecraft.world.item.crafting.SmeltingRecipe;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BedPart;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.shapes.Shapes;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

public class IntentActuatorTest {

    private IntentActuator actuator;
    private AgentNavigator navigator;
    private RegistryAccess.Frozen registryAccess;
    private RecipeManager recipeManager;

    @BeforeAll
    static void initMinecraft() {
        com.civquest.mod.test.MinecraftTestBootstrap.ensureBootstrapped();
    }

    @BeforeEach
    void setUp() {
        registryAccess = RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY);
        recipeManager = createTestRecipeManager(registryAccess);
        actuator = new IntentActuator();
        navigator = new AgentNavigator();
    }

    private static RecipeManager createTestRecipeManager(RegistryAccess.Frozen access) {
        RecipeManager manager = new RecipeManager(access);
        Ingredient logs = Ingredient.of(Items.OAK_LOG, Items.SPRUCE_LOG, Items.BIRCH_LOG);
        Ingredient planks = Ingredient.of(Items.OAK_PLANKS, Items.SPRUCE_PLANKS, Items.BIRCH_PLANKS);
        Ingredient sticks = Ingredient.of(Items.STICK);
        Ingredient coals = Ingredient.of(Items.COAL, Items.CHARCOAL);
        Ingredient cobble = Ingredient.of(Items.COBBLESTONE, Items.COBBLED_DEEPSLATE);

        manager.replaceRecipes(List.of(
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("oak_planks"),
                        new ShapelessRecipe("planks", CraftingBookCategory.BUILDING,
                                new ItemStack(Items.OAK_PLANKS, 4),
                                NonNullList.of(Ingredient.EMPTY, Ingredient.of(Items.OAK_LOG)))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("stick"),
                        new ShapedRecipe("sticks", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('#', planks), "#", "#"),
                                new ItemStack(Items.STICK, 4))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("crafting_table"),
                        new ShapedRecipe("", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('#', planks), "##", "##"),
                                new ItemStack(Items.CRAFTING_TABLE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("torch"),
                        new ShapedRecipe("", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('X', coals, '#', sticks), "X", "#"),
                                new ItemStack(Items.TORCH, 4))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("campfire"),
                        new ShapedRecipe("", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('S', sticks, 'C', coals, 'L', logs), " S ", "SCS", "LLL"),
                                new ItemStack(Items.CAMPFIRE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("wooden_axe"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', planks, '#', sticks), "XX", "X#", " #"),
                                new ItemStack(Items.WOODEN_AXE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("wooden_pickaxe"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', planks, '#', sticks), "XXX", " # ", " # "),
                                new ItemStack(Items.WOODEN_PICKAXE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("wooden_sword"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', planks, '#', sticks), "X", "X", "#"),
                                new ItemStack(Items.WOODEN_SWORD, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("stone_axe"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', cobble, '#', sticks), "XX", "X#", " #"),
                                new ItemStack(Items.STONE_AXE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("stone_pickaxe"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', cobble, '#', sticks), "XXX", " # ", " # "),
                                new ItemStack(Items.STONE_PICKAXE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("stone_sword"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', cobble, '#', sticks), "X", "X", "#"),
                                new ItemStack(Items.STONE_SWORD, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("oak_door"),
                        new ShapedRecipe("wooden_door", CraftingBookCategory.REDSTONE,
                                ShapedRecipePattern.of(Map.of('#', Ingredient.of(Items.OAK_PLANKS)), "##", "##", "##"),
                                new ItemStack(Items.OAK_DOOR, 3))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("chest"),
                        new ShapedRecipe("", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('#', planks), "###", "# #", "###"),
                                new ItemStack(Items.CHEST, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("cake"),
                        new ShapedRecipe("", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(
                                        Map.of('A', Ingredient.of(Items.MILK_BUCKET),
                                                'B', Ingredient.of(Items.SUGAR),
                                                'C', Ingredient.of(Items.WHEAT),
                                                'E', Ingredient.of(Items.EGG)),
                                        "AAA", "BEB", "CCC"),
                                new ItemStack(Items.CAKE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("charcoal"),
                        new SmeltingRecipe("", CookingBookCategory.MISC,
                                Ingredient.of(Items.OAK_LOG),
                                new ItemStack(Items.CHARCOAL, 1), 0.15f, 200)),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("sugar_from_honey_bottle"),
                        new ShapelessRecipe("sugar", CraftingBookCategory.MISC,
                                new ItemStack(Items.SUGAR, 3),
                                NonNullList.of(Ingredient.EMPTY, Ingredient.of(Items.HONEY_BOTTLE))))
        ));
        return manager;
    }

    private MinecraftServer mockServerWithRecipes() {
        MinecraftServer server = mock(MinecraftServer.class);
        when(server.getRecipeManager()).thenReturn(recipeManager);
        when(server.registryAccess()).thenReturn(registryAccess);
        return server;
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
        StreamObserver<IntentBatch> requestObserver = service.connectAgent(new StreamObserver<>() {
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

        // Send NavigateIntent inside IntentBatch over stream
        AgentIntent intent = AgentIntent.newBuilder()
                .setAgentId("agent-stream-test")
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(100.0).setY(64.0).setZ(-50.0).build())
                        .setArrivalTolerance(1)
                        .build())
                .build();
        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId("agent-stream-test")
                .setBatchId(1L)
                .addIntents(intent)
                .build();

        assertDoesNotThrow(() -> requestObserver.onNext(batch));
        assertTrue(streamRegistry.isRegistered("agent-stream-test"));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl stream rejects mutation of bound agent_id with PERMISSION_DENIED")
    void testBridgeServiceStreamAgentIdMismatch() {
        StreamRegistry streamRegistry = new StreamRegistry();
        AgentEntityManager manager = new AgentEntityManager(streamRegistry);
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(streamRegistry, manager, () -> null);

        AtomicReference<Throwable> errorRef = new AtomicReference<>();
        StreamObserver<IntentBatch> requestObserver = service.connectAgent(new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame value) {}

            @Override
            public void onError(Throwable t) {
                errorRef.set(t);
            }

            @Override
            public void onCompleted() {}
        });

        // First batch binds to agent-1
        requestObserver.onNext(IntentBatch.newBuilder()
                .setAgentId("agent-1")
                .setBatchId(1L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("agent-1")
                        .setNavigate(NavigateIntent.newBuilder().build())
                        .build())
                .build());

        // Second batch with different agentId must trigger PERMISSION_DENIED
        requestObserver.onNext(IntentBatch.newBuilder()
                .setAgentId("agent-2")
                .setBatchId(2L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("agent-2")
                        .setNavigate(NavigateIntent.newBuilder().build())
                        .build())
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
    @DisplayName("3x3 tool, campfire, and structure recipes fail immediately when no crafting table is within reach")
    void test3x3RecipesFailWithoutCraftingTable() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        Level level = mock(Level.class);
        when(agent.getServer()).thenReturn(server);
        when(agent.level()).thenReturn(level);
        when(level.getRecipeManager()).thenReturn(recipeManager);
        when(level.registryAccess()).thenReturn(registryAccess);
        when(agent.blockPosition()).thenReturn(new BlockPos(0, 64, 0));
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(100.0);
        BlockState air = mock(BlockState.class);
        when(level.getBlockState(any(BlockPos.class))).thenReturn(air);

        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 10));
        inv.setItem(1, new ItemStack(Items.OAK_PLANKS, 32));
        inv.setItem(2, new ItemStack(Items.STICK, 16));
        inv.setItem(3, new ItemStack(Items.COBBLESTONE, 32));
        inv.setItem(4, new ItemStack(Items.COAL, 4));
        when(agent.getInventory()).thenReturn(inv);

        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_pickaxe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_sword").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_axe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_pickaxe").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_sword").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_door").build()));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:chest").build()));

        // Verify inventory untouched when crafting table is out of reach
        assertEquals(10, inv.countItem(Items.OAK_LOG));
        assertEquals(32, inv.countItem(Items.OAK_PLANKS));
        assertEquals(16, inv.countItem(Items.STICK));
        assertEquals(4, inv.countItem(Items.COAL));
    }

    @Test
    @DisplayName("2x2 pocket crafting succeeds via RecipeManager without a crafting table")
    void test2x2PocketCraftingSucceedsWithoutTable() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        Level level = mock(Level.class);
        when(agent.getServer()).thenReturn(server);
        when(agent.level()).thenReturn(level);
        when(level.getBlockState(any(BlockPos.class))).thenReturn(mock(BlockState.class));

        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 2));
        when(agent.getInventory()).thenReturn(inv);

        // Batch count = 2: crafts 2x oak_planks -> 8 planks, consumes 2 logs
        assertTrue(actuator.actuateCraftItem(agent,
                CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").setBatchCount(2).build()));
        assertEquals(8, inv.countItem(Items.OAK_PLANKS));
        assertEquals(0, inv.countItem(Items.OAK_LOG));
        verify(agent, atLeastOnce()).autoEquipBestGear();

        // Fallback constructor works even when server and level are both null in mock tests
        IntentActuator headlessActuator = new IntentActuator(recipeManager);
        AgentEntity headlessAgent = mock(AgentEntity.class);
        Inventory headlessInv = new Inventory(headlessAgent);
        headlessInv.setItem(0, new ItemStack(Items.OAK_LOG, 1));
        when(headlessAgent.getInventory()).thenReturn(headlessInv);
        assertTrue(headlessActuator.actuateCraftItem(headlessAgent,
                CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").build()));
        assertEquals(4, headlessInv.countItem(Items.OAK_PLANKS));
    }

    @Test
    @DisplayName("3x3 crafting fails on missing ingredients or non-crafting recipes and succeeds with table + authentic materials")
    void test3x3CraftingAuthenticValidationAndRemainderItems() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        Level level = mock(Level.class);
        when(agent.getServer()).thenReturn(server);
        when(agent.level()).thenReturn(level);
        when(agent.blockPosition()).thenReturn(new BlockPos(0, 64, 0));

        BlockPos tablePos = new BlockPos(1, 64, 0);
        BlockState tableState = mock(BlockState.class);
        when(tableState.toString()).thenReturn("Block{minecraft:crafting_table}");
        when(agent.distanceToSqr(tablePos.getX() + 0.5, tablePos.getY() + 0.5, tablePos.getZ() + 0.5)).thenReturn(1.0);
        when(level.getBlockState(tablePos)).thenReturn(tableState);
        when(level.getBlockState(argThat(p -> p != null && !p.equals(tablePos)))).thenReturn(mock(BlockState.class));

        Inventory inv = new Inventory(agent);
        when(agent.getInventory()).thenReturn(inv);

        // 1. Old shortcuts must fail even with crafting table nearby
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 1));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()),
                "Single-log campfire shortcut must fail");
        assertEquals(1, inv.countItem(Items.OAK_LOG));

        inv.clearContent();
        inv.setItem(0, new ItemStack(Items.OAK_PLANKS, 4));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()),
                "4-plank wooden_axe without sticks must fail");
        assertEquals(4, inv.countItem(Items.OAK_PLANKS));

        // 2. Non-CraftingRecipe (SmeltingRecipe) must be rejected
        inv.clearContent();
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 1));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:charcoal").build()),
                "SmeltingRecipe must not be crafted as CraftingRecipe");
        assertEquals(1, inv.countItem(Items.OAK_LOG));

        // 3. Campfire missing coal must fail atomically without consuming logs or sticks
        inv.clearContent();
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 3));
        inv.setItem(1, new ItemStack(Items.STICK, 3));
        assertFalse(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()));
        assertEquals(3, inv.countItem(Items.OAK_LOG));
        assertEquals(3, inv.countItem(Items.STICK));

        // 4. Campfire with 3 logs (mixed variants) + 3 sticks + 1 charcoal succeeds
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 2));
        inv.setItem(1, new ItemStack(Items.BIRCH_LOG, 1));
        inv.setItem(2, new ItemStack(Items.STICK, 3));
        inv.setItem(3, new ItemStack(Items.CHARCOAL, 1));
        assertTrue(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()));
        assertEquals(1, inv.countItem(Items.CAMPFIRE));
        assertEquals(0, inv.countItem(Items.OAK_LOG));
        assertEquals(0, inv.countItem(Items.BIRCH_LOG));
        assertEquals(0, inv.countItem(Items.STICK));
        assertEquals(0, inv.countItem(Items.CHARCOAL));

        // 5. Wooden axe with 3 planks + 2 sticks succeeds and triggers autoEquipBestGear
        inv.clearContent();
        inv.setItem(0, new ItemStack(Items.OAK_PLANKS, 3));
        inv.setItem(1, new ItemStack(Items.STICK, 2));
        assertTrue(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));
        assertEquals(1, inv.countItem(Items.WOODEN_AXE));
        assertEquals(0, inv.countItem(Items.OAK_PLANKS));
        assertEquals(0, inv.countItem(Items.STICK));
        verify(agent, atLeastOnce()).autoEquipBestGear();

        // 6. Cake recipe preserves remaining empty buckets
        inv.clearContent();
        inv.setItem(0, new ItemStack(Items.MILK_BUCKET, 1));
        inv.setItem(1, new ItemStack(Items.MILK_BUCKET, 1));
        inv.setItem(2, new ItemStack(Items.MILK_BUCKET, 1));
        inv.setItem(3, new ItemStack(Items.SUGAR, 2));
        inv.setItem(4, new ItemStack(Items.EGG, 1));
        inv.setItem(5, new ItemStack(Items.WHEAT, 3));
        assertTrue(actuator.actuateCraftItem(agent, CraftItemIntent.newBuilder().setRecipeId("minecraft:cake").build()));
        assertEquals(1, inv.countItem(Items.CAKE));
        assertEquals(3, inv.countItem(Items.BUCKET));
        assertEquals(0, inv.countItem(Items.MILK_BUCKET));
    }

    @Test
    @DisplayName("Full inventory drops crafted output and remainder items at agent feet and rejects invalid inputs")
    void testCraftingDropsOverflowAndHandlesInvalidInputs() {
        // Invalid recipe IDs / null RecipeManager fail cleanly
        AgentEntity bareAgent = mock(AgentEntity.class);
        when(bareAgent.getInventory()).thenReturn(new Inventory(bareAgent));
        assertFalse(actuator.actuateCraftItem(bareAgent, CraftItemIntent.newBuilder().setRecipeId("").build()));
        assertFalse(actuator.actuateCraftItem(bareAgent, CraftItemIntent.newBuilder().setRecipeId("invalid key!").build()));
        assertFalse(actuator.actuateCraftItem(bareAgent, CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").build()),
                "Missing RecipeManager without fallback must fail cleanly");

        // Full 36-slot inventory: slot 0 has 2 honey bottles, slots 1..35 filled with cobblestone
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        Level level = mock(Level.class);
        when(agent.getServer()).thenReturn(server);
        when(agent.level()).thenReturn(level);

        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.HONEY_BOTTLE, 2));
        for (int i = 1; i < 36; i++) {
            inv.setItem(i, new ItemStack(Items.COBBLESTONE, 1));
        }
        when(agent.getInventory()).thenReturn(inv);

        assertTrue(actuator.actuateCraftItem(agent,
                CraftItemIntent.newBuilder().setRecipeId("minecraft:sugar_from_honey_bottle").build()));
        assertEquals(1, inv.countItem(Items.HONEY_BOTTLE));
        verify(agent).drop(argThat(s -> s.is(Items.SUGAR) && s.getCount() == 3), eq(false));
        verify(agent).drop(argThat(s -> s.is(Items.GLASS_BOTTLE) && s.getCount() == 1), eq(false));

        // Partial batch completion: request 3 batches of oak_planks with only 2 logs -> crafts 2 batches
        inv.clearContent();
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 2));
        assertTrue(actuator.actuateCraftItem(agent,
                CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").setBatchCount(3).build()));
        assertEquals(8, inv.countItem(Items.OAK_PLANKS));
        assertEquals(0, inv.countItem(Items.OAK_LOG));
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

    @Test
    @DisplayName("resolveBlockState resolves generic taxonomy tags (#tag) and preserves directional properties")
    void testResolveBlockState_TaxonomyTagResolution() {
        if (Blocks.OAK_STAIRS != null && Blocks.OAK_STAIRS.defaultBlockState() != null) {
            // 1. Generic stairs tag with directional properties fallback
            BlockState genericStairs = actuator.resolveBlockState(null, "#minecraft:stairs[facing=west,half=top]");
            assertNotNull(genericStairs);
            assertEquals(Blocks.OAK_STAIRS, genericStairs.getBlock());
            assertEquals("west", genericStairs.getValue(net.minecraft.world.level.block.StairBlock.FACING).getSerializedName());
            assertEquals("top", genericStairs.getValue(net.minecraft.world.level.block.StairBlock.HALF).getSerializedName());

            // 2. Generic logs tag with axis property fallback
            BlockState genericLogs = actuator.resolveBlockState(null, "#minecraft:logs[axis=z]");
            assertNotNull(genericLogs);
            assertEquals(Blocks.OAK_LOG, genericLogs.getBlock());
            assertEquals("z", genericLogs.getValue(net.minecraft.world.level.block.RotatedPillarBlock.AXIS).getSerializedName());
        }
    }

    @Test
    @DisplayName("IntentBatch executes primitives sequentially and transitions to RECEIPT_STATUS_COMPLETED")
    void testIntentBatchSequentialExecution() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        when(agent.getServer()).thenReturn(server);
        AgentNavigator nav = mock(AgentNavigator.class);
        BlockBreaker breaker = mock(BlockBreaker.class);
        when(agent.getAgentId()).thenReturn("batch-seq-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getBreaker()).thenReturn(breaker);

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.OAK_PLANKS, 4));
        when(agent.getInventory()).thenReturn(inv);

        // Primitive 0: Navigate (async) -> active while pathing, completes when arrived
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);

        AgentIntent navIntent = AgentIntent.newBuilder()
                .setAgentId("batch-seq-agent")
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(10).setY(64).setZ(10).build())
                        .setArrivalTolerance(1)
                        .build())
                .build();
        AgentIntent craftIntent = AgentIntent.newBuilder()
                .setAgentId("batch-seq-agent")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:stick").build())
                .build();

        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId("batch-seq-agent")
                .setBatchId(101L)
                .addIntents(navIntent)
                .addIntents(craftIntent)
                .build();

        actuator.submitBatch(agent, batch);
        IntentReceipt executing = actuator.getCurrentReceipt(agent);
        assertEquals(101L, executing.getBatchId());
        assertEquals(0, executing.getActiveIndex());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, executing.getStatus());

        // Still pathing on tick 1
        fsm.tick();
        actuator.tick(agent);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, actuator.getCurrentReceipt(agent).getStatus());
        assertEquals(0, actuator.getCurrentReceipt(agent).getActiveIndex());

        // Navigator arrives -> FSM goes IDLE -> IntentActuator advances to primitive 1 (CRAFT) and finishes COMPLETED
        when(nav.isPathing()).thenReturn(false);
        when(nav.isArrived()).thenReturn(true);
        fsm.tick();
        actuator.tick(agent);

        IntentReceipt completed = actuator.getCurrentReceipt(agent);
        assertEquals(101L, completed.getBatchId());
        assertEquals(2, completed.getActiveIndex());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, completed.getStatus());
        assertTrue(inv.countItem(Items.STICK) >= 4, "Second primitive (craft stick) must execute after navigation completes");
    }

    @Test
    @DisplayName("IntentBatch stops executing subsequent items and sets RECEIPT_STATUS_FAILED when a primitive fails")
    void testIntentBatchFailureStopsSubsequentItems() {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.getAgentId()).thenReturn("batch-fail-agent");
        when(agent.isAlive()).thenReturn(true);
        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        Inventory inv = new Inventory(agent);
        // Empty inventory so craft #1 fails, and craft #2 (even with planks? no, keep separate indicator) won't run
        when(agent.getInventory()).thenReturn(inv);

        AgentIntent badCraft = AgentIntent.newBuilder()
                .setAgentId("batch-fail-agent")
                .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:nonexistent_recipe").build())
                .build();
        AgentIntent followingNav = AgentIntent.newBuilder()
                .setAgentId("batch-fail-agent")
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(5).setY(64).setZ(5).build())
                        .build())
                .build();

        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId("batch-fail-agent")
                .setBatchId(202L)
                .addIntents(badCraft)
                .addIntents(followingNav)
                .build();

        actuator.submitBatch(agent, batch);

        IntentReceipt receipt = actuator.getCurrentReceipt(agent);
        assertEquals(202L, receipt.getBatchId());
        assertEquals(0, receipt.getActiveIndex());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_FAILED, receipt.getStatus());
        assertFalse(receipt.getFailureReason().isBlank());
        assertTrue(fsm.isMotorIdle(),
                "Subsequent navigation intent must not run after earlier primitive failed");
    }

    @Test
    @DisplayName("IntentBatch with flush_periphery=true clears MotorPDA stack, stops breaker/navigator, and preempts active batch")
    void testIntentBatchFlushPeriphery() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        when(agent.getServer()).thenReturn(server);
        AgentNavigator nav = mock(AgentNavigator.class);
        BlockBreaker breaker = mock(BlockBreaker.class);
        when(agent.getAgentId()).thenReturn("batch-flush-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getBreaker()).thenReturn(breaker);

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);

        IntentBatch initialBatch = IntentBatch.newBuilder()
                .setAgentId("batch-flush-agent")
                .setBatchId(301L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("batch-flush-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(20).setY(64).setZ(20).build())
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, initialBatch);
        fsm.pushReflex(new com.civquest.mod.actuator.motor.Combat(0, 0), "reflex");
        assertEquals(1, fsm.getStackDepth());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_PREEMPTED_BY_REFLEX,
                actuator.getCurrentReceipt(agent).getStatus());

        // Flush batch arrives
        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.OAK_PLANKS, 4));
        when(agent.getInventory()).thenReturn(inv);

        IntentBatch flushBatch = IntentBatch.newBuilder()
                .setAgentId("batch-flush-agent")
                .setBatchId(302L)
                .setFlushPeriphery(true)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("batch-flush-agent")
                        .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:stick").build())
                        .build())
                .build();
        actuator.submitBatch(agent, flushBatch);

        assertEquals(0, fsm.getStackDepth());
        verify(breaker, atLeastOnce()).cancel();
        verify(nav, atLeastOnce()).stop();
        IntentReceipt receipt = actuator.getCurrentReceipt(agent);
        assertEquals(302L, receipt.getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, receipt.getStatus());
    }

    @Test
    @DisplayName("IntentBatch enforces timeout_ticks and queues non-flushing batches in order")
    void testIntentBatchTimeoutAndQueueing() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        when(agent.getServer()).thenReturn(server);
        AgentNavigator nav = mock(AgentNavigator.class);
        BlockBreaker breaker = mock(BlockBreaker.class);
        when(agent.getAgentId()).thenReturn("batch-timeout-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getBreaker()).thenReturn(breaker);

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);

        IntentBatch batch1 = IntentBatch.newBuilder()
                .setAgentId("batch-timeout-agent")
                .setBatchId(401L)
                .setTimeoutTicks(3L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("batch-timeout-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(50).setY(64).setZ(50).build())
                                .build())
                        .build())
                .build();
        IntentBatch batch2 = IntentBatch.newBuilder()
                .setAgentId("batch-timeout-agent")
                .setBatchId(402L)
                .setFlushPeriphery(false)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("batch-timeout-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(10).setY(64).setZ(10).build())
                                .build())
                        .build())
                .build();

        actuator.submitBatch(agent, batch1);
        actuator.submitBatch(agent, batch2);
        assertEquals(1, actuator.getQueuedBatchCount(agent));
        assertEquals(401L, actuator.getCurrentReceipt(agent).getBatchId());

        // Tick 3 times to exceed timeoutTicks = 3
        for (int i = 0; i < 3; i++) {
            fsm.tick();
            actuator.tick(agent);
        }

        IntentReceipt aborted = actuator.getCurrentReceipt(agent);
        assertEquals(401L, aborted.getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_TIMEOUT, aborted.getStatus());
        assertTrue(aborted.getFailureReason().contains("timed out"));
        assertTrue(fsm.isMotorIdle());
        assertEquals(0, actuator.getQueuedBatchCount(agent));
    }

    @Test
    @DisplayName("Queued duplicate HARVEST_BREAK intent on already-broken air block completes gracefully without mining_start_failed")
    void testQueuedHarvestBreakOnAlreadyAirBlockCompletesGracefully() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        BlockBreaker breaker = mock(BlockBreaker.class);
        when(agent.getAgentId()).thenReturn("dup-break-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.level()).thenReturn(level);
        when(agent.getBreaker()).thenReturn(breaker);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(4.0);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);

        BlockState stoneBlock = mock(BlockState.class);
        when(stoneBlock.isAir()).thenReturn(false);
        when(stoneBlock.getDestroySpeed(any(), any())).thenReturn(1.5f);

        BlockState airBlock = mock(BlockState.class);
        when(airBlock.isAir()).thenReturn(true);

        AtomicReference<BlockState> blockAtTarget = new AtomicReference<>(stoneBlock);
        BlockPos targetPos = new BlockPos(5, 64, 5);
        when(level.getBlockState(targetPos)).thenAnswer(inv -> blockAtTarget.get());

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        // First HARVEST_BREAK starts mining; breaker remains active until tick() breaks it
        when(breaker.start(targetPos)).thenReturn(true);
        when(breaker.isBreaking()).thenReturn(true);

        AgentIntent breakIntent = AgentIntent.newBuilder()
                .setAgentId("dup-break-agent")
                .setSequenceId(1L)
                .setInteractBlock(InteractBlockIntent.newBuilder()
                        .setPos(BlockPosProto.newBuilder().setX(5).setY(64).setZ(5).build())
                        .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                        .build())
                .build();

        IntentBatch batch1 = IntentBatch.newBuilder()
                .setAgentId("dup-break-agent")
                .setBatchId(501L)
                .addIntents(breakIntent)
                .build();
        IntentBatch batch2 = IntentBatch.newBuilder()
                .setAgentId("dup-break-agent")
                .setBatchId(502L)
                .setFlushPeriphery(false)
                .addIntents(breakIntent.toBuilder().setSequenceId(2L).build())
                .build();

        actuator.submitBatch(agent, batch1);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, actuator.getCurrentReceipt(agent).getStatus());

        // Queue duplicate break batch while first is still mining
        actuator.submitBatch(agent, batch2);
        assertEquals(1, actuator.getQueuedBatchCount(agent));

        // Simulate block breaking finishing: block becomes AIR and breaker stops
        blockAtTarget.set(airBlock);
        when(breaker.tick()).thenReturn(true);
        when(breaker.isBreaking()).thenReturn(false);

        fsm.tick();
        actuator.tick(agent);

        IntentReceipt finalReceipt = actuator.getCurrentReceipt(agent);
        assertEquals(502L, finalReceipt.getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, finalReceipt.getStatus(),
                "Duplicate HARVEST_BREAK on already-air block must complete gracefully without mining_start_failed");
        assertEquals("", finalReceipt.getFailureReason());
        assertTrue(fsm.isMotorIdle());
        verify(breaker, times(1)).start(targetPos);
    }

    @Test
    @DisplayName("SendChatIntent dispatches immediately out-of-band and does not queue behind active NavigateIntent")
    void testOutOfBandChatDispatch() {
        AgentEntity agent = mock(AgentEntity.class);
        AgentNavigator nav = mock(AgentNavigator.class);
        when(agent.getAgentId()).thenReturn("chat-oob-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getGameProfile()).thenReturn(new com.mojang.authlib.GameProfile(java.util.UUID.randomUUID(), "ChattyAgent"));

        MinecraftServer server = mock(MinecraftServer.class);
        PlayerList playerList = mock(PlayerList.class);
        when(agent.getServer()).thenReturn(server);
        when(server.getPlayerList()).thenReturn(playerList);
        when(agent.level()).thenReturn(mock(Level.class));

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);

        // Submit active NavigateIntent batch
        IntentBatch navBatch = IntentBatch.newBuilder()
                .setAgentId("chat-oob-agent")
                .setBatchId(801L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("chat-oob-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(100).setY(64).setZ(100).build())
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, navBatch);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, actuator.getCurrentReceipt(agent).getStatus());
        assertEquals(0, actuator.getQueuedBatchCount(agent));

        // Submit chat batch while navigate is executing
        IntentBatch chatBatch = IntentBatch.newBuilder()
                .setAgentId("chat-oob-agent")
                .setBatchId(802L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("chat-oob-agent")
                        .setChat(SendChatIntent.newBuilder()
                                .setMessage("Hello world out-of-band!")
                                .setTargetChannel(ChatChannel.CHAT_CHANNEL_LOCAL)
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, chatBatch);

        // Chat batch must NOT be queued in pendingBatches waiting for navigation
        assertEquals(0, actuator.getQueuedBatchCount(agent), "Chat batch must not queue behind in-flight NavigateIntent");

        // Active batch is still the navigate batch
        assertEquals(801L, actuator.getCurrentReceipt(agent).getBatchId());
    }

    @Test
    @DisplayName("actuateSleepInBed adjusts foot pos to head pos and sheathes held weapons before sleeping")
    void testActuateSleepInBedFootToHeadAdjustmentAndSheathing() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        when(agent.level()).thenReturn(level);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(2.0); // Within BED_REACH_SQ
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(level.isNight()).thenReturn(true);

        BlockPos footPos = new BlockPos(10, 64, 10);
        BlockPos headPos = footPos.relative(Direction.SOUTH); // (10, 64, 11)

        BlockState footBedState = mock(BlockState.class);
        when(footBedState.is(net.minecraft.tags.BlockTags.BEDS)).thenReturn(true);
        BedBlock bedBlock = mock(BedBlock.class);
        when(footBedState.getBlock()).thenReturn(bedBlock);
        when(footBedState.hasProperty(BedBlock.PART)).thenReturn(true);
        when(footBedState.getValue(BedBlock.PART)).thenReturn(BedPart.FOOT);
        when(footBedState.hasProperty(BedBlock.FACING)).thenReturn(true);
        when(footBedState.getValue(BedBlock.FACING)).thenReturn(Direction.SOUTH);
        when(footBedState.hasProperty(BedBlock.OCCUPIED)).thenReturn(true);
        when(footBedState.getValue(BedBlock.OCCUPIED)).thenReturn(false);

        BlockState headBedState = mock(BlockState.class);
        when(headBedState.is(net.minecraft.tags.BlockTags.BEDS)).thenReturn(true);
        when(headBedState.getBlock()).thenReturn(bedBlock);
        when(headBedState.hasProperty(BedBlock.PART)).thenReturn(true);
        when(headBedState.getValue(BedBlock.PART)).thenReturn(BedPart.HEAD);
        when(headBedState.hasProperty(BedBlock.OCCUPIED)).thenReturn(true);
        when(headBedState.getValue(BedBlock.OCCUPIED)).thenReturn(false);

        when(level.getBlockState(footPos)).thenReturn(footBedState);
        when(level.getBlockState(headPos)).thenReturn(headBedState);

        // Setup inventory with sword in selected slot 0 and empty slot at 1
        Inventory inventory = new Inventory(mock(Player.class));
        ItemStack diamondSword = new ItemStack(Items.DIAMOND_SWORD);
        inventory.items.set(0, diamondSword);
        inventory.selected = 0;
        when(agent.getInventory()).thenReturn(inventory);
        when(agent.getMainHandItem()).thenAnswer(inv -> inventory.getSelected());
        when(agent.getOffhandItem()).thenReturn(ItemStack.EMPTY);
        when(agent.isSleeping()).thenReturn(true);

        assertTrue(actuator.actuateSleepInBed(agent, footPos));

        // Verify headPos was passed to startSleeping (adjusted from footPos)
        verify(agent).startSleeping(headPos);

        // Verify weapon was sheathed: hotbar selected slot is no longer slot 0 (switched to empty slot 1)
        assertFalse(IntentActuator.isWeaponOrTool(agent.getMainHandItem()),
                "Agent main hand must not hold a weapon while sleeping");
    }

    @Test
    @DisplayName("actuateSendChat constrains human players to 16m and delivers ChatEvent to peer agents within 20m")
    void testActuateSendChatDualRadiusAcousticDelivery() {
        UUID speakerUuid = UUID.randomUUID();
        AgentEntity speaker = mock(AgentEntity.class);
        Level level = mock(Level.class);
        MinecraftServer server = mock(MinecraftServer.class);
        PlayerList playerList = mock(PlayerList.class);

        when(speaker.getAgentId()).thenReturn("speaker-1");
        when(speaker.getUUID()).thenReturn(speakerUuid);
        when(speaker.getGameProfile()).thenReturn(new com.mojang.authlib.GameProfile(speakerUuid, "SpeakerAgent"));
        when(speaker.level()).thenReturn(level);
        when(speaker.getServer()).thenReturn(server);
        when(speaker.getBoundingBox()).thenReturn(new AABB(-0.3, 64.0, -0.3, 0.3, 65.8, 0.3));
        when(server.getPlayerList()).thenReturn(playerList);

        // Human players: 10m (in range <= 16m) and 25m (out of range > 16m)
        ServerPlayer nearPlayer = mock(ServerPlayer.class);
        when(nearPlayer.level()).thenReturn(level);
        when(nearPlayer.distanceToSqr(speaker)).thenReturn(10.0 * 10.0);

        ServerPlayer farPlayer = mock(ServerPlayer.class);
        when(farPlayer.level()).thenReturn(level);
        when(farPlayer.distanceToSqr(speaker)).thenReturn(25.0 * 25.0);

        when(playerList.getPlayers()).thenReturn(List.of(nearPlayer, farPlayer));

        // Peer agents: 12m (in range <= 20m) and 25m (out of range > 20m)
        UUID nearPeerUuid = UUID.randomUUID();
        AgentEntity nearPeer = mock(AgentEntity.class);
        when(nearPeer.getAgentId()).thenReturn("near-peer");
        when(nearPeer.getUUID()).thenReturn(nearPeerUuid);
        when(nearPeer.level()).thenReturn(level);
        when(nearPeer.distanceToSqr(speaker)).thenReturn(12.0 * 12.0);

        UUID farPeerUuid = UUID.randomUUID();
        AgentEntity farPeer = mock(AgentEntity.class);
        when(farPeer.getAgentId()).thenReturn("far-peer");
        when(farPeer.getUUID()).thenReturn(farPeerUuid);
        when(farPeer.level()).thenReturn(level);
        when(farPeer.distanceToSqr(speaker)).thenReturn(25.0 * 25.0);

        // Session peer (also at 15m via sessions map, plus duplicate nearPeer in sessions to verify deduplication)
        UUID sessionPeerUuid = UUID.randomUUID();
        AgentEntity sessionPeer = mock(AgentEntity.class);
        when(sessionPeer.getAgentId()).thenReturn("session-peer");
        when(sessionPeer.getUUID()).thenReturn(sessionPeerUuid);
        when(sessionPeer.level()).thenReturn(level);
        when(sessionPeer.distanceToSqr(speaker)).thenReturn(15.0 * 15.0);

        // Register speaker, nearPeer, farPeer, and sessionPeer into actuator sessions
        actuator.submitBatch(speaker, IntentBatch.newBuilder().setAgentId("speaker-1").build());
        actuator.submitBatch(nearPeer, IntentBatch.newBuilder().setAgentId("near-peer").build());
        actuator.submitBatch(farPeer, IntentBatch.newBuilder().setAgentId("far-peer").build());
        actuator.submitBatch(sessionPeer, IntentBatch.newBuilder().setAgentId("session-peer").build());

        // Level entity query returns nearPeer and speaker (simulating entity query returning self too)
        when(level.getEntitiesOfClass(eq(AgentEntity.class), any(AABB.class), any()))
                .thenReturn(List.of(nearPeer));

        SendChatIntent intent = SendChatIntent.newBuilder()
                .setMessage("  Heading to the oak grove for timber.  ")
                .setTargetChannel(ChatChannel.CHAT_CHANNEL_LOCAL)
                .build();

        assertTrue(actuator.actuateSendChat(speaker, intent));

        // 1. Human player at 10m receives chat; human player at 25m does NOT
        ArgumentCaptor<Component> msgCaptor = ArgumentCaptor.forClass(Component.class);
        verify(nearPlayer, times(1)).sendSystemMessage(msgCaptor.capture());
        assertEquals("<SpeakerAgent> Heading to the oak grove for timber.", msgCaptor.getValue().getString());
        verify(farPlayer, never()).sendSystemMessage(any());

        // 2. Peer agent at 12m receives ChatEvent exactly once (deduplicated across level & sessions)
        ArgumentCaptor<ChatEvent> nearChatCaptor = ArgumentCaptor.forClass(ChatEvent.class);
        verify(nearPeer, times(1)).queueIncomingChat(nearChatCaptor.capture());
        ChatEvent nearEvent = nearChatCaptor.getValue();
        assertEquals(speakerUuid.toString(), nearEvent.getSenderUuid());
        assertEquals("SpeakerAgent", nearEvent.getSenderName());
        assertEquals("Heading to the oak grove for timber.", nearEvent.getMessage());
        assertEquals(12.0f, nearEvent.getDistance(), 0.001f);

        // 3. Session peer at 15m receives ChatEvent
        ArgumentCaptor<ChatEvent> sessionChatCaptor = ArgumentCaptor.forClass(ChatEvent.class);
        verify(sessionPeer, times(1)).queueIncomingChat(sessionChatCaptor.capture());
        assertEquals(15.0f, sessionChatCaptor.getValue().getDistance(), 0.001f);

        // 4. Peer agent at 25m does NOT receive ChatEvent
        verify(farPeer, never()).queueIncomingChat(any());

        // 5. Speaker does NOT receive its own chat
        verify(speaker, never()).queueIncomingChat(any());
    }

    @Test
    @DisplayName("WAKE_UP batch without flush_periphery immediately preempts active SLEEP_IN_BED batch and clears sleeping")
    void testWakeUpPreemptsInFlightSleepBatchWithoutQueueDeadlock() {
        AgentEntity agent = mock(AgentEntity.class);
        Level level = mock(Level.class);
        BlockPos bedPos = new BlockPos(4, 64, 4);

        when(agent.getAgentId()).thenReturn("wake-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.level()).thenReturn(level);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(1.0);
        when(level.hasChunkAt(bedPos)).thenReturn(true);
        when(level.isNight()).thenReturn(true);

        BlockState headBedState = mock(BlockState.class);
        when(headBedState.is(net.minecraft.tags.BlockTags.BEDS)).thenReturn(true);
        when(headBedState.getBlock()).thenReturn(Blocks.WHITE_BED);
        when(headBedState.hasProperty(BedBlock.PART)).thenReturn(true);
        when(headBedState.getValue(BedBlock.PART)).thenReturn(BedPart.HEAD);
        when(headBedState.hasProperty(BedBlock.OCCUPIED)).thenReturn(true);
        when(headBedState.getValue(BedBlock.OCCUPIED)).thenReturn(false);
        when(level.getBlockState(bedPos)).thenReturn(headBedState);

        Inventory inventory = new Inventory(mock(Player.class));
        when(agent.getInventory()).thenReturn(inventory);
        when(agent.getMainHandItem()).thenReturn(ItemStack.EMPTY);
        when(agent.getOffhandItem()).thenReturn(ItemStack.EMPTY);

        AtomicBoolean sleepingState = new AtomicBoolean(false);
        when(agent.isSleeping()).thenAnswer(inv -> sleepingState.get());
        doAnswer(inv -> {
            sleepingState.set(true);
            return null;
        }).when(agent).startSleeping(any(BlockPos.class));
        doAnswer(inv -> {
            sleepingState.set(false);
            return null;
        }).when(agent).stopSleeping();

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        // 1. Submit SLEEP_IN_BED batch: session remains EXECUTING in MotorState.SLEEPING
        IntentBatch sleepBatch = IntentBatch.newBuilder()
                .setAgentId("wake-agent")
                .setBatchId(701L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("wake-agent")
                        .setInteractBlock(InteractBlockIntent.newBuilder()
                                .setPos(BlockPosProto.newBuilder().setX(4).setY(64).setZ(4).build())
                                .setInteractionType(InteractBlockIntent.InteractionType.SLEEP_IN_BED)
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, sleepBatch);
        assertTrue(agent.isSleeping(), "Agent must be sleeping after SLEEP_IN_BED");
        assertInstanceOf(com.civquest.mod.actuator.motor.Sleep.class, fsm.topMotor().orElse(null));
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING,
                actuator.getCurrentReceipt(agent).getStatus());

        // 2. Submit WAKE_UP batch with flush_periphery=false -> must wake immediately, NOT queue behind sleep batch
        IntentBatch wakeBatch = IntentBatch.newBuilder()
                .setAgentId("wake-agent")
                .setBatchId(702L)
                .setFlushPeriphery(false)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("wake-agent")
                        .setInteractBlock(InteractBlockIntent.newBuilder()
                                .setPos(BlockPosProto.newBuilder().setX(4).setY(64).setZ(4).build())
                                .setInteractionType(InteractBlockIntent.InteractionType.WAKE_UP)
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, wakeBatch);

        assertFalse(agent.isSleeping(), "WAKE_UP must immediately clear isSleeping()");
        assertTrue(fsm.isMotorIdle());
        assertEquals(0, actuator.getQueuedBatchCount(agent), "WAKE_UP must not queue behind sleeping batch");
        IntentReceipt wakeReceipt = actuator.getCurrentReceipt(agent);
        assertEquals(702L, wakeReceipt.getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, wakeReceipt.getStatus());
        verify(agent, atLeastOnce()).stopSleeping();
    }

    @Test
    @DisplayName("2x2 pocket CraftItemIntent executes out-of-band during in-flight NavigateIntent without dropping")
    void testPocketCraftExecutesOutOfBandDuringInFlightNavigate() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        when(agent.getServer()).thenReturn(server);
        AgentNavigator nav = mock(AgentNavigator.class);
        when(agent.getAgentId()).thenReturn("craft-oob-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);

        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.OAK_LOG, 4));
        when(agent.getInventory()).thenReturn(inv);

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);

        IntentBatch navBatch = IntentBatch.newBuilder()
                .setAgentId("craft-oob-agent")
                .setBatchId(901L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("craft-oob-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(-34.5).setY(94.0).setZ(14.5).build())
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, navBatch);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, actuator.getCurrentReceipt(agent).getStatus());

        // Submit 2x2 pocket CraftItemIntent(minecraft:oak_planks) with flush_periphery=false while navigating
        IntentBatch craftBatch = IntentBatch.newBuilder()
                .setAgentId("craft-oob-agent")
                .setBatchId(902L)
                .setFlushPeriphery(false)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("craft-oob-agent")
                        .setCraftItem(CraftItemIntent.newBuilder()
                                .setRecipeId("minecraft:oak_planks")
                                .setBatchCount(1)
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, craftBatch);

        assertEquals(4, inv.countItem(Items.OAK_PLANKS), "2x2 pocket craft must execute immediately without waiting behind NavigateIntent");
        assertEquals(3, inv.countItem(Items.OAK_LOG));
        assertEquals(0, actuator.getQueuedBatchCount(agent));
        assertEquals(901L, actuator.getCurrentReceipt(agent).getBatchId(), "Active NavigateIntent receipt must stay intact");
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, actuator.getCurrentReceipt(agent).getStatus());
    }

    @Test
    @DisplayName("Queued batch behind NavigateIntent is not discarded when NavigateIntent fails stuck")
    void testQueuedBatchNotDroppedWhenNavigateStuckTimeoutFails() {
        AgentEntity agent = mock(AgentEntity.class);
        MinecraftServer server = mockServerWithRecipes();
        when(agent.getServer()).thenReturn(server);
        AgentNavigator nav = mock(AgentNavigator.class);
        when(agent.getAgentId()).thenReturn("stuck-queue-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);

        Inventory inv = new Inventory(agent);
        inv.setItem(0, new ItemStack(Items.BREAD, 2));
        when(agent.getInventory()).thenReturn(inv);
        net.minecraft.world.food.FoodData foodData = new net.minecraft.world.food.FoodData();
        foodData.setFoodLevel(10);
        when(agent.getFoodData()).thenReturn(foodData);

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);
        when(nav.isStuck()).thenReturn(false);

        IntentBatch navBatch = IntentBatch.newBuilder()
                .setAgentId("stuck-queue-agent")
                .setBatchId(911L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("stuck-queue-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(50).setY(64).setZ(50).build())
                                .build())
                        .build())
                .build();
        IntentBatch eatBatch = IntentBatch.newBuilder()
                .setAgentId("stuck-queue-agent")
                .setBatchId(912L)
                .setFlushPeriphery(false)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("stuck-queue-agent")
                        .setUseItem(UseItemIntent.newBuilder()
                                .setInventorySlot(0)
                                .setIsEatingOrDrinking(true)
                                .build())
                        .build())
                .build();

        actuator.submitBatch(agent, navBatch);
        actuator.submitBatch(agent, eatBatch);
        assertEquals(1, actuator.getQueuedBatchCount(agent));

        // Navigator STUCK -> Move ends FAILED immediately (ADR-037) -> queued batch advances
        when(nav.isStuck()).thenReturn(true);
        fsm.tick(); // MOVING -> IDLE (failed, facts captured)
        assertTrue(fsm.isMotorIdle());
        actuator.tick(agent);
        actuator.tick(agent);

        IntentReceipt receipt = actuator.getCurrentReceipt(agent);
        assertEquals(912L, receipt.getBatchId(), "Queued batch must advance and execute after prior NavigateIntent fails");
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, receipt.getStatus());
        assertEquals(1, inv.countItem(Items.BREAD), "Queued UseItemIntent must have consumed 1 bread instead of being dropped");
        assertTrue(foodData.getFoodLevel() > 10);
        assertEquals(0, actuator.getQueuedBatchCount(agent));
    }

    @Test
    @DisplayName("SurveyHorizonIntent executes asynchronously as RECEIPT_STATUS_EXECUTING while SURVEYING and completes upon IDLE")
    void testSurveyHorizonBatchAsyncLifecycle() {
        AgentEntity agent = mock(AgentEntity.class);
        AgentNavigator nav = mock(AgentNavigator.class);
        when(agent.getAgentId()).thenReturn("survey-batch-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getEyePosition()).thenReturn(new net.minecraft.world.phys.Vec3(0.5, 65.62, 0.5));

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        AgentIntent surveyIntent = AgentIntent.newBuilder()
                .setAgentId("survey-batch-agent")
                .setSequenceId(501L)
                .setSurveyHorizon(com.civquest.mod.grpc.v1.SurveyHorizonIntent.newBuilder()
                        .setNumSectors(4)
                        .setMaxRange(48.0f)
                        .setTicksPerSector(2)
                        .build())
                .build();
        assertTrue(IntentActuator.isAsyncMotorIntent(surveyIntent));

        IntentBatch batch = IntentBatch.newBuilder()
                .setAgentId("survey-batch-agent")
                .setBatchId(950L)
                .addIntents(surveyIntent)
                .build();

        actuator.submitBatch(agent, batch);
        assertTrue(fsm.isSweeping());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING,
                actuator.getCurrentReceipt(agent).getStatus());

        // Advance ticks until survey finishes (4 sectors * 2 ticks/sector = 7 ticks to scan sector 3)
        for (int t = 0; t < 6; t++) {
            fsm.tick();
            actuator.tick(agent);
            assertTrue(fsm.isSweeping());
            assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING,
                    actuator.getCurrentReceipt(agent).getStatus());
        }

        // 7th tick completes sector 3 -> IDLE -> batch receipt becomes COMPLETED
        fsm.tick();
        actuator.tick(agent);
        assertTrue(fsm.isMotorIdle());
        IntentReceipt completed = actuator.getCurrentReceipt(agent);
        assertEquals(950L, completed.getBatchId());
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_COMPLETED, completed.getStatus());
        verify(agent, times(1)).queueHorizonSurveyReport(any());
    }

    @Test
    @DisplayName("Duplicate SurveyHorizonIntent is deduplicated while SURVEYING, while non-survey and flushPeriphery batches preserve queueing/flushing")
    void testSurveyHorizonDeduplicationPreservesNonSurveyAndFlush() {
        AgentEntity agent = mock(AgentEntity.class);
        AgentNavigator nav = mock(AgentNavigator.class);
        when(agent.getAgentId()).thenReturn("survey-dedup-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getEyePosition()).thenReturn(new net.minecraft.world.phys.Vec3(0.5, 65.62, 0.5));

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);

        AgentIntent surveyIntent = AgentIntent.newBuilder()
                .setAgentId("survey-dedup-agent")
                .setSequenceId(1L)
                .setSurveyHorizon(com.civquest.mod.grpc.v1.SurveyHorizonIntent.newBuilder()
                        .setNumSectors(4)
                        .setMaxRange(48.0f)
                        .setTicksPerSector(2)
                        .build())
                .build();
        actuator.submitBatch(agent, IntentBatch.newBuilder()
                .setAgentId("survey-dedup-agent")
                .setBatchId(1L)
                .addIntents(surveyIntent)
                .build());
        assertTrue(fsm.isSweeping());

        // Duplicate survey-only batch without flushPeriphery must be deduplicated
        actuator.submitBatch(agent, IntentBatch.newBuilder()
                .setAgentId("survey-dedup-agent")
                .setBatchId(2L)
                .addIntents(surveyIntent.toBuilder().setSequenceId(2L).build())
                .build());
        assertEquals(0, actuator.getQueuedBatchCount(agent));
        assertEquals(1L, actuator.getCurrentReceipt(agent).getBatchId());

        // Non-survey NavigateIntent batch must still queue behind active survey
        AgentIntent navIntent = AgentIntent.newBuilder()
                .setAgentId("survey-dedup-agent")
                .setSequenceId(3L)
                .setNavigate(NavigateIntent.newBuilder()
                        .setTarget(Vec3.newBuilder().setX(10).setY(65).setZ(10).build())
                        .setArrivalTolerance(1)
                        .build())
                .build();
        actuator.submitBatch(agent, IntentBatch.newBuilder()
                .setAgentId("survey-dedup-agent")
                .setBatchId(3L)
                .addIntents(navIntent)
                .build());
        assertEquals(1, actuator.getQueuedBatchCount(agent));

        // Explicit flushPeriphery=true survey batch must replace active/queued batches cleanly
        actuator.submitBatch(agent, IntentBatch.newBuilder()
                .setAgentId("survey-dedup-agent")
                .setBatchId(4L)
                .setFlushPeriphery(true)
                .addIntents(surveyIntent.toBuilder().setSequenceId(4L).build())
                .build());
        assertEquals(0, actuator.getQueuedBatchCount(agent));
        assertEquals(4L, actuator.getCurrentReceipt(agent).getBatchId());
        assertTrue(fsm.isSweeping());
    }

    @Test
    @DisplayName("Navigate-only batches coalesce in pendingBatches while preserving non-navigate and compound batches")
    void testNavigateOnlyBatchCoalescingPreservesNonNavigateBatches() {
        AgentEntity agent = mock(AgentEntity.class);
        AgentNavigator nav = mock(AgentNavigator.class);
        Level level = mock(Level.class);
        when(agent.getAgentId()).thenReturn("coalesce-unit-agent");
        when(agent.isAlive()).thenReturn(true);
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.level()).thenReturn(level);

        com.civquest.mod.actuator.motor.AgentMotorFSM fsm = new com.civquest.mod.actuator.motor.AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);

        // 1. Start active navigation batch
        IntentBatch activeNav = IntentBatch.newBuilder()
                .setAgentId("coalesce-unit-agent")
                .setBatchId(1001L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("coalesce-unit-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(40).setY(64).setZ(40).build())
                                .build())
                        .build())
                .build();
        actuator.submitBatch(agent, activeNav);
        assertEquals(IntentReceipt.ReceiptStatus.RECEIPT_STATUS_EXECUTING, actuator.getCurrentReceipt(agent).getStatus());

        // 2. Queue non-navigate InteractBlockIntent (RING_BELL) and first navigate-only batch
        IntentBatch bellBatch = IntentBatch.newBuilder()
                .setAgentId("coalesce-unit-agent")
                .setBatchId(1002L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("coalesce-unit-agent")
                        .setInteractBlock(InteractBlockIntent.newBuilder()
                                .setPos(BlockPosProto.newBuilder().setX(1).setY(64).setZ(1).build())
                                .setInteractionType(InteractBlockIntent.InteractionType.RING_BELL)
                                .build())
                        .build())
                .build();
        IntentBatch queuedNav1 = IntentBatch.newBuilder()
                .setAgentId("coalesce-unit-agent")
                .setBatchId(1003L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("coalesce-unit-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(10).setY(64).setZ(10).build())
                                .build())
                        .build())
                .build();
        IntentBatch queuedNav2 = IntentBatch.newBuilder()
                .setAgentId("coalesce-unit-agent")
                .setBatchId(1004L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("coalesce-unit-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(20).setY(64).setZ(20).build())
                                .build())
                        .build())
                .build();
        IntentBatch queuedNav3 = IntentBatch.newBuilder()
                .setAgentId("coalesce-unit-agent")
                .setBatchId(1005L)
                .addIntents(AgentIntent.newBuilder()
                        .setAgentId("coalesce-unit-agent")
                        .setNavigate(NavigateIntent.newBuilder()
                                .setTarget(Vec3.newBuilder().setX(30).setY(64).setZ(30).build())
                                .build())
                        .build())
                .build();

        actuator.submitBatch(agent, bellBatch);
        actuator.submitBatch(agent, queuedNav1);
        actuator.submitBatch(agent, queuedNav2);
        actuator.submitBatch(agent, queuedNav3);

        // bellBatch + latest coalesced navigate-only batch (1005L) = 2 batches
        assertEquals(2, actuator.getQueuedBatchCount(agent),
                "Multiple queued navigate-only batches must coalesce to 1 while preserving non-navigate batches");

        // 3. Complete active navigation -> bellBatch (1002L) runs first, then 1005L starts
        when(nav.isPathing()).thenReturn(false);
        when(nav.isArrived()).thenReturn(true);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(1.0);
        when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(level.getBlockState(any(BlockPos.class))).thenReturn(Blocks.AIR.defaultBlockState());

        fsm.tick();
        when(nav.isPathing()).thenReturn(true);
        when(nav.isArrived()).thenReturn(false);
        actuator.tick(agent);

        assertEquals(1005L, actuator.getCurrentReceipt(agent).getBatchId(),
                "After bellBatch completes, the coalesced latest NavigateIntent batch (1005L) must execute");
        assertEquals(0, actuator.getQueuedBatchCount(agent));
    }
}
