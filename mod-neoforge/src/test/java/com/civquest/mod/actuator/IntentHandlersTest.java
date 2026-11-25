package com.civquest.mod.actuator;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.actuator.intent.AttackHandler;
import com.civquest.mod.actuator.intent.ChatHandler;
import com.civquest.mod.actuator.intent.CraftHandler;
import com.civquest.mod.actuator.intent.InteractHandler;
import com.civquest.mod.actuator.intent.NavigateHandler;
import com.civquest.mod.actuator.intent.SignpostHandler;
import com.civquest.mod.actuator.intent.SleepHandler;
import com.civquest.mod.actuator.intent.SurveyHandler;
import com.civquest.mod.actuator.intent.TradeHandler;
import com.civquest.mod.actuator.intent.UseItemHandler;
import com.civquest.mod.actuator.intent.BookHandler;
import com.civquest.mod.actuator.intent.BuildHandler;
import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.AttackIntent;
import com.civquest.mod.grpc.v1.BlockPosProto;
import com.civquest.mod.grpc.v1.BuildBlockIntent;
import com.civquest.mod.grpc.v1.CraftItemIntent;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.civquest.mod.grpc.v1.IntentBatch;
import com.civquest.mod.grpc.v1.IntentReceipt;
import com.civquest.mod.grpc.v1.IntentReceipt.ReceiptStatus;
import com.civquest.mod.grpc.v1.InteractBlockIntent;
import com.civquest.mod.grpc.v1.InteractBlockIntent.InteractionType;
import com.civquest.mod.grpc.v1.NavigateIntent;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.civquest.mod.grpc.v1.RespondTradeIntent;
import com.civquest.mod.grpc.v1.SendChatIntent;
import com.civquest.mod.grpc.v1.SurveyHorizonIntent;
import com.civquest.mod.grpc.v1.UseItemIntent;
import com.civquest.mod.grpc.v1.Vec3;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Per-intent {@link com.civquest.mod.actuator.intent.IntentHandler} factories and the one-frame-per-intent batch model. */
class IntentHandlersTest {
    private AgentEntity agent;
    private AgentNavigator nav;
    private AgentMotorFSM fsm;
    private IntentActuator actuator;

    @BeforeEach
    void setUp() {
        agent = mock(AgentEntity.class);
        nav = mock(AgentNavigator.class);
        when(agent.getAgentId()).thenReturn("handler-agent");
        when(agent.getNavigator()).thenReturn(nav);
        when(agent.getBreaker()).thenReturn(mock(BlockBreaker.class));
        fsm = new AgentMotorFSM(agent);
        when(agent.getMotorFSM()).thenReturn(fsm);
        actuator = new IntentActuator();
    }

    private static BlockPosProto pos(int x, int y, int z) {
        return BlockPosProto.newBuilder().setX(x).setY(y).setZ(z).build();
    }

    private static AgentIntent interact(InteractionType type) {
        return AgentIntent.newBuilder().setInteractBlock(
                InteractBlockIntent.newBuilder().setPos(pos(1, 64, 1)).setInteractionType(type).build()).build();
    }

    @Test
    void navigateHandlerBuildsMoveFromIntent() {
        AgentIntent intent = AgentIntent.newBuilder().setNavigate(NavigateIntent.newBuilder()
                .setTarget(Vec3.newBuilder().setX(1).setY(2).setZ(3).build())
                .setSpeedMultiplier(1.2f).setSprint(true).setAllowBreakBlocks(true).setArrivalTolerance(3).build()).build();
        Move m = assertInstanceOf(Move.class, new NavigateHandler().create(agent, intent));
        assertEquals(3.0, m.target().getZ());
        assertEquals(1.2f, m.speed());
        assertTrue(m.sprint());
        assertTrue(m.allowBreak());
        assertFalse(m.allowPlace());
        assertEquals(3, m.arrivalTolerance());
    }

    @Test
    void surveyHandlerClampsAndBuildsSweepStoppingNavigator() {
        AgentIntent intent = AgentIntent.newBuilder().setSurveyHorizon(
                SurveyHorizonIntent.newBuilder().setNumSectors(9999).setTicksPerSector(99).build()).build();
        Sweep sweep = assertInstanceOf(Sweep.class, new SurveyHandler().create(agent, intent));
        assertEquals(360, sweep.numSectors());
        assertEquals(10, sweep.ticksPerSector());
        assertEquals(Sweep.leaseTicks(360, 10), sweep.defaultLeaseTicks());
        verify(nav).stop();
        Sweep defaults = (Sweep) new SurveyHandler().create(agent,
                AgentIntent.newBuilder().setSurveyHorizon(SurveyHorizonIntent.getDefaultInstance()).build());
        assertEquals(36, defaults.numSectors());
        assertEquals(2, defaults.ticksPerSector());
    }

    @Test
    void interactHandlerDispatchesByInteractionType() {
        InteractHandler handler = new InteractHandler(new SleepHandler(), null);
        assertInstanceOf(Mine.class, handler.create(agent, interact(InteractionType.HARVEST_BREAK)));
        assertInstanceOf(Sleep.class, handler.create(agent, interact(InteractionType.SLEEP_IN_BED)));
        assertInstanceOf(Place.class, handler.create(agent, interact(InteractionType.PLACE_BLOCK)));
        assertInstanceOf(Place.class, handler.create(agent, interact(InteractionType.PAVE_ROAD_PATH)));
        assertInstanceOf(Act.class, handler.create(agent, interact(InteractionType.WAKE_UP)));
        assertInstanceOf(Act.class, handler.create(agent, interact(InteractionType.RING_BELL)));
    }

    @Test
    void interactHandlerSkipsHarvestOfAnAlreadyBrokenBlock() {
        net.minecraft.world.level.Level level = mock(net.minecraft.world.level.Level.class);
        net.minecraft.world.level.block.state.BlockState air = mock(net.minecraft.world.level.block.state.BlockState.class);
        when(agent.level()).thenReturn(level);
        when(level.hasChunkAt(any(net.minecraft.core.BlockPos.class))).thenReturn(true);
        when(level.getBlockState(any(net.minecraft.core.BlockPos.class))).thenReturn(air);
        when(air.isAir()).thenReturn(true);
        assertNull(new InteractHandler(new SleepHandler(), null).create(agent, interact(InteractionType.HARVEST_BREAK)));
    }

    @Test
    void remainingHandlersProduceTheirMotorTypes() {
        assertInstanceOf(Strike.class, new AttackHandler().create(agent,
                AgentIntent.newBuilder().setAttack(AttackIntent.newBuilder().setTargetEntityId(3).build()).build()));
        UseItem use = assertInstanceOf(UseItem.class, new UseItemHandler().create(agent, AgentIntent.newBuilder()
                .setUseItem(UseItemIntent.newBuilder().setInventorySlot(4).build()).build()));
        assertEquals("slot:4", use.itemId());
        assertFalse(use.emergency());
        ChatHandler chat = new ChatHandler(List::of);
        assertEquals("CHAT", ((Act) chat.create(agent, AgentIntent.newBuilder()
                .setChat(SendChatIntent.newBuilder().setMessage("hi").build()).build())).label());
        assertEquals("TRADE_RESPONSE", ((Act) new TradeHandler(chat).create(agent, AgentIntent.newBuilder()
                .setTradeResponse(RespondTradeIntent.getDefaultInstance()).build())).label());
        assertEquals("CRAFT_ITEM", ((Act) new CraftHandler(null).create(agent, AgentIntent.newBuilder()
                .setCraftItem(CraftItemIntent.getDefaultInstance()).build())).label());
        assertEquals("CREATE_BOOK", ((Act) new BookHandler().create(agent, AgentIntent.newBuilder()
                .setCreateBook(CreateWrittenBookIntent.getDefaultInstance()).build())).label());
        assertEquals("PLACE_SIGNPOST", ((Place) new SignpostHandler().create(agent, AgentIntent.newBuilder()
                .setPlaceSignpost(PlaceSignpostIntent.getDefaultInstance()).build())).label());
        assertEquals("BUILD_BLOCK", ((Place) new BuildHandler(new InteractHandler(new SleepHandler(), null)).create(agent,
                AgentIntent.newBuilder().setBuildBlock(BuildBlockIntent.getDefaultInstance()).build())).label());
    }

    private static AgentIntent nav(double x) {
        return AgentIntent.newBuilder().setNavigate(NavigateIntent.newBuilder()
                .setTarget(Vec3.newBuilder().setX(x).setY(64).setZ(0).build()).build()).build();
    }

    private static AgentIntent badCraft() {
        return AgentIntent.newBuilder().setCraftItem(CraftItemIntent.newBuilder().setRecipeId("minecraft:nope").build()).build();
    }

    @Test
    void batchIsOneFramePerIntentUnderTheBatchFrameIdWithNoCursor() {
        when(nav.isPathing()).thenReturn(true);
        IntentBatch batch = IntentBatch.newBuilder().setBatchId(7L).setFrameId(70L)
                .addIntents(nav(10)).addIntents(nav(20)).addIntents(nav(30)).build();
        actuator.submitBatch(agent, batch);

        List<Frame> frames = fsm.getMotorStack().snapshot();
        assertEquals(3, frames.size());
        // top-first: index 0 runs first
        assertEquals(List.of(0, 1, 2), frames.stream().map(Frame::index).toList());
        assertTrue(frames.stream().allMatch(f -> f.batchId() == 7L && f.frameId() == 70L));
        IntentReceipt executing = actuator.getCurrentReceipt(agent);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_EXECUTING, executing.getStatus());
        assertEquals(70L, executing.getFrameId());
        assertEquals(0, executing.getActiveIndex());
    }

    @Test
    void failureDropsRestOfBatchAndEmitsOneReceiptWithFrameId() {
        IntentBatch batch = IntentBatch.newBuilder().setBatchId(8L).setFrameId(80L)
                .addIntents(badCraft()).addIntents(nav(5)).addIntents(nav(6)).build();
        actuator.submitBatch(agent, batch);

        assertTrue(fsm.isMotorIdle(), "remaining intents of the failed batch are popped");
        IntentReceipt r = actuator.getCurrentReceipt(agent);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, r.getStatus());
        assertEquals(8L, r.getBatchId());
        assertEquals(80L, r.getFrameId());
        assertEquals(0, r.getActiveIndex());
        assertTrue(r.getFailureReason().contains("index 0"));
        verify(nav, never()).navigate(anyDouble(), anyDouble(), anyDouble(), anyFloat(), anyBoolean(), anyBoolean(), anyBoolean(), anyInt());
    }

    @Test
    void leaseExpiryEmitsTimeoutNotAborted() {
        when(nav.isPathing()).thenReturn(true);
        IntentBatch batch = IntentBatch.newBuilder().setBatchId(9L).setFrameId(90L).setTimeoutTicks(2L)
                .addIntents(nav(10)).addIntents(nav(20)).build();
        actuator.submitBatch(agent, batch);
        fsm.tick();
        fsm.tick();
        IntentReceipt r = actuator.getCurrentReceipt(agent);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_TIMEOUT, r.getStatus());
        assertEquals(90L, r.getFrameId());
        assertTrue(fsm.isMotorIdle());
    }

    @Test
    void unsupportedIntentFailsInsteadOfStallingTheBatch() {
        AgentIntent transfer = AgentIntent.newBuilder()
                .setTransferContainer(com.civquest.mod.grpc.v1.TransferContainerIntent.getDefaultInstance()).build();
        actuator.submitBatch(agent, IntentBatch.newBuilder().setBatchId(11L).addIntents(transfer).build());
        IntentReceipt r = actuator.getCurrentReceipt(agent);
        assertEquals(ReceiptStatus.RECEIPT_STATUS_FAILED, r.getStatus());
        assertTrue(r.getFailureReason().contains("TRANSFER_CONTAINER"));
    }
}
