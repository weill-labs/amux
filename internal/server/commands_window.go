package server

import (
	"fmt"

	"github.com/weill-labs/amux/internal/mux"
)

func cmdNewWindow(ctx *CommandContext) {
	var name string
	for i := 0; i < len(ctx.Args)-1; i += 2 {
		if ctx.Args[i] == "--name" {
			name = ctx.Args[i+1]
		}
	}
	activePid, _, _, err := ctx.activeWindowSnapshot()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	meta := mux.PaneMeta{Dir: mux.PaneCwd(activePid)}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}

		winID := sess.windowCounter.Add(1)
		newWin := mux.NewWindow(pane, w.Width, w.Height)
		newWin.ID = winID
		newWin.LeadPaneID = pane.ID
		if name != "" {
			newWin.Name = name
		} else {
			newWin.Name = fmt.Sprintf(WindowNameFormat, winID)
		}
		sess.Windows = append(sess.Windows, newWin)
		sess.activateWindow(newWin)

		return commandMutationResult{
			output:          fmt.Sprintf("Created %s\n", newWin.Name),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdSelectWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: select-window <index|name>")
		return
	}
	ref := ctx.Args[0]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.resolveWindow(ref)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("window %q not found", ref)}
		}
		sess.activateWindow(w)
		return commandMutationResult{
			output:          "Switched window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(w),
		}
	}))
}

func cmdNextWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.nextWindow()
		return commandMutationResult{
			output:          "Next window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.activeWindow()),
		}
	}))
}

func cmdPrevWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.prevWindow()
		return commandMutationResult{
			output:          "Previous window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.activeWindow()),
		}
	}))
}

func cmdLastWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if !sess.lastWindow() {
			return commandMutationResult{bell: true}
		}
		return commandMutationResult{
			output:          "Last window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.activeWindow()),
		}
	}))
}

func cmdRenameWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: rename-window <name>")
		return
	}
	name := ctx.Args[0]
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		w.Name = name
		return commandMutationResult{
			output:          fmt.Sprintf("Renamed window to %s\n", name),
			broadcastLayout: true,
		}
	}))
}
