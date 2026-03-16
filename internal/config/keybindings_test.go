package config

import (
	"testing"
)

func TestParseKey(t *testing.T) {
	tests := []struct {
		input string
		want  byte
		err   bool
	}{
		{"d", 'd', false},
		{"\\", '\\', false},
		{"-", '-', false},
		{"|", '|', false},
		{"_", '_', false},
		{"C-a", 0x01, false},
		{"C-b", 0x02, false},
		{"C-z", 0x1a, false},
		{"C-A", 0x01, false}, // uppercase
		{"", 0, true},        // empty
		{"C-1", 0, true},     // non-letter ctrl
		{"ab", 0, true},      // multi-char non-ctrl
	}

	for _, tt := range tests {
		got, err := ParseKey(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseKey(%q): expected error, got %d", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseKey(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseKey(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseAction(t *testing.T) {
	tests := []struct {
		input  string
		action string
		args   []string
		err    bool
	}{
		{"split", "split", nil, false},
		{"split v", "split", []string{"v"}, false},
		{"split root v", "split", []string{"root", "v"}, false},
		{"focus next", "focus", []string{"next"}, false},
		{"detach", "detach", nil, false},
		{"", "", nil, true},
	}

	for _, tt := range tests {
		got, err := ParseAction(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("ParseAction(%q): expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAction(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got.Action != tt.action {
			t.Errorf("ParseAction(%q).Action = %q, want %q", tt.input, got.Action, tt.action)
		}
		if len(got.Args) != len(tt.args) {
			t.Errorf("ParseAction(%q).Args = %v, want %v", tt.input, got.Args, tt.args)
		} else {
			for i := range tt.args {
				if got.Args[i] != tt.args[i] {
					t.Errorf("ParseAction(%q).Args[%d] = %q, want %q", tt.input, i, got.Args[i], tt.args[i])
				}
			}
		}
	}
}

func TestDefaultKeybindings(t *testing.T) {
	kb := DefaultKeybindings()
	if kb.Prefix != 0x01 {
		t.Errorf("default prefix = %d, want 0x01 (Ctrl-a)", kb.Prefix)
	}
	if b, ok := kb.Bindings['\\']; !ok || b.Action != "split" {
		t.Error("default: \\ should be bound to split")
	}
	if b, ok := kb.Bindings['d']; !ok || b.Action != "detach" {
		t.Error("default: d should be bound to detach")
	}
	if b, ok := kb.Bindings['o']; !ok || b.Action != "focus" {
		t.Error("default: o should be bound to focus")
	}
}

func TestBuildKeybindingsNil(t *testing.T) {
	kb, err := BuildKeybindings(nil)
	if err != nil {
		t.Fatalf("BuildKeybindings(nil): %v", err)
	}
	if kb.Prefix != 0x01 {
		t.Errorf("nil config: prefix = %d, want 0x01", kb.Prefix)
	}
	if len(kb.Bindings) == 0 {
		t.Error("nil config: should have default bindings")
	}
}

func TestBuildKeybindingsCustomPrefix(t *testing.T) {
	kc := &KeyConfig{Prefix: "C-b"}
	kb, err := BuildKeybindings(kc)
	if err != nil {
		t.Fatalf("BuildKeybindings: %v", err)
	}
	if kb.Prefix != 0x02 {
		t.Errorf("prefix = %d, want 0x02 (Ctrl-b)", kb.Prefix)
	}
	if _, ok := kb.Bindings['\\']; !ok {
		t.Error("default bindings should be preserved with custom prefix")
	}
}

func TestBuildKeybindingsAddBinding(t *testing.T) {
	kc := &KeyConfig{
		Bind: map[string]string{
			"s": "split",
		},
	}
	kb, err := BuildKeybindings(kc)
	if err != nil {
		t.Fatalf("BuildKeybindings: %v", err)
	}
	b, ok := kb.Bindings['s']
	if !ok {
		t.Fatal("'s' should be bound")
	}
	if b.Action != "split" {
		t.Errorf("s action = %q, want split", b.Action)
	}
	if _, ok := kb.Bindings['\\']; !ok {
		t.Error("default \\ binding should still exist")
	}
}

func TestBuildKeybindingsOverrideBinding(t *testing.T) {
	kc := &KeyConfig{
		Bind: map[string]string{
			"o": "split v",
		},
	}
	kb, err := BuildKeybindings(kc)
	if err != nil {
		t.Fatalf("BuildKeybindings: %v", err)
	}
	b := kb.Bindings['o']
	if b.Action != "split" {
		t.Errorf("o action = %q, want split", b.Action)
	}
	if len(b.Args) != 1 || b.Args[0] != "v" {
		t.Errorf("o args = %v, want [v]", b.Args)
	}
}

func TestBuildKeybindingsUnbind(t *testing.T) {
	kc := &KeyConfig{
		Unbind: []string{"o", "h"},
	}
	kb, err := BuildKeybindings(kc)
	if err != nil {
		t.Fatalf("BuildKeybindings: %v", err)
	}
	if _, ok := kb.Bindings['o']; ok {
		t.Error("'o' should be unbound")
	}
	if _, ok := kb.Bindings['h']; ok {
		t.Error("'h' should be unbound")
	}
	if _, ok := kb.Bindings['\\']; !ok {
		t.Error("'\\' should still be bound")
	}
}

func TestBuildKeybindingsInvalidPrefix(t *testing.T) {
	kc := &KeyConfig{Prefix: "C-1"}
	_, err := BuildKeybindings(kc)
	if err == nil {
		t.Error("expected error for invalid prefix C-1")
	}
}

func TestBuildKeybindingsUnknownAction(t *testing.T) {
	kc := &KeyConfig{
		Bind: map[string]string{
			"x": "splti", // typo
		},
	}
	_, err := BuildKeybindings(kc)
	if err == nil {
		t.Error("expected error for unknown action 'splti'")
	}
}

func TestBuildKeybindingsPrefixConflict(t *testing.T) {
	kc := &KeyConfig{
		Prefix: "C-b",
		Bind: map[string]string{
			"C-b": "split", // conflicts with prefix
		},
	}
	_, err := BuildKeybindings(kc)
	if err == nil {
		t.Error("expected error when binding conflicts with prefix key")
	}
}
