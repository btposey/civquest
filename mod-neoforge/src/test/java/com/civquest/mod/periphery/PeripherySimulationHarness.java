package com.civquest.mod.periphery;

import com.civquest.mod.AgentEntity;
import com.civquest.mod.AgentEntityManager;
import com.civquest.mod.CivQuestBridgeServiceImpl;
import com.civquest.mod.StreamRegistry;
import com.civquest.mod.actuator.AgentNavigator;
import com.civquest.mod.actuator.BlockBreaker;
import com.civquest.mod.actuator.IntentActuator;
import com.civquest.mod.grpc.v1.AgentIntent;
import com.civquest.mod.grpc.v1.ChatEvent;
import com.civquest.mod.grpc.v1.DamageEvent;
import com.civquest.mod.grpc.v1.DeathEvent;
import com.civquest.mod.grpc.v1.TelemetryFrame;
import com.civquest.mod.sensory.SensoryFrustumCollector;
import com.mojang.authlib.GameProfile;
import io.grpc.stub.StreamObserver;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.network.chat.Component;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.server.players.PlayerList;
import net.minecraft.world.damagesource.DamageSource;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.item.ItemEntity;
import net.minecraft.world.entity.player.Inventory;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.ClipContext;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.Blocks;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.phys.AABB;
import net.minecraft.world.phys.BlockHitResult;
import net.minecraft.world.phys.HitResult;
import net.minecraft.world.phys.Vec3;
import net.minecraft.world.phys.shapes.Shapes;
import net.neoforged.neoforge.event.entity.living.LivingDamageEvent;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Predicate;
import java.util.function.Supplier;

import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyBoolean;
import static org.mockito.ArgumentMatchers.anyDouble;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.doReturn;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

/**
 * Mock-driven periphery simulation harness for CivQuest NeoForge mod.
 * Simulates Minecraft server/level dependencies, in-memory voxel world, entity registry,
 * and the external Go orchestrator bidirectional gRPC stream without booting a dedicated server.
 */
public class PeripherySimulationHarness {

    private final MinecraftServer mockServer;
    private final ServerLevel mockLevel;
    private final PlayerList mockPlayerList;
    private final StreamRegistry streamRegistry;
    private final com.civquest.mod.sensory.SensoryFrustumCollector sensoryCollector;
    private final IntentActuator intentActuator;
    private final AgentEntityManager entityManager;
    private final CivQuestBridgeServiceImpl bridgeService;

    private final Map<BlockPos, BlockState> blockWorld = new ConcurrentHashMap<>();
    private final Map<BlockPos, Integer> lightWorld = new ConcurrentHashMap<>();
    private final List<Entity> entities = new CopyOnWriteArrayList<>();
    private final List<ServerPlayer> players = new CopyOnWriteArrayList<>();
    private final List<TelemetryFrame> receivedFrames = new CopyOnWriteArrayList<>();

    private final AtomicReference<StreamObserver<AgentIntent>> intentStream = new AtomicReference<>();
    private final AtomicBoolean lineOfSightBlocked = new AtomicBoolean(false);

    public PeripherySimulationHarness() {
        mockServer = mock(MinecraftServer.class, org.mockito.Mockito.withSettings().mockMaker(org.mockito.MockMakers.SUBCLASS));
        mockLevel = mock(ServerLevel.class);
        mockPlayerList = mock(PlayerList.class, org.mockito.Mockito.withSettings().mockMaker(org.mockito.MockMakers.SUBCLASS));

        // Server stubbing
        doReturn(mockPlayerList).when(mockServer).getPlayerList();
        doReturn(players).when(mockPlayerList).getPlayers();
        doReturn(true).when(mockServer).isPvpAllowed();
        doReturn(true).when(mockServer).isRunning();
        doReturn(100).when(mockServer).getTickCount();

        // Synchronous immediate task execution for mock server
        doAnswer(invoc -> {
            Runnable r = invoc.getArgument(0);
            r.run();
            return CompletableFuture.completedFuture(null);
        }).when(mockServer).submit(any(Runnable.class));

        doAnswer(invoc -> {
            Supplier<?> s = invoc.getArgument(0);
            return CompletableFuture.completedFuture(s.get());
        }).when(mockServer).submit(any(Supplier.class));

        // Level stubbing
        when(mockLevel.getServer()).thenReturn(mockServer);
        when(mockLevel.hasChunkAt(any(BlockPos.class))).thenReturn(true);
        when(mockLevel.getMinBuildHeight()).thenReturn(-64);
        when(mockLevel.getMaxBuildHeight()).thenReturn(320);

        when(mockLevel.getBlockState(any(BlockPos.class))).thenAnswer(invoc -> {
            BlockPos p = invoc.getArgument(0);
            return blockWorld.getOrDefault(p, Blocks.AIR.defaultBlockState());
        });

        when(mockLevel.setBlockAndUpdate(any(BlockPos.class), any(BlockState.class))).thenAnswer(invoc -> {
            BlockPos p = invoc.getArgument(0);
            BlockState s = invoc.getArgument(1);
            blockWorld.put(p.immutable(), s);
            return true;
        });

        when(mockLevel.destroyBlock(any(BlockPos.class), anyBoolean(), any())).thenAnswer(invoc -> {
            BlockPos p = invoc.getArgument(0);
            blockWorld.remove(p);
            return true;
        });

        when(mockLevel.getMaxLocalRawBrightness(any(BlockPos.class))).thenAnswer(invoc -> {
            BlockPos p = invoc.getArgument(0);
            return lightWorld.getOrDefault(p, 15);
        });

        // Entity querying stubs
        when(mockLevel.getEntities(any(Entity.class), any(AABB.class), any())).thenAnswer(invoc -> {
            Entity except = invoc.getArgument(0);
            AABB bb = invoc.getArgument(1);
            Predicate<Entity> predicate = invoc.getArgument(2);
            List<Entity> result = new ArrayList<>();
            for (Entity e : entities) {
                if (e != except && (predicate == null || predicate.test(e))) {
                    if (e.getBoundingBox() == null || bb == null || e.getBoundingBox().intersects(bb)) {
                        result.add(e);
                    }
                }
            }
            return result;
        });

        doAnswer(invoc -> {
            Class<?> clazz = invoc.getArgument(0);
            AABB bb = invoc.getArgument(1);
            Predicate<?> predicate = invoc.getArgument(2);
            List<Entity> result = new ArrayList<>();
            for (Entity e : entities) {
                if (clazz.isInstance(e)) {
                    @SuppressWarnings("unchecked")
                    Predicate<Entity> typed = (Predicate<Entity>) predicate;
                    if (typed == null || typed.test(e)) {
                        if (e.getBoundingBox() == null || bb == null || e.getBoundingBox().intersects(bb)) {
                            result.add(e);
                        }
                    }
                }
            }
            return result;
        }).when(mockLevel).getEntitiesOfClass(any(), any(AABB.class), any());

        // Raycasting / clip stubbing
        when(mockLevel.clip(any(ClipContext.class))).thenAnswer(invoc -> {
            if (lineOfSightBlocked.get()) {
                return new BlockHitResult(Vec3.ZERO, Direction.UP, new BlockPos(0, 64, 0), false);
            }
            ClipContext ctx = invoc.getArgument(0);
            return BlockHitResult.miss(ctx.getTo(), Direction.UP, BlockPos.ZERO);
        });

        streamRegistry = new StreamRegistry();
        sensoryCollector = new SensoryFrustumCollector();
        intentActuator = new IntentActuator();
        entityManager = new AgentEntityManager(streamRegistry, sensoryCollector, intentActuator);
        bridgeService = new CivQuestBridgeServiceImpl(streamRegistry, entityManager, () -> mockServer);
    }

    public MinecraftServer getMockServer() {
        return mockServer;
    }

    public ServerLevel getMockLevel() {
        return mockLevel;
    }

    public StreamRegistry getStreamRegistry() {
        return streamRegistry;
    }

    public SensoryFrustumCollector getSensoryCollector() {
        return sensoryCollector;
    }

    public IntentActuator getIntentActuator() {
        return intentActuator;
    }

    public AgentEntityManager getEntityManager() {
        return entityManager;
    }

    public CivQuestBridgeServiceImpl getBridgeService() {
        return bridgeService;
    }

    public void setBlock(BlockPos pos, BlockState state) {
        blockWorld.put(pos.immutable(), state);
    }

    public BlockState getBlock(BlockPos pos) {
        return blockWorld.getOrDefault(pos, Blocks.AIR.defaultBlockState());
    }

    public void setLightLevel(BlockPos pos, int level) {
        lightWorld.put(pos.immutable(), level);
    }

    public void spawnEntity(Entity entity) {
        entities.add(entity);
    }

    public void removeEntity(Entity entity) {
        entities.remove(entity);
    }

    public void addPlayer(ServerPlayer player) {
        players.add(player);
    }

    public void removePlayer(ServerPlayer player) {
        players.remove(player);
    }

    public void setLineOfSightBlocked(boolean blocked) {
        this.lineOfSightBlocked.set(blocked);
    }

    public List<TelemetryFrame> getReceivedFrames() {
        return Collections.unmodifiableList(receivedFrames);
    }

    public TelemetryFrame getLatestFrame() {
        if (receivedFrames.isEmpty()) {
            return null;
        }
        return receivedFrames.get(receivedFrames.size() - 1);
    }

    public void clearReceivedFrames() {
        receivedFrames.clear();
    }

    /**
     * Connects an agent stream via CivQuestBridgeServiceImpl and registers the bidirectional IPC pipeline.
     */
    public StreamObserver<AgentIntent> connectAgentStream(String agentId) {
        StreamObserver<AgentIntent> observer = bridgeService.connectAgent(new StreamObserver<>() {
            @Override
            public void onNext(TelemetryFrame frame) {
                receivedFrames.add(frame);
            }

            @Override
            public void onError(Throwable t) {}

            @Override
            public void onCompleted() {}
        });

        this.intentStream.set(observer);
        // Bind agentId on stream with initial intent
        observer.onNext(AgentIntent.newBuilder().setAgentId(agentId).build());
        return observer;
    }

    /**
     * Dispatches an AgentIntent through the IPC stream observer into the actuation loop.
     */
    public void sendIntent(AgentIntent intent) {
        StreamObserver<AgentIntent> observer = intentStream.get();
        if (observer != null) {
            observer.onNext(intent);
        } else {
            entityManager.handleIntent(mockServer, intent);
        }
    }

    /**
     * Creates and registers a mock AgentEntity with real inventory, block breaker, and sensory collectors.
     */
    public AgentEntity createMockAgent(String agentId, String displayName, BlockPos pos) {
        AgentEntity agent = mock(AgentEntity.class);
        when(agent.getAgentId()).thenReturn(agentId);
        when(agent.getClanId()).thenReturn("clan-" + agentId);

        UUID uuid = AgentEntity.createOfflineUUID(displayName);
        GameProfile profile = new GameProfile(uuid, displayName);
        when(agent.getGameProfile()).thenReturn(profile);
        when(agent.getUUID()).thenReturn(uuid);
        when(agent.getName()).thenReturn(Component.literal(displayName));

        when(agent.level()).thenReturn(mockLevel);
        when(agent.serverLevel()).thenReturn(mockLevel);
        when(agent.getServer()).thenReturn(mockServer);

        when(agent.blockPosition()).thenReturn(pos.immutable());
        when(agent.getX()).thenReturn(pos.getX() + 0.5);
        when(agent.getY()).thenReturn((double) pos.getY());
        when(agent.getZ()).thenReturn(pos.getZ() + 0.5);
        when(agent.getEyeY()).thenReturn(pos.getY() + 1.62);
        when(agent.getEyePosition()).thenReturn(new Vec3(pos.getX() + 0.5, pos.getY() + 1.62, pos.getZ() + 0.5));

        AABB aabb = new AABB(pos.getX() + 0.2, pos.getY(), pos.getZ() + 0.2, pos.getX() + 0.8, pos.getY() + 1.8, pos.getZ() + 0.8);
        when(agent.getBoundingBox()).thenReturn(aabb);

        when(agent.isAlive()).thenReturn(true);
        when(agent.isRemoved()).thenReturn(false);
        when(agent.isDeadOrDying()).thenReturn(false);
        when(agent.isSleeping()).thenReturn(false);
        when(agent.isInWater()).thenReturn(false);
        when(agent.isOnFire()).thenReturn(false);
        when(agent.getHealth()).thenReturn(20.0f);
        when(agent.getMaxHealth()).thenReturn(20.0f);
        when(agent.isCreative()).thenReturn(false);

        Inventory inventory = new Inventory(agent);
        when(agent.getInventory()).thenReturn(inventory);
        when(agent.getMainHandItem()).thenAnswer(invoc -> inventory.getSelected());

        when(agent.distanceToSqr(anyDouble(), anyDouble(), anyDouble())).thenAnswer(invoc -> {
            double tx = invoc.getArgument(0);
            double ty = invoc.getArgument(1);
            double tz = invoc.getArgument(2);
            double dx = agent.getX() - tx;
            double dy = agent.getY() - ty;
            double dz = agent.getZ() - tz;
            return dx * dx + dy * dy + dz * dz;
        });

        when(agent.distanceToSqr(any(Entity.class))).thenAnswer(invoc -> {
            Entity other = invoc.getArgument(0);
            return agent.distanceToSqr(other.getX(), other.getY(), other.getZ());
        });

        when(agent.distanceTo(any(Entity.class))).thenAnswer(invoc ->
                (float) Math.sqrt(agent.distanceToSqr((Entity) invoc.getArgument(0))));

        when(agent.getDestroySpeed(any(BlockState.class))).thenAnswer(invoc -> {
            ItemStack tool = agent.getMainHandItem();
            return !tool.isEmpty() ? tool.getDestroySpeed(invoc.getArgument(0)) : 1.0f;
        });

        when(agent.getDigSpeed(any(BlockState.class), any())).thenAnswer(invoc -> {
            ItemStack tool = agent.getMainHandItem();
            float sp = !tool.isEmpty() ? tool.getDestroySpeed(invoc.getArgument(0)) : 1.0f;
            return sp > 1.0f ? sp : 2.0f;
        });

        when(agent.hasCorrectToolForDrops(any(BlockState.class))).thenAnswer(invoc -> {
            ItemStack tool = agent.getMainHandItem();
            return !tool.isEmpty() && tool.isCorrectToolForDrops(invoc.getArgument(0));
        });

        BlockBreaker breaker = new BlockBreaker(agent);
        when(agent.getBreaker()).thenReturn(breaker);

        when(agent.equipBestToolFor(any(BlockState.class))).thenAnswer(invoc -> {
            BlockState target = invoc.getArgument(0);
            int bestSlot = BlockBreaker.bestToolSlot(agent, target);
            if (bestSlot >= 0) {
                BlockBreaker.equipBestTool(agent, target);
                return true;
            }
            return false;
        });

        // Chat queues
        ConcurrentLinkedQueue<ChatEvent> chatQueue = new ConcurrentLinkedQueue<>();
        doAnswer(invoc -> {
            chatQueue.add(invoc.getArgument(0));
            return null;
        }).when(agent).queueIncomingChat(any(ChatEvent.class));

        when(agent.pollIncomingChats()).thenAnswer(invoc -> {
            List<ChatEvent> list = new ArrayList<>(chatQueue);
            chatQueue.clear();
            return list;
        });

        when(agent.pollPendingTrade()).thenReturn(null);
        when(agent.pollReflexNotices()).thenReturn(Collections.emptyList());

        // Delegate tryPickupNearbyItems to production semantics
        doAnswer(invoc -> {
            performItemPickup(agent);
            return null;
        }).when(agent).tryPickupNearbyItems();

        agent.setStreamRegistry(streamRegistry);
        agent.setSensoryCollector(sensoryCollector);

        entityManager.registerAgent(agent);
        return agent;
    }

    /**
     * Executes item pickup with gift attribution against simulated ItemEntities in the level.
     */
    public void performItemPickup(AgentEntity agent) {
        if (agent.level() == null || agent.isRemoved()) {
            return;
        }

        AABB scanBox = agent.getBoundingBox().inflate(2.5, 1.5, 2.5);
        List<ItemEntity> items = new ArrayList<>();
        for (Entity e : entities) {
            if (e instanceof ItemEntity ie && ie.isAlive() && !ie.hasPickUpDelay() && !ie.getItem().isEmpty()) {
                if (ie.getBoundingBox().intersects(scanBox)) {
                    items.add(ie);
                }
            }
        }

        if (items.isEmpty()) {
            return;
        }

        ServerPlayer nearestPlayer = null;
        double bestDistSq = 16.0 * 16.0;
        for (ServerPlayer sp : players) {
            double dSq = agent.distanceToSqr(sp);
            if (dSq <= bestDistSq) {
                bestDistSq = dSq;
                nearestPlayer = sp;
            }
        }

        String agentName = agent.getGameProfile().getName();
        for (ItemEntity ie : items) {
            ItemStack copy = ie.getItem().copy();
            int origCount = copy.getCount();
            String itemName = copy.getHoverName().getString();

            agent.getInventory().add(copy);
            int pickedCount = origCount - copy.getCount();

            if (pickedCount > 0) {
                if (copy.isEmpty()) {
                    ie.discard();
                    entities.remove(ie);
                } else {
                    ie.setItem(copy);
                    ie.setNoPickUpDelay();
                }

                Entity owner = ie.getOwner();
                UUID thrower = owner != null ? owner.getUUID() : null;

                if (nearestPlayer != null && thrower != null && thrower.equals(nearestPlayer.getUUID())) {
                    nearestPlayer.sendSystemMessage(Component.literal(
                            "<" + agentName + "> Thank you for the " + pickedCount + "x " + itemName + ", " + nearestPlayer.getName().getString() + "!"));
                    agent.queueIncomingChat(ChatEvent.newBuilder()
                            .setSenderUuid(nearestPlayer.getUUID().toString())
                            .setSenderName(nearestPlayer.getName().getString())
                            .setMessage("[DETERMINISTIC_GIFT] Gifted " + pickedCount + "x " + itemName + " to " + agentName + ".")
                            .setDistance((float) Math.sqrt(agent.distanceToSqr(nearestPlayer)))
                            .build());
                }
            }
        }
    }

    /**
     * Simulates damage against the agent and triggers immediate sub-tick TelemetryFrame streaming.
     */
    public void simulateDamage(AgentEntity agent, DamageSource source, float amount) {
        LivingDamageEvent.Post event = mock(LivingDamageEvent.Post.class);
        when(event.getEntity()).thenReturn(agent);
        when(event.getSource()).thenReturn(source);
        when(event.getNewDamage()).thenReturn(amount);

        entityManager.onLivingDamage(event);
    }

    /**
     * Simulates lethal damage and triggers terminal zero-latency TelemetryFrame streaming with DeathEvent.
     */
    public void simulateDeath(AgentEntity agent, DamageSource cause) {
        DeathEvent deathEvent = SensoryFrustumCollector.buildDeathEvent(agent, cause);
        TelemetryFrame deathFrame = sensoryCollector.collectFrame(agent, null, deathEvent);
        streamRegistry.sendCriticalFrame(agent.getAgentId(), deathFrame);
    }
}
