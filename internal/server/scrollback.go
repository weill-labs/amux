package server

import "github.com/weill-labs/amux/internal/mux"

// ScrollbackConfig resolves retained history limits for panes in a session.
type ScrollbackConfig struct {
	DefaultLines int
	HostLines    map[string]int
}

// NewScrollbackConfig returns a normalized scrollback config. Non-positive
// values fall back to the built-in default.
func NewScrollbackConfig(defaultLines int, hostLines map[string]int) ScrollbackConfig {
	cfg := ScrollbackConfig{
		DefaultLines: defaultScrollbackLines(defaultLines),
	}
	if len(hostLines) > 0 {
		cfg.HostLines = make(map[string]int, len(hostLines))
		for host, lines := range hostLines {
			if host == "" || lines <= 0 {
				continue
			}
			cfg.HostLines[host] = lines
		}
	}
	return cfg
}

func defaultScrollbackLines(lines int) int {
	if lines <= 0 {
		return mux.DefaultScrollbackLines
	}
	return lines
}

func (c ScrollbackConfig) LinesForHost(host string) int {
	if host == "" {
		host = mux.DefaultHost
	}
	if c.HostLines != nil {
		if lines, ok := c.HostLines[host]; ok && lines > 0 {
			return lines
		}
	}
	return defaultScrollbackLines(c.DefaultLines)
}
