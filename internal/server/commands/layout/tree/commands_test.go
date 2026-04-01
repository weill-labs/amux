package tree

import (
	"testing"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type fakeTreeContext struct {
	swapForwardActorPaneID  uint32
	swapForwardCalled       bool
	swapForwardResult       commandpkg.Result
	swapBackwardActorPaneID uint32
	swapBackwardCalled      bool
	swapBackwardResult      commandpkg.Result
	swapActorPaneID         uint32
	swapFirst               string
	swapSecond              string
	swapCalled              bool
	swapResult              commandpkg.Result
	swapTreeActorPaneID     uint32
	swapTreeFirst           string
	swapTreeSecond          string
	swapTreeCalled          bool
	swapTreeResult          commandpkg.Result
	moveActorPaneID         uint32
	movePaneRef             string
	moveTargetRef           string
	moveBefore              bool
	moveCalled              bool
	moveResult              commandpkg.Result
	moveToActorPaneID       uint32
	moveToPaneRef           string
	moveToTargetRef         string
	moveToCalled            bool
	moveToResult            commandpkg.Result
	moveSiblingActorPaneID  uint32
	moveSiblingPaneRef      string
	moveSiblingDirection    string
	moveSiblingCalled       bool
	moveSiblingResult       commandpkg.Result
	rotateForward           bool
	rotateCalled            bool
	rotateResult            commandpkg.Result
}

func (f *fakeTreeContext) SwapForward(actorPaneID uint32) commandpkg.Result {
	f.swapForwardActorPaneID = actorPaneID
	f.swapForwardCalled = true
	return f.swapForwardResult
}

func (f *fakeTreeContext) SwapBackward(actorPaneID uint32) commandpkg.Result {
	f.swapBackwardActorPaneID = actorPaneID
	f.swapBackwardCalled = true
	return f.swapBackwardResult
}

func (f *fakeTreeContext) Swap(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	f.swapActorPaneID = actorPaneID
	f.swapFirst = paneRef
	f.swapSecond = targetRef
	f.swapCalled = true
	return f.swapResult
}

func (f *fakeTreeContext) SwapTree(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	f.swapTreeActorPaneID = actorPaneID
	f.swapTreeFirst = paneRef
	f.swapTreeSecond = targetRef
	f.swapTreeCalled = true
	return f.swapTreeResult
}

func (f *fakeTreeContext) Move(actorPaneID uint32, paneRef, targetRef string, before bool) commandpkg.Result {
	f.moveActorPaneID = actorPaneID
	f.movePaneRef = paneRef
	f.moveTargetRef = targetRef
	f.moveBefore = before
	f.moveCalled = true
	return f.moveResult
}

func (f *fakeTreeContext) MoveTo(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	f.moveToActorPaneID = actorPaneID
	f.moveToPaneRef = paneRef
	f.moveToTargetRef = targetRef
	f.moveToCalled = true
	return f.moveToResult
}

func (f *fakeTreeContext) MoveSibling(actorPaneID uint32, paneRef, direction string) commandpkg.Result {
	f.moveSiblingActorPaneID = actorPaneID
	f.moveSiblingPaneRef = paneRef
	f.moveSiblingDirection = direction
	f.moveSiblingCalled = true
	return f.moveSiblingResult
}

func (f *fakeTreeContext) Rotate(forward bool) commandpkg.Result {
	f.rotateForward = forward
	f.rotateCalled = true
	return f.rotateResult
}

func TestSwapDelegatesSupportedModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, ctx *fakeTreeContext)
	}{
		{
			name: "forward",
			args: []string{"forward"},
			check: func(t *testing.T, ctx *fakeTreeContext) {
				t.Helper()
				if !ctx.swapForwardCalled || ctx.swapForwardActorPaneID != 5 {
					t.Fatalf("swap forward call = (%t, %d), want (%t, %d)", ctx.swapForwardCalled, ctx.swapForwardActorPaneID, true, 5)
				}
			},
		},
		{
			name: "backward",
			args: []string{"backward"},
			check: func(t *testing.T, ctx *fakeTreeContext) {
				t.Helper()
				if !ctx.swapBackwardCalled || ctx.swapBackwardActorPaneID != 5 {
					t.Fatalf("swap backward call = (%t, %d), want (%t, %d)", ctx.swapBackwardCalled, ctx.swapBackwardActorPaneID, true, 5)
				}
			},
		},
		{
			name: "explicit panes",
			args: []string{"pane-1", "pane-2"},
			check: func(t *testing.T, ctx *fakeTreeContext) {
				t.Helper()
				if !ctx.swapCalled {
					t.Fatal("Swap() did not call explicit swap")
				}
				if ctx.swapActorPaneID != 5 || ctx.swapFirst != "pane-1" || ctx.swapSecond != "pane-2" {
					t.Fatalf("swap args = (%d, %q, %q), want (%d, %q, %q)", ctx.swapActorPaneID, ctx.swapFirst, ctx.swapSecond, 5, "pane-1", "pane-2")
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := &fakeTreeContext{
				swapForwardResult:  commandpkg.Result{Output: "swapped\n"},
				swapBackwardResult: commandpkg.Result{Output: "swapped\n"},
				swapResult:         commandpkg.Result{Output: "swapped\n"},
			}

			got := Swap(ctx, 5, tt.args)

			tt.check(t, ctx)
			if got.Output != "swapped\n" {
				t.Fatalf("result output = %q, want %q", got.Output, "swapped\n")
			}
		})
	}
}

func TestSwapTreeParsesArgsAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeTreeContext{
		swapTreeResult: commandpkg.Result{Output: "tree\n"},
	}

	got := SwapTree(ctx, 4, []string{"pane-1", "pane-2"})

	if !ctx.swapTreeCalled {
		t.Fatal("SwapTree() did not call context")
	}
	if ctx.swapTreeActorPaneID != 4 || ctx.swapTreeFirst != "pane-1" || ctx.swapTreeSecond != "pane-2" {
		t.Fatalf("swap-tree args = (%d, %q, %q), want (%d, %q, %q)", ctx.swapTreeActorPaneID, ctx.swapTreeFirst, ctx.swapTreeSecond, 4, "pane-1", "pane-2")
	}
	if got.Output != "tree\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "tree\n")
	}
}

func TestMoveParsesArgsAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeTreeContext{
		moveResult: commandpkg.Result{Output: "moved\n"},
	}

	got := Move(ctx, 6, []string{"pane-1", "--before", "pane-2"})

	if !ctx.moveCalled {
		t.Fatal("Move() did not call context")
	}
	if ctx.moveActorPaneID != 6 || ctx.movePaneRef != "pane-1" || ctx.moveTargetRef != "pane-2" || !ctx.moveBefore {
		t.Fatalf("move args = (%d, %q, %q, %t), want (%d, %q, %q, %t)", ctx.moveActorPaneID, ctx.movePaneRef, ctx.moveTargetRef, ctx.moveBefore, 6, "pane-1", "pane-2", true)
	}
	if got.Output != "moved\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "moved\n")
	}
}

func TestMoveSiblingDelegatesDirection(t *testing.T) {
	t.Parallel()

	ctx := &fakeTreeContext{
		moveSiblingResult: commandpkg.Result{Output: "sibling\n"},
	}

	got := MoveUp(ctx, 8, []string{"pane-3"})

	if !ctx.moveSiblingCalled {
		t.Fatal("MoveUp() did not call context")
	}
	if ctx.moveSiblingActorPaneID != 8 || ctx.moveSiblingPaneRef != "pane-3" || ctx.moveSiblingDirection != "up" {
		t.Fatalf("move sibling args = (%d, %q, %q), want (%d, %q, %q)", ctx.moveSiblingActorPaneID, ctx.moveSiblingPaneRef, ctx.moveSiblingDirection, 8, "pane-3", "up")
	}
	if got.Output != "sibling\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "sibling\n")
	}
}

func TestRotateParsesReverseFlag(t *testing.T) {
	t.Parallel()

	ctx := &fakeTreeContext{
		rotateResult: commandpkg.Result{Output: "rotated\n"},
	}

	got := Rotate(ctx, []string{"--reverse"})

	if !ctx.rotateCalled {
		t.Fatal("Rotate() did not call context")
	}
	if ctx.rotateForward {
		t.Fatal("rotate forward = true, want false")
	}
	if got.Output != "rotated\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "rotated\n")
	}
}
