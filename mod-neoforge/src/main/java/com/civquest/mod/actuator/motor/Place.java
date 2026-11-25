package com.civquest.mod.actuator.motor;

import com.civquest.mod.AgentEntity;

import java.util.function.Predicate;

/** Instant block placement (place, build, paving road, signpost). Finishes in the tick it starts. */
public record Place(String label, Predicate<AgentEntity> action) implements Motor {
    @Override
    public Outcome onTick(Body body) {
        return Act.run(this, label, action, body);
    }
}
