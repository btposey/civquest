package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.mojang.logging.LogUtils;
import java.util.List;
import java.util.Optional;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.core.registries.Registries;
import net.minecraft.resources.ResourceLocation;
import net.minecraft.sounds.SoundEvents;
import net.minecraft.sounds.SoundSource;
import net.minecraft.tags.TagKey;
import net.minecraft.world.InteractionHand;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.player.Player;
import net.minecraft.world.item.BlockItem;
import net.minecraft.world.item.Item;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BellBlock;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import org.slf4j.Logger;

/** Block interaction primitives (harvest, place, pave, bell, sleep, wake) and block-state resolution. */
public class InteractHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double INTERACT_REACH_SQ = 36.0; // 6.0 blocks squared
    private final SleepHandler sleep;
    private final java.util.function.BiFunction<AgentEntity, String, BlockState> resolver;

    public InteractHandler(SleepHandler sleep, java.util.function.BiFunction<AgentEntity, String, BlockState> resolver) {
        this.sleep = sleep;
        this.resolver = resolver != null ? resolver : this::resolveBlockState;
    }

    public boolean actuateInteractBlock(AgentEntity agent, InteractBlockIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }

        Level level = agent.level();
        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());

        switch (intent.getInteractionType()) {
            case HARVEST_BREAK -> {
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                // Vanilla reach (eye to nearest AABB point, cq-gpa.9) also admits blocks the centre test
                // rejects, e.g. trunk logs at y+6 from directly beside the trunk.
                if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ
                        && !com.civquest.mod.actuator.BlockBreaker.withinReach(agent.getX(), agent.getEyeY(), agent.getZ(), pos)) {
                    return false;
                }
                BlockState state = level.getBlockState(pos);
                float destroySpeed = state.getDestroySpeed(level, pos);
                if (destroySpeed < 0.0f) {
                    return false;
                }
                agent.equipBestToolFor(state);
                // Timed, tool-aware breaking: best tool equipped, progress accrues per tick with swing animation.
                return agent.getBreaker().start(pos);
            }
            case PAVE_ROAD_PATH -> {
                if (level != null && level.hasChunkAt(pos)) {
                    if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) <= INTERACT_REACH_SQ) {
                        BlockState current = level.getBlockState(pos);
                        if (current.is(Blocks.GRASS_BLOCK) || current.is(Blocks.DIRT)) {
                            boolean modified = level.setBlockAndUpdate(pos, Blocks.DIRT_PATH.defaultBlockState());
                            level.playSound(null, pos, SoundEvents.SHOVEL_FLATTEN, SoundSource.BLOCKS, 1.0F, 1.0F);
                            return modified;
                        }
                    }
                }
                return false;
            }
            case SLEEP_IN_BED -> {
                return sleep.actuateSleepInBed(agent, pos);
            }
            case WAKE_UP -> {
                agent.stopSleeping();
                return !agent.isSleeping();
            }
            case PLACE_BLOCK -> {
                if (level == null || !level.hasChunkAt(pos)) {
                    return false;
                }
                if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                    return false;
                }
                BlockState current = level.getBlockState(pos);
                if (!current.canBeReplaced()) {
                    return false;
                }
                BlockState blockState = resolver.apply(agent, intent.getBlockOrItemId());
                if (blockState == null) {
                    return false;
                }

                // Never place an unsupported block (a floating ladder, torch, etc.): the state's own
                // survival rule (ladder: solid face behind FACING) must hold at the target cell.
                if (!blockState.canSurvive(level, pos)) {
                    return false;
                }

                int slot = -1;
                if (!agent.isCreative()) {
                    slot = findMatchingBlockItemSlot(agent, blockState, intent.getBlockOrItemId());
                    if (slot < 0) {
                        return false;
                    }
                    ItemStack chosen = agent.getInventory().getItem(slot);
                    if (chosen.getItem() instanceof BlockItem chosenBi) {
                        if (chosenBi.getBlock() != blockState.getBlock()) {
                            BlockState newState = chosenBi.getBlock().defaultBlockState();
                            var stateDef = chosenBi.getBlock().getStateDefinition();
                            for (var prop : blockState.getProperties()) {
                                var matchingProp = stateDef.getProperty(prop.getName());
                                if (matchingProp != null) {
                                    newState = applyBlockProperty(newState, matchingProp, blockState.getValue(prop).toString().toLowerCase());
                                }
                            }
                            blockState = newState;
                        }
                    }
                }

                // Verify that solid block placement does not collide with the agent's bounding box
                // or any other player/living entity's bounding box to prevent entombment or suffocation.
                AABB blockBox = new AABB(pos);
                boolean isSolid = blockState.blocksMotion() || !blockState.getCollisionShape(level, pos).isEmpty();
                if (isSolid) {
                    if (agent.getBoundingBox() != null && agent.getBoundingBox().intersects(blockBox)) {
                        return false;
                    }
                    List<Entity> colliding = level.getEntities(agent, blockBox,
                            e -> e != null && (e instanceof Player || (e.isAlive() && !e.isSpectator())));
                    if (colliding != null) {
                        for (Entity entity : colliding) {
                            if (entity.getBoundingBox() != null && entity.getBoundingBox().intersects(blockBox)) {
                                return false;
                            }
                        }
                    }
                }

                if (!agent.isCreative() && slot >= 0) {
                    ItemStack chosen = agent.getInventory().getItem(slot);
                    chosen.shrink(1);
                }
                agent.swing(InteractionHand.MAIN_HAND, true);
                return level.setBlockAndUpdate(pos, blockState);
            }
            case RING_BELL -> {
                if (level != null && level.hasChunkAt(pos)) {
                    if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) <= INTERACT_REACH_SQ) {
                        BlockState state = level.getBlockState(pos);
                        if (state.getBlock() instanceof BellBlock bell) {
                            return bell.attemptToRing(agent, level, pos, Direction.NORTH);
                        }
                    }
                }
                return false;
            }
            default -> {
                LOGGER.debug("InteractBlock type {} not explicitly handled", intent.getInteractionType());
                return false;
            }
        }
    }

    /**
     * Human-readable reason a PLACE_BLOCK at the intent's cell just failed (re-derives the checks; the world is
     * unchanged by a failed placement). Blank when no specific cause is identified.
     */
    public String placementFailureReason(AgentEntity agent, InteractBlockIntent intent) {
        if (agent == null || intent == null) {
            return "";
        }
        Level level = agent.level();
        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());
        if (level == null || !level.hasChunkAt(pos)) {
            return "place target chunk not loaded";
        }
        if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
            return "place target out of reach";
        }
        if (!level.getBlockState(pos).canBeReplaced()) {
            return "place target cell is occupied";
        }
        BlockState state = resolver.apply(agent, intent.getBlockOrItemId());
        if (state == null) {
            return "unknown block state " + intent.getBlockOrItemId();
        }
        if (!state.canSurvive(level, pos)) {
            return "no solid support for " + intent.getBlockOrItemId() + " at target (attach face is not solid)";
        }
        if (!agent.isCreative() && findMatchingBlockItemSlot(agent, state, intent.getBlockOrItemId()) < 0) {
            return "no matching block item in inventory for " + intent.getBlockOrItemId();
        }
        return "";
    }

    int findMatchingBlockItemSlot(AgentEntity agent, BlockState targetState, String requestedId) {
        var inv = agent.getInventory();
        if (inv == null) {
            return -1;
        }
        // 0. Taxonomy tag match (#minecraft:planks, #minecraft:stairs, #c:stones, etc.)
        String req = requestedId != null ? requestedId.trim().toLowerCase() : "";
        if (req.startsWith("#")) {
            String tagId = req.substring(1);
            if (tagId.contains("[")) {
                tagId = tagId.substring(0, tagId.indexOf('['));
            }
            if (!tagId.contains(":")) {
                tagId = "minecraft:" + tagId;
            }
            ResourceLocation rl = ResourceLocation.tryParse(tagId);
            if (rl != null) {
                TagKey<Item> itemTag = TagKey.create(Registries.ITEM, rl);
                TagKey<Block> blockTag = TagKey.create(Registries.BLOCK, rl);
                for (int i = 0; i < inv.getContainerSize(); i++) {
                    ItemStack item = inv.getItem(i);
                    if (item.isEmpty() || !(item.getItem() instanceof BlockItem bi)) {
                        continue;
                    }
                    if (item.is(itemTag) || bi.getBlock().defaultBlockState().is(blockTag)) {
                        return i;
                    }
                }
            }
        }
        // 0b. Ladders are placed exactly or not at all: never substitute another block for a ladder.
        if (targetState != null && targetState.is(Blocks.LADDER)) {
            for (int i = 0; i < inv.getContainerSize(); i++) {
                ItemStack item = inv.getItem(i);
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && bi.getBlock() == Blocks.LADDER) {
                    return i;
                }
            }
            return -1;
        }
        // 1. Exact block match
        if (targetState != null) {
            for (int i = 0; i < inv.getContainerSize(); i++) {
                ItemStack item = inv.getItem(i);
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && bi.getBlock() == targetState.getBlock()) {
                    return i;
                }
            }
        }
        // 2. Category / family match (e.g. any planks, log, stone/cobble, or light source)
        for (int i = 0; i < inv.getContainerSize(); i++) {
            ItemStack item = inv.getItem(i);
            if (item.isEmpty() || !(item.getItem() instanceof BlockItem bi)) {
                continue;
            }
            String key = BuiltInRegistries.ITEM.getKey(item.getItem()).toString().toLowerCase();
            if (req.isEmpty() || req.contains("planks") && key.endsWith("_planks")
                    || req.contains("log") && (key.endsWith("_log") || key.endsWith("_wood") || key.endsWith("_stem"))
                    || (req.contains("cobble") || req.contains("stone") || req.contains("dirt"))
                    && (key.contains("cobblestone") || key.endsWith(":stone") || key.endsWith(":dirt") || key.endsWith("_planks") || key.endsWith("_log"))
                    || (req.contains("campfire") || req.contains("torch") || req.contains("lantern"))
                    && (key.contains("campfire") || key.contains("torch") || key.contains("lantern"))) {
                return i;
            }
        }
        // 3. Fallback to any solid placeable building block if a building block was requested
        if (req.isEmpty() || req.contains("planks") || req.contains("log") || req.contains("cobble") || req.contains("dirt") || req.contains("stone")) {
            for (int i = 0; i < inv.getContainerSize(); i++) {
                ItemStack item = inv.getItem(i);
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && bi.getBlock().defaultBlockState().isSolid()) {
                    return i;
                }
            }
        }
        return -1;
    }

    @SuppressWarnings("unchecked")
    private static <T extends Comparable<T>> BlockState applyBlockProperty(BlockState state, net.minecraft.world.level.block.state.properties.Property<T> prop, String valStr) {
        java.util.Optional<T> val = prop.getValue(valStr);
        return val.map(t -> state.setValue(prop, t)).orElse(state);
    }

    public BlockState resolveBlockState(AgentEntity agent, String blockOrItemId) {
        if (blockOrItemId != null && !blockOrItemId.isBlank()) {
            String trimmed = blockOrItemId.trim();
            String baseId = trimmed;
            String propString = null;
            int bracketStart = trimmed.indexOf('[');
            int bracketEnd = trimmed.lastIndexOf(']');
            if (bracketStart != -1 && bracketEnd > bracketStart) {
                baseId = trimmed.substring(0, bracketStart).trim();
                propString = trimmed.substring(bracketStart + 1, bracketEnd).trim();
            }

            // NeoForge / Minecraft taxonomy tag resolution (#tag)
            if (baseId.startsWith("#")) {
                if (agent != null) {
                    int slot = findMatchingBlockItemSlot(agent, null, trimmed);
                    if (slot >= 0) {
                        ItemStack chosen = agent.getInventory().getItem(slot);
                        if (chosen.getItem() instanceof BlockItem bi) {
                            String concreteId = BuiltInRegistries.BLOCK.getKey(bi.getBlock()).toString();
                            if (propString != null && !propString.isEmpty()) {
                                concreteId += "[" + propString + "]";
                            }
                            return resolveBlockState(agent, concreteId);
                        }
                    }
                }
                String tag = baseId.toLowerCase();
                String fallbackId = switch (tag) {
                    case "#minecraft:stairs", "#stairs" -> "minecraft:oak_stairs";
                    case "#minecraft:logs", "#minecraft:wood", "#logs", "#wood" -> "minecraft:oak_log";
                    case "#minecraft:doors", "#doors" -> "minecraft:oak_door";
                    case "#minecraft:trapdoors", "#trapdoors" -> "minecraft:oak_trapdoor";
                    case "#minecraft:slabs", "#slabs" -> "minecraft:oak_slab";
                    case "#minecraft:fence_gates", "#fence_gates" -> "minecraft:oak_fence_gate";
                    case "#c:stones", "#minecraft:base_stone_overworld", "#stones", "#c:cobblestones" -> "minecraft:cobblestone";
                    default -> "minecraft:oak_planks";
                };
                if (propString != null && !propString.isEmpty()) {
                    fallbackId += "[" + propString + "]";
                }
                return resolveBlockState(agent, fallbackId);
            }

            ResourceLocation id = ResourceLocation.tryParse(baseId);
            if (id != null) {
                Block block = null;
                if (BuiltInRegistries.BLOCK.containsKey(id)) {
                    block = BuiltInRegistries.BLOCK.get(id);
                } else if (BuiltInRegistries.ITEM.containsKey(id)) {
                    Item item = BuiltInRegistries.ITEM.get(id);
                    if (item instanceof BlockItem bi) {
                        block = bi.getBlock();
                    }
                }

                if (block != null) {
                    BlockState state = block.defaultBlockState();
                    if (propString != null && !propString.isEmpty()) {
                        var stateDef = block.getStateDefinition();
                        String[] pairs = propString.split(",");
                        for (String pair : pairs) {
                            String[] kv = pair.split("=", 2);
                            if (kv.length == 2) {
                                String propName = kv[0].trim();
                                String propVal = kv[1].trim();
                                var prop = stateDef.getProperty(propName);
                                if (prop != null) {
                                    state = applyBlockProperty(state, prop, propVal);
                                }
                            }
                        }
                    }
                    return state;
                }
            }
        }
        if (agent != null) {
            ItemStack mainHand = agent.getMainHandItem();
            if (mainHand != null && !mainHand.isEmpty() && mainHand.getItem() instanceof BlockItem bi) {
                return bi.getBlock().defaultBlockState();
            }
        }
        return Blocks.COBBLESTONE != null ? Blocks.COBBLESTONE.defaultBlockState() : null;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        InteractBlockIntent interact = intent.getInteractBlock();
        BlockPos pos = new BlockPos(interact.getPos().getX(), interact.getPos().getY(), interact.getPos().getZ());
        return switch (interact.getInteractionType()) {
            case HARVEST_BREAK -> isAlreadyBroken(agent, interact, pos) ? null
                    : new Mine(pos, 0, 0.0, a -> actuateInteractBlock(a, interact));
            case SLEEP_IN_BED -> sleep.create(agent, intent);
            case PLACE_BLOCK, PAVE_ROAD_PATH -> new Place("INTERACT_BLOCK", a -> actuateInteractBlock(a, interact),
                    a -> placementFailureReason(a, interact));
            default -> new Act("INTERACT_BLOCK", a -> actuateInteractBlock(a, interact));
        };
    }

    private static boolean isAlreadyBroken(AgentEntity agent, InteractBlockIntent interact, BlockPos pos) {
        Level level = agent != null ? agent.level() : null;
        return level != null && level.hasChunkAt(pos) && level.getBlockState(pos).isAir();
    }
}
