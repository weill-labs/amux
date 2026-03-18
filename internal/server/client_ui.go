package server

import "github.com/weill-labs/amux/internal/proto"

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

func (cc *ClientConn) currentUIEvents() []Event {
	evType := proto.UIEventDisplayPanesHidden
	if cc.displayPanesShown {
		evType = proto.UIEventDisplayPanesShown
	}
	return []Event{{
		Type:     evType,
		ClientID: cc.ID,
	}}
}
