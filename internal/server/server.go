package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/iluxav/ntunl/internal/config"
	"github.com/iluxav/ntunl/internal/tunnel"
	"github.com/gorilla/websocket"
)

type Server struct {
	cfg      *config.ServerConfig
	upgrader websocket.Upgrader

	mu     sync.RWMutex
	conn   *tunnel.Conn   // single client connection
	routes []tunnel.RouteInfo
}

func New(cfg *config.ServerConfig) *Server {
	return &Server{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", s.handleTunnel)

	log.Printf("server listening on %s (HTTP) and %s (TCP)", s.cfg.ListenHTTP, s.cfg.ListenTCP)

	go s.startTCPListener()

	return http.ListenAndServe(s.cfg.ListenHTTP, s)
}

// ServeHTTP handles all incoming HTTP requests.
// Requests to /tunnel are the tunnel WebSocket endpoint.
// All other requests are proxied based on subdomain.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/tunnel" {
		s.handleTunnel(w, r)
		return
	}
	s.handleHTTPProxy(w, r)
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
	})

	err = conn.ReadLoop()
	log.Printf("tunnel client disconnected: %v", err)

	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
		s.routes = nil
	}
	s.mu.Unlock()
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
