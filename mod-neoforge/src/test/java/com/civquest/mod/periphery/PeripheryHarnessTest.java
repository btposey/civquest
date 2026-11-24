package com.civquest.mod.periphery;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.ChatEvent;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.DamageEvent;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.grpc.v1.VisibleBlock;
import com.civquest.mod.grpc.v1.VisibleEntity;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import net.minecraft.core.BlockPos;
import net.minecraft.network.chat.Component;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.entity.item.ItemEntity;
import net.minecraft.world.entity.monster.Creeper;
import net.minecraft.world.entity.monster.Zombie;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.BlockStateProperties;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.shapes.Shapes;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

/**
 * Periphery simulation test suite verifying System 1 actuation, sensory frustum scanning,
 * damage reflexes, drop attribution, and IPC streaming in a mock-driven JUnit 5 harness.
 */
public class PeripheryHarnessTest {

    private PeripherySimulationHarness harness;
    private AgentEntity agent;
    private static final String AGENT_ID = "agent-periphery-01";
    private static final String DISPLAY_NAME = "AgentAria";

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
        harness = new PeripherySimulationHarness();
        agent = harness.createMockAgent(AGENT_ID, DISPLAY_NAME, new BlockPos(0, 64, 0));
        harness.connectAgentStream(AGENT_ID);
        harness.clearReceivedFrames();
    }

    @Nested
    @DisplayName("Periphery Actuation Validation")
    class ActuationValidation {

        @Test
        @DisplayName("PLACE_BLOCK rejects placement when agent AABB collides with target block and retains inventory")
        void testPlaceBlockAgentCollisionRejection() {
            BlockPos targetPos = new BlockPos(0, 64, 0); // Directly at agent's position
            harness.setBlock(targetPos, Blocks.AIR.defaultBlockState());

            // Agent inventory with 5 cobblestone
            ItemStack cobbleStack = new ItemStack(Items.COBBLESTONE, 5);
            agent.getInventory().setItem(0, cobbleStack);

            InteractBlockIntent placeIntent = InteractBlockIntent.newBuilder()
                    .setPos(BlockPosProto.newBuilder().setX(0).setY(64).setZ(0).build())
                    .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                    .setBlockOrItemId("minecraft:cobblestone")
                    .build();

            boolean result = harness.getIntentActuator().actuateInteractBlock(agent, placeIntent);

            assertFalse(result, "PLACE_BLOCK must fail when target block intersects agent bounding box");
            assertEquals(5, cobbleStack.getCount(), "Inventory cobblestone must be retained on collision rejection");
            assertEquals(Blocks.AIR.defaultBlockState(), harness.getBlock(targetPos), "Block must not be placed");
        }

        @Test
        @DisplayName("PLACE_BLOCK rejects placement when another living entity AABB collides with target block and retains inventory")
        void testPlaceBlockEntityCollisionRejection() {
            BlockPos targetPos = new BlockPos(2, 64, 0);
            harness.setBlock(targetPos, Blocks.AIR.defaultBlockState());

            // Living entity standing at target block
            LivingEntity zombie = mock(Zombie.class);
            when(zombie.isAlive()).thenReturn(true);
            when(zombie.isSpectator()).thenReturn(false);
            when(zombie.getBoundingBox()).thenReturn(new AABB(1.7, 64.0, -0.3, 2.3, 65.8, 0.3));
            harness.spawnEntity(zombie);

            ItemStack cobbleStack = new ItemStack(Items.COBBLESTONE, 5);
            agent.getInventory().setItem(0, cobbleStack);

            InteractBlockIntent placeIntent = InteractBlockIntent.newBuilder()
                    .setPos(BlockPosProto.newBuilder().setX(2).setY(64).setZ(0).build())
                    .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                    .setBlockOrItemId("minecraft:cobblestone")
                    .build();

            boolean result = harness.getIntentActuator().actuateInteractBlock(agent, placeIntent);

            assertFalse(result, "PLACE_BLOCK must fail when another living entity collides with target block");
            assertEquals(5, cobbleStack.getCount(), "Inventory item must be retained when placement is rejected");
            assertEquals(Blocks.AIR.defaultBlockState(), harness.getBlock(targetPos));
        }

        @Test
        @DisplayName("PLACE_BLOCK succeeds when space is clear, places block in world, and decrements inventory")
        void testPlaceBlockSucceedsWhenSpaceClear() {
            BlockPos targetPos = new BlockPos(2, 64, 0);
            harness.setBlock(targetPos, Blocks.AIR.defaultBlockState());

            ItemStack cobbleStack = new ItemStack(Items.COBBLESTONE, 5);
            agent.getInventory().setItem(0, cobbleStack);

            InteractBlockIntent placeIntent = InteractBlockIntent.newBuilder()
                    .setPos(BlockPosProto.newBuilder().setX(2).setY(64).setZ(0).build())
                    .setInteractionType(InteractBlockIntent.InteractionType.PLACE_BLOCK)
                    .setBlockOrItemId("minecraft:cobblestone")
                    .build();

            boolean result = harness.getIntentActuator().actuateInteractBlock(agent, placeIntent);

            assertTrue(result, "PLACE_BLOCK must succeed when destination space is unobstructed");
            assertEquals(4, cobbleStack.getCount(), "One block must be consumed from inventory");
            assertEquals(Blocks.COBBLESTONE.defaultBlockState(), harness.getBlock(targetPos), "Cobblestone must be placed in world");
        }

        @Test
        @DisplayName("CRAFT_ITEM 2x2 recipes succeed anywhere without requiring a crafting table")
        void testCraft2x2RecipesSucceedAnywhere() {
            // 1. Planks from log
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_LOG, 1));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").build()),
                    "2x2 planks craft must succeed anywhere");
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.OAK_PLANKS) && s.getCount() >= 4));
            assertEquals(0, agent.getInventory().countItem(Items.OAK_LOG));

            // 2. Stick from planks (uses authoritative recipe id minecraft:stick)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 2));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:stick").build()),
                    "2x2 stick craft must succeed anywhere");
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.STICK) && s.getCount() >= 4));
            assertEquals(0, agent.getInventory().countItem(Items.OAK_PLANKS));

            // 3. Crafting table from planks
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 4));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:crafting_table").build()),
                    "2x2 crafting_table craft must succeed anywhere");
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.CRAFTING_TABLE)));
            assertEquals(0, agent.getInventory().countItem(Items.OAK_PLANKS));

            // 4. Torch from coal and stick (and shortcut from 1 log must fail)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_LOG, 1));
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:torch").build()),
                    "Torch craft from single log shortcut must fail");
            assertEquals(1, agent.getInventory().countItem(Items.OAK_LOG));

            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.COAL, 1));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 1));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:torch").build()),
                    "2x2 torch craft must succeed anywhere");
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.TORCH) && s.getCount() >= 4));
        }

        @Test
        @DisplayName("CRAFT_ITEM 3x3 recipes fail when crafting table is >4.5m away or ingredients are missing")
        void testCraft3x3RecipesFailWithoutCraftingTableWithinReach() {
            // Place crafting table at (5, 64, 0), distance squared = 25.0 > 20.25
            harness.setBlock(new BlockPos(5, 64, 0), Blocks.CRAFTING_TABLE.defaultBlockState());

            // Prepare all ingredients for 3x3 recipes
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_LOG, 10));
            agent.getInventory().setItem(1, new ItemStack(Items.OAK_PLANKS, 32));
            agent.getInventory().setItem(2, new ItemStack(Items.STICK, 16));
            agent.getInventory().setItem(3, new ItemStack(Items.COBBLESTONE, 32));
            agent.getInventory().setItem(4, new ItemStack(Items.COAL, 4));

            // Campfire (3x3) must fail without crafting table within reach
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()));

            // Wooden tool recipes
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_pickaxe").build()));
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_sword").build()));
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));

            // Stone tool recipes
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_pickaxe").build()));
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_sword").build()));
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_axe").build()));

            // Structure / container recipes
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_door").build()));
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:chest").build()));

            // Verify inventory untouched after rejections
            assertEquals(10, agent.getInventory().countItem(Items.OAK_LOG));
            assertEquals(32, agent.getInventory().countItem(Items.OAK_PLANKS));
            assertEquals(16, agent.getInventory().countItem(Items.STICK));
            assertEquals(4, agent.getInventory().countItem(Items.COAL));

            // Place crafting table within reach and verify missing-ingredient failures
            harness.setBlock(new BlockPos(1, 64, 0), Blocks.CRAFTING_TABLE.defaultBlockState());
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_LOG, 3));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 3)); // missing coal
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()),
                    "Campfire must fail when coal/charcoal is missing");
            assertEquals(3, agent.getInventory().countItem(Items.OAK_LOG));
            assertEquals(3, agent.getInventory().countItem(Items.STICK));

            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 4)); // missing sticks
            assertFalse(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()),
                    "Wooden axe must fail when sticks are missing");
            assertEquals(4, agent.getInventory().countItem(Items.OAK_PLANKS));
        }

        @Test
        @DisplayName("CRAFT_ITEM 3x3 recipes succeed when crafting table is <=4.5m away (within reach)")
        void testCraft3x3RecipesSucceedWithCraftingTableWithinReach() {
            // Place crafting table at (1, 64, 0) -> distance squared = 1.0 <= 20.25
            harness.setBlock(new BlockPos(1, 64, 0), Blocks.CRAFTING_TABLE.defaultBlockState());

            // 1. Campfire (3 logs + 3 sticks + 1 coal)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_LOG, 3));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 3));
            agent.getInventory().setItem(2, new ItemStack(Items.COAL, 1));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:campfire").build()));
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.CAMPFIRE)));
            assertEquals(0, agent.getInventory().countItem(Items.OAK_LOG));
            assertEquals(0, agent.getInventory().countItem(Items.STICK));
            assertEquals(0, agent.getInventory().countItem(Items.COAL));

            // 2. Wooden Pickaxe (3 planks + 2 sticks)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 3));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 2));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_pickaxe").build()));
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.WOODEN_PICKAXE)));

            // 3. Wooden Sword (2 planks + 1 stick)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 2));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 1));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_sword").build()));
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.WOODEN_SWORD)));

            // 4. Wooden Axe (3 planks + 2 sticks)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 3));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 2));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:wooden_axe").build()));
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.WOODEN_AXE)));

            // 5. Stone Pickaxe (3 cobble + 2 sticks)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.COBBLESTONE, 3));
            agent.getInventory().setItem(1, new ItemStack(Items.STICK, 2));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:stone_pickaxe").build()));
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.STONE_PICKAXE)));

            // 6. Oak Door (6 oak planks -> 3 oak doors)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 6));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_door").build()));
            assertEquals(3, agent.getInventory().countItem(Items.OAK_DOOR));

            // 7. Chest (8 planks -> 1 chest)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_PLANKS, 8));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:chest").build()));
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.CHEST)));

            // 8. Cake (3 milk buckets + 2 sugar + 1 egg + 3 wheat -> 1 cake + 3 empty buckets)
            agent.getInventory().clearContent();
            agent.getInventory().setItem(0, new ItemStack(Items.MILK_BUCKET, 1));
            agent.getInventory().setItem(1, new ItemStack(Items.MILK_BUCKET, 1));
            agent.getInventory().setItem(2, new ItemStack(Items.MILK_BUCKET, 1));
            agent.getInventory().setItem(3, new ItemStack(Items.SUGAR, 2));
            agent.getInventory().setItem(4, new ItemStack(Items.EGG, 1));
            agent.getInventory().setItem(5, new ItemStack(Items.WHEAT, 3));
            assertTrue(harness.getIntentActuator().actuateCraftItem(agent,
                    CraftItemIntent.newBuilder().setRecipeId("minecraft:cake").build()));
            assertEquals(1, agent.getInventory().countItem(Items.CAKE));
            assertEquals(3, agent.getInventory().countItem(Items.BUCKET));
            assertEquals(0, agent.getInventory().countItem(Items.MILK_BUCKET));
        }

        @Test
        @DisplayName("HARVEST_BREAK breaks block and applies tool durability damage")
        void testHarvestBreakAndToolDamageApplication() {
            BlockPos targetPos = new BlockPos(1, 64, 0);
            harness.setBlock(targetPos, Blocks.STONE.defaultBlockState());

            // Equip an undamaged wooden pickaxe in inventory
            ItemStack woodenPickaxe = new ItemStack(Items.WOODEN_PICKAXE);
            assertEquals(0, woodenPickaxe.getDamageValue(), "Pickaxe starts undamaged");
            agent.getInventory().setItem(0, woodenPickaxe);
            agent.getInventory().selected = 0;

            InteractBlockIntent harvestIntent = InteractBlockIntent.newBuilder()
                    .setPos(BlockPosProto.newBuilder().setX(1).setY(64).setZ(0).build())
                    .setInteractionType(InteractBlockIntent.InteractionType.HARVEST_BREAK)
                    .build();

            boolean started = harness.getIntentActuator().actuateInteractBlock(agent, harvestIntent);
            assertTrue(started, "Harvest break must start successfully on breakable stone block");
            assertTrue(agent.getBreaker().isBreaking());

            // Advance breaker ticks until block breaks
            BlockBreaker breaker = agent.getBreaker();
            boolean broken = false;
            for (int i = 0; i < 200 && !broken; i++) {
                broken = breaker.tick();
            }

            assertTrue(broken, "Block must eventually break from accumulated progress");
            assertEquals(Blocks.AIR.defaultBlockState(), harness.getBlock(targetPos), "Broken stone must be removed from world");

            // Verify tool damage applied: damageValue > 0
            ItemStack heldTool = agent.getMainHandItem();
            assertTrue(heldTool.is(Items.WOODEN_PICKAXE));
            assertTrue(heldTool.getDamageValue() > 0, "Wooden pickaxe must have durability damage applied");
        }
    }

    @Nested
    @DisplayName("Periphery Sensory & Stimulus Validation")
    class SensoryAndStimulusValidation {

        @Test
        @DisplayName("SensoryFrustumCollector scans block voxels and captures interesting blocks and farmland survey")
        void testSensoryFrustumBlockScanning() {
            // Place interesting blocks around agent
            BlockPos furnacePos = new BlockPos(2, 64, 0);
            BlockPos chestPos = new BlockPos(0, 64, 2);
            BlockPos farmlandPos = new BlockPos(1, 63, 1);
            BlockPos cropPos = new BlockPos(1, 64, 1);

            harness.setBlock(furnacePos, Blocks.FURNACE.defaultBlockState());
            harness.setBlock(chestPos, Blocks.CHEST.defaultBlockState());
            harness.setBlock(farmlandPos, Blocks.FARMLAND.defaultBlockState());
            harness.setBlock(cropPos, Blocks.WHEAT.defaultBlockState().setValue(BlockStateProperties.AGE_7, 7));

            TelemetryFrame frame = harness.getSensoryCollector().collectFrame(agent, null, null);

            assertNotNull(frame);
            assertTrue(frame.getInterestingBlocksCount() > 0, "Frame must contain interesting blocks");

            List<String> blockIds = frame.getInterestingBlocksList().stream()
                    .map(VisibleBlock::getBlockId)
                    .toList();

            assertTrue(blockIds.contains("minecraft:furnace"), "Must detect furnace block");
            assertTrue(blockIds.contains("minecraft:chest"), "Must detect chest block");
            assertTrue(blockIds.contains("minecraft:farmland"), "Must detect farmland block");

            // Verify farmland survey populated
            assertTrue(frame.hasFarmlandSurvey(), "Farmland survey must be populated");
            assertEquals(1, frame.getFarmlandSurvey().getTotalFarmlandBlocks());
            assertEquals(1, frame.getFarmlandSurvey().getMatureCropCount());
            assertEquals("minecraft:wheat", frame.getFarmlandSurvey().getPrimaryCropId());
        }

        @Test
        @DisplayName("SensoryFrustumCollector accurately detects voxel light level")
        void testSensoryLightLevelDetection() {
            BlockPos sunnyPos = new BlockPos(10, 64, 10);
            BlockPos darkCavePos = new BlockPos(0, 20, 0);

            harness.setLightLevel(sunnyPos, 15);
            harness.setLightLevel(darkCavePos, 2);

            assertEquals(15, SensoryFrustumCollector.getLightLevel(harness.getMockLevel(), sunnyPos));
            assertEquals(2, SensoryFrustumCollector.getLightLevel(harness.getMockLevel(), darkCavePos));
            assertEquals(0, SensoryFrustumCollector.getLightLevel(null, sunnyPos));
        }

        @Test
        @DisplayName("SensoryFrustumCollector detects hostile mobs within frustum cone and respects line of sight")
        void testHostileMobDetectionAndFrustumLOS() {
            // 1. Visible Creeper 5 blocks away
            Creeper creeper = mock(Creeper.class);
            when(creeper.getId()).thenReturn(101);
            when(creeper.getUUID()).thenReturn(UUID.randomUUID());
            when(creeper.isAlive()).thenReturn(true);
            when(creeper.isRemoved()).thenReturn(false);
            when(creeper.getX()).thenReturn(5.5);
            when(creeper.getY()).thenReturn(64.0);
            when(creeper.getZ()).thenReturn(0.5);
            when(creeper.getEyePosition()).thenReturn(new net.minecraft.world.phys.Vec3(5.5, 65.5, 0.5));
            when(creeper.getName()).thenReturn(Component.literal("Creeper"));
            doReturn(net.minecraft.world.entity.EntityType.CREEPER).when(creeper).getType();
            when(creeper.getHealth()).thenReturn(20.0f);
            when(creeper.isIgnited()).thenReturn(true); // hissing

            harness.spawnEntity(creeper);

            TelemetryFrame frame = harness.getSensoryCollector().collectFrame(agent, null, null);
            assertEquals(1, frame.getVisibleEntitiesCount());

            VisibleEntity visibleCreeper = frame.getVisibleEntities(0);
            assertEquals("minecraft:creeper", visibleCreeper.getEntityType());
            assertTrue(visibleCreeper.getIsHostile());
            assertEquals(30, visibleCreeper.getFuseTicks(), "Ignited creeper must report 30 fuse ticks");
            assertEquals(5.0f, visibleCreeper.getDistance(), 0.01);

            // 2. Block line of sight with solid wall
            harness.setLineOfSightBlocked(true);
            TelemetryFrame blockedFrame = harness.getSensoryCollector().collectFrame(agent, null, null);
            assertEquals(0, blockedFrame.getVisibleEntitiesCount(), "Entity must be hidden when LOS is obstructed");
        }

        @Test
        @DisplayName("Simulated LivingDamageEvent emits immediate sub-tick TelemetryFrame with DamageEvent")
        void testDamageReflexStimulusImmediateStreaming() {
            harness.clearReceivedFrames();

            DamageSource damageSource = mock(DamageSource.class);
            harness.simulateDamage(agent, damageSource, 6.5f);

            TelemetryFrame frame = harness.getLatestFrame();
            assertNotNull(frame, "TelemetryFrame must be streamed immediately upon damage reflex");
            assertTrue(frame.hasLastDamageTaken(), "Frame must contain last_damage_taken");

            DamageEvent damageEvent = frame.getLastDamageTaken();
            assertEquals(6.5f, damageEvent.getDamageAmount(), 0.01);
        }

        @Test
        @DisplayName("Simulated lethal damage emits immediate terminal TelemetryFrame with DeathEvent")
        void testDeathReflexTerminalStreaming() {
            harness.clearReceivedFrames();

            DamageSource lethalSource = mock(DamageSource.class);
            harness.simulateDeath(agent, lethalSource);

            TelemetryFrame frame = harness.getLatestFrame();
            assertNotNull(frame, "Terminal TelemetryFrame must be streamed upon lethal damage");
            assertTrue(frame.hasDeathEvent(), "Frame must contain DeathEvent");

            DeathEvent death = frame.getDeathEvent();
            assertEquals(AGENT_ID, death.getAgentId());
        }

        @Test
        @DisplayName("Item collection: natural ground drops are collected silently without player chat gratitude")
        void testItemPickupNaturalDropAttribution() {
            ItemEntity naturalDrop = mock(ItemEntity.class);
            ItemStack seeds = new ItemStack(Items.WHEAT_SEEDS, 3);
            when(naturalDrop.isAlive()).thenReturn(true);
            when(naturalDrop.hasPickUpDelay()).thenReturn(false);
            when(naturalDrop.getItem()).thenReturn(seeds);
            when(naturalDrop.getBoundingBox()).thenReturn(new AABB(0.4, 64.0, 0.4, 0.6, 64.2, 0.6));
            when(naturalDrop.getOwner()).thenReturn(null); // No thrower / natural drop

            harness.spawnEntity(naturalDrop);
            harness.performItemPickup(agent);

            // Seeds collected into agent inventory
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.WHEAT_SEEDS) && s.getCount() == 3));
            verify(naturalDrop).discard();

            // Assert NO chat events or messages were emitted
            List<ChatEvent> chats = agent.pollIncomingChats();
            assertTrue(chats.isEmpty(), "Natural ground drops must not emit gift chat events");
        }

        @Test
        @DisplayName("Item collection: player-thrown gifts emit gratitude chat and queue [DETERMINISTIC_GIFT]")
        void testItemPickupPlayerGiftAttribution() {
            ServerPlayer player = mock(ServerPlayer.class);
            UUID playerUuid = UUID.randomUUID();
            when(player.getUUID()).thenReturn(playerUuid);
            when(player.getName()).thenReturn(Component.literal("Brian"));
            when(player.getX()).thenReturn(2.0);
            when(player.getY()).thenReturn(64.0);
            when(player.getZ()).thenReturn(0.0);
            when(agent.distanceToSqr(player)).thenReturn(4.0);
            harness.addPlayer(player);

            ItemEntity giftItem = mock(ItemEntity.class);
            ItemStack ironIngots = new ItemStack(Items.IRON_INGOT, 2);
            when(giftItem.isAlive()).thenReturn(true);
            when(giftItem.hasPickUpDelay()).thenReturn(false);
            when(giftItem.getItem()).thenReturn(ironIngots);
            when(giftItem.getBoundingBox()).thenReturn(new AABB(0.4, 64.0, 0.4, 0.6, 64.2, 0.6));
            when(giftItem.getOwner()).thenReturn(player); // Thrower matches player

            harness.spawnEntity(giftItem);
            harness.performItemPickup(agent);

            // Item added to inventory
            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.IRON_INGOT) && s.getCount() == 2));
            verify(giftItem).discard();

            // Verify thank-you chat message was sent to player
            verify(player).sendSystemMessage(argThat(component ->
                    component.getString().contains("Thank you for the 2x Iron Ingot, Brian!")));

            // Verify [DETERMINISTIC_GIFT] was queued on agent
            List<ChatEvent> chats = agent.pollIncomingChats();
            assertEquals(1, chats.size());
            ChatEvent giftChat = chats.get(0);
            assertEquals(playerUuid.toString(), giftChat.getSenderUuid());
            assertEquals("Brian", giftChat.getSenderName());
            assertTrue(giftChat.getMessage().contains("[DETERMINISTIC_GIFT] Gifted 2x Iron Ingot to AgentAria."));
        }
    }

    @Nested
    @DisplayName("External IPC Actuation Loop Validation")
    class IpcActuationLoopValidation {

        @Test
        @DisplayName("AgentIntent dispatched through IPC stream actuates agent in mock harness")
        void testIpcStreamActuationLoop() {
            // Send CraftItemIntent over external IPC stream
            agent.getInventory().setItem(0, new ItemStack(Items.OAK_LOG, 1));

            AgentIntent craftIntent = AgentIntent.newBuilder()
                    .setAgentId(AGENT_ID)
                    .setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:oak_planks").build())
                    .build();

            harness.sendIntent(craftIntent);

            assertTrue(agent.getInventory().hasAnyMatching(s -> s.is(Items.OAK_PLANKS) && s.getCount() >= 4),
                    "Intent sent through IPC stream must actuate crafting logic on agent");
        }
    }
}
