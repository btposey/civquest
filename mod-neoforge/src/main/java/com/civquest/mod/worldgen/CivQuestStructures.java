package com.civquest.mod.worldgen;

import com.civquest.mod.CivQuestMod;
import net.minecraft.core.registries.Registries;
import net.minecraft.resources.ResourceKey;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.world.level.levelgen.structure.Structure;
import net.minecraft.world.level.levelgen.structure.StructureSet;
import net.minecraft.world.level.levelgen.structure.pools.StructureTemplatePool;

/**
 * Registry keys, identifiers, and configuration constants for CivQuest worldgen structures.
 * Custom starter pioneer campfire structures co-exist alongside preserved vanilla villages.
 */
public final class CivQuestStructures {
    private CivQuestStructures() {}

    public static final String PIONEER_CAMPFIRE_NAME = "pioneer_campfire";

    public static final ResourceLocation PIONEER_CAMPFIRE_ID =
            ResourceLocation.fromNamespaceAndPath(CivQuestMod.MOD_ID, PIONEER_CAMPFIRE_NAME);

    public static final ResourceKey<Structure> PIONEER_CAMPFIRE_STRUCTURE =
            ResourceKey.create(Registries.STRUCTURE, PIONEER_CAMPFIRE_ID);

    public static final ResourceKey<StructureSet> PIONEER_CAMPFIRE_SET =
            ResourceKey.create(Registries.STRUCTURE_SET, PIONEER_CAMPFIRE_ID);

    public static final ResourceKey<StructureTemplatePool> PIONEER_CAMPFIRE_POOL =
            ResourceKey.create(Registries.TEMPLATE_POOL, PIONEER_CAMPFIRE_ID);
}
