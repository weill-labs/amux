package config

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/weill-labs/amux/internal/mux"
)

// catppuccinMocha is the accent palette in official order (catppuccin.com/palette).
// Unexported to prevent mutation; access via AccentColor/NumAccentColors.
var catppuccinMocha = [...]string{
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

// NumAccentColors is the number of colors in the Catppuccin Mocha accent palette.
const NumAccentColors = 14

// AccentColor returns the hex color at index i (mod palette size).
func AccentColor(i uint32) string {
	return catppuccinMocha[i%NumAccentColors]
}

// AccentColors returns a copy of the full palette.
func AccentColors() []string {
	out := make([]string, NumAccentColors)
	copy(out, catppuccinMocha[:])
	return out
}

// catppuccinLetters maps each hex color to a single-letter abbreviation
// for use in color map output (e.g., `amux capture --colors`).
var catppuccinLetters = map[string]byte{
	"f5e0dc": 'R', // Rosewater
	"f2cdcd": 'F', // Flamingo
	"f5c2e7": 'P', // Pink
	"cba6f7": 'M', // Mauve
	"f38ba8": 'E', // Red
	"eba0ac": 'A', // Maroon
	"fab387": 'H', // Peach
	"f9e2af": 'Y', // Yellow
	"a6e3a1": 'G', // Green
	"94e2d5": 'T', // Teal
	"89dceb": 'S', // Sky
	"74c7ec": 'B', // Sapphire
	"89b4fa": 'U', // Blue
	"b4befe": 'L', // Lavender
}

// AccentColorLetter returns the single-letter abbreviation for a hex color,
// or 0 if the color is not in the palette.
func AccentColorLetter(hex string) (byte, bool) {
	l, ok := catppuccinLetters[hex]
	return l, ok
}

// Named hex color constants from the Catppuccin Mocha palette.
const (
	DimColorHex  = "6c7086" // Overlay 0 — inactive/dim borders
	TextColorHex = "cdd6f4" // Text foreground
	Surface0Hex  = "313244" // Surface 0 — status bar background
	BlueHex      = "89b4fa" // Blue — active tab highlight
	GreenHex     = "a6e3a1" // Green — status indicator
	YellowHex    = "f9e2af" // Yellow — status indicator
	RedHex       = "f38ba8" // Red — status indicator
)

// Host defines a machine that can run agents.
type Host struct {
	Type         string `toml:"type"`          // "local" or "remote"
	User         string `toml:"user"`          // SSH user (remote only)
	Address      string `toml:"address"`       // IP or hostname (remote only)
	IdentityFile string `toml:"identity_file"` // SSH private key path (optional)
	ProjectDir   string `toml:"project_dir"`
	GPU          string `toml:"gpu"`
	Color        string `toml:"color"`  // hex color, auto-assigned if empty
	Deploy       *bool  `toml:"deploy"` // auto-deploy binary; nil = true (opt-out with false)
}

// Config is the top-level amux configuration.
type Config struct {
	ScrollbackLines *int            `toml:"scrollback_lines"`
	Hosts           map[string]Host `toml:"hosts"`
	Keys            KeyConfig       `toml:"keys"`
}

// DefaultPath returns the default config file path.
// Checks AMUX_CONFIG env var first, then ~/.config/amux/config.toml,
// then falls back to ~/.config/amux/hosts.toml for backward compatibility.
func DefaultPath() string {
	if p := os.Getenv("AMUX_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "amux", "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		return configPath
	}
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

	if _, err := ResolveScrollbackLines(cfg.ScrollbackLines); err != nil {
		return nil, err
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

// ResolveScrollbackLines validates a configured scrollback limit and returns
// the effective value. Zero means "use the built-in default".
func ResolveScrollbackLines(lines *int) (int, error) {
	switch {
	case lines == nil:
		return mux.DefaultScrollbackLines, nil
	case *lines < 1:
		return 0, fmt.Errorf("scrollback_lines must be >= 1")
	default:
		return *lines, nil
	}
}

// EffectiveScrollbackLines returns the resolved retained scrollback limit,
// falling back to the built-in default for nil configs or unset values.
func (c *Config) EffectiveScrollbackLines() int {
	if c == nil {
		return mux.DefaultScrollbackLines
	}
	lines, err := ResolveScrollbackLines(c.ScrollbackLines)
	if err != nil {
		return mux.DefaultScrollbackLines
	}
	return lines
}

// ColorForHost deterministically picks a Catppuccin color based on hostname.
func ColorForHost(hostname string) string {
	h := crc32.ChecksumIEEE([]byte(hostname))
	return AccentColor(h)
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
