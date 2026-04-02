package server

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

const (
	mouseCommandUsage          = "usage: mouse [--client <id>] [--timeout <duration>] (press <x> <y> | motion <x> <y> | release <x> <y> | click <x> <y> | click <pane> [--status-line] | drag <pane> --to <pane>)"
	defaultMouseCommandTimeout = 5 * time.Second
	mouseEventGap              = 50 * time.Millisecond
)

type mouseCommandKind int

const (
	mouseCommandPress mouseCommandKind = iota
	mouseCommandMotion
	mouseCommandRelease
	mouseCommandClickCoords
	mouseCommandClickPane
	mouseCommandDragPane
)

type mouseCommandOptions struct {
	requestedClientID string
	waitTimeout       time.Duration
	kind              mouseCommandKind
	x                 int
	y                 int
	paneRef           string
	targetPaneRef     string
	statusLine        bool
}

type mouseClientTarget struct {
	uiWait       uiClientSnapshot
	layout       *mux.LayoutCell
	paneID       uint32
	targetPaneID uint32
}

func parseMouseCommandArgs(args []string) (mouseCommandOptions, error) {
	opts := mouseCommandOptions{waitTimeout: defaultMouseCommandTimeout}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--client":
			if i+1 >= len(args) {
				return mouseCommandOptions{}, fmt.Errorf("missing value for --client")
			}
			opts.requestedClientID = args[i+1]
			i += 2
		case "--timeout":
			if i+1 >= len(args) {
				return mouseCommandOptions{}, fmt.Errorf("missing value for --timeout")
			}
			timeout, err := time.ParseDuration(args[i+1])
			if err != nil {
				return mouseCommandOptions{}, fmt.Errorf("invalid timeout: %s", args[i+1])
			}
			opts.waitTimeout = timeout
			i += 2
		default:
			goto parseAction
		}
	}

parseAction:
	if i >= len(args) {
		return mouseCommandOptions{}, fmt.Errorf(mouseCommandUsage)
	}

	action := args[i]
	rest := args[i+1:]
	switch action {
	case "press":
		x, y, err := parseMouseCoordinates(rest)
		if err != nil {
			return mouseCommandOptions{}, err
		}
		opts.kind = mouseCommandPress
		opts.x = x
		opts.y = y
	case "motion":
		x, y, err := parseMouseCoordinates(rest)
		if err != nil {
			return mouseCommandOptions{}, err
		}
		opts.kind = mouseCommandMotion
		opts.x = x
		opts.y = y
	case "release":
		x, y, err := parseMouseCoordinates(rest)
		if err != nil {
			return mouseCommandOptions{}, err
		}
		opts.kind = mouseCommandRelease
		opts.x = x
		opts.y = y
	case "click":
		switch {
		case len(rest) == 2 && looksLikeMouseCoordinatePair(rest):
			x, y, err := parseMouseCoordinates(rest)
			if err != nil {
				return mouseCommandOptions{}, err
			}
			opts.kind = mouseCommandClickCoords
			opts.x = x
			opts.y = y
		case len(rest) >= 1 && rest[0] != "" && rest[0][0] != '-':
			opts.kind = mouseCommandClickPane
			opts.paneRef = rest[0]
			for _, arg := range rest[1:] {
				if arg != "--status-line" {
					return mouseCommandOptions{}, fmt.Errorf(mouseCommandUsage)
				}
				opts.statusLine = true
			}
		default:
			return mouseCommandOptions{}, fmt.Errorf(mouseCommandUsage)
		}
	case "drag":
		if len(rest) != 3 || rest[1] != "--to" {
			return mouseCommandOptions{}, fmt.Errorf(mouseCommandUsage)
		}
		opts.kind = mouseCommandDragPane
		opts.paneRef = rest[0]
		opts.targetPaneRef = rest[2]
	default:
		return mouseCommandOptions{}, fmt.Errorf(mouseCommandUsage)
	}

	return opts, nil
}

func looksLikeMouseCoordinatePair(args []string) bool {
	if len(args) != 2 {
		return false
	}
	_, errX := strconv.Atoi(args[0])
	_, errY := strconv.Atoi(args[1])
	return errX == nil && errY == nil
}

func parseMouseCoordinates(args []string) (int, int, error) {
	if len(args) != 2 {
		return 0, 0, fmt.Errorf(mouseCommandUsage)
	}
	x, err := parsePositiveCoordinate(args[0], "x")
	if err != nil {
		return 0, 0, err
	}
	y, err := parsePositiveCoordinate(args[1], "y")
	if err != nil {
		return 0, 0, err
	}
	return x, y, nil
}

func parsePositiveCoordinate(raw, axis string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("mouse: invalid %s coordinate %q", axis, raw)
	}
	if value < 1 {
		return 0, fmt.Errorf("mouse: %s coordinate must be >= 1", axis)
	}
	return value, nil
}

func buildMouseVisibleLayout(w *mux.Window, cols, rows int) (*mux.LayoutCell, error) {
	layoutHeight := rows - render.GlobalBarHeight
	if cols < 1 || layoutHeight < 1 {
		return nil, fmt.Errorf("client size %dx%d cannot render mouse targets", cols, rows)
	}
	if w.ZoomedPaneID != 0 {
		return mux.NewLeafByID(w.ZoomedPaneID, 0, 0, cols, layoutHeight), nil
	}
	layout := mux.CloneLayout(w.Root)
	if layout == nil {
		return nil, fmt.Errorf("window has no layout root")
	}
	if w.Width != cols || w.Height != layoutHeight {
		layout.ResizeAll(cols, layoutHeight)
	}
	return layout, nil
}

func queryMouseClientTarget(sess *Session, actorPaneID uint32, requestedClientID string, paneRef, targetPaneRef string) (mouseClientTarget, error) {
	return enqueueSessionQuery(sess, func(sess *Session) (mouseClientTarget, error) {
		uiWait, err := sess.resolveUIClientSnapshot(requestedClientID, "")
		if err != nil {
			return mouseClientTarget{}, err
		}
		if uiWait.client == nil {
			return mouseClientTarget{}, fmt.Errorf("no client attached")
		}

		activeWindow := sess.activeWindow()
		if activeWindow == nil {
			return mouseClientTarget{}, fmt.Errorf("no window")
		}

		layout, err := buildMouseVisibleLayout(activeWindow, uiWait.client.cols, uiWait.client.rows)
		if err != nil {
			return mouseClientTarget{}, err
		}

		target := mouseClientTarget{
			uiWait: uiWait,
			layout: layout,
		}

		if paneRef != "" {
			pane, window, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, paneRef)
			if err != nil {
				return mouseClientTarget{}, err
			}
			if window == nil || window.ID != activeWindow.ID {
				return mouseClientTarget{}, fmt.Errorf("pane %q is not in the active window", paneRef)
			}
			target.paneID = pane.ID
		}

		if targetPaneRef != "" {
			pane, window, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, targetPaneRef)
			if err != nil {
				return mouseClientTarget{}, err
			}
			if window == nil || window.ID != activeWindow.ID {
				return mouseClientTarget{}, fmt.Errorf("pane %q is not in the active window", targetPaneRef)
			}
			target.targetPaneID = pane.ID
		}

		return target, nil
	})
}

func paneMouseCenter(layout *mux.LayoutCell, paneID uint32, statusLine bool) (int, int, error) {
	cell := layout.FindByPaneID(paneID)
	if cell == nil {
		return 0, 0, fmt.Errorf("pane %d is not visible in the current client layout", paneID)
	}
	if statusLine {
		return cell.X + cell.W/2 + 1, cell.Y + 1, nil
	}
	return cell.X + cell.W/2 + 1, cell.Y + cell.H/2 + 1, nil
}

func mouseChunk(button ansi.MouseButton, x, y int, motion, release bool, delay time.Duration) (encodedKeyChunk, error) {
	if x < 1 || y < 1 {
		return encodedKeyChunk{}, fmt.Errorf("mouse coordinates must be >= 1")
	}
	code := ansi.EncodeMouseButton(button, motion, false, false, false)
	if code == 0xff {
		return encodedKeyChunk{}, fmt.Errorf("unsupported mouse button")
	}
	return encodedKeyChunk{
		data:        []byte(ansi.MouseSgr(code, x-1, y-1, release)),
		delayBefore: delay,
	}, nil
}

func mousePressChunk(x, y int) (encodedKeyChunk, error) {
	return mouseChunk(ansi.MouseLeft, x, y, false, false, 0)
}

func mouseMotionChunk(x, y int, delay time.Duration) (encodedKeyChunk, error) {
	return mouseChunk(ansi.MouseLeft, x, y, true, false, delay)
}

func mouseReleaseChunk(x, y int, delay time.Duration) (encodedKeyChunk, error) {
	return mouseChunk(ansi.MouseLeft, x, y, false, true, delay)
}

func mouseChunksForAction(layout *mux.LayoutCell, opts mouseCommandOptions, paneID, targetPaneID uint32) ([]encodedKeyChunk, error) {
	switch opts.kind {
	case mouseCommandPress:
		chunk, err := mousePressChunk(opts.x, opts.y)
		if err != nil {
			return nil, err
		}
		return []encodedKeyChunk{chunk}, nil
	case mouseCommandMotion:
		chunk, err := mouseMotionChunk(opts.x, opts.y, 0)
		if err != nil {
			return nil, err
		}
		return []encodedKeyChunk{chunk}, nil
	case mouseCommandRelease:
		chunk, err := mouseReleaseChunk(opts.x, opts.y, 0)
		if err != nil {
			return nil, err
		}
		return []encodedKeyChunk{chunk}, nil
	case mouseCommandClickCoords:
		press, err := mousePressChunk(opts.x, opts.y)
		if err != nil {
			return nil, err
		}
		release, err := mouseReleaseChunk(opts.x, opts.y, mouseEventGap)
		if err != nil {
			return nil, err
		}
		return []encodedKeyChunk{press, release}, nil
	case mouseCommandClickPane:
		var (
			x   int
			y   int
			err error
		)
		x, y, err = paneMouseCenter(layout, paneID, opts.statusLine)
		if err != nil {
			return nil, err
		}
		press, err := mousePressChunk(x, y)
		if err != nil {
			return nil, err
		}
		release, err := mouseReleaseChunk(x, y, mouseEventGap)
		if err != nil {
			return nil, err
		}
		return []encodedKeyChunk{press, release}, nil
	case mouseCommandDragPane:
		startX, startY, err := paneMouseCenter(layout, paneID, true)
		if err != nil {
			return nil, err
		}
		endX, endY, err := paneMouseCenter(layout, targetPaneID, false)
		if err != nil {
			return nil, err
		}
		press, err := mousePressChunk(startX, startY)
		if err != nil {
			return nil, err
		}
		motion, err := mouseMotionChunk(endX, endY, mouseEventGap)
		if err != nil {
			return nil, err
		}
		release, err := mouseReleaseChunk(endX, endY, mouseEventGap)
		if err != nil {
			return nil, err
		}
		return []encodedKeyChunk{press, motion, release}, nil
	default:
		return nil, fmt.Errorf(mouseCommandUsage)
	}
}

func enqueueClientTypeKeys(sess *Session, uiWait uiClientSnapshot, chunks []encodedKeyChunk, waitTimeout time.Duration) error {
	if err := uiWait.client.enqueueTypeKeys(chunks); err != nil {
		return err
	}
	return waitForNextUIEvent(sess, uiWait, "input-idle", waitTimeout)
}

func (ctx inputCommandContext) Mouse(actorPaneID uint32, args []string) (string, int, error) {
	opts, err := parseMouseCommandArgs(args)
	if err != nil {
		return "", 0, err
	}

	var paneRef, targetRef string
	switch opts.kind {
	case mouseCommandClickPane:
		paneRef = opts.paneRef
	case mouseCommandDragPane:
		paneRef = opts.paneRef
		targetRef = opts.targetPaneRef
	}

	target, err := queryMouseClientTarget(ctx.Sess, actorPaneID, opts.requestedClientID, paneRef, targetRef)
	if err != nil {
		return "", 0, err
	}

	chunks, err := mouseChunksForAction(target.layout, opts, target.paneID, target.targetPaneID)
	if err != nil {
		return "", 0, err
	}
	if err := enqueueClientTypeKeys(ctx.Sess, target.uiWait, chunks, opts.waitTimeout); err != nil {
		return "", 0, err
	}

	return target.uiWait.clientID, len(chunks), nil
}

func cmdMouse(ctx *CommandContext) {
	clientID, eventCount, err := inputCommandContext{ctx}.Mouse(ctx.ActorPaneID, ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Sent %d mouse events via client %s\n", eventCount, clientID))
}
