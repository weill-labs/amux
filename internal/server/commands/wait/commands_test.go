package wait

import (
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

func TestCursorUsage(t *testing.T) {
	t.Parallel()

	got := Cursor(stubWaitContext{}, nil)
	if got.Err == nil || got.Err.Error() != cursorCommandUsage {
		t.Fatalf("Cursor(nil) error = %v, want %q", got.Err, cursorCommandUsage)
	}
}

func TestWaitUsage(t *testing.T) {
	t.Parallel()

	got := Wait(stubWaitContext{}, 0, nil)
	if got.Err == nil || got.Err.Error() != waitCommandUsage {
		t.Fatalf("Wait(nil) error = %v, want %q", got.Err, waitCommandUsage)
	}
}
