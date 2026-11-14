package com.civquest.mod.worldgen;

import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.IntTag;
import net.minecraft.nbt.ListTag;
import net.minecraft.nbt.NbtAccounter;
import net.minecraft.nbt.NbtIo;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.io.File;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

public class PioneerCampWorldgenTest {

    private static final Path STRUCTURE_FILE_PATH = Path.of(
            "src/main/resources/data/civquest/structure/pioneer_campfire.nbt"
    );

    @Test
    @DisplayName("Verify CivQuestStructures registry keys and constants")
    void testStructureRegistryKeys() {
        assertEquals("civquest:pioneer_campfire", CivQuestStructures.PIONEER_CAMPFIRE_ID.toString());
        assertEquals("pioneer_campfire", CivQuestStructures.PIONEER_CAMPFIRE_NAME);
        assertEquals(CivQuestStructures.PIONEER_CAMPFIRE_ID, CivQuestStructures.PIONEER_CAMPFIRE_STRUCTURE.location());
        assertEquals(CivQuestStructures.PIONEER_CAMPFIRE_ID, CivQuestStructures.PIONEER_CAMPFIRE_SET.location());
        assertEquals(CivQuestStructures.PIONEER_CAMPFIRE_ID, CivQuestStructures.PIONEER_CAMPFIRE_POOL.location());
    }

    @Test
    @DisplayName("Verify worldgen JSON definitions exist and are valid JSON")
    void testWorldgenJsonFilesExist() {
        Path structureJson = Path.of("src/main/resources/data/civquest/worldgen/structure/pioneer_campfire.json");
        Path structureSetJson = Path.of("src/main/resources/data/civquest/worldgen/structure_set/pioneer_campfire.json");
        Path templatePoolJson = Path.of("src/main/resources/data/civquest/worldgen/template_pool/pioneer_campfire.json");
        Path biomeTagJson = Path.of("src/main/resources/data/civquest/tags/worldgen/biome/has_structure/pioneer_campfire.json");

        assertTrue(Files.exists(structureJson), "Structure JSON must exist");
        assertTrue(Files.exists(structureSetJson), "Structure set JSON must exist");
        assertTrue(Files.exists(templatePoolJson), "Template pool JSON must exist");
        assertTrue(Files.exists(biomeTagJson), "Biome tag JSON must exist");
    }

    @Test
    @DisplayName("Generate and verify pioneer_campfire.nbt structure template")
    void testGenerateAndValidatePioneerCampfireNbt() throws IOException {
        CompoundTag structureTag = buildPioneerCampfireStructureTag();

        // Write compressed NBT to resources directory
        File targetFile = STRUCTURE_FILE_PATH.toFile();
        targetFile.getParentFile().mkdirs();
        NbtIo.writeCompressed(structureTag, targetFile.toPath());
        assertTrue(targetFile.exists(), "Structure NBT file must exist on disk");
        assertTrue(targetFile.length() > 0, "Structure NBT file must not be empty");

        // Read back and parse with Minecraft NbtIo loader
        CompoundTag loadedTag = NbtIo.readCompressed(targetFile.toPath(), NbtAccounter.unlimitedHeap());
        assertNotNull(loadedTag, "Loaded CompoundTag must not be null");
        assertEquals(3953, loadedTag.getInt("DataVersion"), "DataVersion must match 1.21.1 baseline (3953)");

        ListTag size = loadedTag.getList("size", 3);
        assertEquals(3, size.size(), "Size tag must have 3 dimensions");
        assertEquals(7, size.getInt(0), "Camp width must be 7");
        assertEquals(4, size.getInt(1), "Camp height must be 4");
        assertEquals(7, size.getInt(2), "Camp length must be 7");

        // Verify required blocks in palette
        ListTag palette = loadedTag.getList("palette", 10);
        List<String> blockNames = new ArrayList<>();
        for (int i = 0; i < palette.size(); i++) {
            blockNames.add(palette.getCompound(i).getString("Name"));
        }

        assertTrue(blockNames.contains("minecraft:campfire"), "Palette must contain campfire");
        assertTrue(blockNames.contains("minecraft:barrel"), "Palette must contain supply barrel");
        assertTrue(blockNames.contains("minecraft:chest"), "Palette must contain supply chest");
        assertTrue(blockNames.contains("minecraft:cyan_banner"), "Palette must contain clan banner");
        assertTrue(blockNames.contains("minecraft:crafting_table"), "Palette must contain crafting table");
        assertTrue(blockNames.contains("minecraft:lantern"), "Palette must contain lantern");

        // Verify block entities (chests, barrels, banner, campfire)
        ListTag blocks = loadedTag.getList("blocks", 10);
        assertEquals(7 * 4 * 7, blocks.size(), "All block positions in bounding box must be defined");
        boolean foundCampfire = false;
        boolean foundBarrel = false;
        boolean foundChest = false;
        boolean foundBanner = false;

        for (int i = 0; i < blocks.size(); i++) {
            CompoundTag b = blocks.getCompound(i);
            if (b.contains("nbt")) {
                CompoundTag nbt = b.getCompound("nbt");
                String id = nbt.getString("id");
                if ("minecraft:campfire".equals(id)) {
                    foundCampfire = true;
                } else if ("minecraft:barrel".equals(id)) {
                    foundBarrel = true;
                    assertTrue(nbt.contains("LootTable"), "Barrel must specify starter supply LootTable");
                } else if ("minecraft:chest".equals(id)) {
                    foundChest = true;
                    assertTrue(nbt.contains("LootTable"), "Chest must specify starter supply LootTable");
                } else if ("minecraft:banner".equals(id)) {
                    foundBanner = true;
                    assertTrue(nbt.contains("CustomName"), "Banner must specify clan banner name");
                }
            }
        }

        assertTrue(foundCampfire, "Block entity for campfire must be present");
        assertTrue(foundBarrel, "Block entity for barrel must be present");
        assertTrue(foundChest, "Block entity for chest must be present");
        assertTrue(foundBanner, "Block entity for banner must be present");
    }

    private static CompoundTag buildPioneerCampfireStructureTag() {
        int width = 7;
        int height = 4;
        int length = 7;

        CompoundTag root = new CompoundTag();
        root.putInt("DataVersion", 3953);

        ListTag size = new ListTag();
        size.add(IntTag.valueOf(width));
        size.add(IntTag.valueOf(height));
        size.add(IntTag.valueOf(length));
        root.put("size", size);

        // Define Palette
        List<CompoundTag> paletteList = new ArrayList<>();
        Map<String, Integer> stateIndexMap = new HashMap<>();

        java.util.function.BiFunction<String, Map<String, String>, Integer> addState = (name, props) -> {
            String key = name + props.toString();
            if (stateIndexMap.containsKey(key)) {
                return stateIndexMap.get(key);
            }
            int index = paletteList.size();
            CompoundTag entry = new CompoundTag();
            entry.putString("Name", name);
            if (!props.isEmpty()) {
                CompoundTag p = new CompoundTag();
                props.forEach(p::putString);
                entry.put("Properties", p);
            }
            paletteList.add(entry);
            stateIndexMap.put(key, index);
            return index;
        };

        int stateAir = addState.apply("minecraft:air", Map.of());
        int stateCobble = addState.apply("minecraft:cobblestone", Map.of());
        int stateStoneBricks = addState.apply("minecraft:stone_bricks", Map.of());
        int stateDirtPath = addState.apply("minecraft:dirt_path", Map.of());
        int stateCoarseDirt = addState.apply("minecraft:coarse_dirt", Map.of());

        int stateCampfire = addState.apply("minecraft:campfire", Map.of(
                "lit", "true",
                "facing", "south",
                "signal_fire", "false",
                "waterlogged", "false"
        ));

        int stateBarrel = addState.apply("minecraft:barrel", Map.of(
                "facing", "up",
                "open", "false"
        ));

        int stateChest = addState.apply("minecraft:chest", Map.of(
                "facing", "south",
                "type", "single",
                "waterlogged", "false"
        ));

        int stateCraftingTable = addState.apply("minecraft:crafting_table", Map.of());

        int stateOakLogX = addState.apply("minecraft:oak_log", Map.of(
                "axis", "x"
        ));

        int stateOakStairsEast = addState.apply("minecraft:oak_stairs", Map.of(
                "facing", "east",
                "half", "bottom",
                "shape", "straight",
                "waterlogged", "false"
        ));

        int stateOakStairsWest = addState.apply("minecraft:oak_stairs", Map.of(
                "facing", "west",
                "half", "bottom",
                "shape", "straight",
                "waterlogged", "false"
        ));

        int stateOakFence = addState.apply("minecraft:oak_fence", Map.of(
                "east", "false",
                "north", "false",
                "south", "false",
                "west", "false",
                "waterlogged", "false"
        ));

        int stateCyanBanner = addState.apply("minecraft:cyan_banner", Map.of(
                "rotation", "8"
        ));

        int stateLantern = addState.apply("minecraft:lantern", Map.of(
                "hanging", "false",
                "waterlogged", "false"
        ));

        int stateJigsaw = addState.apply("minecraft:jigsaw", Map.of(
                "orientation", "north_up"
        ));

        // Place Blocks
        int[][][] grid = new int[width][height][length];
        CompoundTag[][][] blockNbtGrid = new CompoundTag[width][height][length];

        for (int x = 0; x < width; x++) {
            for (int y = 0; y < height; y++) {
                for (int z = 0; z < length; z++) {
                    grid[x][y][z] = stateAir;
                }
            }
        }

        // --- Y = 0 (Floor / Foundation) ---
        grid[3][0][3] = stateCobble;
        grid[3][0][2] = stateCobble;
        grid[3][0][4] = stateCobble;
        grid[2][0][3] = stateCobble;
        grid[4][0][3] = stateCobble;

        grid[2][0][2] = stateStoneBricks;
        grid[4][0][2] = stateStoneBricks;
        grid[2][0][4] = stateStoneBricks;
        grid[4][0][4] = stateStoneBricks;

        grid[1][0][2] = stateDirtPath;
        grid[1][0][3] = stateDirtPath;
        grid[1][0][4] = stateDirtPath;
        grid[5][0][2] = stateDirtPath;
        grid[5][0][3] = stateDirtPath;
        grid[5][0][4] = stateDirtPath;
        grid[2][0][1] = stateDirtPath;
        grid[3][0][1] = stateDirtPath;
        grid[4][0][1] = stateDirtPath;
        grid[2][0][5] = stateDirtPath;
        grid[3][0][5] = stateDirtPath;
        grid[4][0][5] = stateDirtPath;

        grid[1][0][1] = stateCoarseDirt;
        grid[5][0][1] = stateCoarseDirt;
        grid[1][0][5] = stateCoarseDirt;
        grid[5][0][5] = stateCoarseDirt;

        // Jigsaw expansion connector at south camp edge (Y=0)
        grid[3][0][0] = stateJigsaw;
        CompoundTag jigsawNbt = new CompoundTag();
        jigsawNbt.putString("id", "minecraft:jigsaw");
        jigsawNbt.putString("name", "civquest:camp_entrance");
        jigsawNbt.putString("target", "civquest:camp_entrance");
        jigsawNbt.putString("pool", "minecraft:empty");
        jigsawNbt.putString("final_state", "minecraft:dirt_path");
        jigsawNbt.putString("joint", "rollable");
        blockNbtGrid[3][0][0] = jigsawNbt;

        // --- Y = 1 (Camp Surface) ---
        grid[3][1][3] = stateCampfire;
        CompoundTag campfireNbt = new CompoundTag();
        campfireNbt.putString("id", "minecraft:campfire");
        campfireNbt.put("Items", new ListTag());
        campfireNbt.putIntArray("CookingTimes", new int[]{0, 0, 0, 0});
        campfireNbt.putIntArray("CookingTotalTimes", new int[]{0, 0, 0, 0});
        blockNbtGrid[3][1][3] = campfireNbt;

        // Supply crates
        grid[2][1][2] = stateBarrel;
        CompoundTag barrelNbt = new CompoundTag();
        barrelNbt.putString("id", "minecraft:barrel");
        barrelNbt.putString("LootTable", "minecraft:chests/spawn_bonus_chest");
        blockNbtGrid[2][1][2] = barrelNbt;

        grid[4][1][2] = stateChest;
        CompoundTag chestNbt = new CompoundTag();
        chestNbt.putString("id", "minecraft:chest");
        chestNbt.putString("LootTable", "minecraft:chests/village/village_plains_house");
        blockNbtGrid[4][1][2] = chestNbt;

        grid[2][1][4] = stateCraftingTable;

        grid[3][1][1] = stateOakLogX;
        grid[3][1][5] = stateOakLogX;

        grid[1][1][3] = stateOakStairsEast;
        grid[5][1][3] = stateOakStairsWest;

        grid[4][1][4] = stateOakFence;
        grid[5][1][1] = stateOakFence;

        // --- Y = 2 (Upper Features) ---
        grid[4][2][4] = stateCyanBanner;
        CompoundTag bannerNbt = new CompoundTag();
        bannerNbt.putString("id", "minecraft:banner");
        bannerNbt.putString("CustomName", "{\"text\":\"Pioneer Clan Banner\",\"color\":\"aqua\",\"bold\":true}");
        blockNbtGrid[4][2][4] = bannerNbt;

        grid[5][2][1] = stateLantern;

        // Assemble NBT
        ListTag paletteTag = new ListTag();
        for (CompoundTag pEntry : paletteList) {
            paletteTag.add(pEntry);
        }
        root.put("palette", paletteTag);

        ListTag blocksTag = new ListTag();
        for (int x = 0; x < width; x++) {
            for (int y = 0; y < height; y++) {
                for (int z = 0; z < length; z++) {
                    int state = grid[x][y][z];
                    CompoundTag b = new CompoundTag();
                    b.putInt("state", state);

                    ListTag pos = new ListTag();
                    pos.add(IntTag.valueOf(x));
                    pos.add(IntTag.valueOf(y));
                    pos.add(IntTag.valueOf(z));
                    b.put("pos", pos);

                    if (blockNbtGrid[x][y][z] != null) {
                        b.put("nbt", blockNbtGrid[x][y][z]);
                    }
                    blocksTag.add(b);
                }
            }
        }
        root.put("blocks", blocksTag);
        root.put("entities", new ListTag());

        return root;
    }
}
