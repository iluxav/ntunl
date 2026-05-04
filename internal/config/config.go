package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// GenerateToken returns a fresh 32-byte hex-encoded token suitable for
// either the tunnel auth token or other secret values.
func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

type Route struct {
	Name      string `yaml:"name" json:"name"`
	Type      string `yaml:"type" json:"type"`
	Target    string `yaml:"target" json:"target"`
	LocalPort int    `yaml:"local_port,omitempty" json:"local_port,omitempty"`
	Auth      *Auth  `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// Auth describes optional protection for an HTTP route. Exactly one of
// three mutually-exclusive forms must be set:
//
//   - Bearer: checks "Authorization: Bearer <Bearer>" (API clients)
//   - Header+Value: checks "<Header>: <Value>" (API clients)
//   - Users: cookie-based browser session; the proxy serves a login form
//     at /___login___ and validates a signed cookie on subsequent requests
type Auth struct {
	Bearer string     `yaml:"bearer,omitempty" json:"bearer,omitempty"`
	Header string     `yaml:"header,omitempty" json:"header,omitempty"`
	Value  string     `yaml:"value,omitempty" json:"value,omitempty"`
	Users  []AuthUser `yaml:"users,omitempty" json:"users,omitempty"`
}

// AuthUser is one credential pair for cookie-session auth.
type AuthUser struct {
	User     string `yaml:"user" json:"user"`
	Password string `yaml:"password" json:"password"`
}

// Validate reports whether the auth block is well-formed. Caller is
// responsible for checking that the parent route is HTTP.
func (a *Auth) Validate() error {
	if a == nil {
		return nil
	}
	hasBearer := a.Bearer != ""
	hasHeader := a.Header != "" || a.Value != ""
	hasUsers := len(a.Users) > 0
	forms := 0
	for _, on := range []bool{hasBearer, hasHeader, hasUsers} {
		if on {
			forms++
		}
	}
	if forms == 0 {
		return fmt.Errorf("auth: must set bearer, header+value, or users")
	}
	if forms > 1 {
		return fmt.Errorf("auth: set only one of bearer, header+value, or users")
	}
	if hasHeader && (a.Header == "" || a.Value == "") {
		return fmt.Errorf("auth: custom header form requires both header and value")
	}
	if hasUsers {
		for i, u := range a.Users {
			if u.User == "" || u.Password == "" {
				return fmt.Errorf("auth: users[%d] requires both user and password", i)
			}
		}
	}
	return nil
}

type ClientConfig struct {
	Server      string  `yaml:"server"`
	Token       string  `yaml:"token"`
	MachineName string  `yaml:"machine_name"`
	Routes      []Route `yaml:"routes"`
}

type ServerConfig struct {
	ListenHTTP     string `yaml:"listen_http"`
	TCPPortRange   string `yaml:"tcp_port_range"`
	Token          string `yaml:"token"`
	SessionSecret  string `yaml:"session_secret,omitempty"`
	AdminSubdomain string `yaml:"admin_subdomain,omitempty"`
	AdminUser      string `yaml:"admin_user,omitempty"`
	AdminPassword  string `yaml:"admin_password,omitempty"`
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".etunl", "config.yaml")
}

func DefaultServerConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".etunl", "server.yaml")
}

func LoadClientConfig(path string) (*ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ClientConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	for i := range cfg.Routes {
		if err := cfg.Routes[i].validate(); err != nil {
			return nil, fmt.Errorf("route %q: %w", cfg.Routes[i].Name, err)
		}
	}
	return &cfg, nil
}

func (r *Route) validate() error {
	if r.Auth != nil {
		if r.Type != "http" {
			return fmt.Errorf("auth is only supported on http routes")
		}
		if err := r.Auth.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		token, terr := GenerateToken()
		if terr != nil {
			return nil, terr
		}
		sessionSecret, serr := GenerateToken()
		if serr != nil {
			return nil, serr
		}
		cfg := &ServerConfig{
			ListenHTTP:     ":80",
			TCPPortRange:   "15000-15100",
			AdminSubdomain: "manage",
			Token:          token,
			SessionSecret:  sessionSecret,
		}
		if err := SaveServerConfig(path, cfg); err != nil {
			return nil, fmt.Errorf("create default server config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.ListenHTTP == "" {
		cfg.ListenHTTP = ":80"
	}
	if cfg.TCPPortRange == "" {
		cfg.TCPPortRange = "15000-15100"
	}
	if cfg.AdminSubdomain == "" {
		cfg.AdminSubdomain = "manage"
	}
	persist := false
	if cfg.Token == "" {
		token, terr := GenerateToken()
		if terr != nil {
			return nil, terr
		}
		cfg.Token = token
		persist = true
	}
	if cfg.SessionSecret == "" {
		secret, serr := GenerateToken()
		if serr != nil {
			return nil, serr
		}
		cfg.SessionSecret = secret
		persist = true
	}
	if persist {
		if err := SaveServerConfig(path, &cfg); err != nil {
			return nil, fmt.Errorf("persist generated secrets: %w", err)
		}
	}
	return &cfg, nil
}

func SaveServerConfig(path string, cfg *ServerConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

func SaveClientConfig(path string, cfg *ClientConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

func (c *ServerConfig) ParseTCPPortRange() (int, int, error) {
	parts := strings.SplitN(c.TCPPortRange, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid tcp_port_range format %q, expected 'start-end'", c.TCPPortRange)
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range start: %w", err)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range end: %w", err)
	}
	if start >= end {
		return 0, 0, fmt.Errorf("range start must be less than end")
	}
	if start < 1 || end > 65535 {
		return 0, 0, fmt.Errorf("port range must be within 1-65535")
	}
	return start, end, nil
}

func (c *ClientConfig) FindRoute(name string) *Route {
	for i := range c.Routes {
		if c.Routes[i].Name == name {
			return &c.Routes[i]
		}
	}
	return nil
}

func (c *ClientConfig) AddRoute(r Route) error {
	if c.FindRoute(r.Name) != nil {
		return fmt.Errorf("route %q already exists", r.Name)
	}
	c.Routes = append(c.Routes, r)
	return nil
}

func (c *ClientConfig) RemoveRoute(name string) error {
	for i := range c.Routes {
		if c.Routes[i].Name == name {
			c.Routes = append(c.Routes[:i], c.Routes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("route %q not found", name)
}
