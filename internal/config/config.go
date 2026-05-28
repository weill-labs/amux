package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/weill-labs/amux/internal/proto"
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
	Surface1Hex  = "45475a" // Surface 1 — pressed status bar background
	BlueHex      = "89b4fa" // Blue — active tab highlight
	GreenHex     = "a6e3a1" // Green — status indicator
	YellowHex    = "f9e2af" // Yellow — status indicator
	RedHex       = "f38ba8" // Red — status indicator
)

type DebugConfig struct {
	Pprof bool `toml:"pprof"`
}

type ClientConfig struct {
	LocalEcho      string `toml:"local_echo"`
	LocalEchoStyle string `toml:"local_echo_style"`
}

const (
	StatusStyleCompact   = "compact"
	StatusStylePlain     = "plain"
	StatusStylePowerline = "powerline"

	ThemeIconsASCII   = "ascii"
	ThemeIconsUnicode = "unicode"
	ThemeIconsNerd    = "nerd"
)

// ThemeConfig controls client-side presentation.
//
// Example:
//
//	[theme]
//	icons = "unicode" # ascii | unicode | nerd
type ThemeConfig struct {
	StatusStyle string  `toml:"status_style"`
	Icons       *string `toml:"icons"`
}

type RemoteConfig struct {
	Hosts map[string]Host `toml:"hosts"`
}

type Host struct {
	SSH        string `toml:"ssh"`
	Session    string `toml:"session"`
	SocketPath string `toml:"socket_path"`
}

// Config is the top-level amux configuration.
type Config struct {
	ScrollbackLines *int         `toml:"scrollback_lines"`
	Debug           DebugConfig  `toml:"debug"`
	Client          ClientConfig `toml:"client"`
	Theme           ThemeConfig  `toml:"theme"`
	Remote          RemoteConfig `toml:"remote"`
}

// DefaultPath returns the default config file path.
// Checks AMUX_CONFIG env var first, then ~/.config/amux/config.toml.
func DefaultPath() string {
	if p := os.Getenv("AMUX_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "amux", "config.toml")
}

// Load reads the config from the given path. Returns an empty config if the file doesn't exist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return parseConfig(data)
}

func parseConfig(data []byte) (*Config, error) {
	cfg := &Config{}

	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if hasLegacyKeysConfig(md.Undecoded()) {
		return nil, fmt.Errorf(`unsupported config section "keys"`)
	}

	if _, err := ResolveScrollbackLines(cfg.ScrollbackLines); err != nil {
		return nil, err
	}
	if _, err := ResolveLocalEchoMode(cfg.Client.LocalEcho); err != nil {
		return nil, err
	}
	if _, err := ResolveLocalEchoStyle(cfg.Client.LocalEchoStyle); err != nil {
		return nil, err
	}
	if _, err := ResolveStatusStyle(cfg.Theme.StatusStyle); err != nil {
		return nil, err
	}
	if _, err := ResolveThemeIcons(cfg.Theme.Icons); err != nil {
		return nil, err
	}
	if err := ValidateRemoteHosts(cfg.Remote.Hosts); err != nil {
		return nil, err
	}

	return cfg, nil
}

func hasLegacyKeysConfig(undecoded []toml.Key) bool {
	for _, key := range undecoded {
		if len(key) > 0 && key[0] == "keys" {
			return true
		}
	}
	return false
}

// ResolveScrollbackLines validates a configured scrollback limit and returns
// the effective value. Zero means "use the built-in default".
func ResolveScrollbackLines(lines *int) (int, error) {
	return resolveScrollbackLinesField("scrollback_lines", lines)
}

func resolveScrollbackLinesField(field string, lines *int) (int, error) {
	switch {
	case lines == nil:
		return proto.DefaultScrollbackLines, nil
	case *lines < 1:
		return 0, fmt.Errorf("%s must be >= 1", field)
	default:
		return *lines, nil
	}
}

// EffectiveScrollbackLines returns the resolved retained scrollback limit,
// falling back to the built-in default for nil configs or unset values.
func (c *Config) EffectiveScrollbackLines() int {
	if c == nil {
		return proto.DefaultScrollbackLines
	}
	lines, err := ResolveScrollbackLines(c.ScrollbackLines)
	if err != nil {
		return proto.DefaultScrollbackLines
	}
	return lines
}

func (c *Config) PprofEnabled() bool {
	return c != nil && c.Debug.Pprof
}

func ResolveLocalEchoMode(mode string) (string, error) {
	switch mode {
	case "", "auto":
		return "auto", nil
	case "off", "always":
		return mode, nil
	default:
		return "", fmt.Errorf(`local_echo must be one of "auto", "off", or "always"`)
	}
}

func ResolveLocalEchoStyle(style string) (string, error) {
	switch style {
	case "", "dim":
		return "dim", nil
	case "underline", "none":
		return style, nil
	default:
		return "", fmt.Errorf(`local_echo_style must be one of "dim", "underline", or "none"`)
	}
}

func ResolveStatusStyle(style string) (string, error) {
	switch style {
	case "", StatusStyleCompact:
		return StatusStyleCompact, nil
	case StatusStylePlain, StatusStylePowerline:
		return style, nil
	default:
		return "", fmt.Errorf(`status_style must be one of "compact", "plain", or "powerline"`)
	}
}

func (c *Config) EffectiveLocalEchoMode() string {
	if c == nil {
		return "auto"
	}
	mode, err := ResolveLocalEchoMode(c.Client.LocalEcho)
	if err != nil {
		return "auto"
	}
	return mode
}

func (c *Config) EffectiveLocalEchoStyle() string {
	if c == nil {
		return "dim"
	}
	style, err := ResolveLocalEchoStyle(c.Client.LocalEchoStyle)
	if err != nil {
		return "dim"
	}
	return style
}

func (c *Config) EffectiveStatusStyle() string {
	if c == nil {
		return StatusStyleCompact
	}
	style, err := ResolveStatusStyle(c.Theme.StatusStyle)
	if err != nil {
		return StatusStyleCompact
	}
	return style
}

func ResolveThemeIcons(icons *string) (string, error) {
	if icons == nil {
		return ThemeIconsUnicode, nil
	}
	switch *icons {
	case ThemeIconsASCII, ThemeIconsUnicode, ThemeIconsNerd:
		return *icons, nil
	default:
		return "", fmt.Errorf(`theme.icons must be one of "ascii", "unicode", or "nerd"`)
	}
}

func (c *Config) EffectiveThemeIcons() string {
	if c == nil {
		return ThemeIconsUnicode
	}
	icons, err := ResolveThemeIcons(c.Theme.Icons)
	if err != nil {
		return ThemeIconsUnicode
	}
	return icons
}

func ValidateRemoteHosts(hosts map[string]Host) error {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		host := hosts[name]
		if strings.TrimSpace(host.SSH) == "" {
			return fmt.Errorf("remote.hosts.%s.ssh is required", name)
		}
		if strings.TrimSpace(host.Session) == "" {
			return fmt.Errorf("remote.hosts.%s.session is required", name)
		}
		if strings.TrimSpace(host.SocketPath) == "" {
			return fmt.Errorf("remote.hosts.%s.socket_path is required", name)
		}
		if !filepath.IsAbs(host.SocketPath) {
			return fmt.Errorf("remote.hosts.%s.socket_path must be absolute", name)
		}
	}
	return nil
}
