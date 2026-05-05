package test

import (
	"os"
	"strings"
	"testing"
)

func TestSystemdUnitAndOperationsPlaybook(t *testing.T) {
	t.Parallel()

	service := readRepoFile(t, "packaging/systemd/amux@.service")
	for _, want := range []string{
		"[Service]",
		"Environment=PATH=%h/.local/bin:",
		"ExecStart=/usr/bin/env amux _server %i",
		"OOMScoreAdjust=-500",
		"MemoryHigh=2G",
		"MemoryMax=4G",
		"Restart=on-failure",
		"RestartSec=2s",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(service, want) {
			t.Fatalf("packaging/systemd/amux@.service missing %q", want)
		}
	}

	operations := readRepoFile(t, "docs/operations.md")
	for _, want := range []string{
		"systemctl --user enable --now amux@main.service",
		"grep -E 'Out of memory|oom-reaper|oom-invocation' /var/log/kern.log",
		`checkpoint_kind:"crash"`,
		"CAP_SYS_RESOURCE",
		"internal/reload/reload.go",
		"LAB-1594",
		"/bin/bash",
	} {
		if !strings.Contains(operations, want) {
			t.Fatalf("docs/operations.md missing %q", want)
		}
	}
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	data, err := os.ReadFile(repoPath(t, rel))
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", rel, err)
	}
	return string(data)
}
