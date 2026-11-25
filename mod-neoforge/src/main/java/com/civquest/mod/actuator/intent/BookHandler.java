package com.civquest.mod.actuator.intent;

import com.civquest.mod.actuator.motor.*;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.AgentEntity;
import com.civquest.mod.grpc.v1.CreateWrittenBookIntent;
import com.mojang.logging.LogUtils;
import java.util.ArrayList;
import java.util.List;
import net.minecraft.core.BlockPos;
import net.minecraft.core.component.DataComponents;
import net.minecraft.network.chat.Component;
import net.minecraft.server.network.Filterable;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.item.Items;
import net.minecraft.world.item.component.WrittenBookContent;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.LecternBlock;
import net.minecraft.world.level.block.entity.BlockEntity;
import net.minecraft.world.level.block.entity.LecternBlockEntity;
import net.minecraft.world.level.block.state.BlockState;
import org.slf4j.Logger;

/** Written-book creation on a lectern (CreateWrittenBookIntent); instant. */
public class BookHandler implements IntentHandler {
    private static final Logger LOGGER = LogUtils.getLogger();
    private static final double INTERACT_REACH_SQ = 36.0;

    public boolean actuateCreateWrittenBook(AgentEntity agent, CreateWrittenBookIntent intent) {
        if (agent == null || intent == null) {
            return false;
        }

        List<String> rawPages = intent.getPagesList();
        if (rawPages.size() > 100) {
            return false;
        }

        String title = intent.getTitle() != null ? intent.getTitle() : "Chronicle";
        if (title.length() > 32) {
            return false;
        }

        List<Filterable<Component>> pages = new ArrayList<>();
        for (String pageText : rawPages) {
            if (pageText != null && pageText.length() > 1024) {
                return false;
            }
            pages.add(Filterable.passThrough(Component.literal(pageText != null ? pageText : "")));
        }

        ItemStack book = new ItemStack(Items.WRITTEN_BOOK);
        WrittenBookContent content = new WrittenBookContent(
                Filterable.passThrough(title),
                intent.getAuthor() != null ? intent.getAuthor() : agent.getGameProfile().getName(),
                0,
                pages,
                true
        );
        book.set(DataComponents.WRITTEN_BOOK_CONTENT, content);

        if (intent.hasTargetLecternOrShelf()) {
            if (agent.level() == null) {
                return false;
            }
            BlockPos targetPos = new BlockPos(
                    intent.getTargetLecternOrShelf().getX(),
                    intent.getTargetLecternOrShelf().getY(),
                    intent.getTargetLecternOrShelf().getZ()
            );
            if (!agent.level().hasChunkAt(targetPos)) {
                return false;
            }
            if (agent.distanceToSqr(targetPos.getX() + 0.5, targetPos.getY() + 0.5, targetPos.getZ() + 0.5) > INTERACT_REACH_SQ) {
                return false;
            }
            BlockState state = agent.level().getBlockState(targetPos);
            if (state.is(Blocks.LECTERN)) {
                BlockEntity be = agent.level().getBlockEntity(targetPos);
                if (!(be instanceof LecternBlockEntity lectern) || lectern.hasBook()) {
                    return false;
                }
                lectern.setBook(book, agent);
                LecternBlock.resetBookState(agent, agent.level(), targetPos, state, true);
                return true;
            } else if (state.is(Blocks.CHISELED_BOOKSHELF)) {
                BlockEntity be = agent.level().getBlockEntity(targetPos);
                if (be instanceof net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity shelf) {
                    int slot = intent.getBookshelfSlot();
                    if (slot < 0 || slot >= shelf.getContainerSize() || !shelf.getItem(slot).isEmpty()) {
                        // Find first available slot
                        slot = -1;
                        for (int i = 0; i < shelf.getContainerSize(); i++) {
                            if (shelf.getItem(i).isEmpty()) {
                                slot = i;
                                break;
                            }
                        }
                    }
                    if (slot >= 0 && slot < shelf.getContainerSize() && shelf.getItem(slot).isEmpty()) {
                        shelf.setItem(slot, book);
                        return true;
                    }
                }
                return false;
            }
            return false;
        }

        agent.getInventory().add(book);
        return true;
    }

    @Override
    public Motor create(AgentEntity agent, AgentIntent intent) {
        return new Act("CREATE_BOOK", a -> actuateCreateWrittenBook(a, intent.getCreateBook()));
    }
}
