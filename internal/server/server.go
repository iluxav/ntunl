package server

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/iluxav/ntunl/internal/config"
	"github.com/iluxav/ntunl/internal/tunnel"
)

type tcpListener struct {
	port     int
	listener net.Listener
	cancel   context.CancelFunc
}

type Server struct {
	cfg      *config.ServerConfig
	upgrader websocket.Upgrader

	mu           sync.RWMutex
	conn         *tunnel.Conn // single client connection
	routes       []tunnel.RouteInfo
	tcpListeners map[string]*tcpListener // route name → listener
	portStart    int
	portEnd      int
}

func New(cfg *config.ServerConfig) *Server {
	portStart, portEnd, err := cfg.ParseTCPPortRange()
	if err != nil {
		log.Printf("WARNING: invalid tcp_port_range: %v, TCP tunneling disabled", err)
		portStart, portEnd = 0, 0
	}
	return &Server{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		tcpListeners: make(map[string]*tcpListener),
		portStart:    portStart,
		portEnd:      portEnd,
	}
}

func (s *Server) Start() error {
	log.Printf("server listening on %s (HTTP), TCP port range %d-%d", s.cfg.ListenHTTP, s.portStart, s.portEnd)

	return http.ListenAndServe(s.cfg.ListenHTTP, s)
}

// allocatePort returns a deterministic port for the given route name.
// Caller must hold s.mu.
func (s *Server) allocatePort(routeName string) (int, error) {
	if s.portStart == 0 && s.portEnd == 0 {
		return 0, fmt.Errorf("TCP port range not configured")
	}
	rangeSize := s.portEnd - s.portStart
	h := fnv.New32a()
	h.Write([]byte(routeName))
	port := s.portStart + int(h.Sum32())%rangeSize

	// Check if already in use by another route
	for name, tl := range s.tcpListeners {
		if tl.port == port && name != routeName {
			// Linear scan for next free port
			for p := s.portStart; p < s.portEnd; p++ {
				taken := false
				for _, tl2 := range s.tcpListeners {
					if tl2.port == p {
						taken = true
						break
					}
				}
				if !taken {
					return p, nil
				}
			}
			return 0, fmt.Errorf("no free TCP ports in range %d-%d", s.portStart, s.portEnd)
		}
	}
	return port, nil
}

// ServeHTTP handles all incoming HTTP requests.
// Requests to /tunnel are the tunnel WebSocket endpoint.
// All other requests are proxied based on subdomain.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/tunnel":
		s.handleTunnel(w, r)
	case "/health":
		s.handleHealth(w, r)
	default:
		s.handleHTTPProxy(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	connected := s.conn != nil
	routeCount := len(s.routes)
	routes := make([]tunnel.RouteInfo, len(s.routes))
	copy(routes, s.routes)
	tcpPorts := make(map[string]int)
	for name, tl := range s.tcpListeners {
		tcpPorts[name] = tl.port
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":           "ok",
		"tunnel_connected": connected,
		"routes":           routeCount,
		"route_list":       routes,
		"tcp_ports":        tcpPorts,
	})
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	expected := "Bearer " + s.cfg.Token
	if token != expected {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}

	// Close previous connection if any
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	conn := tunnel.NewConn(ws, nil)
	s.conn = conn
	s.mu.Unlock()

	log.Printf("tunnel client connected from %s", r.RemoteAddr)
	conn.StartPing()

	// Override the RouteSync handler
	originalMux := conn.Mux()
	originalMux.SetRouteSyncHandler(func(payload []byte) {
		var routes []tunnel.RouteInfo
		if err := json.Unmarshal(payload, &routes); err != nil {
			log.Printf("invalid route sync: %v", err)
			return
		}
		s.mu.Lock()
		s.routes = routes
		s.mu.Unlock()
		log.Printf("routes updated: %d routes", len(routes))
		for _, r := range routes {
			log.Printf("  - %s (%s)", r.Name, r.Type)
		}
		s.syncTCPListeners(routes)
	})

	err = conn.ReadLoop()
	log.Printf("tunnel client disconnected: %v", err)

	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
		s.routes = nil
	}
	s.mu.Unlock()
	s.syncTCPListeners(nil) // stop all TCP listeners
}

func (s *Server) getConn() *tunnel.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn
}

func (s *Server) findRoute(name string) *tunnel.RouteInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.routes {
		if s.routes[i].Name == name {
			return &s.routes[i]
		}
	}
	return nil
}
