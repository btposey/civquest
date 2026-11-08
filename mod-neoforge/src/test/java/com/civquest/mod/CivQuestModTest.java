package com.civquest.mod;

import com.civquest.mod.grpc.v1.ChatEvent;
import com.civquest.mod.grpc.v1.SpawnAgentRequest;
import com.civquest.mod.grpc.v1.Vec3;
import net.minecraft.commands.CommandSourceStack;
import net.minecraft.network.chat.Component;
import net.minecraft.server.level.ServerPlayer;
import net.neoforged.bus.api.IEventBus;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import org.mockito.Mockito;

import java.util.UUID;
import java.util.function.Supplier;

import static org.junit.jupiter.api.Assertions.*;

public class CivQuestModTest {

    @org.junit.jupiter.api.BeforeAll
    static void initMinecraft() {
        try {
            net.minecraft.SharedConstants.tryDetectVersion();
            net.minecraft.server.Bootstrap.bootStrap();
        } catch (Throwable ignored) {}
    }

    @Test
    @DisplayName("CivQuestMod initialization does not eagerly instantiate CivQuestBridgeServer")
    void testLazyBridgeServerInitialization() {
        IEventBus mockModBus = Mockito.mock(IEventBus.class);
        CivQuestMod mod = new CivQuestMod(mockModBus);

        // Invariant: On physical client launch or before server startup, bridgeServer MUST remain uninstantiated
        assertNull(mod.getBridgeServer(), "CivQuestBridgeServer must not be eagerly instantiated during mod construction");
        assertNull(mod.getSettlerEntityManager(), "SettlerEntityManager must be null when bridgeServer is uninstantiated");
    }

    @Test
    @DisplayName("executeTell fails with error message when no settlers exist")
    void testExecuteTellNoSettlers() {
        IEventBus mockModBus = Mockito.mock(IEventBus.class);
        CivQuestMod mod = new CivQuestMod(mockModBus);

        CommandSourceStack mockSource = Mockito.mock(CommandSourceStack.class);
        int result = mod.executeTell(mockSource, "aria", "hello");

        assertEquals(0, result);
        Mockito.verify(mockSource).sendFailure(Mockito.argThat(comp -> comp.getString().contains("No active settlers found")));
    }

    @Test
    @DisplayName("executeTell dispatches ChatEvent and sends confirmation to sender")
    void testExecuteTellSuccess() throws Exception {
        IEventBus mockModBus = Mockito.mock(IEventBus.class);
        CivQuestMod mod = new CivQuestMod(mockModBus);

        SettlerEntityManager mockManager = Mockito.mock(SettlerEntityManager.class);
        SettlerEntity mockSettler = Mockito.mock(SettlerEntity.class);
        com.mojang.authlib.GameProfile profile = new com.mojang.authlib.GameProfile(UUID.randomUUID(), "aria");
        Mockito.when(mockSettler.getGameProfile()).thenReturn(profile);
        Mockito.when(mockSettler.getAgentId()).thenReturn("aria");

        java.util.Map<String, SettlerEntity> settlersMap = java.util.Map.of("aria", mockSettler);
        Mockito.when(mockManager.getAllSettlers()).thenReturn(settlersMap);

        CivQuestBridgeServer bridgeServer = Mockito.mock(CivQuestBridgeServer.class);
        Mockito.when(bridgeServer.getSettlerEntityManager()).thenReturn(mockManager);

        java.lang.reflect.Field bridgeField = CivQuestMod.class.getDeclaredField("bridgeServer");
        bridgeField.setAccessible(true);
        bridgeField.set(mod, bridgeServer);

        CommandSourceStack mockSource = Mockito.mock(CommandSourceStack.class);
        ServerPlayer mockPlayer = Mockito.mock(ServerPlayer.class);
        Mockito.when(mockPlayer.getUUID()).thenReturn(UUID.randomUUID());
        Mockito.when(mockPlayer.getName()).thenReturn(Component.literal("pbots"));
        Mockito.when(mockSource.getPlayer()).thenReturn(mockPlayer);

        int result = mod.executeTell(mockSource, "aria", "Hello from test!");
        assertEquals(1, result);

        // Verify settler received ChatEvent
        ArgumentCaptor<ChatEvent> chatCaptor = ArgumentCaptor.forClass(ChatEvent.class);
        Mockito.verify(mockSettler).queueIncomingChat(chatCaptor.capture());
        ChatEvent event = chatCaptor.getValue();
        assertEquals("pbots", event.getSenderName());
        assertEquals("Hello from test!", event.getMessage());

        // Verify sender received confirmation whisper message
        ArgumentCaptor<Supplier<Component>> supplierCaptor = ArgumentCaptor.forClass(Supplier.class);
        Mockito.verify(mockSource).sendSuccess(supplierCaptor.capture(), Mockito.eq(false));
        Component sentComponent = supplierCaptor.getValue().get();
        assertTrue(sentComponent.getString().contains("You whisper to aria: Hello from test!"));
    }

    @Test
    @DisplayName("executeTell succeeds when executed from console source (player is null)")
    void testExecuteTellConsoleSource() throws Exception {
        CivQuestMod mod = new CivQuestMod(Mockito.mock(IEventBus.class));
        SettlerEntityManager mockManager = Mockito.mock(SettlerEntityManager.class);
        SettlerEntity mockSettler = Mockito.mock(SettlerEntity.class);
        com.mojang.authlib.GameProfile profile = new com.mojang.authlib.GameProfile(UUID.randomUUID(), "aria");
        Mockito.when(mockSettler.getGameProfile()).thenReturn(profile);
        Mockito.when(mockSettler.getAgentId()).thenReturn("aria");

        java.util.Map<String, SettlerEntity> settlersMap = java.util.Map.of("aria", mockSettler);
        Mockito.when(mockManager.getAllSettlers()).thenReturn(settlersMap);

        CivQuestBridgeServer bridgeServer = Mockito.mock(CivQuestBridgeServer.class);
        Mockito.when(bridgeServer.getSettlerEntityManager()).thenReturn(mockManager);

        java.lang.reflect.Field bridgeField = CivQuestMod.class.getDeclaredField("bridgeServer");
        bridgeField.setAccessible(true);
        bridgeField.set(mod, bridgeServer);

        CommandSourceStack mockSource = Mockito.mock(CommandSourceStack.class);
        Mockito.when(mockSource.getPlayer()).thenReturn(null); // Console

        int result = mod.executeTell(mockSource, "aria", "System broadcast message");
        assertEquals(1, result);

        ArgumentCaptor<ChatEvent> chatCaptor = ArgumentCaptor.forClass(ChatEvent.class);
        Mockito.verify(mockSettler).queueIncomingChat(chatCaptor.capture());
        ChatEvent event = chatCaptor.getValue();
        assertEquals("Console", event.getSenderName());
        assertEquals("CONSOLE", event.getSenderUuid());
        assertEquals("System broadcast message", event.getMessage());
    }
}

