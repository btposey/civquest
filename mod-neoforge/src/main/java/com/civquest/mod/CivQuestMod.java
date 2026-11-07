package com.civquest.mod;

import com.civquest.mod.gametest.SettlerGameTest;
import com.mojang.logging.LogUtils;
import net.neoforged.bus.api.IEventBus;
import net.neoforged.fml.common.Mod;
import net.neoforged.neoforge.common.NeoForge;
import net.neoforged.neoforge.event.RegisterGameTestsEvent;
import net.neoforged.neoforge.event.server.ServerStartedEvent;
import net.neoforged.neoforge.event.server.ServerStartingEvent;
import net.neoforged.neoforge.event.server.ServerStoppingEvent;
import net.neoforged.neoforge.network.event.RegisterPayloadHandlersEvent;
import net.neoforged.neoforge.network.registration.PayloadRegistrar;
import org.slf4j.Logger;

@Mod(CivQuestMod.MOD_ID)
public class CivQuestMod {
    public static final String MOD_ID = "civquest";
    private static final Logger LOGGER = LogUtils.getLogger();
    private final CivQuestBridgeServer bridgeServer;

    public CivQuestMod(IEventBus modEventBus) {
        LOGGER.info("CivQuest mod initializing");
        modEventBus.addListener(this::onRegisterGameTests);
        modEventBus.addListener(this::onRegisterPayloadHandlers);

        this.bridgeServer = new CivQuestBridgeServer();
        NeoForge.EVENT_BUS.addListener(this::onServerStarting);
        NeoForge.EVENT_BUS.addListener(this::onServerStarted);
        NeoForge.EVENT_BUS.addListener(this::onServerStopping);
    }

    private void onRegisterPayloadHandlers(RegisterPayloadHandlersEvent event) {
        LOGGER.info("Registering CivQuest network payloads");
        PayloadRegistrar registrar = event.registrar("1");
        registrar.playToClient(
                ClientboundSettlerSkinPacket.TYPE,
                ClientboundSettlerSkinPacket.STREAM_CODEC,
                (payload, context) -> {
                    LOGGER.debug("Received ClientboundSettlerSkinPacket on client: agentId='{}', uuid='{}'",
                            payload.agentId(), payload.settlerUuid());
                }
        );
    }

    private void onRegisterGameTests(RegisterGameTestsEvent event) {
        LOGGER.info("Registering CivQuest GameTests");
        event.register(SettlerGameTest.class);
    }

    private void onServerStarting(ServerStartingEvent event) {
        bridgeServer.onServerStarting(event);
    }

    private void onServerStarted(ServerStartedEvent event) {
        bridgeServer.onServerStarted(event);
    }

    private void onServerStopping(ServerStoppingEvent event) {
        bridgeServer.onServerStopping(event);
    }

    public CivQuestBridgeServer getBridgeServer() {
        return bridgeServer;
    }

    public SettlerEntityManager getSettlerEntityManager() {
        return bridgeServer.getSettlerEntityManager();
    }
}
