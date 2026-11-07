package com.civquest.mod;

import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;

/**
 * ServerInterceptor validating bearer token authentication for gRPC bridge calls.
 */
public class SharedKeyAuthInterceptor implements ServerInterceptor {
    public static final Metadata.Key<String> AUTHORIZATION_METADATA_KEY =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);

    private final String expectedBearerToken;

    public SharedKeyAuthInterceptor(String sharedKey) {
        if (sharedKey == null || sharedKey.isBlank()) {
            throw new IllegalArgumentException("sharedKey cannot be null or blank");
        }
        this.expectedBearerToken = "Bearer " + sharedKey.trim();
    }

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call,
            Metadata headers,
            ServerCallHandler<ReqT, RespT> next) {

        // Exempt health checking service from authentication to allow readiness probes
        String serviceName = call.getMethodDescriptor().getServiceName();
        if ("grpc.health.v1.Health".equals(serviceName)) {
            return next.startCall(call, headers);
        }

        String authHeader = headers.get(AUTHORIZATION_METADATA_KEY);
        if (authHeader == null || !expectedBearerToken.equals(authHeader.trim())) {
            call.close(
                    Status.UNAUTHENTICATED.withDescription("Missing or invalid authorization bearer token"),
                    new Metadata()
            );
            return new ServerCall.Listener<ReqT>() {};
        }

        return next.startCall(call, headers);
    }

    public String getExpectedBearerToken() {
        return expectedBearerToken;
    }
}
