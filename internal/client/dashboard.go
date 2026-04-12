package client

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/iluxav/ntunl/internal/config"
)

//go:embed web/index.html
var webFS embed.FS

func (c *Client) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		cfg := c.watcher.Config()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.Token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="etunl"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (c *Client) startDashboard(addr string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", c.basicAuth(func(w http.ResponseWriter, r *http.Request) {
		data, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	}))

	mux.HandleFunc("/api/status", c.basicAuth(func(w http.ResponseWriter, r *http.Request) {
		cfg := c.watcher.Config()
		connected := c.conn != nil
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tunnel_connected": connected,
			"routes":           len(cfg.Routes),
			"server":           cfg.Server,
		})
	}))

	mux.HandleFunc("/api/routes", c.basicAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := c.watcher.Config()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cfg.Routes)

		case http.MethodPost:
			var route config.Route
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if err := json.Unmarshal(body, &route); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if route.Name == "" || route.Target == "" {
				http.Error(w, "name and target are required", http.StatusBadRequest)
				return
			}
			if route.Type == "" {
				route.Type = "http"
			}

			cfg := c.watcher.Config()
			if err := cfg.AddRoute(route); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if err := config.SaveClientConfig(c.configPath, cfg); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	mux.HandleFunc("/api/routes/", c.basicAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/routes/")
		if name == "" {
			http.Error(w, "route name required", http.StatusBadRequest)
			return
		}

		cfg := c.watcher.Config()
		if err := cfg.RemoveRoute(name); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err := config.SaveClientConfig(c.configPath, cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	log.Printf("dashboard on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("dashboard failed: %v", err)
	}
}
