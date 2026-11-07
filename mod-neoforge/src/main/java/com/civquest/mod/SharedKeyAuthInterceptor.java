package com.civquest.mod;

import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;

/**
 * ServerInterceptor validating bearer token authentication for gRPC bridge calls.
 */
public class SharedKeyAuthInterceptor implements ServerInterceptor {
    public static final Metadata.Key<String> AUTHORIZATION_METADATA_KEY =
            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);

    public static final String HEALTH_CHECK_METHOD = "grpc.health.v1.Health/Check";

    private final String expectedSharedKey;
    private final byte[] expectedTokenBytes;

    public SharedKeyAuthInterceptor(String sharedKey) {
        if (sharedKey == null || sharedKey.isBlank()) {
            throw new IllegalArgumentException("sharedKey cannot be null or blank");
        }
        this.expectedSharedKey = sharedKey.trim();
        this.expectedTokenBytes = this.expectedSharedKey.getBytes(StandardCharsets.UTF_8);
    }

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call,
            Metadata headers,
            ServerCallHandler<ReqT, RespT> next) {

        // Exempt unary health checking call from authentication to allow readiness probes (H-3)
        String fullMethodName = call.getMethodDescriptor().getFullMethodName();
        if (HEALTH_CHECK_METHOD.equals(fullMethodName)) {
            return next.startCall(call, headers);
        }

        String authHeader = headers.get(AUTHORIZATION_METADATA_KEY);
        if (authHeader != null) {
            String trimmedHeader = authHeader.trim();
            // Case-insensitive "Bearer " prefix parsing (H-1)
            if (trimmedHeader.regionMatches(true, 0, "Bearer ", 0, 7)) {
                String candidateToken = trimmedHeader.substring(7).trim();
                byte[] candidateBytes = candidateToken.getBytes(StandardCharsets.UTF_8);
                // Constant-time comparison per ADR-012 to prevent timing side-channel attacks
                if (MessageDigest.isEqual(expectedTokenBytes, candidateBytes)) {
                    return next.startCall(call, headers);
                }
            }
        }

        call.close(
                Status.UNAUTHENTICATED.withDescription("Missing or invalid authorization bearer token"),
                new Metadata()
        );
        return new ServerCall.Listener<ReqT>() {};
    }

    public String getExpectedBearerToken() {
        return "Bearer " + expectedSharedKey;
    }

    public String getExpectedSharedKey() {
        return expectedSharedKey;
    }
}
