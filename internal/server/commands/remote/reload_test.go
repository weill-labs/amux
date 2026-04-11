package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

func TestRequestedReloadExecPathNormalizesProvidedPath(t *testing.T) {
	t.Parallel()

	execPath := filepath.Join(t.TempDir(), "amux")
	if err := os.WriteFile(execPath, []byte("placeholder"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", execPath, err)
	}

	got, err := RequestedReloadExecPath([]string{ReloadServerExecPathFlag, execPath})
	if err != nil {
		t.Fatalf("RequestedReloadExecPath() error = %v", err)
	}
	if got != execPath {
		t.Fatalf("RequestedReloadExecPath() = %q, want %q", got, execPath)
	}
}

func TestRequestedReloadExecPathRejectsMissingValue(t *testing.T) {
	t.Parallel()

	if _, err := RequestedReloadExecPath([]string{ReloadServerExecPathFlag}); err == nil {
		t.Fatal("RequestedReloadExecPath() should fail without a value")
	}
}

func TestRequestedReloadExecPathRejectsMissingBinary(t *testing.T) {
	t.Parallel()

	missingPath := filepath.Join(t.TempDir(), "missing-amux")
	if _, err := RequestedReloadExecPath([]string{ReloadServerExecPathFlag, missingPath}); err == nil {
		t.Fatalf("RequestedReloadExecPath(%q) should fail", missingPath)
	}
}

func TestReloadServerFlushesReplyBeforeReload(t *testing.T) {
	t.Parallel()

	sender := &reloadTestSender{}
	ctx := reloadTestContext{
		execPath: "/tmp/amux",
		onReload: func(execPath string) error {
			if execPath != "/tmp/amux" {
				return fmt.Errorf("execPath = %q, want /tmp/amux", execPath)
			}
			if !sender.flushed {
				return fmt.Errorf("reload started before reply flush")
			}
			return nil
		},
	}

	res := ReloadServer(ctx, nil)
	if res.Stream == nil {
		t.Fatal("ReloadServer() should stream the reload notice")
	}
	if err := res.Stream(sender); err != nil {
		t.Fatalf("res.Stream() error = %v", err)
	}
	if len(sender.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.msgs))
	}
	if got := sender.msgs[0].CmdOutput; got != "Server reloading...\n" {
		t.Fatalf("reload notice = %q, want %q", got, "Server reloading...\n")
	}
}

type reloadTestContext struct {
	execPath string
	onReload func(string) error
}

func (ctx reloadTestContext) HostStatuses() map[string]string { return nil }

func (ctx reloadTestContext) DisconnectHost(string) error { return nil }

func (ctx reloadTestContext) ReconnectHost(string) error { return nil }

func (ctx reloadTestContext) ResolveReloadExecPath() (string, error) { return ctx.execPath, nil }

func (ctx reloadTestContext) ReloadServer(execPath string) error { return ctx.onReload(execPath) }

func (ctx reloadTestContext) UnspliceHost(string) commandpkg.Result { return commandpkg.Result{} }

func (ctx reloadTestContext) InjectProxy(string) commandpkg.Result { return commandpkg.Result{} }

type reloadTestSender struct {
	msgs    []*proto.Message
	flushed bool
}

func (s *reloadTestSender) Send(msg *proto.Message) error {
	s.msgs = append(s.msgs, msg)
	return nil
}

func (s *reloadTestSender) Flush() error {
	s.flushed = true
	return nil
}
