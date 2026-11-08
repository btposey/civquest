package com.civquest.mod;

import net.neoforged.bus.api.IEventBus;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;

import static org.junit.jupiter.api.Assertions.*;

public class CivQuestModTest {

    @Test
    @DisplayName("CivQuestMod initialization does not eagerly instantiate CivQuestBridgeServer")
    void testLazyBridgeServerInitialization() {
        IEventBus mockModBus = Mockito.mock(IEventBus.class);
        CivQuestMod mod = new CivQuestMod(mockModBus);

        // Invariant: On physical client launch or before server startup, bridgeServer MUST remain uninstantiated
        assertNull(mod.getBridgeServer(), "CivQuestBridgeServer must not be eagerly instantiated during mod construction");
        assertNull(mod.getSettlerEntityManager(), "SettlerEntityManager must be null when bridgeServer is uninstantiated");
    }
}
