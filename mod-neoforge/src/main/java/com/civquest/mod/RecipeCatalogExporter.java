package com.civquest.mod;

import com.civquest.mod.grpc.v1.ItemStackProto;
import com.civquest.mod.grpc.v1.RecipeIngredientSlot;
import com.civquest.mod.grpc.v1.RecipeNode;
import net.minecraft.core.HolderLookup;
import net.minecraft.core.NonNullList;
import net.minecraft.core.RegistryAccess;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.crafting.CraftingRecipe;
import net.minecraft.world.item.crafting.Ingredient;
import net.minecraft.world.item.crafting.RecipeHolder;
import net.minecraft.world.item.crafting.RecipeManager;
import net.minecraft.world.item.crafting.RecipeType;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeSet;

/**
 * Pure helper converting Minecraft's runtime {@link RecipeManager} crafting recipes into
 * protobuf {@link RecipeNode} catalog entries per ADR-029 §4.
 */
public final class RecipeCatalogExporter {
    public static final String WORKSTATION_CRAFTING_TABLE = "minecraft:crafting_table";

    private RecipeCatalogExporter() {
    }

    /**
     * Exports all valid crafting recipes from {@code recipeManager} using {@code registryAccess}.
     */
    public static List<RecipeNode> exportCraftingRecipes(RecipeManager recipeManager, RegistryAccess registryAccess) {
        return exportCraftingRecipes(recipeManager, (HolderLookup.Provider) registryAccess);
    }

    /**
     * Exports all valid crafting recipes from {@code recipeManager} using {@code registryAccess}.
     */
    public static List<RecipeNode> exportCraftingRecipes(RecipeManager recipeManager, HolderLookup.Provider registryAccess) {
        if (recipeManager == null || registryAccess == null) {
            return List.of();
        }

        List<RecipeHolder<CraftingRecipe>> holders = recipeManager.getAllRecipesFor(RecipeType.CRAFTING);
        if (holders == null || holders.isEmpty()) {
            return List.of();
        }

        List<RecipeNode> nodes = new ArrayList<>(holders.size());
        for (RecipeHolder<CraftingRecipe> holder : holders) {
            if (holder == null || holder.id() == null || holder.value() == null) {
                continue;
            }
            RecipeNode node = convertRecipe(holder.id(), holder.value(), registryAccess);
            if (node != null) {
                nodes.add(node);
            }
        }

        nodes.sort(Comparator.comparing(RecipeNode::getRecipeId));
        return List.copyOf(nodes);
    }

    private static RecipeNode convertRecipe(
            ResourceLocation recipeId,
            CraftingRecipe recipe,
            HolderLookup.Provider registryAccess) {
        ItemStack resultStack = recipe.getResultItem(registryAccess);
        if (resultStack == null || resultStack.isEmpty()) {
            return null;
        }
        ResourceLocation resultLoc = BuiltInRegistries.ITEM.getKey(resultStack.getItem());
        if (resultLoc == null || "minecraft:air".equals(resultLoc.toString())) {
            return null;
        }

        NonNullList<Ingredient> ingredients = recipe.getIngredients();
        if (ingredients == null || ingredients.isEmpty() || ingredients.stream().allMatch(ing -> ing == null || ing.isEmpty())) {
            return null;
        }

        Map<List<String>, Integer> slotCounts = new LinkedHashMap<>();
        for (Ingredient ingredient : ingredients) {
            if (ingredient == null || ingredient.isEmpty()) {
                continue;
            }
            TreeSet<String> acceptedIds = new TreeSet<>();
            ItemStack[] stacks = ingredient.getItems();
            if (stacks != null) {
                for (ItemStack stack : stacks) {
                    if (stack == null || stack.isEmpty()) {
                        continue;
                    }
                    ResourceLocation itemKey = BuiltInRegistries.ITEM.getKey(stack.getItem());
                    if (itemKey != null && !"minecraft:air".equals(itemKey.toString())) {
                        acceptedIds.add(itemKey.toString());
                    }
                }
            }
            if (acceptedIds.isEmpty()) {
                // Skip recipes with unresolvable/empty tag ingredients rather than exporting partial recipes.
                return null;
            }
            slotCounts.merge(List.copyOf(acceptedIds), 1, Integer::sum);
        }

        if (slotCounts.isEmpty()) {
            return null;
        }

        boolean requires3x3 = !recipe.canCraftInDimensions(2, 2);
        String workstationBlock = requires3x3 ? WORKSTATION_CRAFTING_TABLE : "";

        RecipeNode.Builder builder = RecipeNode.newBuilder()
                .setRecipeId(recipeId.toString())
                .setRecipeType(resolveRecipeType(recipe))
                .setWorkstationBlock(workstationBlock)
                .setRequires3X3(requires3x3)
                .setResult(ItemStackProto.newBuilder()
                        .setItemId(resultLoc.toString())
                        .setCount(Math.max(1, resultStack.getCount()))
                        .build());

        for (Map.Entry<List<String>, Integer> entry : slotCounts.entrySet()) {
            List<String> accepted = entry.getKey();
            int count = entry.getValue();
            builder.addIngredientSlots(RecipeIngredientSlot.newBuilder()
                    .addAllAcceptedItemIds(accepted)
                    .setCount(count)
                    .build());
            builder.addIngredients(ItemStackProto.newBuilder()
                    .setItemId(accepted.get(0))
                    .setCount(count)
                    .build());
        }

        return builder.build();
    }

    private static String resolveRecipeType(CraftingRecipe recipe) {
        if (recipe.getSerializer() != null) {
            ResourceLocation serializerKey = BuiltInRegistries.RECIPE_SERIALIZER.getKey(recipe.getSerializer());
            if (serializerKey != null) {
                return serializerKey.toString();
            }
        }
        if (recipe.getType() != null) {
            ResourceLocation typeKey = BuiltInRegistries.RECIPE_TYPE.getKey(recipe.getType());
            if (typeKey != null) {
                return typeKey.toString();
            }
        }
        return "minecraft:crafting";
    }
}
