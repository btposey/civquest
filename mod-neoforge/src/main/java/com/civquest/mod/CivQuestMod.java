package com.civquest.mod;

import com.civquest.mod.gametest.SettlerGameTest;
import com.mojang.logging.LogUtils;
import net.neoforged.bus.api.IEventBus;
import net.neoforged.fml.common.Mod;
import net.neoforged.neoforge.event.RegisterGameTestsEvent;
import org.slf4j.Logger;

@Mod(CivQuestMod.MOD_ID)
public class CivQuestMod {
    public static final String MOD_ID = "civquest";
    private static final Logger LOGGER = LogUtils.getLogger();

    public CivQuestMod(IEventBus modEventBus) {
        LOGGER.info("CivQuest mod initializing");
        modEventBus.addListener(this::onRegisterGameTests);
    }

    private void onRegisterGameTests(RegisterGameTestsEvent event) {
        LOGGER.info("Registering CivQuest GameTests");
        event.register(SettlerGameTest.class);
    }
}
