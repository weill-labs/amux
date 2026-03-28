package config

import "testing"

func TestDefaultKeybindingsEqualize(t *testing.T) {
	t.Parallel()

	kb := DefaultKeybindings()
	got, ok := kb.Bindings['=']
	if !ok {
		t.Fatal("default: '=' should be bound")
	}
	if got.Action != "equalize" {
		t.Fatalf("default: '=' action = %q, want %q", got.Action, "equalize")
	}
	if len(got.Args) != 0 {
		t.Fatalf("default: '=' args = %v, want none", got.Args)
	}
}
