package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func main() {
	agentName := flag.String("name", "aria", "Agent avatar identifier")
	configPath := flag.String("config", "", "Explicit path to agent YAML configuration")
	flag.Parse()

	r := xdg.DefaultResolver()

	var cfg *xdg.AgentIdentityConfig
	var err error
	if *configPath != "" {
		cfg, err = xdg.LoadAgentConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] Failed loading agent config from %s: %v\n", *configPath, err)
			os.Exit(1)
		}
	} else {
		cfg, err = r.LoadAgentConfig(*agentName)
		if err != nil {
			// If not found in config dir, create a default profile for this run
			cfg = xdg.DefaultAgentIdentityConfig(*agentName)
			cfg.ClanID = "pioneers"
			cfg.Profession = "EXPLORER"
		}
	}

	// Initialize agent-specific directories ($XDG_DATA_HOME/civquest/agents/{name}, etc.)
	if err := r.InitAgentDirectories(cfg.Name); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Failed initializing directories for agent %s: %v\n", cfg.Name, err)
		os.Exit(1)
	}

	// Load global config if present
	globalCfg, _ := r.LoadGlobalConfig()
	grpcAddr := "127.0.0.1:50051"
	llmGateway := "http://127.0.0.1:11434/v1"
	if globalCfg != nil {
		grpcAddr = globalCfg.GRPC.Address()
		llmGateway = globalCfg.LLMGateway.BaseURL
	}

	fmt.Println("======================================================================")
	fmt.Printf(" [civquest-agent] Starting Avatar: %s (PID: %d)\n", cfg.Name, os.Getpid())
	fmt.Println("======================================================================")
	fmt.Printf(" Clan:           %s\n", cfg.ClanID)
	fmt.Printf(" Profession:     %s\n", cfg.Profession)
	if cfg.SecondaryHobby != "" {
		fmt.Printf(" Secondary:      %s\n", cfg.SecondaryHobby)
	}
	fmt.Printf(" Personality:    Bravery=%.2f Industriousness=%.2f Sociability=%.2f Curiosity=%.2f Aggressiveness=%.2f\n",
		cfg.Traits.Bravery, cfg.Traits.Industriousness, cfg.Traits.Sociability, cfg.Traits.Curiosity, cfg.Traits.Aggressiveness)
	fmt.Printf(" gRPC Target:    %s\n", grpcAddr)
	fmt.Printf(" LLM Gateway:    %s\n", llmGateway)

	// Set up UNIX domain socket in $XDG_STATE_HOME/civquest/agents/{name}.sock
	sockPath := r.AgentSocketPath(cfg.Name)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0750); err == nil {
		_ = os.Remove(sockPath)
		listener, err := net.Listen("unix", sockPath)
		if err == nil {
			defer func() {
				_ = listener.Close()
				_ = os.Remove(sockPath)
			}()
			fmt.Printf(" IPC Socket:     %s\n", sockPath)

			// Handle incoming socket connections asynchronously
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					response := fmt.Sprintf(`{"agent":"%s","clan":"%s","status":"OK","pid":%d}`+"\n",
						cfg.Name, cfg.ClanID, os.Getpid())
					_, _ = conn.Write([]byte(response))
					_ = conn.Close()
				}
			}()
		}
	}

	fmt.Println("======================================================================")
	fmt.Println("[civquest-agent] Settler loop active. Waiting for sensory frustum frames...")

	// Listen for termination signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	fmt.Printf("[civquest-agent] Received signal %v. Performing clean agent shutdown...\n", sig)
	os.Exit(0)
}
