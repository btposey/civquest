package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type recipeCatalogMockServer struct {
	civquestv1.UnimplementedCivQuestBridgeServiceServer
	recipes []*civquestv1.RecipeNode
}

func (s *recipeCatalogMockServer) ExportRecipeGraph(ctx context.Context, req *civquestv1.ExportRecipeRequest) (*civquestv1.ExportRecipeResponse, error) {
	return &civquestv1.ExportRecipeResponse{Recipes: s.recipes}, nil
}

func TestSyncRecipeGraph_PopulatesSystem1State(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	civquestv1.RegisterCivQuestBridgeServiceServer(srv, &recipeCatalogMockServer{
		recipes: []*civquestv1.RecipeNode{
			{
				RecipeId: "minecraft:birch_planks",
				IngredientSlots: []*civquestv1.RecipeIngredientSlot{
					{AcceptedItemIds: []string{"minecraft:birch_log"}, Count: 1},
				},
				Result: &civquestv1.ItemStackProto{ItemId: "minecraft:birch_planks", Count: 4},
			},
		},
	})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	client, err := bridge.NewClient(
		"passthrough:///bufnet",
		"",
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create bridge client: %v", err)
	}
	defer client.Close()

	tmp := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmp))
	rt, err := NewRuntime(Config{
		AgentID:      "recipe-sync-agent",
		Resolver:     resolver,
		BridgeClient: client,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	defer rt.Stop()

	if rt.system1State.RecipeGraph() != nil {
		t.Fatalf("expected nil recipe graph before sync")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.syncRecipeGraph(ctx, client); err != nil {
		t.Fatalf("syncRecipeGraph failed: %v", err)
	}

	graph := rt.system1State.RecipeGraph()
	if graph == nil || graph.IsEmpty() {
		t.Fatalf("expected non-empty synced recipe graph")
	}
	step, ok := graph.PlanNextStep("minecraft:birch_planks", map[string]int{"minecraft:birch_log": 1}, false)
	if !ok || !step.CanExecuteNow || step.RecipeID != "minecraft:birch_planks" {
		t.Fatalf("unexpected step from synced recipe graph: %+v ok=%v", step, ok)
	}
}
