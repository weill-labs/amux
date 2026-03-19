package server

import "github.com/weill-labs/amux/internal/proto"

const (
	chooserHidden = ""
	chooserTree   = "tree"
	chooserWindow = "window"
)

func (cc *ClientConn) applyUIEvent(name string) (bool, error) {
	mode, shown, ok := chooserEventState(name)
	if ok {
		if shown {
			if cc.chooserMode == mode {
				return false, nil
			}
			cc.chooserMode = mode
			return true, nil
		}
		if cc.chooserMode != mode {
			return false, nil
		}
		cc.chooserMode = chooserHidden
		return true, nil
	}

	switch name {
	case proto.UIEventDisplayPanesShown:
		if cc.displayPanesShown {
			return false, nil
		}
		cc.displayPanesShown = true
		return true, nil
	case proto.UIEventDisplayPanesHidden:
		if !cc.displayPanesShown {
			return false, nil
		}
		cc.displayPanesShown = false
		return true, nil
	default:
		return false, errUnknownUIEvent(name)
	}
}

func (cc *ClientConn) matchesUIEvent(name string) bool {
	mode, shown, ok := chooserEventState(name)
	if ok {
		if shown {
			return cc.chooserMode == mode
		}
		return cc.chooserMode != mode
	}

	switch name {
	case proto.UIEventDisplayPanesShown:
		return cc.displayPanesShown
	case proto.UIEventDisplayPanesHidden:
		return !cc.displayPanesShown
	default:
		return false
	}
}

func (cc *ClientConn) displayPanesState() string {
	if cc.displayPanesShown {
		return "shown"
	}
	return "hidden"
}

func (cc *ClientConn) chooserState() string {
	if cc.chooserMode == chooserHidden {
		return "hidden"
	}
	return cc.chooserMode
}

func (cc *ClientConn) currentUIEvents() []Event {
	events := []Event{{
		Type:     proto.UIEventDisplayPanesHidden,
		ClientID: cc.ID,
	}}
	if cc.displayPanesShown {
		events[0].Type = proto.UIEventDisplayPanesShown
	}

	events = append(events,
		Event{Type: chooserSnapshotEvent(chooserTree, cc.chooserMode), ClientID: cc.ID},
		Event{Type: chooserSnapshotEvent(chooserWindow, cc.chooserMode), ClientID: cc.ID},
	)
	return events
}

func chooserEventState(name string) (mode string, shown bool, ok bool) {
	switch name {
	case proto.UIEventChooseTreeShown:
		return chooserTree, true, true
	case proto.UIEventChooseTreeHidden:
		return chooserTree, false, true
	case proto.UIEventChooseWindowShown:
		return chooserWindow, true, true
	case proto.UIEventChooseWindowHidden:
		return chooserWindow, false, true
	default:
		return "", false, false
	}
}

func chooserSnapshotEvent(mode, current string) string {
	if current == mode {
		switch mode {
		case chooserTree:
			return proto.UIEventChooseTreeShown
		case chooserWindow:
			return proto.UIEventChooseWindowShown
		}
	}
	switch mode {
	case chooserTree:
		return proto.UIEventChooseTreeHidden
	case chooserWindow:
		return proto.UIEventChooseWindowHidden
	default:
		return ""
	}
}
