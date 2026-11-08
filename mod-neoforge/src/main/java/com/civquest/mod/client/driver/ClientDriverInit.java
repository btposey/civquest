package com.civquest.mod.client.driver;

import net.neoforged.bus.api.IEventBus;
import net.neoforged.neoforge.common.NeoForge;

/**
 * Initializes and registers the client driver harness event listeners.
 * Isolated behind physical client execution checks (ADR-014).
 */
public class ClientDriverInit {
    public static void register(IEventBus modEventBus) {
        if (ClientDriverHarness.isEnabled()) {
            NeoForge.EVENT_BUS.addListener(ClientDriverHarness::onClientTick);
            NeoForge.EVENT_BUS.addListener(ClientDriverHarness::onClientLoggedIn);
            NeoForge.EVENT_BUS.addListener(ClientDriverHarness::onClientLoggedOut);
            ClientDriverHarness.initialize();
        }
    }
}
