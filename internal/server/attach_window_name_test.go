package server

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestAttachWindowUsesExactName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		first   string
		second  string
		ref     string
		wantErr string
	}{
		{name: "numeric name", first: "2", second: "other", ref: "2"},
		{name: "ordinary name", first: "logs", second: "other", ref: "logs"},
		{name: "missing name", first: "logs", second: "other", ref: "missing", wantErr: "not found"},
		{name: "numeric name missing", first: "logs", second: "other", ref: "2", wantErr: "not found"},
		{name: "prefix is not a name", first: "logs", second: "other", ref: "log", wantErr: "not found"},
		{name: "duplicate name", first: "logs", second: "logs", ref: "logs", wantErr: "ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()
			p1 := newTestPane(sess, 1, "pane-1")
			p2 := newTestPane(sess, 2, "pane-2")
			first := newTestWindowWithPanes(t, sess, 10, tt.first, p1)
			second := newTestWindowWithPanes(t, sess, 20, tt.second, p2)
			setSessionLayoutForTest(t, sess, first.ID, []*mux.Window{first, second}, p1, p2)
			cc := newClientConn(discardConn{})
			defer cc.Close()
			result := sess.enqueueAttachWindowClient(cc, tt.ref, 0, 0)
			if tt.wantErr != "" {
				if result.err == nil || !strings.Contains(result.err.Error(), tt.wantErr) {
					t.Fatalf("attach error = %v, want %q", result.err, tt.wantErr)
				}
				return
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.windowID != first.ID {
				t.Fatalf("attached window %d, want %d", result.windowID, first.ID)
			}
		})
	}
}
