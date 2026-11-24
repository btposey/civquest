package com.civquest.mod;

import com.civquest.mod.grpc.v1.ExportRecipeRequest;
import com.civquest.mod.grpc.v1.ExportRecipeResponse;
import com.civquest.mod.grpc.v1.RecipeIngredientSlot;
import com.civquest.mod.grpc.v1.RecipeNode;
import io.grpc.stub.StreamObserver;
import net.minecraft.core.NonNullList;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.MinecraftServer;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.crafting.ArmorDyeRecipe;
import net.minecraft.world.item.crafting.CookingBookCategory;
import net.minecraft.world.item.crafting.CraftingBookCategory;
import net.minecraft.world.item.crafting.Ingredient;
import net.minecraft.world.item.crafting.RecipeHolder;
import net.minecraft.world.item.crafting.RecipeManager;
import net.minecraft.world.item.crafting.ShapedRecipe;
import net.minecraft.world.item.crafting.ShapedRecipePattern;
import net.minecraft.world.item.crafting.ShapelessRecipe;
import net.minecraft.world.item.crafting.SmeltingRecipe;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Function;
import java.util.stream.Collectors;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

class RecipeCatalogExporterTest {

    private RegistryAccess.Frozen registryAccess;
    private RecipeManager recipeManager;

    @BeforeAll
    static void initMinecraft() {
        com.civquest.mod.test.MinecraftTestBootstrap.ensureBootstrapped();
    }

    @BeforeEach
    void setUp() {
        registryAccess = RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY);
        recipeManager = new RecipeManager(registryAccess);

        Ingredient logs = Ingredient.of(Items.OAK_LOG, Items.SPRUCE_LOG, Items.BIRCH_LOG);
        Ingredient planks = Ingredient.of(Items.OAK_PLANKS, Items.SPRUCE_PLANKS, Items.BIRCH_PLANKS);
        Ingredient sticks = Ingredient.of(Items.STICK);
        Ingredient coals = Ingredient.of(Items.COAL, Items.CHARCOAL);

        recipeManager.replaceRecipes(List.of(
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("birch_planks"),
                        new ShapelessRecipe("planks", CraftingBookCategory.BUILDING,
                                new ItemStack(Items.BIRCH_PLANKS, 4),
                                NonNullList.of(Ingredient.EMPTY, Ingredient.of(Items.BIRCH_LOG)))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("stick"),
                        new ShapedRecipe("sticks", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('#', planks), "#", "#"),
                                new ItemStack(Items.STICK, 4))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("wooden_axe"),
                        new ShapedRecipe("", CraftingBookCategory.EQUIPMENT,
                                ShapedRecipePattern.of(Map.of('X', planks, '#', sticks), "XX", "X#", " #"),
                                new ItemStack(Items.WOODEN_AXE, 1))),
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("campfire"),
                        new ShapedRecipe("", CraftingBookCategory.MISC,
                                ShapedRecipePattern.of(Map.of('S', sticks, 'C', coals, 'L', logs), " S ", "SCS", "LLL"),
                                new ItemStack(Items.CAMPFIRE, 1))),
                // Special dynamic recipe (empty result / no static ingredients) must be skipped
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("armor_dye"),
                        new ArmorDyeRecipe(CraftingBookCategory.MISC)),
                // Non-crafting smelting recipe must NOT be exported by ExportRecipeGraph
                new RecipeHolder<>(ResourceLocation.withDefaultNamespace("charcoal"),
                        new SmeltingRecipe("", CookingBookCategory.MISC,
                                Ingredient.of(Items.OAK_LOG),
                                new ItemStack(Items.CHARCOAL, 1), 0.15f, 200))
        ));
    }

    @Test
    @DisplayName("Null RecipeManager or RegistryAccess returns empty list safely")
    void testNullInputs() {
        assertTrue(RecipeCatalogExporter.exportCraftingRecipes(null, registryAccess).isEmpty());
        assertTrue(RecipeCatalogExporter.exportCraftingRecipes(recipeManager, (RegistryAccess) null).isEmpty());
    }

    @Test
    @DisplayName("Exports crafting recipes with grouped RecipeIngredientSlots, 2x2 vs 3x3 flags, and workstation metadata")
    void testExportCraftingRecipes() {
        List<RecipeNode> nodes = RecipeCatalogExporter.exportCraftingRecipes(recipeManager, registryAccess);
        assertEquals(4, nodes.size(), "Expected 4 crafting recipes (excluding smelting and special recipes)");

        Map<String, RecipeNode> byId = nodes.stream()
                .collect(Collectors.toMap(RecipeNode::getRecipeId, Function.identity()));

        // 1. birch_planks: 2x2 pocket craft, count 4 result
        RecipeNode planks = byId.get("minecraft:birch_planks");
        assertNotNull(planks);
        assertFalse(planks.getRequires3X3());
        assertEquals("", planks.getWorkstationBlock());
        assertEquals("minecraft:birch_planks", planks.getResult().getItemId());
        assertEquals(4, planks.getResult().getCount());
        assertEquals(1, planks.getIngredientSlotsCount());
        assertEquals(List.of("minecraft:birch_log"), planks.getIngredientSlots(0).getAcceptedItemIdsList());
        assertEquals(1, planks.getIngredientSlots(0).getCount());
        assertEquals(1, planks.getIngredientsCount());
        assertEquals("minecraft:birch_log", planks.getIngredients(0).getItemId());

        // 2. stick: 2x2 pocket craft, 2x #planks -> 4 sticks
        RecipeNode stick = byId.get("minecraft:stick");
        assertNotNull(stick);
        assertFalse(stick.getRequires3X3());
        assertEquals("", stick.getWorkstationBlock());
        assertEquals("minecraft:stick", stick.getResult().getItemId());
        assertEquals(4, stick.getResult().getCount());
        assertEquals(1, stick.getIngredientSlotsCount());
        RecipeIngredientSlot stickSlot = stick.getIngredientSlots(0);
        assertEquals(2, stickSlot.getCount());
        assertEquals(List.of("minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"),
                stickSlot.getAcceptedItemIdsList());

        // 3. wooden_axe: 3x3 table craft, 3x planks + 2x stick
        RecipeNode axe = byId.get("minecraft:wooden_axe");
        assertNotNull(axe);
        assertTrue(axe.getRequires3X3());
        assertEquals("minecraft:crafting_table", axe.getWorkstationBlock());
        assertEquals("minecraft:wooden_axe", axe.getResult().getItemId());
        assertEquals(1, axe.getResult().getCount());
        assertEquals(2, axe.getIngredientSlotsCount());
        assertEquals(3, axe.getIngredientSlots(0).getCount());
        assertEquals(List.of("minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"),
                axe.getIngredientSlots(0).getAcceptedItemIdsList());
        assertEquals(2, axe.getIngredientSlots(1).getCount());
        assertEquals(List.of("minecraft:stick"), axe.getIngredientSlots(1).getAcceptedItemIdsList());

        // 4. campfire: 3x3 table craft, sticks x3, coals x1, logs x3
        RecipeNode campfire = byId.get("minecraft:campfire");
        assertNotNull(campfire);
        assertTrue(campfire.getRequires3X3());
        assertEquals("minecraft:crafting_table", campfire.getWorkstationBlock());
        assertEquals(3, campfire.getIngredientSlotsCount());
        assertTrue(campfire.getIngredientSlotsList().stream()
                .anyMatch(s -> s.getCount() == 1 && s.getAcceptedItemIdsList().equals(List.of("minecraft:charcoal", "minecraft:coal"))));
        assertTrue(campfire.getIngredientSlotsList().stream()
                .anyMatch(s -> s.getCount() == 3 && s.getAcceptedItemIdsList().equals(List.of("minecraft:stick"))));
        assertTrue(campfire.getIngredientSlotsList().stream()
                .anyMatch(s -> s.getCount() == 3 && s.getAcceptedItemIdsList().equals(List.of("minecraft:birch_log", "minecraft:oak_log", "minecraft:spruce_log"))));
    }

    @Test
    @DisplayName("CivQuestBridgeServiceImpl.exportRecipeGraph exports recipes from server or empty when server is null")
    void testBridgeExportRecipeGraph() {
        // 1. Server null -> empty response
        CivQuestBridgeServiceImpl nullService = new CivQuestBridgeServiceImpl(new StreamRegistry());
        AtomicReference<ExportRecipeResponse> emptyRef = new AtomicReference<>();
        nullService.exportRecipeGraph(ExportRecipeRequest.getDefaultInstance(), new StreamObserver<>() {
            @Override
            public void onNext(ExportRecipeResponse value) {
                emptyRef.set(value);
            }
            @Override
            public void onError(Throwable t) {
                fail(t);
            }
            @Override
            public void onCompleted() {}
        });
        assertNotNull(emptyRef.get());
        assertEquals(0, emptyRef.get().getRecipesCount());

        // 2. Active server mock -> returns exported recipes
        MinecraftServer server = mock(MinecraftServer.class);
        when(server.isRunning()).thenReturn(false);
        when(server.getRecipeManager()).thenReturn(recipeManager);
        when(server.registryAccess()).thenReturn(registryAccess);

        StreamRegistry reg = new StreamRegistry();
        CivQuestBridgeServiceImpl service = new CivQuestBridgeServiceImpl(reg, new AgentEntityManager(reg), () -> server);
        AtomicReference<ExportRecipeResponse> respRef = new AtomicReference<>();
        service.exportRecipeGraph(ExportRecipeRequest.getDefaultInstance(), new StreamObserver<>() {
            @Override
            public void onNext(ExportRecipeResponse value) {
                respRef.set(value);
            }
            @Override
            public void onError(Throwable t) {
                fail(t);
            }
            @Override
            public void onCompleted() {}
        });
        assertNotNull(respRef.get());
        assertEquals(4, respRef.get().getRecipesCount());
    }
}
