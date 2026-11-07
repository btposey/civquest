package com.civquest.mod.grpc.v1;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 * <pre>
 * CivQuestBridgeService defines the gRPC bridge between the Minecraft NeoForge mod and external orchestrators.
 * </pre>
 */
@javax.annotation.Generated(
    value = "by gRPC proto compiler (version 1.68.1)",
    comments = "Source: civquest/v1/civquest.proto")
@io.grpc.stub.annotations.GrpcGenerated
public final class CivQuestBridgeServiceGrpc {

  private CivQuestBridgeServiceGrpc() {}

  public static final java.lang.String SERVICE_NAME = "civquest.v1.CivQuestBridgeService";

  // Static method descriptors that strictly reflect the proto.
  private static volatile io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.AgentIntent,
      com.civquest.mod.grpc.v1.TelemetryFrame> getConnectAgentMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "ConnectAgent",
      requestType = com.civquest.mod.grpc.v1.AgentIntent.class,
      responseType = com.civquest.mod.grpc.v1.TelemetryFrame.class,
      methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
  public static io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.AgentIntent,
      com.civquest.mod.grpc.v1.TelemetryFrame> getConnectAgentMethod() {
    io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.AgentIntent, com.civquest.mod.grpc.v1.TelemetryFrame> getConnectAgentMethod;
    if ((getConnectAgentMethod = CivQuestBridgeServiceGrpc.getConnectAgentMethod) == null) {
      synchronized (CivQuestBridgeServiceGrpc.class) {
        if ((getConnectAgentMethod = CivQuestBridgeServiceGrpc.getConnectAgentMethod) == null) {
          CivQuestBridgeServiceGrpc.getConnectAgentMethod = getConnectAgentMethod =
              io.grpc.MethodDescriptor.<com.civquest.mod.grpc.v1.AgentIntent, com.civquest.mod.grpc.v1.TelemetryFrame>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "ConnectAgent"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.AgentIntent.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.TelemetryFrame.getDefaultInstance()))
              .setSchemaDescriptor(new CivQuestBridgeServiceMethodDescriptorSupplier("ConnectAgent"))
              .build();
        }
      }
    }
    return getConnectAgentMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.SpawnAgentRequest,
      com.civquest.mod.grpc.v1.SpawnAgentResponse> getSpawnAgentEntityMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "SpawnAgentEntity",
      requestType = com.civquest.mod.grpc.v1.SpawnAgentRequest.class,
      responseType = com.civquest.mod.grpc.v1.SpawnAgentResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.SpawnAgentRequest,
      com.civquest.mod.grpc.v1.SpawnAgentResponse> getSpawnAgentEntityMethod() {
    io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.SpawnAgentRequest, com.civquest.mod.grpc.v1.SpawnAgentResponse> getSpawnAgentEntityMethod;
    if ((getSpawnAgentEntityMethod = CivQuestBridgeServiceGrpc.getSpawnAgentEntityMethod) == null) {
      synchronized (CivQuestBridgeServiceGrpc.class) {
        if ((getSpawnAgentEntityMethod = CivQuestBridgeServiceGrpc.getSpawnAgentEntityMethod) == null) {
          CivQuestBridgeServiceGrpc.getSpawnAgentEntityMethod = getSpawnAgentEntityMethod =
              io.grpc.MethodDescriptor.<com.civquest.mod.grpc.v1.SpawnAgentRequest, com.civquest.mod.grpc.v1.SpawnAgentResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "SpawnAgentEntity"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.SpawnAgentRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.SpawnAgentResponse.getDefaultInstance()))
              .setSchemaDescriptor(new CivQuestBridgeServiceMethodDescriptorSupplier("SpawnAgentEntity"))
              .build();
        }
      }
    }
    return getSpawnAgentEntityMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.DespawnAgentRequest,
      com.civquest.mod.grpc.v1.DespawnAgentResponse> getDespawnAgentEntityMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "DespawnAgentEntity",
      requestType = com.civquest.mod.grpc.v1.DespawnAgentRequest.class,
      responseType = com.civquest.mod.grpc.v1.DespawnAgentResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.DespawnAgentRequest,
      com.civquest.mod.grpc.v1.DespawnAgentResponse> getDespawnAgentEntityMethod() {
    io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.DespawnAgentRequest, com.civquest.mod.grpc.v1.DespawnAgentResponse> getDespawnAgentEntityMethod;
    if ((getDespawnAgentEntityMethod = CivQuestBridgeServiceGrpc.getDespawnAgentEntityMethod) == null) {
      synchronized (CivQuestBridgeServiceGrpc.class) {
        if ((getDespawnAgentEntityMethod = CivQuestBridgeServiceGrpc.getDespawnAgentEntityMethod) == null) {
          CivQuestBridgeServiceGrpc.getDespawnAgentEntityMethod = getDespawnAgentEntityMethod =
              io.grpc.MethodDescriptor.<com.civquest.mod.grpc.v1.DespawnAgentRequest, com.civquest.mod.grpc.v1.DespawnAgentResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "DespawnAgentEntity"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.DespawnAgentRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.DespawnAgentResponse.getDefaultInstance()))
              .setSchemaDescriptor(new CivQuestBridgeServiceMethodDescriptorSupplier("DespawnAgentEntity"))
              .build();
        }
      }
    }
    return getDespawnAgentEntityMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.ExportRecipeRequest,
      com.civquest.mod.grpc.v1.ExportRecipeResponse> getExportRecipeGraphMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "ExportRecipeGraph",
      requestType = com.civquest.mod.grpc.v1.ExportRecipeRequest.class,
      responseType = com.civquest.mod.grpc.v1.ExportRecipeResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.ExportRecipeRequest,
      com.civquest.mod.grpc.v1.ExportRecipeResponse> getExportRecipeGraphMethod() {
    io.grpc.MethodDescriptor<com.civquest.mod.grpc.v1.ExportRecipeRequest, com.civquest.mod.grpc.v1.ExportRecipeResponse> getExportRecipeGraphMethod;
    if ((getExportRecipeGraphMethod = CivQuestBridgeServiceGrpc.getExportRecipeGraphMethod) == null) {
      synchronized (CivQuestBridgeServiceGrpc.class) {
        if ((getExportRecipeGraphMethod = CivQuestBridgeServiceGrpc.getExportRecipeGraphMethod) == null) {
          CivQuestBridgeServiceGrpc.getExportRecipeGraphMethod = getExportRecipeGraphMethod =
              io.grpc.MethodDescriptor.<com.civquest.mod.grpc.v1.ExportRecipeRequest, com.civquest.mod.grpc.v1.ExportRecipeResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "ExportRecipeGraph"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.ExportRecipeRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.civquest.mod.grpc.v1.ExportRecipeResponse.getDefaultInstance()))
              .setSchemaDescriptor(new CivQuestBridgeServiceMethodDescriptorSupplier("ExportRecipeGraph"))
              .build();
        }
      }
    }
    return getExportRecipeGraphMethod;
  }

  /**
   * Creates a new async stub that supports all call types for the service
   */
  public static CivQuestBridgeServiceStub newStub(io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<CivQuestBridgeServiceStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<CivQuestBridgeServiceStub>() {
        @java.lang.Override
        public CivQuestBridgeServiceStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new CivQuestBridgeServiceStub(channel, callOptions);
        }
      };
    return CivQuestBridgeServiceStub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports unary and streaming output calls on the service
   */
  public static CivQuestBridgeServiceBlockingStub newBlockingStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<CivQuestBridgeServiceBlockingStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<CivQuestBridgeServiceBlockingStub>() {
        @java.lang.Override
        public CivQuestBridgeServiceBlockingStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new CivQuestBridgeServiceBlockingStub(channel, callOptions);
        }
      };
    return CivQuestBridgeServiceBlockingStub.newStub(factory, channel);
  }

  /**
   * Creates a new ListenableFuture-style stub that supports unary calls on the service
   */
  public static CivQuestBridgeServiceFutureStub newFutureStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<CivQuestBridgeServiceFutureStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<CivQuestBridgeServiceFutureStub>() {
        @java.lang.Override
        public CivQuestBridgeServiceFutureStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new CivQuestBridgeServiceFutureStub(channel, callOptions);
        }
      };
    return CivQuestBridgeServiceFutureStub.newStub(factory, channel);
  }

  /**
   * <pre>
   * CivQuestBridgeService defines the gRPC bridge between the Minecraft NeoForge mod and external orchestrators.
   * </pre>
   */
  public interface AsyncService {

    /**
     * <pre>
     * Primary per-agent bidirectional control and telemetry stream
     * </pre>
     */
    default io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.AgentIntent> connectAgent(
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.TelemetryFrame> responseObserver) {
      return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getConnectAgentMethod(), responseObserver);
    }

    /**
     * <pre>
     * Administrative RPCs called by civctl supervisor
     * </pre>
     */
    default void spawnAgentEntity(com.civquest.mod.grpc.v1.SpawnAgentRequest request,
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.SpawnAgentResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getSpawnAgentEntityMethod(), responseObserver);
    }

    /**
     */
    default void despawnAgentEntity(com.civquest.mod.grpc.v1.DespawnAgentRequest request,
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.DespawnAgentResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getDespawnAgentEntityMethod(), responseObserver);
    }

    /**
     */
    default void exportRecipeGraph(com.civquest.mod.grpc.v1.ExportRecipeRequest request,
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.ExportRecipeResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getExportRecipeGraphMethod(), responseObserver);
    }
  }

  /**
   * Base class for the server implementation of the service CivQuestBridgeService.
   * <pre>
   * CivQuestBridgeService defines the gRPC bridge between the Minecraft NeoForge mod and external orchestrators.
   * </pre>
   */
  public static abstract class CivQuestBridgeServiceImplBase
      implements io.grpc.BindableService, AsyncService {

    @java.lang.Override public final io.grpc.ServerServiceDefinition bindService() {
      return CivQuestBridgeServiceGrpc.bindService(this);
    }
  }

  /**
   * A stub to allow clients to do asynchronous rpc calls to service CivQuestBridgeService.
   * <pre>
   * CivQuestBridgeService defines the gRPC bridge between the Minecraft NeoForge mod and external orchestrators.
   * </pre>
   */
  public static final class CivQuestBridgeServiceStub
      extends io.grpc.stub.AbstractAsyncStub<CivQuestBridgeServiceStub> {
    private CivQuestBridgeServiceStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected CivQuestBridgeServiceStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new CivQuestBridgeServiceStub(channel, callOptions);
    }

    /**
     * <pre>
     * Primary per-agent bidirectional control and telemetry stream
     * </pre>
     */
    public io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.AgentIntent> connectAgent(
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.TelemetryFrame> responseObserver) {
      return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(
          getChannel().newCall(getConnectAgentMethod(), getCallOptions()), responseObserver);
    }

    /**
     * <pre>
     * Administrative RPCs called by civctl supervisor
     * </pre>
     */
    public void spawnAgentEntity(com.civquest.mod.grpc.v1.SpawnAgentRequest request,
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.SpawnAgentResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getSpawnAgentEntityMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void despawnAgentEntity(com.civquest.mod.grpc.v1.DespawnAgentRequest request,
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.DespawnAgentResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getDespawnAgentEntityMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     */
    public void exportRecipeGraph(com.civquest.mod.grpc.v1.ExportRecipeRequest request,
        io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.ExportRecipeResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getExportRecipeGraphMethod(), getCallOptions()), request, responseObserver);
    }
  }

  /**
   * A stub to allow clients to do synchronous rpc calls to service CivQuestBridgeService.
   * <pre>
   * CivQuestBridgeService defines the gRPC bridge between the Minecraft NeoForge mod and external orchestrators.
   * </pre>
   */
  public static final class CivQuestBridgeServiceBlockingStub
      extends io.grpc.stub.AbstractBlockingStub<CivQuestBridgeServiceBlockingStub> {
    private CivQuestBridgeServiceBlockingStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected CivQuestBridgeServiceBlockingStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new CivQuestBridgeServiceBlockingStub(channel, callOptions);
    }

    /**
     * <pre>
     * Administrative RPCs called by civctl supervisor
     * </pre>
     */
    public com.civquest.mod.grpc.v1.SpawnAgentResponse spawnAgentEntity(com.civquest.mod.grpc.v1.SpawnAgentRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getSpawnAgentEntityMethod(), getCallOptions(), request);
    }

    /**
     */
    public com.civquest.mod.grpc.v1.DespawnAgentResponse despawnAgentEntity(com.civquest.mod.grpc.v1.DespawnAgentRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getDespawnAgentEntityMethod(), getCallOptions(), request);
    }

    /**
     */
    public com.civquest.mod.grpc.v1.ExportRecipeResponse exportRecipeGraph(com.civquest.mod.grpc.v1.ExportRecipeRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getExportRecipeGraphMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do ListenableFuture-style rpc calls to service CivQuestBridgeService.
   * <pre>
   * CivQuestBridgeService defines the gRPC bridge between the Minecraft NeoForge mod and external orchestrators.
   * </pre>
   */
  public static final class CivQuestBridgeServiceFutureStub
      extends io.grpc.stub.AbstractFutureStub<CivQuestBridgeServiceFutureStub> {
    private CivQuestBridgeServiceFutureStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected CivQuestBridgeServiceFutureStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new CivQuestBridgeServiceFutureStub(channel, callOptions);
    }

    /**
     * <pre>
     * Administrative RPCs called by civctl supervisor
     * </pre>
     */
    public com.google.common.util.concurrent.ListenableFuture<com.civquest.mod.grpc.v1.SpawnAgentResponse> spawnAgentEntity(
        com.civquest.mod.grpc.v1.SpawnAgentRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getSpawnAgentEntityMethod(), getCallOptions()), request);
    }

    /**
     */
    public com.google.common.util.concurrent.ListenableFuture<com.civquest.mod.grpc.v1.DespawnAgentResponse> despawnAgentEntity(
        com.civquest.mod.grpc.v1.DespawnAgentRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getDespawnAgentEntityMethod(), getCallOptions()), request);
    }

    /**
     */
    public com.google.common.util.concurrent.ListenableFuture<com.civquest.mod.grpc.v1.ExportRecipeResponse> exportRecipeGraph(
        com.civquest.mod.grpc.v1.ExportRecipeRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getExportRecipeGraphMethod(), getCallOptions()), request);
    }
  }

  private static final int METHODID_SPAWN_AGENT_ENTITY = 0;
  private static final int METHODID_DESPAWN_AGENT_ENTITY = 1;
  private static final int METHODID_EXPORT_RECIPE_GRAPH = 2;
  private static final int METHODID_CONNECT_AGENT = 3;

  private static final class MethodHandlers<Req, Resp> implements
      io.grpc.stub.ServerCalls.UnaryMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ServerStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ClientStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.BidiStreamingMethod<Req, Resp> {
    private final AsyncService serviceImpl;
    private final int methodId;

    MethodHandlers(AsyncService serviceImpl, int methodId) {
      this.serviceImpl = serviceImpl;
      this.methodId = methodId;
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public void invoke(Req request, io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        case METHODID_SPAWN_AGENT_ENTITY:
          serviceImpl.spawnAgentEntity((com.civquest.mod.grpc.v1.SpawnAgentRequest) request,
              (io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.SpawnAgentResponse>) responseObserver);
          break;
        case METHODID_DESPAWN_AGENT_ENTITY:
          serviceImpl.despawnAgentEntity((com.civquest.mod.grpc.v1.DespawnAgentRequest) request,
              (io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.DespawnAgentResponse>) responseObserver);
          break;
        case METHODID_EXPORT_RECIPE_GRAPH:
          serviceImpl.exportRecipeGraph((com.civquest.mod.grpc.v1.ExportRecipeRequest) request,
              (io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.ExportRecipeResponse>) responseObserver);
          break;
        default:
          throw new AssertionError();
      }
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public io.grpc.stub.StreamObserver<Req> invoke(
        io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        case METHODID_CONNECT_AGENT:
          return (io.grpc.stub.StreamObserver<Req>) serviceImpl.connectAgent(
              (io.grpc.stub.StreamObserver<com.civquest.mod.grpc.v1.TelemetryFrame>) responseObserver);
        default:
          throw new AssertionError();
      }
    }
  }

  public static final io.grpc.ServerServiceDefinition bindService(AsyncService service) {
    return io.grpc.ServerServiceDefinition.builder(getServiceDescriptor())
        .addMethod(
          getConnectAgentMethod(),
          io.grpc.stub.ServerCalls.asyncBidiStreamingCall(
            new MethodHandlers<
              com.civquest.mod.grpc.v1.AgentIntent,
              com.civquest.mod.grpc.v1.TelemetryFrame>(
                service, METHODID_CONNECT_AGENT)))
        .addMethod(
          getSpawnAgentEntityMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              com.civquest.mod.grpc.v1.SpawnAgentRequest,
              com.civquest.mod.grpc.v1.SpawnAgentResponse>(
                service, METHODID_SPAWN_AGENT_ENTITY)))
        .addMethod(
          getDespawnAgentEntityMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              com.civquest.mod.grpc.v1.DespawnAgentRequest,
              com.civquest.mod.grpc.v1.DespawnAgentResponse>(
                service, METHODID_DESPAWN_AGENT_ENTITY)))
        .addMethod(
          getExportRecipeGraphMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              com.civquest.mod.grpc.v1.ExportRecipeRequest,
              com.civquest.mod.grpc.v1.ExportRecipeResponse>(
                service, METHODID_EXPORT_RECIPE_GRAPH)))
        .build();
  }

  private static abstract class CivQuestBridgeServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoFileDescriptorSupplier, io.grpc.protobuf.ProtoServiceDescriptorSupplier {
    CivQuestBridgeServiceBaseDescriptorSupplier() {}

    @java.lang.Override
    public com.google.protobuf.Descriptors.FileDescriptor getFileDescriptor() {
      return com.civquest.mod.grpc.v1.Civquest.getDescriptor();
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.ServiceDescriptor getServiceDescriptor() {
      return getFileDescriptor().findServiceByName("CivQuestBridgeService");
    }
  }

  private static final class CivQuestBridgeServiceFileDescriptorSupplier
      extends CivQuestBridgeServiceBaseDescriptorSupplier {
    CivQuestBridgeServiceFileDescriptorSupplier() {}
  }

  private static final class CivQuestBridgeServiceMethodDescriptorSupplier
      extends CivQuestBridgeServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoMethodDescriptorSupplier {
    private final java.lang.String methodName;

    CivQuestBridgeServiceMethodDescriptorSupplier(java.lang.String methodName) {
      this.methodName = methodName;
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.MethodDescriptor getMethodDescriptor() {
      return getServiceDescriptor().findMethodByName(methodName);
    }
  }

  private static volatile io.grpc.ServiceDescriptor serviceDescriptor;

  public static io.grpc.ServiceDescriptor getServiceDescriptor() {
    io.grpc.ServiceDescriptor result = serviceDescriptor;
    if (result == null) {
      synchronized (CivQuestBridgeServiceGrpc.class) {
        result = serviceDescriptor;
        if (result == null) {
          serviceDescriptor = result = io.grpc.ServiceDescriptor.newBuilder(SERVICE_NAME)
              .setSchemaDescriptor(new CivQuestBridgeServiceFileDescriptorSupplier())
              .addMethod(getConnectAgentMethod())
              .addMethod(getSpawnAgentEntityMethod())
              .addMethod(getDespawnAgentEntityMethod())
              .addMethod(getExportRecipeGraphMethod())
              .build();
        }
      }
    }
    return result;
  }
}
