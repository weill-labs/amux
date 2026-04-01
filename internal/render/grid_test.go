package render

import (
	"testing"
)

func TestMaterializeGrid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		width  int
		height int
		want   string
	}{
		{
			name:   "plain text at origin",
			input:  "hello",
			width:  10,
			height: 1,
			want:   "hello",
		},
		{
			name:   "cursor positioning",
			input:  "\033[1;1HA\033[1;5HB",
			width:  10,
			height: 1,
			want:   "A   B",
		},
		{
			name:   "multirow positioning",
			input:  "\033[1;1Htop\033[3;1Hbottom",
			width:  10,
			height: 3,
			want:   "top\n\nbottom",
		},
		{
			name:   "SGR sequences stripped",
			input:  "\033[1;1H\033[38;2;255;0;0mred\033[0m plain",
			width:  15,
			height: 1,
			want:   "red plain",
		},
		{
			name:   "cursor home",
			input:  "\033[Hhome",
			width:  10,
			height: 1,
			want:   "home",
		},
		{
			name:   "clear then write",
			input:  "\033[2J\033[1;1Hcleared",
			width:  10,
			height: 1,
			want:   "cleared",
		},
		{
			name:   "unicode box drawing",
			input:  "\033[1;1H─│┤",
			width:  5,
			height: 1,
			want:   "─│┤",
		},
		{
			name:   "trailing spaces trimmed",
			input:  "\033[1;1Hhi",
			width:  20,
			height: 2,
			want:   "hi\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := MaterializeGrid(tt.input, tt.width, tt.height)
			if got != tt.want {
				t.Errorf("MaterializeGrid() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}
