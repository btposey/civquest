package viewer

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

//go:embed static/*
var staticFS embed.FS

// Config specifies the runtime configuration for the Agent State Viewer HTTP server.
type Config struct {
	Host     string
	Port     int
	Resolver *xdg.Resolver
}

// AgentInfo describes a detected or configured agent avatar.
type AgentInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ClanID         string `json:"clan_id"`
	Profession     string `json:"profession"`
	SecondaryHobby string `json:"secondary_hobby,omitempty"`
	Active         bool   `json:"active"`
	SocketPath     string `json:"socket_path"`
}

// Server provides the embedded HTTP server and WebSocket telemetry streamer for the Agent Viewer.
type Server struct {
	cfg      Config
	httpSrv  *http.Server
	listener net.Listener
	addr     string
	mu       sync.Mutex
	running  bool
}

// NewServer initializes a new Agent Viewer Server with the provided configuration.
func NewServer(cfg Config) *Server {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port <= 0 {
		cfg.Port = 8766
	}
	if cfg.Resolver == nil {
		cfg.Resolver = xdg.DefaultResolver()
	}

	s := &Server{
		cfg: cfg,
	}

	mux := http.NewServeMux()

	// REST Endpoints
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgentStatus)

	// Real-Time WebSocket Streaming
	mux.Handle("/ws/agents/", websocket.Handler(s.handleAgentWS))

	// Static Web UI & SPA Routing
	mux.HandleFunc("/", s.handleStatic)

	s.httpSrv = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return s
}

// Start binds the TCP listener and starts serving HTTP requests.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("viewer server is already running")
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("binding viewer listener on %s: %w", addr, err)
	}

	s.listener = ln
	s.addr = ln.Addr().String()
	s.running = true
	s.mu.Unlock()

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "[viewer] http serve error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	return s.httpSrv.Shutdown(ctx)
}

// Addr returns the bound TCP host:port string.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// URL returns the full HTTP URL to access the viewer.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("http://%s", s.addr)
}

// -----------------------------------------------------------------------------
// REST Handlers
// -----------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// resolveAgentSocket returns the active or configured socket path for an agent ID.
func (s *Server) resolveAgentSocket(agentID string) string {
	r := s.cfg.Resolver
	if r == nil {
		r = xdg.DefaultResolver()
	}

	// 1. Check $XDG_STATE_HOME/civquest/sockets/{agent_id}.sock
	cand1 := filepath.Join(r.StateDir(), "sockets", agentID+".sock")
	if _, err := os.Stat(cand1); err == nil {
		return cand1
	}

	// 2. Check resolver's AgentSocketPath
	cand2 := r.AgentSocketPath(agentID)
	if _, err := os.Stat(cand2); err == nil {
		return cand2
	}

	// 3. Check $XDG_STATE_HOME/civquest/agents/{agent_id}.sock
	cand3 := filepath.Join(r.StateDir(), "agents", agentID+".sock")
	if _, err := os.Stat(cand3); err == nil {
		return cand3
	}

	// 4. Check $XDG_STATE_HOME/civquest/agents/{agent_id}/agent.sock
	cand4 := filepath.Join(r.StateDir(), "agents", agentID, "agent.sock")
	if _, err := os.Stat(cand4); err == nil {
		return cand4
	}

	// Default fallback to cand1
	return cand1
}

// handleAgents lists active and configured agents across XDG directories and sockets.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	resolver := s.cfg.Resolver
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}

	agentsMap := make(map[string]*AgentInfo)

	// 1. Scan manifests directory ($XDG_CONFIG_HOME/civquest/manifests/)
	manifestsDir := filepath.Join(resolver.ConfigDir(), "manifests")
	if entries, err := os.ReadDir(manifestsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".json" && ext != ".yaml" && ext != ".yml" {
				continue
			}
			agentID := strings.TrimSuffix(name, ext)
			fullPath := filepath.Join(manifestsDir, name)

			info := &AgentInfo{
				ID:   agentID,
				Name: agentID,
			}

			if data, err := os.ReadFile(fullPath); err == nil {
				var parsed struct {
					Name           string `json:"name" yaml:"name"`
					ClanID         string `json:"clan_id" yaml:"clan_id"`
					Profession     string `json:"profession" yaml:"profession"`
					SecondaryHobby string `json:"secondary_hobby" yaml:"secondary_hobby"`
				}
				if json.Unmarshal(data, &parsed) == nil && parsed.Name != "" {
					info.Name = parsed.Name
					info.ClanID = parsed.ClanID
					info.Profession = parsed.Profession
					info.SecondaryHobby = parsed.SecondaryHobby
				}
			}

			agentsMap[agentID] = info
		}
	}

	// 2. Scan agents config directory ($XDG_CONFIG_HOME/civquest/agents/)
	agentsConfigDir := resolver.AgentsConfigDir()
	if entries, err := os.ReadDir(agentsConfigDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".yaml" && ext != ".yml" && ext != ".json" {
				continue
			}
			agentID := strings.TrimSuffix(name, ext)
			if _, exists := agentsMap[agentID]; !exists {
				agentsMap[agentID] = &AgentInfo{
					ID:   agentID,
					Name: agentID,
				}
			}
			if cfg, err := resolver.LoadAgentConfig(agentID); err == nil {
				agentsMap[agentID].Name = cfg.Name
				agentsMap[agentID].ClanID = cfg.ClanID
				agentsMap[agentID].Profession = cfg.Profession
				agentsMap[agentID].SecondaryHobby = cfg.SecondaryHobby
			}
		}
	}

	// 3. Scan sockets directory ($XDG_STATE_HOME/civquest/sockets/)
	socketsDir := filepath.Join(resolver.StateDir(), "sockets")
	if entries, err := os.ReadDir(socketsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".sock") {
				agentID := strings.TrimSuffix(name, ".sock")
				if _, exists := agentsMap[agentID]; !exists {
					agentsMap[agentID] = &AgentInfo{
						ID:   agentID,
						Name: agentID,
					}
				}
			}
		}
	}

	// 4. Scan agents state directory ($XDG_STATE_HOME/civquest/agents/)
	agentsStateDir := resolver.AgentsStateDir()
	if entries, err := os.ReadDir(agentsStateDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				// Also check {agent_id}/agent.sock
				nestedSock := filepath.Join(agentsStateDir, entry.Name(), "agent.sock")
				if _, err := os.Stat(nestedSock); err == nil {
					agentID := entry.Name()
					if _, exists := agentsMap[agentID]; !exists {
						agentsMap[agentID] = &AgentInfo{
							ID:   agentID,
							Name: agentID,
						}
					}
				}
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".sock") {
				agentID := strings.TrimSuffix(name, ".sock")
				if _, exists := agentsMap[agentID]; !exists {
					agentsMap[agentID] = &AgentInfo{
						ID:   agentID,
						Name: agentID,
					}
				}
			}
		}
	}

	// 5. Test socket responsiveness for each detected agent
	result := make([]AgentInfo, 0, len(agentsMap))
	for agentID, info := range agentsMap {
		sockPath := s.resolveAgentSocket(agentID)
		info.SocketPath = sockPath

		if _, err := os.Stat(sockPath); err == nil {
			conn, err := net.DialTimeout("unix", sockPath, 50*time.Millisecond)
			if err == nil {
				info.Active = true
				_ = conn.Close()
			}
		}

		result = append(result, *info)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	writeJSON(w, http.StatusOK, result)
}

// handleAgentStatus queries the agent's UNIX socket and returns real-time status JSON.
func (s *Server) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	sub = strings.Trim(sub, "/")

	if sub == "" {
		s.handleAgents(w, r)
		return
	}

	agentID := sub
	if strings.HasSuffix(sub, "/status") {
		agentID = strings.TrimSuffix(sub, "/status")
	}

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sockPath := s.resolveAgentSocket(agentID)
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		// Provide offline identity fallback
		resp := map[string]any{
			"agent":       agentID,
			"active":      false,
			"status":      "OFFLINE",
			"error":       fmt.Sprintf("agent socket unavailable: %v", err),
			"socket_path": sockPath,
			"time":        time.Now().UTC().Format(time.RFC3339),
		}

		if s.cfg.Resolver != nil {
			if cfg, cfgErr := s.cfg.Resolver.LoadAgentConfig(agentID); cfgErr == nil && cfg != nil {
				resp["clan"] = cfg.ClanID
				resp["profession"] = cfg.Profession
				resp["secondary_hobby"] = cfg.SecondaryHobby
				if cfg.Name != "" {
					resp["name"] = cfg.Name
				}
			}
		}

		writeJSON(w, http.StatusOK, resp)
		return
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		raw := scanner.Bytes()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	writeJSONError(w, http.StatusInternalServerError, "empty response from agent socket")
}

// -----------------------------------------------------------------------------
// Real-Time WebSocket Telemetry
// -----------------------------------------------------------------------------

func (s *Server) handleAgentWS(ws *websocket.Conn) {
	defer ws.Close()

	agentID := strings.TrimPrefix(ws.Request().URL.Path, "/ws/agents/")
	agentID = strings.Trim(agentID, "/")

	if agentID == "" {
		_ = websocket.Message.Send(ws, `{"error":"missing agent id"}`)
		return
	}

	disconnectChan := make(chan struct{})
	go func() {
		var dummy string
		for {
			if err := websocket.Message.Receive(ws, &dummy); err != nil {
				close(disconnectChan)
				return
			}
		}
	}()

	ticker := time.NewTicker(280 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-disconnectChan:
			return
		case <-ticker.C:
			sockPath := s.resolveAgentSocket(agentID)
			conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
			if err != nil {
				resp := map[string]any{
					"agent":       agentID,
					"active":      false,
					"status":      "OFFLINE",
					"error":       fmt.Sprintf("socket unavailable: %v", err),
					"socket_path": sockPath,
					"time":        time.Now().UTC().Format(time.RFC3339),
				}
				if s.cfg.Resolver != nil {
					if cfg, cfgErr := s.cfg.Resolver.LoadAgentConfig(agentID); cfgErr == nil && cfg != nil {
						resp["clan"] = cfg.ClanID
						resp["profession"] = cfg.Profession
						resp["secondary_hobby"] = cfg.SecondaryHobby
						if cfg.Name != "" {
							resp["name"] = cfg.Name
						}
					}
				}
				msg, _ := json.Marshal(resp)
				if err := websocket.Message.Send(ws, string(msg)); err != nil {
					return
				}
				continue
			}

			_ = conn.SetDeadline(time.Now().Add(250 * time.Millisecond))
			scanner := bufio.NewScanner(conn)
			if scanner.Scan() {
				line := scanner.Text()
				if err := websocket.Message.Send(ws, line); err != nil {
					_ = conn.Close()
					return
				}
			}
			_ = conn.Close()
		}
	}
}

// -----------------------------------------------------------------------------
// Static Content & SPA Routing
// -----------------------------------------------------------------------------

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		http.NotFound(w, r)
		return
	}

	reqPath := strings.TrimPrefix(r.URL.Path, "/")
	if reqPath == "" {
		reqPath = "index.html"
	}

	embedPath := "static/" + reqPath

	file, err := staticFS.Open(embedPath)
	if err != nil {
		// SPA Fallback: serve index.html for any client routes
		indexFile, idxErr := staticFS.Open("static/index.html")
		if idxErr != nil {
			http.Error(w, "embedded index.html not found", http.StatusNotFound)
			return
		}
		defer indexFile.Close()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, indexFile)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		// Directory or stat error fallback
		indexFile, idxErr := staticFS.Open("static/index.html")
		if idxErr != nil {
			http.Error(w, "embedded index.html not found", http.StatusNotFound)
			return
		}
		defer indexFile.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, indexFile)
		return
	}

	contentType := detectContentType(reqPath)
	w.Header().Set("Content-Type", contentType)

	if rs, ok := file.(io.ReadSeeker); ok {
		http.ServeContent(w, r, stat.Name(), stat.ModTime(), rs)
		return
	}

	_, _ = io.Copy(w, file)
}

func detectContentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

// -----------------------------------------------------------------------------
// HTTP Utilities
// -----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}
