package server

import "github.com/weill-labs/amux/internal/proto"

const (
	chooserHidden = ""
	chooserTree   = "tree"
	chooserWindow = "window"
)

func (cc *ClientConn) applyUIEvent(name string) (bool, error) {
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
	case proto.UIEventChooseTreeShown:
		if cc.chooserMode == chooserTree {
			return false, nil
		}
		cc.chooserMode = chooserTree
		return true, nil
	case proto.UIEventChooseTreeHidden:
		if cc.chooserMode != chooserTree {
			return false, nil
		}
		cc.chooserMode = chooserHidden
		return true, nil
	case proto.UIEventChooseWindowShown:
		if cc.chooserMode == chooserWindow {
			return false, nil
		}
		cc.chooserMode = chooserWindow
		return true, nil
	case proto.UIEventChooseWindowHidden:
		if cc.chooserMode != chooserWindow {
			return false, nil
		}
		cc.chooserMode = chooserHidden
		return true, nil
	default:
		return false, errUnknownUIEvent(name)
	}
}

func (cc *ClientConn) matchesUIEvent(name string) bool {
	switch name {
	case proto.UIEventDisplayPanesShown:
		return cc.displayPanesShown
	case proto.UIEventDisplayPanesHidden:
		return !cc.displayPanesShown
	case proto.UIEventChooseTreeShown:
		return cc.chooserMode == chooserTree
	case proto.UIEventChooseTreeHidden:
		return cc.chooserMode != chooserTree
	case proto.UIEventChooseWindowShown:
		return cc.chooserMode == chooserWindow
	case proto.UIEventChooseWindowHidden:
		return cc.chooserMode != chooserWindow
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

	switch cc.chooserMode {
	case chooserTree:
		events = append(events,
			Event{Type: proto.UIEventChooseTreeShown, ClientID: cc.ID},
			Event{Type: proto.UIEventChooseWindowHidden, ClientID: cc.ID},
		)
	case chooserWindow:
		events = append(events,
			Event{Type: proto.UIEventChooseTreeHidden, ClientID: cc.ID},
			Event{Type: proto.UIEventChooseWindowShown, ClientID: cc.ID},
		)
	default:
		events = append(events,
			Event{Type: proto.UIEventChooseTreeHidden, ClientID: cc.ID},
			Event{Type: proto.UIEventChooseWindowHidden, ClientID: cc.ID},
		)
	}
	return events
}
