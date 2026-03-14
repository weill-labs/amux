package config

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// CatppuccinMocha accent palette in official order (catppuccin.com/palette).
var CatppuccinMocha = []string{
	"f5e0dc", // Rosewater
	"f2cdcd", // Flamingo
	"f5c2e7", // Pink
	"cba6f7", // Mauve
	"f38ba8", // Red
	"eba0ac", // Maroon
	"fab387", // Peach
	"f9e2af", // Yellow
	"a6e3a1", // Green
	"94e2d5", // Teal
	"89dceb", // Sky
	"74c7ec", // Sapphire
	"89b4fa", // Blue
	"b4befe", // Lavender
}

// Host defines a machine that can run agents.
type Host struct {
	Type       string `toml:"type"`    // "local" or "remote"
	User       string `toml:"user"`    // SSH user (remote only)
	Address    string `toml:"address"` // IP or hostname (remote only)
	ProjectDir string `toml:"project_dir"`
	GPU        string `toml:"gpu"`
	Color      string `toml:"color"` // hex color, auto-assigned if empty
}

// Config is the top-level amux configuration.
type Config struct {
	Hosts map[string]Host `toml:"hosts"`
}

// DefaultPath returns the default config file path.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "amux", "hosts.toml")
}

// Load reads the config from the given path. Returns an empty config if the file doesn't exist.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Hosts: make(map[string]Host),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Auto-assign colors for hosts without explicit color
	for name, host := range cfg.Hosts {
		if host.Color == "" {
			host.Color = ColorForHost(name)
			cfg.Hosts[name] = host
		}
	}

	return cfg, nil
}

// ColorForHost deterministically picks a Catppuccin color based on hostname.
func ColorForHost(hostname string) string {
	h := crc32.ChecksumIEEE([]byte(hostname))
	return CatppuccinMocha[h%uint32(len(CatppuccinMocha))]
}

// HostUser returns the SSH user for a host, defaulting to "ubuntu".
func (c *Config) HostUser(hostname string) string {
	if h, ok := c.Hosts[hostname]; ok && h.User != "" {
		return h.User
	}
	return "ubuntu"
}

// HostAddress returns the address for a host, falling back to the hostname itself.
func (c *Config) HostAddress(hostname string) string {
	if h, ok := c.Hosts[hostname]; ok && h.Address != "" {
		return h.Address
	}
	return hostname
}

// HostColor returns the color for a host.
func (c *Config) HostColor(hostname string) string {
	if h, ok := c.Hosts[hostname]; ok && h.Color != "" {
		return h.Color
	}
	return ColorForHost(hostname)
}
