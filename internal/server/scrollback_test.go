package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestScrollbackConfigLinesForHost(t *testing.T) {
	t.Parallel()

	cfg := NewScrollbackConfig(2000, map[string]int{
		"local":   1000,
		"builder": 3000,
		"ignored": 0,
	})

	tests := []struct {
		name string
		host string
		want int
	}{
		{name: "local override", host: "local", want: 1000},
		{name: "remote override", host: "builder", want: 3000},
		{name: "missing host uses default", host: "unknown", want: 2000},
		{name: "empty host resolves local", host: "", want: 1000},
		{name: "invalid override omitted", host: "ignored", want: 2000},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := cfg.LinesForHost(tt.host); got != tt.want {
				t.Fatalf("LinesForHost(%q) = %d, want %d", tt.host, got, tt.want)
			}
		})
	}
}

func TestScrollbackConfigFallsBackToBuiltInDefault(t *testing.T) {
	t.Parallel()

	cfg := NewScrollbackConfig(0, nil)
	if got := cfg.LinesForHost("local"); got != mux.DefaultScrollbackLines {
		t.Fatalf("LinesForHost(local) = %d, want %d", got, mux.DefaultScrollbackLines)
	}
}
