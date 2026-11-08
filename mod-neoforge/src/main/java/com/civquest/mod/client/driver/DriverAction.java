package com.civquest.mod.client.driver;

/**
 * Represents a discrete scripted action executed by the {@link ClientDriverHarness}.
 */
public sealed interface DriverAction permits
        DriverAction.WaitAction,
        DriverAction.LookAtSettlerAction,
        DriverAction.HoldKeyAction,
        DriverAction.ReleaseKeysAction,
        DriverAction.ChatAction,
        DriverAction.WhisperAction,
        DriverAction.ScreenshotAction,
        DriverAction.ExitAction {

    record WaitAction(int ticks) implements DriverAction {}
    record LookAtSettlerAction(String target) implements DriverAction {}
    record HoldKeyAction(String key, int ticks) implements DriverAction {}
    record ReleaseKeysAction() implements DriverAction {}
    record ChatAction(String message) implements DriverAction {}
    record WhisperAction(String target, String message) implements DriverAction {}
    record ScreenshotAction(String filename) implements DriverAction {}
    record ExitAction() implements DriverAction {}
}
