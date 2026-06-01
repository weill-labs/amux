package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestInputRouterSyncPanesReplacesQueueWhenPaneInstanceChanges(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	var oldWrites [][]byte
	oldPane := mux.NewProxyPaneWithScrollback(7, mux.PaneMeta{
		Name:  "worker",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		oldWrites = append(oldWrites, append([]byte(nil), data...))
		return len(data), nil
	})

	var newWrites [][]byte
	newPane := mux.NewProxyPaneWithScrollback(7, mux.PaneMeta{
		Name:  "worker",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		newWrites = append(newWrites, append([]byte(nil), data...))
		return len(data), nil
	})

	router := newInputRouter()
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.Panes = []*mux.Pane{oldPane}
	})
	router.syncPanes([]*mux.Pane{oldPane})
	oldQueue := router.paneQueue(sess, oldPane)
	if err := oldQueue.queue.enqueue([]encodedKeyChunk{{data: []byte("old")}}); err != nil {
		t.Fatalf("enqueue old pane input: %v", err)
	}

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.Panes = []*mux.Pane{newPane}
	})
	router.syncPanes([]*mux.Pane{newPane})
	newQueue := router.paneQueue(sess, newPane)
	if newQueue == oldQueue {
		t.Fatal("syncPanes should replace the pane queue when the pane pointer changes")
	}
	if err := newQueue.queue.enqueue([]encodedKeyChunk{{data: []byte("new")}}); err != nil {
		t.Fatalf("enqueue new pane input: %v", err)
	}

	if len(oldWrites) != 1 || string(oldWrites[0]) != "old" {
		t.Fatalf("old pane writes = %q, want only old input", oldWrites)
	}
	if len(newWrites) != 1 || string(newWrites[0]) != "new" {
		t.Fatalf("new pane writes = %q, want only new input", newWrites)
	}
}

func TestEnqueueLivePaneInputChunksSkipsEmptyChunks(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	writes := make(chan string, 1)
	pane := sess.ownPane(newProxyPane(7, mux.PaneMeta{
		Name:  "worker",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		writes <- string(data)
		return len(data), nil
	}))

	if err := sess.enqueueLivePaneInputChunks(pane, nil); err != nil {
		t.Fatalf("enqueue nil chunks: %v", err)
	}
	if err := sess.enqueueLivePaneInputChunks(pane, []encodedKeyChunk{{}, {data: nil}}); err != nil {
		t.Fatalf("enqueue empty chunks: %v", err)
	}
	select {
	case got := <-writes:
		t.Fatalf("unexpected write for empty chunks: %q", got)
	case <-time.After(50 * time.Millisecond):
	}

	if err := sess.enqueueLivePaneInputChunks(pane, []encodedKeyChunk{{}, {data: []byte("payload")}}); err != nil {
		t.Fatalf("enqueue mixed chunks: %v", err)
	}
	select {
	case got := <-writes:
		if got != "payload" {
			t.Fatalf("write = %q, want payload", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for non-empty chunk write")
	}
}
