package com.civquest.mod.actuator;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.*;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.intent.FishingHandler;
import com.civquest.mod.actuator.intent.UseItemHandler;
import com.civquest.mod.actuator.motor.Place;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.test.MinecraftTestBootstrap;
import net.minecraft.sounds.SoundSource;
import net.minecraft.world.entity.animal.Sheep;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.entity.projectile.FishingHook;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.Level;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Shear and fishing-rod UseItem modes (cq-390.3 / cq-390.4): factories, guards and bobber state. */
class FishingShearHandlerTest {
    private AgentEntity agent;
    private Level level;
    private Inventory inv;
    private Sheep sheep;
    private ItemStack held;

    @BeforeAll
    static void bootstrap() {
        MinecraftTestBootstrap.ensureBootstrapped();
    }

    @BeforeEach
    void setUp() {
        agent = mock(AgentEntity.class);
        level = mock(Level.class);
        inv = mock(Inventory.class);
        sheep = mock(Sheep.class);
        held = ItemStack.EMPTY;
        when(agent.level()).thenReturn(level);
        when(agent.getInventory()).thenReturn(inv);
        when(inv.getContainerSize()).thenReturn(1);
        when(inv.getItem(anyInt())).thenReturn(ItemStack.EMPTY);
        when(agent.getMainHandItem()).thenAnswer(i -> held);
        when(agent.getItemInHand(any())).thenAnswer(i -> held);
        when(level.getEntity(7)).thenReturn(sheep);
        when(level.getRandom()).thenReturn(net.minecraft.util.RandomSource.create());
        when(sheep.isAlive()).thenReturn(true);
        when(sheep.readyForShearing()).thenReturn(true);
        when(agent.distanceTo(sheep)).thenReturn(2.0f);
    }

    private static AgentIntent use(UseItemIntent.Mode mode, int target) {
        return AgentIntent.newBuilder().setUseItem(UseItemIntent.newBuilder().setMode(mode).setTargetEntityId(target)).build();
    }

    @Test
    void modesBuildLabelledInstantMotors() {
        UseItemHandler h = new UseItemHandler();
        assertEquals("USE_ITEM_SHEAR", assertInstanceOf(Place.class, h.create(agent, use(UseItemIntent.Mode.SHEAR_ENTITY, 7))).label());
        assertEquals("USE_ITEM_CAST", assertInstanceOf(Place.class, h.create(agent, use(UseItemIntent.Mode.CAST_FISHING_ROD, 0))).label());
        assertEquals("USE_ITEM_REEL", assertInstanceOf(Place.class, h.create(agent, use(UseItemIntent.Mode.REEL_FISHING_ROD, 0))).label());
    }

    @Test
    void shearRequiresShearsAndAShearableTargetInReach() {
        UseItemHandler h = new UseItemHandler();
        assertEquals("no shears in inventory", h.shearFailureReason(agent, 7));
        assertFalse(h.shear(agent, 7));
        verify(sheep, never()).shear(any());

        when(sheep.readyForShearing()).thenReturn(false); // baby or already sheared
        assertTrue(h.shearFailureReason(agent, 7).contains("not shearable"));
        when(sheep.readyForShearing()).thenReturn(true);

        when(agent.distanceTo(sheep)).thenReturn(9.0f);
        assertTrue(h.shearFailureReason(agent, 7).contains("out of reach"));
        assertTrue(h.shearFailureReason(agent, 99).contains("not found"));
    }

    @Test
    void shearWithShearsInInventoryShearsTheSheep() {
        ItemStack shears = new ItemStack(Items.SHEARS);
        when(inv.getItem(0)).thenReturn(shears);
        held = shears;
        UseItemHandler h = new UseItemHandler();
        assertEquals("", h.shearFailureReason(agent, 7));
        assertTrue(h.shear(agent, 7));
        verify(sheep).shear(SoundSource.PLAYERS);
    }

    @Test
    void castNeedsARodAndNoLineOut() {
        assertFalse(FishingHandler.cast(agent), "no rod carried");
        agent.fishing = mock(FishingHook.class);
        assertFalse(FishingHandler.cast(agent), "a line is already out");
        agent.fishing = null;
        assertFalse(FishingHandler.isCast(agent));
        assertFalse(FishingHandler.isBiting(agent));
    }

    @Test
    void biteStateFollowsTheHookAndReelRetrievesIt() throws Exception {
        FishingHook hook = mock(FishingHook.class);
        agent.fishing = hook;
        assertTrue(FishingHandler.isCast(agent));
        assertFalse(FishingHandler.isBiting(agent));
        java.lang.reflect.Field f = FishingHook.class.getDeclaredField("biting");
        f.setAccessible(true);
        f.setBoolean(hook, true);
        assertTrue(FishingHandler.isBiting(agent));

        held = new ItemStack(Items.FISHING_ROD);
        FishingHandler.reel(agent);
        verify(hook).retrieve(any());
    }

    @Test
    void reelWithoutALineFails() {
        assertFalse(FishingHandler.reel(agent));
    }
}
