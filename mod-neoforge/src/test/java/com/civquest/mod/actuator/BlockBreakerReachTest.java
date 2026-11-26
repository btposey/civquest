package com.civquest.mod.actuator;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import net.minecraft.core.BlockPos;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.*;

/**
 * cq-gpa.9 contract: BlockBreaker.withinReach must agree with Go vertical.ReachableAABB on the shared fixture
 * orchestrator-go/test/integration/testdata/reach/block_reach.json (eye to nearest point of the block AABB).
 */
class BlockBreakerReachTest {
    private static final Path FIXTURE = Path.of("..", "orchestrator-go", "test", "integration", "testdata", "reach", "block_reach.json");

    @Test
    @DisplayName("withinReach agrees with every case of the shared Go/Java fixture")
    void fixtureAgreesWithGo() throws IOException {
        JsonObject root = JsonParser.parseString(Files.readString(FIXTURE, StandardCharsets.UTF_8)).getAsJsonObject();
        double eye = root.get("eyeHeight").getAsDouble();
        assertEquals(1.62, eye, 1e-12);
        assertEquals(BlockBreaker.REACH, root.get("reach").getAsDouble(), 1e-12);
        JsonArray cases = root.getAsJsonArray("cases");
        assertTrue(cases.size() > 0);
        for (JsonElement el : cases) {
            JsonObject c = el.getAsJsonObject();
            JsonArray f = c.getAsJsonArray("feet");
            JsonArray b = c.getAsJsonArray("block");
            boolean got = BlockBreaker.withinReach(f.get(0).getAsDouble(), f.get(1).getAsDouble() + eye, f.get(2).getAsDouble(),
                    new BlockPos(b.get(0).getAsInt(), b.get(1).getAsInt(), b.get(2).getAsInt()));
            assertEquals(c.get("reachable").getAsBoolean(), got, c.get("name").getAsString());
        }
    }

    @Test
    @DisplayName("Standing beside a trunk, logs up to y+6 are reachable and y+7 is not")
    void trunkReach() {
        double eyeY = 64.0 + 1.62;
        for (int dy = 0; dy <= 6; dy++) {
            assertTrue(BlockBreaker.withinReach(1.5, eyeY, 0.5, new BlockPos(0, 64 + dy, 0)), "dy=" + dy);
        }
        assertFalse(BlockBreaker.withinReach(1.5, eyeY, 0.5, new BlockPos(0, 71, 0)));
    }
}
