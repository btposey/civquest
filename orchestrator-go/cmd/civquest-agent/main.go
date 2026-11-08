package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Fatal error: %v\n", err)
		os.Exit(1)
	}
}

func run(parentCtx context.Context, args []string, stdout, stderr io.Writer, r *xdg.Resolver) error {
	flags := flag.NewFlagSet("civquest-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)

	agentIDFlag := flags.String("agent-id", "aria", "Agent avatar identifier")
	nameFlag := flags.String("name", "", "Agent avatar identifier (alias for --agent-id)")
	configPathFlag := flags.String("config", "", "Explicit path to agent YAML configuration")
	grpcAddrFlag := flags.String("grpc-addr", "", "CivQuest gRPC bridge server address (defaults to global config or 127.0.0.1:50051)")
	sharedKeyFlag := flags.String("shared-key", "", "Shared secret authentication key (ADR-012)")
	authTokenFlag := flags.String("auth-token", "", "Shared secret authentication key (alias for --shared-key)")
	socketPathFlag := flags.String("socket-path", "", "Override UNIX domain socket path")
	serverIDFlag := flags.String("server-id", "", "CivQuest server instance identifier for namespaced storage")

	if err := flags.Parse(args); err != nil {
		return err
	}

	// Resolve agent ID taking flag priority into account
	agentID := "aria"
	var agentIDSet, nameSet bool
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "agent-id" {
			agentIDSet = true
		}
		if f.Name == "name" {
			nameSet = true
		}
	})
	if agentIDSet {
		agentID = *agentIDFlag
	} else if nameSet {
		agentID = *nameFlag
	} else if *agentIDFlag != "" {
		agentID = *agentIDFlag
	}

	if err := xdg.ValidateAgentName(agentID); err != nil {
		return fmt.Errorf("invalid agent identifier %q: %w", agentID, err)
	}

	if r == nil {
		r = xdg.DefaultResolver()
	}

	// Read discovery metadata if available
	var meta *bridge.ServerMeta
	if metaRead, discErr := bridge.ReadDiscovery(""); discErr == nil && metaRead.IsReady() {
		meta = metaRead
	}

	// Resolve server ID: flag takes precedence, then discovery artifact, fallback to resolver's ServerID
	serverID := *serverIDFlag
	if serverID == "" && meta != nil && meta.ServerID != "" {
		serverID = meta.ServerID
	}
	if serverID == "" && r != nil {
		serverID = r.ServerID()
	}
	if serverID != "" {
		if err := xdg.ValidateServerID(serverID); err != nil {
			return fmt.Errorf("invalid server identifier %q: %w", serverID, err)
		}
	}
	r = r.ForServer(serverID)

	// Load or create agent identity YAML profile
	var cfg *xdg.AgentIdentityConfig
	var err error
	if *configPathFlag != "" {
		cfg, err = xdg.LoadAgentConfig(*configPathFlag)
		if err != nil {
			return fmt.Errorf("loading agent config from %s: %w", *configPathFlag, err)
		}
	} else {
		cfg, err = r.LoadAgentConfig(agentID)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("loading agent config for %s: %w", agentID, err)
			}
			// If not found in config dir, generate a default profile for this run
			cfg = xdg.DefaultAgentIdentityConfig(agentID)
			cfg.ClanID = "pioneers"
			cfg.Profession = "EXPLORER"
		}
	}

	// Invariant 4: Strict Permadeath Lifecycle. If an agent is in the graveyard, never respawn them.
	graveyardDir := r.GraveyardDir()
	if matches, globErr := filepath.Glob(filepath.Join(graveyardDir, fmt.Sprintf("%s_*", cfg.Name))); globErr == nil && len(matches) > 0 {
		return fmt.Errorf("agent %s is deceased (found memorial archive in graveyard: %s); permadeath invariant prevents respawning", cfg.Name, matches[0])
	}

	// Initialize agent-specific XDG directories ($XDG_DATA_HOME/civquest/servers/{server_id}/agents/{name}, etc.)
	if err := r.InitAgentDirectories(cfg.Name); err != nil {
		return fmt.Errorf("initializing directories for agent %s: %w", cfg.Name, err)
	}

	// Resolve gRPC and SharedKey from flags, discovery artifact, or global config
	globalCfg, _ := r.LoadGlobalConfig()
	if globalCfg == nil {
		globalCfg = xdg.DefaultGlobalConfig()
	}

	grpcAddr := *grpcAddrFlag
	sharedKey := *sharedKeyFlag
	if sharedKey == "" {
		sharedKey = *authTokenFlag
	}

	// Attempt discovery artifact lookup if gRPC address or shared key are unset
	if grpcAddr == "" && meta != nil {
		grpcAddr = meta.Addr()
	}
	if sharedKey == "" && meta != nil {
		sharedKey = meta.SharedKey
	}

	// Fallback to global config if discovery was not found
	if grpcAddr == "" && globalCfg != nil {
		grpcAddr = globalCfg.GRPC.Address()
	}

	if grpcAddr == "" {
		grpcAddr = "127.0.0.1:50051"
	}

	llmGateway := "http://127.0.0.1:11434/v1"
	if globalCfg.LLMGateway.BaseURL != "" {
		llmGateway = globalCfg.LLMGateway.BaseURL
	} else {
		globalCfg.LLMGateway.BaseURL = llmGateway
	}

	llmClient, err := system2.NewClient(globalCfg)
	if err != nil {
		return fmt.Errorf("creating System 2 LLM client: %w", err)
	}
	defer llmClient.Close()

	dialogueSvc, err := system2.NewDialogueService(llmClient, cfg)
	if err != nil {
		return fmt.Errorf("creating System 2 dialogue service: %w", err)
	}
	defer dialogueSvc.Close()

	chroniclerSvc := system2.NewChroniclerService(llmClient)
	toponymySvc := system2.NewToponymyService(llmClient)

	displayServerID := serverID
	if displayServerID == "" {
		displayServerID = "default"
	}
	fmt.Fprintln(stdout, "======================================================================")
	fmt.Fprintf(stdout, " [civquest-agent] Starting Agent Avatar: %s (PID: %d)\n", cfg.Name, os.Getpid())
	fmt.Fprintln(stdout, "======================================================================")
	fmt.Fprintf(stdout, " Server ID:      %s\n", displayServerID)
	fmt.Fprintf(stdout, " Clan:           %s\n", cfg.ClanID)
	fmt.Fprintf(stdout, " Profession:     %s\n", cfg.Profession)
	if cfg.SecondaryHobby != "" {
		fmt.Fprintf(stdout, " Secondary:      %s\n", cfg.SecondaryHobby)
	}
	fmt.Fprintf(stdout, " Personality:    Bravery=%.2f Industriousness=%.2f Sociability=%.2f Curiosity=%.2f Aggressiveness=%.2f\n",
		cfg.Traits.Bravery, cfg.Traits.Industriousness, cfg.Traits.Sociability, cfg.Traits.Curiosity, cfg.Traits.Aggressiveness)
	fmt.Fprintf(stdout, " gRPC Target:    %s\n", grpcAddr)
	if sharedKey != "" {
		fmt.Fprintf(stdout, " Authentication: Shared-Key Bearer Token (ADR-012)\n")
	} else {
		fmt.Fprintf(stdout, " Authentication: None\n")
	}
	fmt.Fprintf(stdout, " LLM Gateway:    %s\n", llmGateway)

	// Open per-agent SQLite database (memory.db and map_graph.db with sqlite-vec)
	agentDB, err := db.OpenAgentDBForAgent(r, cfg.Name)
	if err != nil {
		return fmt.Errorf("opening agent database: %w", err)
	}
	defer agentDB.Close()

	// Open clan reputation database if available for companion and murder prestige scoring
	var repDB *db.ReputationDB
	if rep, repErr := db.OpenReputationDBForClan(r, cfg.ClanID); repErr == nil {
		repDB = rep
		defer repDB.Close()
	}

	// Open clan landmarks database if available for cultural toponymy
	var lmDB *db.LandmarksDB
	if lm, lmErr := db.OpenLandmarksDBForClan(r, cfg.ClanID); lmErr == nil {
		lmDB = lm
		defer lmDB.Close()
	}

	// Dial gRPC bridge with shared key
	bridgeClient, err := bridge.NewClient(grpcAddr, sharedKey)
	if err != nil {
		return fmt.Errorf("creating gRPC bridge client for %s: %w", grpcAddr, err)
	}
	defer bridgeClient.Close()

	sockPath := *socketPathFlag
	if sockPath == "" {
		sockPath = r.AgentSocketPath(cfg.Name)
	}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:           cfg.Name,
		ClanID:            cfg.ClanID,
		IdentityConfig:    cfg,
		Resolver:          r,
		AgentDB:           agentDB,
		ReputationDB:      repDB,
		LandmarksDB:       lmDB,
		BridgeClient:      bridgeClient,
		SocketPath:        sockPath,
		DialogueService:   dialogueSvc,
		ChroniclerService: chroniclerSvc,
		ToponymyService:   toponymySvc,
		OnDeath: func(e *civquestv1.DeathEvent) {
			fmt.Fprintf(stdout, "[civquest-agent] Permadeath event recorded at tick %d (cause: %s)\n",
				e.GetWorldTick(), e.GetDamageSource())
		},
	})
	if err != nil {
		return fmt.Errorf("creating agent runtime: %w", err)
	}

	// Spawn the agent avatar in the Minecraft world
	spawnResp, err := bridgeClient.SpawnAgentEntity(parentCtx, &civquestv1.SpawnAgentRequest{
		AgentId:     cfg.Name,
		DisplayName: cfg.Name,
		ClanId:      cfg.ClanID,
	})
	if err != nil {
		return fmt.Errorf("spawning agent %s into Minecraft: %w", cfg.Name, err)
	}
	if !spawnResp.GetSuccess() {
		return fmt.Errorf("failed spawning agent %s: %s", cfg.Name, spawnResp.GetErrorMessage())
	}
	if spawnResp.GetOfflineUuid() != "" {
		rt.SetAgentUUID(spawnResp.GetOfflineUuid())
	}
	fmt.Fprintf(stdout, "[civquest-agent] Agent '%s' spawned into Minecraft (EntityID: %d, UUID: %s)\n",
		cfg.Name, spawnResp.GetMinecraftEntityId(), spawnResp.GetOfflineUuid())

	fmt.Fprintf(stdout, " IPC Socket:     %s\n", rt.SocketPath())
	fmt.Fprintln(stdout, "======================================================================")
	fmt.Fprintln(stdout, "[civquest-agent] Agent runtime active. Connecting telemetry stream...")

	// Listen for termination signals (SIGINT, SIGTERM)
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case sig := <-sigChan:
			fmt.Fprintf(stdout, "\n[civquest-agent] Received signal %v. Performing clean agent shutdown...\n", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	runErr := rt.Start(ctx)
	if rt.IsDead() {
		graveyardPath := ""
		if res := rt.PermadeathResult(); res != nil && res.GraveyardDir != "" {
			graveyardPath = res.GraveyardDir
		} else {
			graveyardPath = r.GraveyardDir()
		}
		fmt.Fprintf(stdout, "[civquest-agent] Permadeath complete. Memorial archive created at %s. Exiting cleanly.\n", graveyardPath)
		fmt.Fprintf(stdout, "[civquest-agent] Agent %s has died. Clean permadeath exit code 0.\n", cfg.Name)
		return nil
	}

	if runErr != nil && ctx.Err() == nil {
		return fmt.Errorf("agent runtime error: %w", runErr)
	}

	fmt.Fprintln(stdout, "[civquest-agent] Agent runtime terminated cleanly.")
	return nil
}
