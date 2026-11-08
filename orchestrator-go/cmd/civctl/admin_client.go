package main

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var (
	flagTarget    string
	flagSharedKey string
)

// AdminClient wraps the bridge Client for administrative operations (Spawn, Despawn, ExportRecipe).
type AdminClient struct {
	client *bridge.Client
}

// NewAdminClient creates a new administrative gRPC client for the specified target.
func NewAdminClient(target string, sharedKey string, opts ...grpc.DialOption) (*AdminClient, error) {
	c, err := bridge.NewClient(target, sharedKey, opts...)
	if err != nil {
		return nil, err
	}
	return &AdminClient{client: c}, nil
}

// GetAdminClient resolves target and shared key from discovery or flags and creates an AdminClient.
func GetAdminClient(r *xdg.Resolver) (*AdminClient, error) {
	target := flagTarget
	key := flagSharedKey

	serverDir := ""
	if r != nil {
		serverDir = r.ServerDir()
	}

	if meta, err := bridge.ReadDiscovery(serverDir); err == nil && meta.IsReady() {
		if target == "" {
			target = meta.Addr()
		}
		if key == "" {
			key = meta.SharedKey
		}
	}

	if target == "" {
		target = "127.0.0.1:50051"
	}

	return NewAdminClient(target, key)
}

// SpawnAgentEntity invokes SpawnAgentEntity RPC on the CivQuest bridge.
func (a *AdminClient) SpawnAgentEntity(ctx context.Context, req *civquestv1.SpawnAgentRequest) (*civquestv1.SpawnAgentResponse, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("admin client not initialized")
	}
	return a.client.SpawnAgentEntity(ctx, req)
}

// DespawnAgentEntity invokes DespawnAgentEntity RPC on the CivQuest bridge.
func (a *AdminClient) DespawnAgentEntity(ctx context.Context, req *civquestv1.DespawnAgentRequest) (*civquestv1.DespawnAgentResponse, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("admin client not initialized")
	}
	return a.client.DespawnAgentEntity(ctx, req)
}

// ExportRecipeGraph invokes ExportRecipeGraph RPC on the CivQuest bridge.
func (a *AdminClient) ExportRecipeGraph(ctx context.Context, req *civquestv1.ExportRecipeRequest) (*civquestv1.ExportRecipeResponse, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("admin client not initialized")
	}
	return a.client.ExportRecipeGraph(ctx, req)
}

// Close closes the underlying gRPC connection.
func (a *AdminClient) Close() error {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.Close()
}

// Target returns the connection target string.
func (a *AdminClient) Target() string {
	if a == nil || a.client == nil {
		return ""
	}
	return a.client.Target()
}

// HasAuth returns true if sharedKey is non-empty.
func (a *AdminClient) HasAuth() bool {
	if a == nil || a.client == nil {
		return false
	}
	return a.client.HasAuth()
}

func parseCoords(raw string) (*civquestv1.Vec3, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("coords must be in format 'X,Y,Z', got %q", raw)
	}
	var x, y, z float64
	if _, err := fmt.Sscanf(raw, "%f,%f,%f", &x, &y, &z); err != nil {
		return nil, fmt.Errorf("invalid coordinates %q: %w", raw, err)
	}
	return &civquestv1.Vec3{X: x, Y: y, Z: z}, nil
}
