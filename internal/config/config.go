package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Route struct {
	Name      string `yaml:"name" json:"name"`
	Type      string `yaml:"type" json:"type"`
	Target    string `yaml:"target" json:"target"`
	LocalPort int    `yaml:"local_port,omitempty" json:"local_port,omitempty"`
}

type ClientConfig struct {
	Server string  `yaml:"server"`
	Token  string  `yaml:"token"`
	Routes []Route `yaml:"routes"`
}

type ServerConfig struct {
	ListenHTTP   string `yaml:"listen_http"`
	TCPPortRange string `yaml:"tcp_port_range"`
	Token        string `yaml:"token"`
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
	return &cfg, nil
}

func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
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
