package com.civquest.mod.actuator;

import com.civquest.mod.AgentEntity;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

/** cq-gpa.10: tool selection when no inventory tool suits the block. */
class BlockBreakerToolTest {
    @BeforeAll
    static void initMinecraft() {
        com.civquest.mod.test.MinecraftTestBootstrap.ensureBootstrapped();
    }

    private static Inventory inventoryOf(AgentEntity agent) {
        Inventory inv = new Inventory(agent);
        when(agent.getInventory()).thenReturn(inv);
        return inv;
    }

    @Test
    @DisplayName("Axe selected + stone + no pickaxe: moves to an empty hotbar slot, never keeps the axe")
    void axeOnStoneSelectsEmptySlot() {
        AgentEntity agent = mock(AgentEntity.class);
        Inventory inv = inventoryOf(agent);
        inv.setItem(0, new ItemStack(Items.WOODEN_AXE));
        inv.selected = 0;
        BlockState stone = Blocks.STONE.defaultBlockState();

        assertEquals(-1, BlockBreaker.bestToolSlot(agent, stone), "an axe does not beat hands on stone");
        BlockBreaker.equipBestTool(agent, stone);
        assertNotEquals(0, inv.selected);
        assertTrue(inv.items.get(inv.selected).isEmpty(), "selected slot must be empty");
    }

    @Test
    @DisplayName("Full hotbar: prefers a non-tool item over an axe; all tools -> selection untouched")
    void fullHotbarPrefersNonTool() {
        AgentEntity agent = mock(AgentEntity.class);
        Inventory inv = inventoryOf(agent);
        for (int i = 0; i < 9; i++) {
            inv.setItem(i, new ItemStack(Items.WOODEN_AXE));
        }
        inv.setItem(5, new ItemStack(Items.COBBLESTONE, 4));
        inv.selected = 0;
        BlockBreaker.equipBestTool(agent, Blocks.STONE.defaultBlockState());
        assertEquals(5, inv.selected);

        inv.setItem(5, new ItemStack(Items.WOODEN_AXE));
        inv.selected = 2;
        BlockBreaker.equipBestTool(agent, Blocks.STONE.defaultBlockState());
        assertEquals(2, inv.selected, "nothing neutral available: leave selection");
    }

    // Block/item tags are not bound in the unit-test bootstrap (vanilla tool speed would read 1.0 for
    // every tool), so tool behaviour is modelled with stubs of the two queries BlockBreaker relies on.
    private static ItemStack tool(float speed, boolean correctForDrops) {
        ItemStack s = mock(ItemStack.class);
        when(s.isEmpty()).thenReturn(false);
        when(s.isDamageableItem()).thenReturn(true);
        when(s.getDestroySpeed(any(BlockState.class))).thenReturn(speed);
        when(s.isCorrectToolForDrops(any(BlockState.class))).thenReturn(correctForDrops);
        return s;
    }

    private static BlockState block(boolean requiresCorrectTool) {
        BlockState st = mock(BlockState.class);
        when(st.requiresCorrectToolForDrops()).thenReturn(requiresCorrectTool);
        return st;
    }

    @Test
    @DisplayName("The right tool is still equipped when one exists")
    void rightToolStillChosen() {
        AgentEntity agent = mock(AgentEntity.class);
        Inventory inv = inventoryOf(agent);
        inv.setItem(0, tool(1.0F, false)); // axe on stone
        inv.setItem(1, tool(2.0F, true)); // wooden pickaxe on stone
        inv.selected = 0;
        BlockBreaker.equipBestTool(agent, block(true));
        assertEquals(1, inv.selected);
    }

    @Test
    @DisplayName("A tool that yields no drops (wooden pickaxe on iron ore) never counts as best")
    void noDropToolIgnored() {
        AgentEntity agent = mock(AgentEntity.class);
        Inventory inv = inventoryOf(agent);
        BlockState iron = block(true);
        inv.setItem(0, tool(2.0F, false)); // faster than hands but no drops
        assertEquals(-1, BlockBreaker.bestToolSlot(agent, iron));
        inv.setItem(1, tool(4.0F, true));
        assertEquals(1, BlockBreaker.bestToolSlot(agent, iron));
        // A block that needs no specific tool keeps pure speed selection.
        assertEquals(1, BlockBreaker.bestToolSlot(agent, block(false)));
        assertFalse(BlockBreaker.yieldsDrops(tool(2.0F, false), iron));
        assertTrue(BlockBreaker.yieldsDrops(tool(2.0F, true), iron));
        assertFalse(BlockBreaker.yieldsDrops(ItemStack.EMPTY, iron));
        assertTrue(BlockBreaker.yieldsDrops(ItemStack.EMPTY, block(false)));
    }
}
