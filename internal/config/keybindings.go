package config

import (
	"fmt"
	"strings"
)

// KeyConfig represents the [keys] section of the config file.
type KeyConfig struct {
	Prefix string            `toml:"prefix"`
	Unbind []string          `toml:"unbind"`
	Bind   map[string]string `toml:"bind"`
}

// Binding represents a single key binding: an action name and its arguments.
type Binding struct {
	Action string
	Args   []string
}

// Keybindings holds the resolved dispatch table for client-side input handling.
// The Prefix byte triggers prefix mode; after prefix, the Bindings map
// dispatches the next byte to an action.
type Keybindings struct {
	Prefix   byte
	Bindings map[byte]Binding
}

// DefaultKeybindings returns the built-in default keybindings.
func DefaultKeybindings() *Keybindings {
	return &Keybindings{
		Prefix: 0x01, // Ctrl-a
		Bindings: map[byte]Binding{
			'\\': {Action: "split", Args: []string{"v"}},
			'-':  {Action: "split"},
			'|':  {Action: "split", Args: []string{"root", "v"}},
			'_':  {Action: "split", Args: []string{"root"}},
			'}':  {Action: "swap", Args: []string{"forward"}},
			'{':  {Action: "swap", Args: []string{"backward"}},
			'o':  {Action: "focus", Args: []string{"next"}},
			'h':  {Action: "focus", Args: []string{"left"}},
			'l':  {Action: "focus", Args: []string{"right"}},
			'k':  {Action: "focus", Args: []string{"up"}},
			'j':  {Action: "focus", Args: []string{"down"}},
			'H':  {Action: "resize-active", Args: []string{"left", "2"}},
			'J':  {Action: "resize-active", Args: []string{"down", "2"}},
			'K':  {Action: "resize-active", Args: []string{"up", "2"}},
			'L':  {Action: "resize-active", Args: []string{"right", "2"}},
			'x':  {Action: "kill"},
			'z':  {Action: "zoom"},
			'm':  {Action: "toggle-minimize"},
			'[':  {Action: "copy-mode"},
			'c':  {Action: "new-window"},
			'n':  {Action: "next-window"},
			'p':  {Action: "prev-window"},
			'1':  {Action: "select-window", Args: []string{"1"}},
			'2':  {Action: "select-window", Args: []string{"2"}},
			'3':  {Action: "select-window", Args: []string{"3"}},
			'4':  {Action: "select-window", Args: []string{"4"}},
			'5':  {Action: "select-window", Args: []string{"5"}},
			'6':  {Action: "select-window", Args: []string{"6"}},
			'7':  {Action: "select-window", Args: []string{"7"}},
			'8':  {Action: "select-window", Args: []string{"8"}},
			'9':  {Action: "select-window", Args: []string{"9"}},
			'd':  {Action: "detach"},
			'r':  {Action: "reload"},
		},
	}
}

// knownActions is the set of valid action names for key bindings.
// Actions handled client-side (detach, reload, copy-mode) and actions
// forwarded as server commands are both included.
// Keep in sync with server/client_conn.go handleCommand().
var knownActions = map[string]bool{
	"split": true, "focus": true, "swap": true, "zoom": true,
	"rotate": true, "minimize": true, "restore": true, "kill": true,
	"spawn": true, "send-keys": true, "resize-active": true,
	"toggle-minimize": true, "new-window": true, "next-window": true,
	"prev-window": true, "select-window": true, "rename-window": true,
	"detach": true, "reload": true, "copy-mode": true,
}

// BuildKeybindings resolves a KeyConfig into a Keybindings dispatch table.
// It starts with defaults, applies user bind overrides, then removes unbound keys.
// Note: unbind is applied after bind, so unbinding a key that was just bound
// will remove it. To replace a default binding, use bind alone.
func BuildKeybindings(kc *KeyConfig) (*Keybindings, error) {
	kb := DefaultKeybindings()

	if kc == nil {
		return kb, nil
	}

	// Override prefix
	if kc.Prefix != "" {
		b, err := ParseKey(kc.Prefix)
		if err != nil {
			return nil, fmt.Errorf("invalid prefix %q: %w", kc.Prefix, err)
		}
		kb.Prefix = b
	}

	// Apply user bindings (override or add)
	for key, action := range kc.Bind {
		b, err := ParseKey(key)
		if err != nil {
			return nil, fmt.Errorf("invalid key %q: %w", key, err)
		}
		if b == kb.Prefix {
			return nil, fmt.Errorf("key %q conflicts with prefix key (use prefix-prefix to send literal)", key)
		}
		binding, err := ParseAction(action)
		if err != nil {
			return nil, fmt.Errorf("invalid action %q for key %q: %w", action, key, err)
		}
		if !knownActions[binding.Action] {
			return nil, fmt.Errorf("unknown action %q for key %q", binding.Action, key)
		}
		kb.Bindings[b] = binding
	}

	// Remove unbound keys
	for _, key := range kc.Unbind {
		b, err := ParseKey(key)
		if err != nil {
			return nil, fmt.Errorf("invalid unbind key %q: %w", key, err)
		}
		delete(kb.Bindings, b)
	}

	return kb, nil
}

// ParseKey converts a key string to its byte value.
//
// Supported formats:
//   - Single printable char: "d", "\\", "-", "|"
//   - Ctrl combo: "C-a" (0x01), "C-b" (0x02), ..., "C-z" (0x1a)
func ParseKey(s string) (byte, error) {
	if len(s) == 0 {
		return 0, fmt.Errorf("empty key string")
	}

	// Ctrl combo: "C-x"
	if len(s) == 3 && s[0] == 'C' && s[1] == '-' {
		ch := s[2]
		if ch >= 'a' && ch <= 'z' {
			return ch - 'a' + 1, nil
		}
		if ch >= 'A' && ch <= 'Z' {
			return ch - 'A' + 1, nil
		}
		return 0, fmt.Errorf("unsupported ctrl key: %q", s)
	}

	// Single character
	if len(s) == 1 {
		return s[0], nil
	}

	return 0, fmt.Errorf("unsupported key format: %q", s)
}

// ParseAction splits an action string like "split v" into a Binding
// with Action="split" and Args=["v"].
func ParseAction(s string) (Binding, error) {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return Binding{}, fmt.Errorf("empty action string")
	}
	b := Binding{Action: parts[0]}
	if len(parts) > 1 {
		b.Args = parts[1:]
	}
	return b, nil
}
