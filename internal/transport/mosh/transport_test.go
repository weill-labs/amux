package mosh

import (
	"context"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/transport"
)

func TestRegisteredMoshTransportReturnsStubErrors(t *testing.T) {
	t.Parallel()

	tr, err := transport.Get("mosh", config.Host{})
	if err != nil {
		t.Fatalf("Get(%q) error = %v", "mosh", err)
	}
	if got := tr.Name(); got != "mosh" {
		t.Fatalf("Name() = %q, want mosh", got)
	}

	want := "mosh transport not yet implemented"
	if _, err := tr.Dial(context.Background(), transport.Target{}); err == nil || err.Error() != want {
		t.Fatalf("Dial() error = %v, want %q", err, want)
	}
	if err := tr.Deploy(context.Background(), transport.Target{}, "hash"); err == nil || err.Error() != want {
		t.Fatalf("Deploy() error = %v, want %q", err, want)
	}
	if err := tr.EnsureServer(context.Background(), transport.Target{}, "main"); err == nil || err.Error() != want {
		t.Fatalf("EnsureServer() error = %v, want %q", err, want)
	}
}
