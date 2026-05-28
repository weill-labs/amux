// Package remote contains client-side helpers for amux-to-amux federation.
package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

// ResolvePaneIDErrorKind identifies name-resolution failures that callers can
// handle without string matching.
type ResolvePaneIDErrorKind string

const (
	ResolvePaneIDNotFound  ResolvePaneIDErrorKind = "not_found"
	ResolvePaneIDAmbiguous ResolvePaneIDErrorKind = "ambiguous"
)

// ResolvePaneIDMatch describes one leaf pane that matched a requested name.
type ResolvePaneIDMatch struct {
	ID   uint32
	Name string
}

// ResolvePaneIDError reports a typed failure to resolve a pane name.
type ResolvePaneIDError struct {
	Name    string
	Kind    ResolvePaneIDErrorKind
	Matches []ResolvePaneIDMatch
}

func (e *ResolvePaneIDError) Error() string {
	if e.Kind == ResolvePaneIDAmbiguous {
		parts := make([]string, 0, len(e.Matches))
		for _, match := range e.Matches {
			parts = append(parts, fmt.Sprintf("%s#%d", match.Name, match.ID))
		}
		return fmt.Sprintf("pane name %q is ambiguous (matches: %s)", e.Name, strings.Join(parts, ", "))
	}
	return fmt.Sprintf("pane name %q not found", e.Name)
}

// ResolvePaneID asks a remote amux server for its current leaf panes and
// resolves name to the current pane ID. Callers should provide a fresh
// connection because MsgTypeListPanes is a one-shot request.
func ResolvePaneID(ctx context.Context, conn net.Conn, session, name string) (uint32, error) {
	if conn == nil {
		return 0, fmt.Errorf("resolve pane name %q: nil connection", name)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	cleanupDeadline := bindConnDeadlineToContext(ctx, conn)
	defer cleanupDeadline()

	if err := proto.NewWriter(conn).WriteMsg(&proto.Message{
		Type:    proto.MsgTypeListPanes,
		Session: session,
	}); err != nil {
		if ctxErr := contextErrorAfterConnIO(ctx, err); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, fmt.Errorf("list panes: %w", err)
	}

	msg, err := proto.NewReader(conn).ReadMsg()
	if err != nil {
		if ctxErr := contextErrorAfterConnIO(ctx, err); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, fmt.Errorf("read list panes response: %w", err)
	}

	layout, err := listPanesLayout(msg)
	if err != nil {
		return 0, err
	}
	return ResolvePaneIDFromLayout(layout, name)
}

// ResolvePaneIDFromLayout resolves name against the leaf panes represented by
// a LayoutSnapshot. When Windows is populated, every window is searched and the
// legacy active-window fields are ignored to avoid double-counting panes.
func ResolvePaneIDFromLayout(layout *proto.LayoutSnapshot, name string) (uint32, error) {
	matches := matchingLeafPanes(layout, name)
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return 0, &ResolvePaneIDError{Name: name, Kind: ResolvePaneIDNotFound}
	default:
		return 0, &ResolvePaneIDError{Name: name, Kind: ResolvePaneIDAmbiguous, Matches: matches}
	}
}

func bindConnDeadlineToContext(ctx context.Context, conn net.Conn) func() {
	touchedDeadline := false
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err == nil {
			touchedDeadline = true
		}
	}
	afterFuncDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(afterFuncDone)
		_ = conn.SetDeadline(time.Now())
	})
	return func() {
		if stop() {
			if touchedDeadline {
				_ = conn.SetDeadline(time.Time{})
			}
			return
		}
		<-afterFuncDone
		_ = conn.SetDeadline(time.Time{})
	}
}

func contextErrorAfterConnIO(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
	}
	return nil
}

func listPanesLayout(msg *proto.Message) (*proto.LayoutSnapshot, error) {
	if msg.Type == proto.MsgTypeCmdResult && msg.CmdErr != "" {
		return nil, fmt.Errorf("list panes: %s", msg.CmdErr)
	}
	if msg.Type != proto.MsgTypeLayout {
		return nil, fmt.Errorf("list panes: unexpected response type %d", msg.Type)
	}
	if msg.Layout == nil {
		return nil, fmt.Errorf("list panes: nil layout")
	}
	return msg.Layout, nil
}

func matchingLeafPanes(layout *proto.LayoutSnapshot, name string) []ResolvePaneIDMatch {
	if layout == nil {
		return nil
	}

	var matches []ResolvePaneIDMatch
	seenLeafID := make(map[uint32]bool)
	if len(layout.Windows) > 0 {
		for _, window := range layout.Windows {
			snapshots := paneSnapshotsByID(window.Panes)
			for _, paneID := range leafPaneIDs(window.Root, seenLeafID) {
				if pane, ok := snapshots[paneID]; ok && pane.Name == name {
					matches = append(matches, ResolvePaneIDMatch{ID: pane.ID, Name: pane.Name})
				}
			}
		}
		return matches
	}

	snapshots := paneSnapshotsByID(layout.Panes)
	for _, paneID := range leafPaneIDs(layout.Root, seenLeafID) {
		if pane, ok := snapshots[paneID]; ok && pane.Name == name {
			matches = append(matches, ResolvePaneIDMatch{ID: pane.ID, Name: pane.Name})
		}
	}
	return matches
}

func paneSnapshotsByID(panes []proto.PaneSnapshot) map[uint32]proto.PaneSnapshot {
	snapshots := make(map[uint32]proto.PaneSnapshot, len(panes))
	for _, pane := range panes {
		snapshots[pane.ID] = pane
	}
	return snapshots
}

func leafPaneIDs(root proto.CellSnapshot, seen map[uint32]bool) []uint32 {
	var ids []uint32
	walkLeafPaneIDs(root, seen, &ids)
	return ids
}

func walkLeafPaneIDs(cell proto.CellSnapshot, seen map[uint32]bool, ids *[]uint32) {
	if cell.IsLeaf {
		if cell.PaneID != 0 && !seen[cell.PaneID] {
			seen[cell.PaneID] = true
			*ids = append(*ids, cell.PaneID)
		}
		return
	}
	for _, child := range cell.Children {
		walkLeafPaneIDs(child, seen, ids)
	}
}
