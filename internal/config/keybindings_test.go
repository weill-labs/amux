package config

import "testing"

func TestDefaultKeybindings(t *testing.T) {
	t.Parallel()

	kb := DefaultKeybindings()
	if kb.Prefix != 0x01 {
		t.Errorf("default prefix = %d, want 0x01 (Ctrl-a)", kb.Prefix)
	}
	if b, ok := kb.Bindings['\\']; !ok || b.Action != "split-focus" {
		t.Error("default: \\ should be bound to split-focus")
	}
	if b, ok := kb.Bindings['d']; !ok || b.Action != "detach" {
		t.Error("default: d should be bound to detach")
	}
	if b, ok := kb.Bindings['o']; !ok || b.Action != "focus" {
		t.Error("default: o should be bound to focus")
	}
	if b, ok := kb.Bindings['q']; !ok || b.Action != "display-panes" {
		t.Error("default: q should be bound to display-panes")
	}
	if b, ok := kb.Bindings['a']; !ok || b.Action != "add-pane" {
		t.Error("default: a should be bound to add-pane")
	}
	if b, ok := kb.Bindings['s']; !ok || b.Action != "choose-tree" {
		t.Error("default: s should be bound to choose-tree")
	}
	if b, ok := kb.Bindings['w']; !ok || b.Action != "choose-window" {
		t.Error("default: w should be bound to choose-window")
	}
	if _, ok := kb.Bindings['M']; ok {
		t.Error("default: M should be unbound")
	}
	if b, ok := kb.Bindings['m']; !ok || b.Action != "compat-bell" {
		t.Error("default: m should be reserved with compat-bell")
	}
}
