package transport

import (
	"context"
	"net"

	"github.com/weill-labs/amux/internal/config"
)

// Target identifies a remote amux server plus any transport-agnostic args.
type Target struct {
	Host    string
	User    string
	Port    string
	Session string
	Extra   map[string]string
}

// Transport establishes a connection to a remote amux server.
// Each host gets one Transport; concurrent Dial calls are serialized by the
// caller, not the implementation.
type Transport interface {
	Name() string
	Dial(context.Context, Target) (net.Conn, error)
	Deploy(context.Context, Target, string) error
	EnsureServer(context.Context, Target, string) error
	Close() error
}

type Factory func(cfg config.Host) (Transport, error)
