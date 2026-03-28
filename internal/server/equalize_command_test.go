package server

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestParseEqualizeCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantWidths  bool
		wantHeights bool
		wantErr     string
	}{
		{
			name:        "default",
			args:        nil,
			wantWidths:  true,
			wantHeights: false,
		},
		{
			name:        "vertical",
			args:        []string{"--vertical"},
			wantWidths:  false,
			wantHeights: true,
		},
		{
			name:        "all",
			args:        []string{"--all"},
			wantWidths:  true,
			wantHeights: true,
		},
		{
			name:        "duplicate mode",
			args:        []string{"--all", "--all"},
			wantWidths:  true,
			wantHeights: true,
		},
		{
			name:    "unknown mode",
			args:    []string{"--bogus"},
			wantErr: `equalize: unknown mode "--bogus" (use --vertical or --all)`,
		},
		{
			name:    "conflicting modes",
			args:    []string{"--vertical", "--all"},
			wantErr: "equalize: conflicting equalize modes",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotWidths, gotHeights, err := parseEqualizeCommandArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseEqualizeCommandArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEqualizeCommandArgs(%v): %v", tt.args, err)
			}
			if gotWidths != tt.wantWidths || gotHeights != tt.wantHeights {
				t.Fatalf("parseEqualizeCommandArgs(%v) = (%t, %t), want (%t, %t)", tt.args, gotWidths, gotHeights, tt.wantWidths, tt.wantHeights)
			}
		})
	}
}

func TestCommandEqualize(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w1 := mux.NewWindow(p1, 80, 23)
	w1.ID = 1
	w1.Name = "main"
	if _, err := w1.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w1.SplitRoot(mux.SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	if !w1.ResizePane(p1.ID, "right", 6) {
		t.Fatal("ResizePane should skew root column widths")
	}
	setSessionLayoutForTest(t, sess, w1.ID, []*mux.Window{w1}, p1, p2, p3)

	res := runTestCommand(t, srv, sess, "equalize")
	if res.cmdErr != "" {
		t.Fatalf("equalize error: %s", res.cmdErr)
	}
	if got := res.output; got != "Equalized layout\n" {
		t.Fatalf("equalize output = %q, want %q", got, "Equalized layout\n")
	}

	gotWidths := []int{w1.Root.Children[0].W, w1.Root.Children[1].W, w1.Root.Children[2].W}
	wantWidths := []int{26, 26, 26}
	if !reflect.DeepEqual(gotWidths, wantWidths) {
		t.Fatalf("equalized root widths = %v, want %v", gotWidths, wantWidths)
	}
}

func TestCommandEqualizeAlreadyBalancedKeepsZoom(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w1 := mux.NewWindow(p1, 80, 23)
	w1.ID = 1
	w1.Name = "main"
	if _, err := w1.SplitRoot(mux.SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w1.SplitRoot(mux.SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	if err := w1.Zoom(p2.ID); err != nil {
		t.Fatalf("Zoom pane-2: %v", err)
	}
	setSessionLayoutForTest(t, sess, w1.ID, []*mux.Window{w1}, p1, p2, p3)

	res := runTestCommand(t, srv, sess, "equalize")
	if res.cmdErr != "" {
		t.Fatalf("equalize error: %s", res.cmdErr)
	}
	if got := res.output; got != "Already equalized\n" {
		t.Fatalf("equalize output = %q, want %q", got, "Already equalized\n")
	}
	if got := w1.ZoomedPaneID; got != p2.ID {
		t.Fatalf("ZoomedPaneID after no-op equalize = %d, want %d", got, p2.ID)
	}
}
