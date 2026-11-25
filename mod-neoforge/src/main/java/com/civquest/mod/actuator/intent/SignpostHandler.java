package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.PlaceSignpostIntent;
import com.mojang.logging.LogUtils;
import net.minecraft.core.BlockPos;
import net.minecraft.network.chat.Component;
import net.minecraft.world.item.HangingSignItem;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.SignItem;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.entity.BlockEntity;
import net.minecraft.world.level.block.entity.SignBlockEntity;
import net.minecraft.world.level.block.entity.SignText;
import net.minecraft.world.level.block.state.BlockState;
import org.slf4j.Logger;

/** Signpost placement (PlaceSignpostIntent); instant. */
public class SignpostHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double INTERACT_REACH_SQ = 36.0;

    public boolean actuatePlaceSignpost(AgentEntity agent, PlaceSignpostIntent intent) {
        if (agent == null || intent == null || agent.level() == null) {
            return false;
        }

        BlockPos pos = new BlockPos(intent.getPos().getX(), intent.getPos().getY(), intent.getPos().getZ());
        if (!agent.level().hasChunkAt(pos)) {
            return false;
        }
        if (agent.distanceToSqr(pos.getX() + 0.5, pos.getY() + 0.5, pos.getZ() + 0.5) > INTERACT_REACH_SQ) {
            return false;
        }

        BlockState existing = agent.level().getBlockState(pos);
        if (!existing.canBeReplaced()) {
            pos = pos.above();
            existing = agent.level().getBlockState(pos);
            if (!existing.canBeReplaced()) {
                return false;
            }
        }

        BlockState signState = intent.getIsHangingSign()
                ? Blocks.OAK_HANGING_SIGN.defaultBlockState()
                : Blocks.OAK_SIGN.defaultBlockState();

        if (!signState.canSurvive(agent.level(), pos)) {
            return false;
        }

        if (!agent.isCreative()) {
            int signSlot = -1;
            int woodSlot = -1;
            for (int i = 0; i < agent.getInventory().getContainerSize(); i++) {
                ItemStack stack = agent.getInventory().getItem(i);
                if (!stack.isEmpty()) {
                    if (intent.getIsHangingSign()) {
                        if (stack.getItem() instanceof HangingSignItem) {
                            signSlot = i;
                            break;
                        }
                    } else {
                        if (stack.getItem() instanceof SignItem && !(stack.getItem() instanceof HangingSignItem)) {
                            signSlot = i;
                            break;
                        }
                    }
                    if (stack.is(net.minecraft.tags.ItemTags.PLANKS) || stack.is(net.minecraft.tags.ItemTags.LOGS)) {
                        woodSlot = i;
                    }
                }
            }
            if (signSlot >= 0) {
                agent.getInventory().getItem(signSlot).shrink(1);
            } else if (woodSlot >= 0) {
                agent.getInventory().getItem(woodSlot).shrink(1);
            } else {
                return false;
            }
        }

        boolean placed = agent.level().setBlockAndUpdate(pos, signState);
        if (!placed) {
            return false;
        }

        BlockEntity be = agent.level().getBlockEntity(pos);
        if (be instanceof SignBlockEntity signBe) {
            SignText front = signBe.getFrontText();
            front = front.setMessage(0, Component.literal(sanitizeSignLine(intent.getLine1())));
            front = front.setMessage(1, Component.literal(sanitizeSignLine(intent.getLine2())));
            front = front.setMessage(2, Component.literal(sanitizeSignLine(intent.getLine3())));
            front = front.setMessage(3, Component.literal(sanitizeSignLine(intent.getLine4())));
            signBe.setText(front, true);
            signBe.setChanged();
            agent.level().sendBlockUpdated(pos, signState, signState, 3);
            return true;
        }
        return placed;
    }

    private static String sanitizeSignLine(String line) {
        if (line == null) {
            return "";
        }
        return line.length() > 80 ? line.substring(0, 80) : line;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Place("PLACE_SIGNPOST", a -> actuatePlaceSignpost(a, intent.getPlaceSignpost()));
    }
}
