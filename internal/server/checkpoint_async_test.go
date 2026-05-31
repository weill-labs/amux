package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestCheckpointPaneSnapshotsDoNotBlockSessionLoop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func(*Session, func(*mux.Pane) ([]string, string, uint64)) error
	}{
		{
			name: "reload checkpoint",
			build: func(sess *Session, snapshot func(*mux.Pane) ([]string, string, uint64)) error {
				_, err := sess.buildReloadCheckpointWithSnapshot(snapshot)
				return err
			},
		},
		{
			name: "crash checkpoint",
			build: func(sess *Session, snapshot func(*mux.Pane) ([]string, string, uint64)) error {
				if cp := sess.buildCrashCheckpointWithSnapshot(snapshot); cp == nil {
					return fmt.Errorf("buildCrashCheckpointWithSnapshot returned nil")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sess := newSession("checkpoint-responsive-" + tt.name)
			stopCrashCheckpointLoop(t, sess)
			t.Cleanup(func() {
				sess.shutdown.Store(true)
				stopSessionBackgroundLoops(t, sess)
				for _, pane := range sess.Panes {
					_ = pane.Close()
					_ = pane.WaitClosed()
				}
			})

			pane := newTestPane(sess, 1, "pane-1")
			window := newTestWindowWithPanes(t, sess, 1, "main", pane)
			setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

			snapshotStarted := make(chan struct{})
			releaseSnapshot := make(chan struct{})
			snapshot := func(*mux.Pane) ([]string, string, uint64) {
				close(snapshotStarted)
				<-releaseSnapshot
				return []string{"history"}, "screen", 1
			}

			buildDone := make(chan error, 1)
			go func() {
				buildDone <- tt.build(sess, snapshot)
			}()

			select {
			case <-snapshotStarted:
			case err := <-buildDone:
				t.Fatalf("checkpoint build finished before pane snapshot started: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for pane snapshot to start")
			}

			queryDone := make(chan error, 1)
			go func() {
				_, err := enqueueSessionQueryOnState(sess.context(), sess, func(*Session) (struct{}, error) {
					return struct{}{}, nil
				})
				queryDone <- err
			}()

			select {
			case err := <-queryDone:
				if err != nil {
					t.Fatalf("session query while checkpoint snapshot blocked: %v", err)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("session event loop stayed blocked by pane history snapshot")
			}

			close(releaseSnapshot)
			select {
			case err := <-buildDone:
				if err != nil {
					t.Fatalf("checkpoint build: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for checkpoint build to finish")
			}
		})
	}
}
