package wrap

import "testing"

func TestParserAltScreen(t *testing.T) {
	tests := []struct {
		name    string
		feeds   []string
		wantAlt bool
	}{
		{
			name:    "enter alt screen",
			feeds:   []string{"hello\x1b[?1049hworld"},
			wantAlt: true,
		},
		{
			name:    "enter then exit",
			feeds:   []string{"\x1b[?1049h", "stuff\x1b[?1049l"},
			wantAlt: false,
		},
		{
			name:    "split enter sequence across feeds",
			feeds:   []string{"text\x1b[?10", "49h more text"},
			wantAlt: true,
		},
		{
			name:    "split exit sequence across feeds",
			feeds:   []string{"\x1b[?1049h", "data\x1b[?1049", "l"},
			wantAlt: false,
		},
		{
			name:    "multiple transitions",
			feeds:   []string{"\x1b[?1049h vim starts \x1b[?1049l back to shell \x1b[?1049h vim again"},
			wantAlt: true,
		},
		{
			name:    "no sequences",
			feeds:   []string{"just regular output with no escape sequences"},
			wantAlt: false,
		},
		{
			name:    "empty feed",
			feeds:   []string{""},
			wantAlt: false,
		},
		{
			name:    "enter and exit in same chunk",
			feeds:   []string{"\x1b[?1049h\x1b[?1049l"},
			wantAlt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Parser{}
			for _, data := range tt.feeds {
				p.Feed([]byte(data))
			}
			if got := p.InAltScreen(); got != tt.wantAlt {
				t.Errorf("InAltScreen() = %v, want %v", got, tt.wantAlt)
			}
		})
	}
}

func TestParserDataPassthrough(t *testing.T) {
	p := &Parser{}
	input := []byte("hello\x1b[?1049hworld")
	output := p.Feed(input)
	if string(output) != string(input) {
		t.Errorf("Feed should return data unmodified, got %q", output)
	}
}
