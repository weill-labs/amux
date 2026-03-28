package main

import (
	"os"
	"strings"
	"testing"
)

func TestDocsWarnAboutRaceOnlyCoverageHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "agent guidance", path: "CLAUDE.md"},
		{name: "contributor guide", path: "CONTRIBUTING.md"},
	}

	wants := []string{
		"helpers defined only in `//go:build !race` test files are unavailable to race-enabled CI test builds",
		"run `go test -race` on the touched package",
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("read %s: %v", tt.path, err)
			}

			text := string(data)
			for _, want := range wants {
				if !strings.Contains(text, want) {
					t.Fatalf("%s is missing guidance %q", tt.path, want)
				}
			}
		})
	}
}
