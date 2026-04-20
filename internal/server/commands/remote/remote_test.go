package remote

import (
	"errors"
	"fmt"
	"testing"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type commandTestContext struct {
	disconnectErr error
	reconnectErr  error
	finalizeRes   commandpkg.Result
	disconnects   []string
	reconnects    []string
	finalized     []string
}

func (ctx *commandTestContext) HostStatuses() map[string]string { return nil }

func (ctx *commandTestContext) DisconnectHost(host string) error {
	ctx.disconnects = append(ctx.disconnects, host)
	return ctx.disconnectErr
}

func (ctx *commandTestContext) FinalizeDisconnect(host string) commandpkg.Result {
	ctx.finalized = append(ctx.finalized, host)
	if ctx.finalizeRes.Output == "" && ctx.finalizeRes.Err == nil && ctx.finalizeRes.Mutate == nil && ctx.finalizeRes.Stream == nil && ctx.finalizeRes.Message == nil {
		return commandpkg.Result{Output: fmt.Sprintf("Disconnected from %s\n", host)}
	}
	return ctx.finalizeRes
}

func (ctx *commandTestContext) ReconnectHost(host string) error {
	ctx.reconnects = append(ctx.reconnects, host)
	return ctx.reconnectErr
}

func (ctx *commandTestContext) ResolveReloadExecPath() (string, error) { return "", nil }

func (ctx *commandTestContext) ReloadServer(string) error { return nil }

func (ctx *commandTestContext) UnspliceHost(string) commandpkg.Result { return commandpkg.Result{} }

func (ctx *commandTestContext) InjectProxy(string) commandpkg.Result { return commandpkg.Result{} }

func TestDisconnectRequiresHost(t *testing.T) {
	t.Parallel()

	res := Disconnect(&commandTestContext{}, nil)
	if res.Err == nil || res.Err.Error() != "usage: disconnect <host>" {
		t.Fatalf("Disconnect() error = %v, want usage", res.Err)
	}
}

func TestDisconnectDelegatesToContextAndFinalizer(t *testing.T) {
	t.Parallel()

	ctx := &commandTestContext{
		finalizeRes: commandpkg.Result{Output: "done\n"},
	}

	res := Disconnect(ctx, []string{"gpu"})
	if res.Output != "done\n" {
		t.Fatalf("Disconnect() output = %q, want %q", res.Output, "done\n")
	}
	if len(ctx.disconnects) != 1 || ctx.disconnects[0] != "gpu" {
		t.Fatalf("disconnects = %v, want [gpu]", ctx.disconnects)
	}
	if len(ctx.finalized) != 1 || ctx.finalized[0] != "gpu" {
		t.Fatalf("finalized = %v, want [gpu]", ctx.finalized)
	}
}

func TestDisconnectReturnsDisconnectError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	ctx := &commandTestContext{disconnectErr: wantErr}
	res := Disconnect(ctx, []string{"gpu"})
	if !errors.Is(res.Err, wantErr) {
		t.Fatalf("Disconnect() error = %v, want %v", res.Err, wantErr)
	}
	if len(ctx.finalized) != 0 {
		t.Fatalf("FinalizeDisconnect should not run on error, got %v", ctx.finalized)
	}
}

func TestReconnectRequiresHost(t *testing.T) {
	t.Parallel()

	res := Reconnect(&commandTestContext{}, nil)
	if res.Err == nil || res.Err.Error() != "usage: reconnect <host>" {
		t.Fatalf("Reconnect() error = %v, want usage", res.Err)
	}
}

func TestReconnectDelegatesToContext(t *testing.T) {
	t.Parallel()

	ctx := &commandTestContext{}
	res := Reconnect(ctx, []string{"gpu"})
	if res.Output != "Reconnected to gpu\n" {
		t.Fatalf("Reconnect() output = %q, want %q", res.Output, "Reconnected to gpu\n")
	}
	if len(ctx.reconnects) != 1 || ctx.reconnects[0] != "gpu" {
		t.Fatalf("reconnects = %v, want [gpu]", ctx.reconnects)
	}
}

func TestReconnectReturnsReconnectError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	ctx := &commandTestContext{reconnectErr: wantErr}
	res := Reconnect(ctx, []string{"gpu"})
	if !errors.Is(res.Err, wantErr) {
		t.Fatalf("Reconnect() error = %v, want %v", res.Err, wantErr)
	}
}
