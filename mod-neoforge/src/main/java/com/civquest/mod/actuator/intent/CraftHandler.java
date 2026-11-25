package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.mojang.logging.LogUtils;
import it.unimi.dsi.fastutil.ints.IntArrayList;
import it.unimi.dsi.fastutil.ints.IntList;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Locale;
import java.util.Optional;
import net.minecraft.core.BlockPos;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.NonNullList;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.server.MinecraftServer;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.player.StackedContents;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.crafting.CraftingInput;
import net.minecraft.world.item.crafting.CraftingRecipe;
import net.minecraft.world.item.crafting.Ingredient;
import net.minecraft.world.item.crafting.RecipeHolder;
import net.minecraft.world.item.crafting.RecipeManager;
import net.minecraft.world.item.crafting.ShapedRecipe;
import net.minecraft.world.item.crafting.ShapelessRecipe;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import org.slf4j.Logger;

/** Data-driven crafting (CraftItemIntent, ADR-029). */
public class CraftHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private final RecipeManager fallbackRecipeManager;

    public CraftHandler(RecipeManager fallbackRecipeManager) {
        this.fallbackRecipeManager = fallbackRecipeManager;
    }

    public boolean isPocketCraftOnlyBatch(AgentEntity agent, IntentBatch batch) {
        if (batch == null || batch.getIntentsCount() == 0) {
            return false;
        }
        RecipeManager recipeManager = resolveRecipeManager(agent);
        if (recipeManager == null) {
            return false;
        }
        for (AgentIntent intent : batch.getIntentsList()) {
            if (!intent.hasCraftItem()) {
                return false;
            }
            String recipeId = intent.getCraftItem().getRecipeId();
            if (recipeId == null || recipeId.isBlank()) {
                return false;
            }
            ResourceLocation recipeLoc = ResourceLocation.tryParse(recipeId.trim().toLowerCase(Locale.ROOT));
            if (recipeLoc == null) {
                return false;
            }
            Optional<RecipeHolder<?>> holderOpt = recipeManager.byKey(recipeLoc);
            if (holderOpt.isEmpty() || !(holderOpt.get().value() instanceof CraftingRecipe craftingRecipe)) {
                return false;
            }
            if (!craftingRecipe.canCraftInDimensions(2, 2)) {
                return false;
            }
        }
        return true;
    }

    protected RecipeManager resolveRecipeManager(AgentEntity agent) {
        if (agent != null) {
            MinecraftServer server = agent.getServer();
            if (server != null && server.getRecipeManager() != null) {
                return server.getRecipeManager();
            }
            Level level = agent.level();
            if (level != null && level.getRecipeManager() != null) {
                return level.getRecipeManager();
            }
        }
        return fallbackRecipeManager;
    }

    /**
     * Resolves registry lookup access for recipe result assembly, falling back to built-in registries
     * in headless mock tests.
     */
    protected HolderLookup.Provider resolveRegistryAccess(AgentEntity agent) {
        if (agent != null) {
            MinecraftServer server = agent.getServer();
            if (server != null && server.registryAccess() != null) {
                return server.registryAccess();
            }
            Level level = agent.level();
            if (level != null && level.registryAccess() != null) {
                return level.registryAccess();
            }
        }
        return RegistryAccess.fromRegistryOfRegistries(BuiltInRegistries.REGISTRY);
    }

    /**
     * Executes a data-driven crafting intent against Minecraft's {@link RecipeManager} (ADR-029).
     * Enforces 2x2 pocket vs. 3x3 crafting table dimension rules, atomic ingredient consumption,
     * remainder container item preservation, and automatic gear equipping.
     */
    public boolean actuateCraftItem(AgentEntity agent, com.civquest.mod.grpc.v1.CraftItemIntent intent) {
        if (agent == null || intent == null || intent.getRecipeId() == null || intent.getRecipeId().isBlank()) {
            return false;
        }
        Inventory inv = agent.getInventory();
        if (inv == null) {
            return false;
        }

        ResourceLocation recipeLoc = ResourceLocation.tryParse(intent.getRecipeId().trim().toLowerCase(Locale.ROOT));
        if (recipeLoc == null) {
            LOGGER.debug("Invalid recipe ResourceLocation '{}'", intent.getRecipeId());
            return false;
        }

        RecipeManager recipeManager = resolveRecipeManager(agent);
        if (recipeManager == null) {
            LOGGER.debug("No RecipeManager available to resolve recipe '{}'", recipeLoc);
            return false;
        }

        Optional<RecipeHolder<?>> holderOpt = recipeManager.byKey(recipeLoc);
        if (holderOpt.isEmpty() || !(holderOpt.get().value() instanceof CraftingRecipe craftingRecipe)) {
            LOGGER.debug("Recipe '{}' not found in RecipeManager or is not a CraftingRecipe", recipeLoc);
            return false;
        }

        NonNullList<Ingredient> ingredients = craftingRecipe.getIngredients();
        if (ingredients.isEmpty() || ingredients.stream().allMatch(Ingredient::isEmpty)) {
            LOGGER.debug("Recipe '{}' has no static ingredients and cannot be crafted directly", recipeLoc);
            return false;
        }

        if (!craftingRecipe.canCraftInDimensions(2, 2) && !isCraftingTableWithinReach(agent, intent)) {
            LOGGER.debug("Recipe '{}' requires 3x3 workstation within reach", recipeLoc);
            return false;
        }

        HolderLookup.Provider registryAccess = resolveRegistryAccess(agent);
        Level level = agent.level();
        int requestedBatches = Math.max(1, intent.getBatchCount());
        int crafted = 0;

        for (int b = 0; b < requestedBatches; b++) {
            if (!craftSingleBatch(agent, inv, craftingRecipe, ingredients, level, registryAccess)) {
                break;
            }
            crafted++;
        }

        if (crafted > 0) {
            agent.autoEquipBestGear();
            return true;
        }
        return false;
    }

    private static boolean craftSingleBatch(
            AgentEntity agent,
            Inventory inv,
            CraftingRecipe craftingRecipe,
            NonNullList<Ingredient> ingredients,
            Level level,
            HolderLookup.Provider registryAccess) {
        int slotLimit = (inv.items != null && !inv.items.isEmpty())
                ? Math.min(inv.items.size(), inv.getContainerSize())
                : inv.getContainerSize();

        int[] remainingInSlot = new int[slotLimit];
        for (int i = 0; i < slotLimit; i++) {
            ItemStack stack = inv.getItem(i);
            remainingInSlot[i] = (stack != null && !stack.isEmpty()) ? stack.getCount() : 0;
        }

        int[] plannedSlots = matchInventorySlots(inv, slotLimit, remainingInSlot, craftingRecipe, ingredients);
        if (plannedSlots == null) {
            return false;
        }

        List<ItemStack> inputStacks = new ArrayList<>(ingredients.size());
        for (int k = 0; k < ingredients.size(); k++) {
            int slot = plannedSlots[k];
            inputStacks.add(slot >= 0 ? inv.getItem(slot).copyWithCount(1) : ItemStack.EMPTY);
        }

        CraftingInput craftingInput = buildCraftingInput(craftingRecipe, inputStacks);
        if (craftingInput.isEmpty()) {
            return false;
        }
        if ((level != null || craftingRecipe instanceof ShapedRecipe || craftingRecipe instanceof ShapelessRecipe)
                && !craftingRecipe.matches(craftingInput, level)) {
            return false;
        }

        ItemStack assembled = craftingRecipe.assemble(craftingInput, registryAccess);
        if (assembled == null || assembled.isEmpty()) {
            ItemStack fallbackResult = craftingRecipe.getResultItem(registryAccess);
            if (fallbackResult == null || fallbackResult.isEmpty()) {
                return false;
            }
            assembled = fallbackResult.copy();
        }

        NonNullList<ItemStack> remainingItems;
        net.neoforged.neoforge.common.CommonHooks.setCraftingPlayer(agent);
        try {
            remainingItems = craftingRecipe.getRemainingItems(craftingInput);
        } finally {
            net.neoforged.neoforge.common.CommonHooks.setCraftingPlayer(null);
        }

        for (int slot : plannedSlots) {
            if (slot >= 0) {
                ItemStack slotStack = inv.getItem(slot);
                slotStack.shrink(1);
                if (slotStack.isEmpty()) {
                    inv.setItem(slot, ItemStack.EMPTY);
                }
            }
        }
        inv.setChanged();

        addOrDropItem(agent, inv, assembled);
        if (remainingItems != null) {
            for (ItemStack rem : remainingItems) {
                if (rem != null && !rem.isEmpty()) {
                    addOrDropItem(agent, inv, rem);
                }
            }
        }
        return true;
    }

    private static int[] matchInventorySlots(
            Inventory inv,
            int slotLimit,
            int[] remainingInSlot,
            CraftingRecipe craftingRecipe,
            NonNullList<Ingredient> ingredients) {
        boolean allSimple = true;
        for (Ingredient ing : ingredients) {
            if (!ing.isEmpty() && !ing.isSimple()) {
                allSimple = false;
                break;
            }
        }

        if (allSimple) {
            StackedContents stacked = new StackedContents();
            for (int i = 0; i < slotLimit; i++) {
                ItemStack s = inv.getItem(i);
                if (s != null && !s.isEmpty() && remainingInSlot[i] > 0) {
                    stacked.accountStack(s, Integer.MAX_VALUE);
                }
            }
            IntList pickedStackingIds = new IntArrayList();
            if (!stacked.canCraft(craftingRecipe, pickedStackingIds) || pickedStackingIds.size() != ingredients.size()) {
                return null;
            }

            int[] plannedSlots = new int[ingredients.size()];
            int[] availableCopy = Arrays.copyOf(remainingInSlot, slotLimit);
            for (int k = 0; k < ingredients.size(); k++) {
                Ingredient ing = ingredients.get(k);
                if (ing.isEmpty()) {
                    plannedSlots[k] = -1;
                    continue;
                }
                int targetId = pickedStackingIds.getInt(k);
                int chosenSlot = -1;
                for (int s = 0; s < slotLimit; s++) {
                    if (availableCopy[s] <= 0) {
                        continue;
                    }
                    ItemStack stack = inv.getItem(s);
                    if (stack != null && !stack.isEmpty()
                            && StackedContents.getStackingIndex(stack) == targetId
                            && ing.test(stack)) {
                        chosenSlot = s;
                        break;
                    }
                }
                if (chosenSlot < 0) {
                    return null;
                }
                availableCopy[chosenSlot]--;
                plannedSlots[k] = chosenSlot;
            }
            return plannedSlots;
        }

        // Exact backtracking fallback for recipes containing NeoForge custom ingredients
        for (Ingredient ing : ingredients) {
            if (ing.isEmpty()) {
                continue;
            }
            boolean anySlotMatches = false;
            for (int s = 0; s < slotLimit; s++) {
                if (remainingInSlot[s] > 0 && ing.test(inv.getItem(s))) {
                    anySlotMatches = true;
                    break;
                }
            }
            if (!anySlotMatches) {
                return null;
            }
        }

        int[] plannedSlots = new int[ingredients.size()];
        Arrays.fill(plannedSlots, -1);
        int[] availableCopy = Arrays.copyOf(remainingInSlot, slotLimit);
        return matchCustomIngredientsBacktrack(inv, slotLimit, availableCopy, ingredients, 0, plannedSlots)
                ? plannedSlots
                : null;
    }

    private static boolean matchCustomIngredientsBacktrack(
            Inventory inv,
            int slotLimit,
            int[] available,
            NonNullList<Ingredient> ingredients,
            int idx,
            int[] plannedSlots) {
        if (idx >= ingredients.size()) {
            return true;
        }
        Ingredient ing = ingredients.get(idx);
        if (ing.isEmpty()) {
            plannedSlots[idx] = -1;
            return matchCustomIngredientsBacktrack(inv, slotLimit, available, ingredients, idx + 1, plannedSlots);
        }

        List<ItemStack> triedVariants = new ArrayList<>();
        for (int s = 0; s < slotLimit; s++) {
            if (available[s] <= 0) {
                continue;
            }
            ItemStack stack = inv.getItem(s);
            if (stack == null || stack.isEmpty() || !ing.test(stack)) {
                continue;
            }
            boolean alreadyTried = false;
            for (ItemStack prev : triedVariants) {
                if (ItemStack.isSameItemSameComponents(prev, stack)) {
                    alreadyTried = true;
                    break;
                }
            }
            if (alreadyTried) {
                continue;
            }
            triedVariants.add(stack);

            available[s]--;
            plannedSlots[idx] = s;
            if (matchCustomIngredientsBacktrack(inv, slotLimit, available, ingredients, idx + 1, plannedSlots)) {
                return true;
            }
            available[s]++;
            plannedSlots[idx] = -1;
        }
        return false;
    }

    private static CraftingInput buildCraftingInput(CraftingRecipe recipe, List<ItemStack> inputStacks) {
        if (recipe instanceof ShapedRecipe shaped) {
            int w = shaped.getWidth();
            int h = shaped.getHeight();
            if (w > 0 && h > 0 && inputStacks.size() == w * h) {
                return CraftingInput.of(w, h, inputStacks);
            }
        }
        int count = inputStacks.size();
        if (count <= 0) {
            return CraftingInput.EMPTY;
        }
        if (count == 1) {
            return CraftingInput.of(1, 1, inputStacks);
        }
        int width = count <= 4 ? 2 : 3;
        int height = (count + width - 1) / width;
        List<ItemStack> padded = new ArrayList<>(width * height);
        padded.addAll(inputStacks);
        while (padded.size() < width * height) {
            padded.add(ItemStack.EMPTY);
        }
        return CraftingInput.of(width, height, padded);
    }

    private static void addOrDropItem(AgentEntity agent, Inventory inv, ItemStack stack) {
        if (stack == null || stack.isEmpty()) {
            return;
        }
        ItemStack toInsert = stack.copy();
        inv.add(toInsert);
        if (!toInsert.isEmpty() && agent.level() != null && !agent.level().isClientSide()) {
            agent.drop(toInsert, false);
        }
    }

    static final double CRAFTING_TABLE_REACH_SQ = 20.25; // 4.5 blocks squared

    public boolean isCraftingTableWithinReach(AgentEntity agent, com.civquest.mod.grpc.v1.CraftItemIntent intent) {
        if (agent == null || agent.level() == null) {
            return false;
        }
        if (intent != null && intent.hasWorkstationPos()) {
            com.civquest.mod.grpc.v1.BlockPosProto wp = intent.getWorkstationPos();
            BlockPos targetPos = new BlockPos(wp.getX(), wp.getY(), wp.getZ());
            if (agent.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY() + 0.5, targetPos.getZ() + 0.5) <= CRAFTING_TABLE_REACH_SQ) {
                if (isCraftingTableBlock(agent.level().getBlockState(targetPos))) {
                    return true;
                }
            }
        }
        BlockPos center = agent.blockPosition();
        if (center == null) {
            return false;
        }
        int reach = 5;
        for (int dx = -reach; dx <= reach; dx++) {
            for (int dy = -reach; dy <= reach; dy++) {
                for (int dz = -reach; dz <= reach; dz++) {
                    BlockPos p = center.offset(dx, dy, dz);
                    if (agent.distanceToSqr(p.getX() + 0.5, p.getY() + 0.5, p.getZ() + 0.5) <= CRAFTING_TABLE_REACH_SQ) {
                        if (isCraftingTableBlock(agent.level().getBlockState(p))) {
                            return true;
                        }
                    }
                }
            }
        }
        return false;
    }

    public static boolean isCraftingTableBlock(BlockState bs) {
        if (bs == null) {
            return false;
        }
        try {
            if (bs.is(Blocks.CRAFTING_TABLE)) {
                return true;
            }
        } catch (Throwable ignored) {}
        try {
            if (bs.toString().contains("crafting_table")) {
                return true;
            }
        } catch (Throwable ignored) {}
        try {
            if (bs.getBlock() != null) {
                ResourceLocation loc = BuiltInRegistries.BLOCK.getKey(bs.getBlock());
                if (loc != null && (loc.getPath().equals("crafting_table") || loc.toString().equals("minecraft:crafting_table"))) {
                    return true;
                }
            }
        } catch (Throwable ignored) {}
        return false;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Act("CRAFT_ITEM", a -> actuateCraftItem(a, intent.getCraftItem()));
    }
}
