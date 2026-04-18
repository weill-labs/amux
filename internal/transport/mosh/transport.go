package mosh

import (
	"context"
	"errors"
	"net"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/transport"
)

var errNotImplemented = errors.New("mosh transport not yet implemented")

func init() {
	transport.Register("mosh", newMosh)
}

type moshTransport struct{}

func newMosh(config.Host) (transport.Transport, error) {
	return &moshTransport{}, nil
}

func (t *moshTransport) Name() string {
	return "mosh"
}

func (t *moshTransport) Dial(context.Context, transport.Target) (net.Conn, error) {
	return nil, errNotImplemented
}

func (t *moshTransport) Deploy(context.Context, transport.Target, string) error {
	return errNotImplemented
}

func (t *moshTransport) EnsureServer(context.Context, transport.Target, string) error {
	return errNotImplemented
}

func (t *moshTransport) Close() error {
	return nil
}
