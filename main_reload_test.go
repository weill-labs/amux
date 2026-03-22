package main

import (
	"testing"

	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/server"
)

func TestPrependReloadExecPathArgIncludesResolvedExecutable(t *testing.T) {
	t.Parallel()

	wantPath, err := reload.ResolveExecutable()
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}

	got := prependReloadExecPathArg([]string{"reload-server"})
	if len(got) != 3 {
		t.Fatalf("len(prependReloadExecPathArg) = %d, want 3", len(got))
	}
	if got[0] != server.ReloadServerExecPathFlag {
		t.Fatalf("flag = %q, want %q", got[0], server.ReloadServerExecPathFlag)
	}
	if got[1] != wantPath {
		t.Fatalf("exec path = %q, want %q", got[1], wantPath)
	}
	if got[2] != "reload-server" {
		t.Fatalf("trailing args = %v, want [reload-server]", got[2:])
	}
}
