package com.civquest.mod.client.driver;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

public class ClientDriverHarnessTest {

    @BeforeEach
    @AfterEach
    void resetProperties() {
        System.clearProperty(ClientDriverHarness.PROP_ENABLED);
        System.clearProperty(ClientDriverHarness.PROP_TARGET);
        System.clearProperty(ClientDriverHarness.PROP_SCREENSHOTS_DIR);
        System.clearProperty(ClientDriverHarness.PROP_ACTIONS);
        System.clearProperty(ClientDriverHarness.PROP_EXIT_ON_COMPLETE);
    }

    @Test
    @DisplayName("Harness defaults to disabled when system property is not present")
    void testDisabledByDefault() {
        assertFalse(ClientDriverHarness.isEnabled());
    }

    @Test
    @DisplayName("Harness isEnabled respects system property")
    void testEnabledViaProperty() {
        System.setProperty(ClientDriverHarness.PROP_ENABLED, "true");
        assertTrue(ClientDriverHarness.isEnabled());

        System.setProperty(ClientDriverHarness.PROP_ENABLED, "false");
        assertFalse(ClientDriverHarness.isEnabled());
    }

    @Test
    @DisplayName("Default scenario creates expected sequence of baseline and interaction actions")
    void testDefaultScenarioConstruction() {
        System.setProperty(ClientDriverHarness.PROP_ENABLED, "true");
        System.setProperty(ClientDriverHarness.PROP_TARGET, "aria");

        // Force harness re-initialization
        try {
            java.lang.reflect.Field initField = ClientDriverHarness.class.getDeclaredField("initialized");
            initField.setAccessible(true);
            initField.set(null, false);
        } catch (Exception e) {
            fail("Failed to reset initialized field: " + e.getMessage());
        }

        ClientDriverHarness.initialize();
        List<DriverAction> queue = ClientDriverHarness.getActionQueue();
        assertFalse(queue.isEmpty(), "Default queue must not be empty");

        // First action is wait (world load stabilization)
        assertInstanceOf(DriverAction.WaitAction.class, queue.get(0));

        // Contains look action
        boolean hasLook = queue.stream().anyMatch(a -> a instanceof DriverAction.LookAtAgentAction);
        assertTrue(hasLook, "Default scenario must contain LookAtAgentAction");

        // Contains screenshot action
        boolean hasScreenshot = queue.stream().anyMatch(a -> a instanceof DriverAction.ScreenshotAction);
        assertTrue(hasScreenshot, "Default scenario must contain ScreenshotAction");

        // Contains whisper action
        boolean hasWhisper = queue.stream().anyMatch(a -> a instanceof DriverAction.WhisperAction);
        assertTrue(hasWhisper, "Default scenario must contain WhisperAction");

        // Contains key inputs
        boolean hasKeyHold = queue.stream().anyMatch(a -> a instanceof DriverAction.HoldKeyAction);
        assertTrue(hasKeyHold, "Default scenario must contain HoldKeyAction for navigation");
    }

    @Test
    @DisplayName("Custom action pipeline string parses discrete driver actions correctly")
    void testCustomActionPipelineParsing() {
        System.setProperty(ClientDriverHarness.PROP_ENABLED, "true");
        System.setProperty(ClientDriverHarness.PROP_ACTIONS,
                "wait:50,look_at_agent:boris,forward:15,jump:5,whisper:boris:come here,screenshot:test.png,exit");

        try {
            java.lang.reflect.Field initField = ClientDriverHarness.class.getDeclaredField("initialized");
            initField.setAccessible(true);
            initField.set(null, false);
        } catch (Exception e) {
            fail("Failed to reset initialized field: " + e.getMessage());
        }

        ClientDriverHarness.initialize();
        List<DriverAction> queue = ClientDriverHarness.getActionQueue();
        assertEquals(7, queue.size());

        assertInstanceOf(DriverAction.WaitAction.class, queue.get(0));
        assertEquals(50, ((DriverAction.WaitAction) queue.get(0)).ticks());

        assertInstanceOf(DriverAction.LookAtAgentAction.class, queue.get(1));
        assertEquals("boris", ((DriverAction.LookAtAgentAction) queue.get(1)).target());

        assertInstanceOf(DriverAction.HoldKeyAction.class, queue.get(2));
        assertEquals("FORWARD", ((DriverAction.HoldKeyAction) queue.get(2)).key());
        assertEquals(15, ((DriverAction.HoldKeyAction) queue.get(2)).ticks());

        assertInstanceOf(DriverAction.HoldKeyAction.class, queue.get(3));
        assertEquals("JUMP", ((DriverAction.HoldKeyAction) queue.get(3)).key());

        assertInstanceOf(DriverAction.WhisperAction.class, queue.get(4));
        assertEquals("boris", ((DriverAction.WhisperAction) queue.get(4)).target());
        assertEquals("come here", ((DriverAction.WhisperAction) queue.get(4)).message());

        assertInstanceOf(DriverAction.ScreenshotAction.class, queue.get(5));
        assertEquals("test.png", ((DriverAction.ScreenshotAction) queue.get(5)).filename());

        assertInstanceOf(DriverAction.ExitAction.class, queue.get(6));
    }

    @Test
    @DisplayName("Parses pipe-delimited action list with commas in chat/whisper")
    void testPipeDelimitedParser() {
        System.setProperty(ClientDriverHarness.PROP_ENABLED, "true");
        System.setProperty(ClientDriverHarness.PROP_ACTIONS,
                "wait:30|whisper:aria:Hello, world! How are you?|screenshot:comma,view.png|exit");

        try {
            java.lang.reflect.Field initField = ClientDriverHarness.class.getDeclaredField("initialized");
            initField.setAccessible(true);
            initField.set(null, false);
        } catch (Exception e) {
            fail("Failed to reset initialized field: " + e.getMessage());
        }

        ClientDriverHarness.initialize();
        List<DriverAction> queue = ClientDriverHarness.getActionQueue();
        assertEquals(4, queue.size());

        assertInstanceOf(DriverAction.WaitAction.class, queue.get(0));
        assertEquals(30, ((DriverAction.WaitAction) queue.get(0)).ticks());

        assertInstanceOf(DriverAction.WhisperAction.class, queue.get(1));
        DriverAction.WhisperAction whisper = (DriverAction.WhisperAction) queue.get(1);
        assertEquals("aria", whisper.target());
        assertEquals("Hello, world! How are you?", whisper.message());

        assertInstanceOf(DriverAction.ScreenshotAction.class, queue.get(2));
        assertEquals("comma,view.png", ((DriverAction.ScreenshotAction) queue.get(2)).filename());

        assertInstanceOf(DriverAction.ExitAction.class, queue.get(3));
    }
}
