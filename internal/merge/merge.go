package merge

import (
	"fmt"

	"github.com/weill-labs/amux/internal/tmux"
)

// Merge moves all panes from srcWindow into dstWindow within the current session.
// The source window is automatically destroyed by tmux when its last pane is moved.
// Windows are referenced by index (e.g., "0", "1", "2").
func Merge(t tmux.Tmux, srcWindow, dstWindow string) (int, error) {
	session := t.CurrentSession()
	if session == "" {
		return 0, fmt.Errorf("not inside a tmux session")
	}

	srcPanes, err := t.SessionWindowPanes(session + ":" + srcWindow)
	if err != nil || len(srcPanes) == 0 {
		return 0, fmt.Errorf("no panes found in window %s", srcWindow)
	}

	dstPanes, err := t.SessionWindowPanes(session + ":" + dstWindow)
	if err != nil || len(dstPanes) == 0 {
		return 0, fmt.Errorf("no panes found in window %s", dstWindow)
	}

	dstTarget := dstPanes[0]
	for _, paneID := range srcPanes {
		if err := t.JoinPane(paneID, dstTarget); err != nil {
			return 0, fmt.Errorf("joining pane %s into window %s: %w", paneID, dstWindow, err)
		}
	}

	return len(srcPanes), nil
}
