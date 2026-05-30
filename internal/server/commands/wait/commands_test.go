package wait

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

type stubWaitContext struct{}

func (stubWaitContext) Generation() uint64 { return 0 }

func (stubWaitContext) LayoutJSON() (string, error) { return "", nil }

func (stubWaitContext) WaitLayout(uint64, bool, time.Duration) (uint64, bool) { return 0, false }

func (stubWaitContext) ClipboardGeneration() uint64 { return 0 }

func (stubWaitContext) WaitClipboard(uint64, bool, time.Duration) (string, bool) { return "", false }

func (stubWaitContext) WaitCheckpoint(uint64, bool, time.Duration) (CheckpointRecord, bool) {
	return CheckpointRecord{}, false
}

func (stubWaitContext) UIGeneration(string) (uint64, error) { return 0, nil }

func (stubWaitContext) WaitContent(uint32, string, string, time.Duration) error { return nil }

func (stubWaitContext) WaitExited(uint32, string, time.Duration) error { return nil }

func (stubWaitContext) WaitBusy(uint32, string, time.Duration) error { return nil }

func (stubWaitContext) WaitUI(string, string, uint64, bool, time.Duration) error { return nil }

func (stubWaitContext) WaitReady(uint32, []string) error { return nil }

func (stubWaitContext) WaitIdle(uint32, []string) error { return nil }

func (stubWaitContext) WaitMessage(uint32, MessageWaitOptions) (proto.MailboxMessageSummary, error) {
	return proto.MailboxMessageSummary{}, nil
}

type messageWaitContext struct {
	stubWaitContext
	summary proto.MailboxMessageSummary
	err     error
}

func (ctx messageWaitContext) WaitMessage(uint32, MessageWaitOptions) (proto.MailboxMessageSummary, error) {
	return ctx.summary, ctx.err
}

func TestCursorUsage(t *testing.T) {
	t.Parallel()

	got := Cursor(stubWaitContext{}, nil)
	if got.Err == nil || got.Err.Error() != cursorCommandUsage {
		t.Fatalf("Cursor(nil) error = %v, want %q", got.Err, cursorCommandUsage)
	}
}

func TestWaitMessageResultFormatting(t *testing.T) {
	t.Parallel()

	ctx := messageWaitContext{summary: proto.MailboxMessageSummary{
		ID:      "msg-000001",
		Subject: "Review ready",
	}}

	text := Wait(ctx, 0, []string{"msg", "pane-2"})
	if text.Err != nil || text.Output != "msg-000001\n" {
		t.Fatalf("Wait msg text = (%q, %v), want message ID output", text.Output, text.Err)
	}

	jsonResult := Wait(ctx, 0, []string{"msg", "pane-2", "--format", "json"})
	if jsonResult.Err != nil {
		t.Fatalf("Wait msg json error = %v", jsonResult.Err)
	}
	if !strings.Contains(jsonResult.Output, `"id":"msg-000001"`) || !strings.Contains(jsonResult.Output, `"subject":"Review ready"`) {
		t.Fatalf("Wait msg json output = %q, want summary", jsonResult.Output)
	}
}

func TestWaitMessageErrors(t *testing.T) {
	t.Parallel()

	usage := Wait(stubWaitContext{}, 0, []string{"msg"})
	if usage.Err == nil || !strings.Contains(usage.Err.Error(), "usage: wait msg <pane>") {
		t.Fatalf("Wait msg usage error = %v, want usage", usage.Err)
	}

	boom := errors.New("boom")
	failed := Wait(messageWaitContext{err: boom}, 0, []string{"msg", "pane-2"})
	if !errors.Is(failed.Err, boom) {
		t.Fatalf("Wait msg context error = %v, want boom", failed.Err)
	}
}

func TestWaitUsage(t *testing.T) {
	t.Parallel()

	got := Wait(stubWaitContext{}, 0, nil)
	if got.Err == nil || got.Err.Error() != waitCommandUsage {
		t.Fatalf("Wait(nil) error = %v, want %q", got.Err, waitCommandUsage)
	}
}
