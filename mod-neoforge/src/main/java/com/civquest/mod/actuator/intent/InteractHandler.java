package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.mojang.logging.LogUtils;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
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
import net.minecraft.world.item.StandingAndWallBlockItem;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.BedBlock;
import net.minecraft.world.level.block.BellBlock;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.CrossCollisionBlock;
import net.minecraft.world.level.block.DoorBlock;
import net.minecraft.world.level.block.FenceBlock;
import net.minecraft.world.level.block.FenceGateBlock;
import net.minecraft.world.level.block.IronBarsBlock;
import net.minecraft.world.level.block.LadderBlock;
import net.minecraft.world.level.block.TrapDoorBlock;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.block.state.properties.DoubleBlockHalf;
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
                Placement placement = placementFor(agent, intent);
                if (placement.refusal() != null) {
                    return false;
                }
                if (!agent.isCreative() && placement.slot() >= 0) {
                    agent.getInventory().getItem(placement.slot()).shrink(1);
                }
                agent.swing(InteractionHand.MAIN_HAND, true);
                if (placement.bedHead() != null) {
                    return Beds.place(level, pos, placement.state(), placement.bedHead());
                }
                if (placement.state().getBlock() instanceof DoorBlock) {
                    return hangDoor(level, pos, placement.state());
                }
                // A fence or pane joins what stands beside it only if asked to when it is set.
                return level.setBlockAndUpdate(pos, placement.state().getBlock() instanceof CrossCollisionBlock
                        ? Block.updateFromNeighbourShapes(placement.state(), level, pos) : placement.state());
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

    /** Whether {@code placed} has no body and the cell holds fluid, which would carry it off. */
    private static boolean isWashedAway(Level level, BlockPos pos, BlockState current, BlockState placed) {
        return current.getFluidState() != null && !current.getFluidState().isEmpty()
                && !placed.blocksMotion() && placed.getCollisionShape(level, pos).isEmpty();
    }

    /** What a PLACE_BLOCK would do now: why it is refused, or the state to set and the slot it is taken from. */
    private record Placement(String refusal, BlockState state, int slot, Direction bedHead) {
        static Placement refused(String why) {
            return new Placement(why, null, -1, null);
        }
    }

    /** Reads the world and the inventory without changing them. */
    private Placement placementFor(AgentEntity agent, InteractBlockIntent intent) {
        Level level = agent.level();
        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());
        String requestedId = intent.getBlockOrItemId();
        if (level == null || !level.hasChunkAt(pos)) {
            return Placement.refused("place target chunk not loaded");
        }
        if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
            return Placement.refused("place target out of reach");
        }
        BlockState current = level.getBlockState(pos);
        if (!current.canBeReplaced()) {
            return Placement.refused("place target cell is occupied");
        }
        BlockState state = resolver.apply(agent, requestedId);
        if (state == null) {
            return Placement.refused("unknown block state " + requestedId);
        }
        int slot = -1;
        if (!agent.isCreative()) {
            slot = findMatchingBlockItemSlot(agent, state, requestedId);
            if (slot >= 0) {
                state = stateFrom(state, agent.getInventory().getItem(slot));
            }
            if (slot < 0 || state == null) {
                return Placement.refused("no matching block item in inventory for " + requestedId);
            }
        }
        // Everything below judges the state that will be set, which a substitute item may have changed.
        // A torch or the like put into fluid is washed away at once: refuse it.
        if (isWashedAway(level, pos, current, state)) {
            return Placement.refused("place target is under water");
        }
        // Never place an unsupported block (a floating ladder, torch, etc.): the state's own
        // survival rule (ladder: solid face behind FACING) must hold at the target cell.
        if (!state.canSurvive(level, pos)) {
            return Placement.refused("no solid support for " + requestedId + " at target (attach face is not solid)");
        }
        boolean door = state.getBlock() instanceof DoorBlock;
        if (door && state.getValue(DoorBlock.HALF) != DoubleBlockHalf.LOWER) {
            return Placement.refused("place target for a door is the cell of its lower half");
        }
        if (door && !level.getBlockState(pos.above()).canBeReplaced()) {
            return Placement.refused("place target has no room for the door's upper half");
        }
        if (isEntombing(agent, level, pos, state) || door && isEntombing(agent, level, pos.above(), state)) {
            return Placement.refused("someone stands in the place target");
        }
        Direction bedHead = null;
        if (state.getBlock() instanceof BedBlock) {
            bedHead = Beds.headDirection(agent, level, pos, requiredFacing(requestedId, state));
            if (bedHead == null) {
                return Placement.refused("no room for the head half of " + requestedId);
            }
        }
        return new Placement(null, state, slot, bedHead);
    }

    /** One door item makes both halves (cq-0x4): a lone half is removed by the next neighbour update. */
    private static boolean hangDoor(Level level, BlockPos pos, BlockState lower) {
        boolean hung = level.setBlockAndUpdate(pos, lower);
        return level.setBlockAndUpdate(pos.above(), lower.setValue(DoorBlock.HALF, DoubleBlockHalf.UPPER)) && hung;
    }

    /** A shaped fitting is only itself: it never stands in for building material, nor that for it. */
    private static boolean isFitting(Block block) {
        return block instanceof DoorBlock || block instanceof TrapDoorBlock || block instanceof FenceBlock
                || block instanceof FenceGateBlock || block instanceof IronBarsBlock
                || block instanceof LadderBlock || block instanceof BedBlock;
    }

    /**
     * Whether a block with a body would be set into the agent or another living thing, entombing or
     * suffocating it. A ladder entombs nobody: a climber hangs it in the cell it stands in (cq-htv.1).
     */
    private static boolean isEntombing(AgentEntity agent, Level level, BlockPos pos, BlockState state) {
        boolean isSolid = state.blocksMotion() || !state.getCollisionShape(level, pos).isEmpty();
        if (!isSolid || state.is(Blocks.LADDER)) {
            return false;
        }
        AABB blockBox = new AABB(pos);
        if (agent.getBoundingBox() != null && agent.getBoundingBox().intersects(blockBox)) {
            return true;
        }
        List<Entity> colliding = level.getEntities(agent, blockBox,
                e -> e != null && (e instanceof Player || (e.isAlive() && !e.isSpectator())));
        if (colliding != null) {
            for (Entity entity : colliding) {
                if (entity.getBoundingBox() != null && entity.getBoundingBox().intersects(blockBox)) {
                    return true;
                }
            }
        }
        return false;
    }

    /**
     * The state {@code held} puts down where {@code requested} was asked for, carrying over the properties
     * they share; null when the item cannot make that form. An item with a standing and a wall form
     * (torches, signs, banners, heads) fills a wall request with its wall form only (cq-vuv).
     */
    private static BlockState stateFrom(BlockState requested, ItemStack held) {
        if (!(held.getItem() instanceof BlockItem item)) {
            return requested;
        }
        Block wanted = requested.getBlock();
        if (item.getBlock() == wanted || wanted.asItem() == item) {
            return requested;
        }
        Block placed = isWallForm(wanted) ? wallFormOf(item) : item.getBlock();
        if (placed == null) {
            return null;
        }
        BlockState state = placed.defaultBlockState();
        var definition = placed.getStateDefinition();
        for (var prop : requested.getProperties()) {
            var matching = definition.getProperty(prop.getName());
            if (matching != null) {
                state = applyBlockProperty(state, matching, requested.getValue(prop).toString().toLowerCase());
            }
        }
        return state;
    }

    private static boolean isWallForm(Block block) {
        return block.asItem() instanceof StandingAndWallBlockItem item && item.getBlock() != block;
    }

    private static Block wallFormOf(BlockItem item) {
        if (!(item instanceof StandingAndWallBlockItem)) {
            return null;
        }
        Map<Block, Item> forms = new HashMap<>();
        item.registerBlocks(forms, item);
        return forms.keySet().stream().filter(b -> b != item.getBlock()).findFirst().orElse(null);
    }

    /**
     * Human-readable reason a PLACE_BLOCK at the intent's cell just failed (the world is unchanged by a
     * failed placement). Blank when no specific cause is identified.
     */
    public String placementFailureReason(AgentEntity agent, InteractBlockIntent intent) {
        if (agent == null || intent == null) {
            return "";
        }
        String refusal = placementFor(agent, intent).refusal();
        return refusal != null ? refusal : "";
    }

    /** The bed facing the intent spelled out, or null to let the placement choose. */
    private static Direction requiredFacing(String blockId, BlockState bed) {
        return blockId != null && blockId.contains("facing=") ? bed.getValue(BedBlock.FACING) : null;
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
        // 1. Exact match: the item of the block asked for (a wall torch is the torch item's wall form)
        if (targetState != null) {
            for (int i = 0; i < inv.getContainerSize(); i++) {
                ItemStack item = inv.getItem(i);
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi
                        && (bi.getBlock() == targetState.getBlock() || targetState.getBlock().asItem() == bi)) {
                    return i;
                }
            }
        }
        if (targetState != null && isFitting(targetState.getBlock())) {
            return -1;
        }
        // 2. Category / family match (e.g. any planks, log, stone/cobble, or light source)
        for (int i = 0; i < inv.getContainerSize(); i++) {
            ItemStack item = inv.getItem(i);
            if (item.isEmpty() || !(item.getItem() instanceof BlockItem bi)
                    || isFitting(bi.getBlock()) || targetState != null && stateFrom(targetState, item) == null) {
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
                if (!item.isEmpty() && item.getItem() instanceof BlockItem bi && !isFitting(bi.getBlock())
                        && bi.getBlock().defaultBlockState().isSolid()) {
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
