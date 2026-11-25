package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.intent.BuildHandler;
import com.civquest.mod.actuator.intent.InteractHandler;
import com.civquest.mod.actuator.intent.SleepHandler;
import com.civquest.mod.actuator.motor.Body;
import com.civquest.mod.actuator.motor.Outcome;
import com.civquest.mod.actuator.motor.Place;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.BuildBlockIntent;
import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.LadderBlock;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.Vec3;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyBoolean;
import static org.mockito.Mockito.*;

/** cq-3a0.4: ladder placement facing/support, no stray blocks, and climbing a placed ladder column. */
class LadderTest {
    @BeforeAll
    static void bootstrap() {
        com.civquest.mod.test.MinecraftTestBootstrap.ensureBootstrapped();
    }

    /** Tiny mutable world: unset cells are air, y &lt;= 63 is stone. */
    private static final class World {
        final Map<BlockPos, BlockState> cells = new HashMap<>();
        final Level level = mock(Level.class);

        World() {
            when(level.hasChunkAt(any(BlockPos.class))).thenReturn(true);
            when(level.getBlockState(any(BlockPos.class))).thenAnswer(inv -> {
                BlockPos p = inv.getArgument(0);
                BlockState s = cells.get(p);
                if (s != null) return s;
                return p.getY() <= 63 ? Blocks.STONE.defaultBlockState() : Blocks.AIR.defaultBlockState();
            });
            when(level.setBlockAndUpdate(any(BlockPos.class), any(BlockState.class))).thenAnswer(inv -> {
                cells.put(inv.getArgument(0), inv.getArgument(1));
                return true;
            });
        }
    }

    private static AgentEntity placer(World w, boolean creative, ItemStack... stacks) {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.level()).thenReturn(w.level);
        when(agent.isCreative()).thenReturn(creative);
        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenReturn(1.0);
        when(agent.getBoundingBox()).thenReturn(new AABB(100, 100, 100, 101, 101, 101));
        Inventory inv = mock(Inventory.class);
        when(inv.getContainerSize()).thenReturn(stacks.length);
        for (int i = 0; i < stacks.length; i++) when(inv.getItem(i)).thenReturn(stacks[i]);
        when(agent.getInventory()).thenReturn(inv);
        return agent;
    }

    private static double anyDouble() {
        return org.mockito.ArgumentMatchers.anyDouble();
    }

    private static AgentIntent build(BlockPos p, String state) {
        return AgentIntent.newBuilder().setBuildBlock(BuildBlockIntent.newBuilder()
                .setTargetVoxel(BlockPosProto.newBuilder().setX(p.getX()).setY(p.getY()).setZ(p.getZ()))
                .setBlockStateString(state)).build();
    }

    private static Outcome run(AgentEntity agent, AgentIntent intent) {
        Body body = mock(Body.class);
        when(body.agent()).thenReturn(agent);
        BuildHandler h = new BuildHandler(new InteractHandler(new SleepHandler(), null));
        return ((Place) h.create(agent, intent)).onTick(body);
    }

    @Test
    void placesLadderWithEachFacingOnSolidFace() {
        for (Direction facing : new Direction[] {Direction.NORTH, Direction.SOUTH, Direction.EAST, Direction.WEST}) {
            World w = new World();
            BlockPos target = new BlockPos(0, 64, 0);
            w.cells.put(target.relative(facing.getOpposite()), Blocks.STONE.defaultBlockState());
            AgentEntity agent = placer(w, false, new ItemStack(Items.LADDER, 3));
            Outcome o = run(agent, build(target, "minecraft:ladder[facing=" + facing.getName() + "]"));
            assertEquals(ReceiptStatus.RECEIPT_STATUS_COMPLETED, o.status(), "facing " + facing);
            BlockState placed = w.cells.get(target);
            assertTrue(placed.is(Blocks.LADDER));
            assertEquals(facing, placed.getValue(LadderBlock.FACING));
            assertEquals(2, agent.getInventory().getItem(0).getCount(), "one ladder consumed");
        }
    }

    @Test
    void ladderWithoutSupportFailsWithReasonAndPlacesNothing() {
        World w = new World();
        BlockPos target = new BlockPos(0, 64, 0);
        AgentEntity agent = placer(w, false, new ItemStack(Items.LADDER, 3));
        Outcome o = run(agent, build(target, "minecraft:ladder[facing=east]"));
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, o.status());
        assertTrue(o.reason().contains("no solid support"), o.reason());
        assertNull(w.cells.get(target));
        assertEquals(3, agent.getInventory().getItem(0).getCount(), "no ladder consumed");
    }

    @Test
    void ladderRequestNeverSubstitutesAnotherBlock() {
        World w = new World();
        BlockPos target = new BlockPos(0, 64, 0);
        w.cells.put(target.west(), Blocks.STONE.defaultBlockState());
        AgentEntity agent = placer(w, false, new ItemStack(Items.COBBLESTONE, 8), new ItemStack(Items.OAK_PLANKS, 8));
        Outcome o = run(agent, build(target, "minecraft:ladder[facing=east]"));
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, o.status());
        assertTrue(o.reason().contains("no matching block item"), o.reason());
        assertNull(w.cells.get(target), "no cobblestone/planks may be placed in place of a ladder");
        assertEquals(8, agent.getInventory().getItem(0).getCount());
    }

    // ---- Climb ----

    private static final class Sim {
        final AgentNavigator nav;
        final AtomicReference<Vec3> pos;
        final AtomicReference<Vec3> delta = new AtomicReference<>(Vec3.ZERO);
        final World world;

        Sim(World w, Vec3 start) {
            AgentEntity agent = mock(AgentEntity.class);
            pos = new AtomicReference<>(start);
            this.world = w;
            when(agent.level()).thenReturn(w.level);
            when(agent.position()).thenAnswer(i -> pos.get());
            when(agent.getDeltaMovement()).thenAnswer(i -> delta.get());
            doAnswer(i -> {
                delta.set(i.getArgument(0));
                return null;
            }).when(agent).setDeltaMovement(any(Vec3.class));
            when(agent.getYRot()).thenReturn(0.0f);
            when(agent.onClimbable()).thenAnswer(i -> {
                Vec3 p = pos.get();
                return w.level.getBlockState(BlockPos.containing(p.x, p.y + 0.1, p.z)).is(Blocks.LADDER);
            });
            when(agent.onGround()).thenAnswer(i -> {
                Vec3 p = pos.get();
                BlockState below = w.level.getBlockState(BlockPos.containing(p.x, p.y - 0.05, p.z));
                return !below.is(Blocks.LADDER) && !below.getCollisionShape(w.level, BlockPos.ZERO).isEmpty() && p.y - Math.floor(p.y) < 0.06;
            });
            doAnswer(inv -> {
                Vec3 d = inv.getArgument(1);
                pos.set(pos.get().add(d));
                return null;
            }).when(agent).move(any(), any(Vec3.class));
            nav = new AgentNavigator(agent);
            this.agent = agent;
        }

        final AgentEntity agent;

        /** One tick of the navigator plus crude gravity (agents off the ground and off a climbable fall). */
        void tick() {
            nav.tick();
            // The entity's travel() moves it by its deltaMovement; reject moves into solid stone.
            Vec3 d = delta.getAndSet(Vec3.ZERO);
            Vec3 np = pos.get().add(d);
            BlockState feet = world.level.getBlockState(BlockPos.containing(np.x, np.y + 0.1, np.z));
            if (!feet.is(Blocks.STONE)) {
                pos.set(np);
            }
            if (!agent.onGround() && !agent.onClimbable()) {
                Vec3 p = pos.get();
                double floor = Math.floor(p.y - 0.01);
                pos.set(new Vec3(p.x, Math.max(p.y - 0.3, floor < p.y - 0.3 ? p.y - 0.3 : floor), p.z));
            }
        }
    }

    private static World ladderTower() {
        World w = new World();
        for (int y = 64; y <= 69; y++) {
            w.cells.put(new BlockPos(-1, y, 0), Blocks.STONE.defaultBlockState());
            w.cells.put(new BlockPos(0, y, 0),
                    Blocks.LADDER.defaultBlockState().setValue(LadderBlock.FACING, Direction.EAST));
        }
        return w;
    }

    @Test
    void navigateClimbsUpPlacedLadderColumnToTopRung() {
        World w = ladderTower();
        Sim s = new Sim(w, new Vec3(1.5, 64.0, 0.5));
        s.nav.navigate(0.5, 69.0, 0.5, 1.0f, false, false, false, 1);
        double maxY = 0;
        for (int i = 0; i < 400; i++) {
            s.tick();
            maxY = Math.max(maxY, s.pos.get().y);
        }
        assertTrue(maxY >= 68.0, "agent must ascend the ladder column; max y=" + maxY + " at " + s.pos.get());
    }

    @Test
    void navigateDescendsPlacedLadderColumnAfterWalkingOntoTop() {
        World w = ladderTower();
        Sim s = new Sim(w, new Vec3(-0.5, 70.0, 0.5));
        s.nav.navigate(0.5, 64.0, 0.5, 1.0f, false, false, false, 1);
        double minY = 1000;
        for (int i = 0; i < 400; i++) {
            s.tick();
            minY = Math.min(minY, s.pos.get().y);
        }
        assertTrue(minY <= 65.5, "agent must descend the ladder column; min y=" + minY + " at " + s.pos.get());
    }
}
