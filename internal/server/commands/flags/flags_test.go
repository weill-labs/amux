package flags

import (
	"testing"
	"time"
)

func TestParseCommandFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		args            []string
		specs           []flagSpec
		wantPositionals []string
		wantName        string
		wantTimeout     time.Duration
		wantCleanup     bool
		wantAfter       int
		wantSeen        map[string]bool
		wantErr         string
	}{
		{
			name: "parses defaults flags and positionals",
			args: []string{"pane-1", "--cleanup", "--timeout", "25ms", "--name", "worker", "tail"},
			specs: []flagSpec{
				{Name: "--name", Type: flagTypeString},
				{Name: "--timeout", Type: flagTypeDuration, Default: 5 * time.Second},
				{Name: "--cleanup", Type: flagTypeBool},
				{Name: "--after", Type: flagTypeInt, Default: 3},
			},
			wantPositionals: []string{"pane-1", "tail"},
			wantName:        "worker",
			wantTimeout:     25 * time.Millisecond,
			wantCleanup:     true,
			wantAfter:       3,
			wantSeen: map[string]bool{
				"--name":    true,
				"--timeout": true,
				"--cleanup": true,
				"--after":   false,
			},
		},
		{
			name: "missing string value",
			args: []string{"--name"},
			specs: []flagSpec{
				{Name: "--name", Type: flagTypeString},
			},
			wantErr: "missing value for --name",
		},
		{
			name: "invalid duration value",
			args: []string{"--timeout", "later"},
			specs: []flagSpec{
				{Name: "--timeout", Type: flagTypeDuration},
			},
			wantErr: "invalid value for --timeout: later",
		},
		{
			name: "invalid int value",
			args: []string{"--after", "later"},
			specs: []flagSpec{
				{Name: "--after", Type: flagTypeInt},
			},
			wantErr: "invalid value for --after: later",
		},
		{
			name: "unknown flag",
			args: []string{"--bogus"},
			specs: []flagSpec{
				{Name: "--timeout", Type: flagTypeDuration},
			},
			wantErr: "unknown flag: --bogus",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCommandFlags(tt.args, tt.specs)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseCommandFlags(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCommandFlags(%v): %v", tt.args, err)
			}
			if got.String("--name") != tt.wantName {
				t.Fatalf("String(--name) = %q, want %q", got.String("--name"), tt.wantName)
			}
			if got.Duration("--timeout") != tt.wantTimeout {
				t.Fatalf("Duration(--timeout) = %v, want %v", got.Duration("--timeout"), tt.wantTimeout)
			}
			if got.Bool("--cleanup") != tt.wantCleanup {
				t.Fatalf("Bool(--cleanup) = %t, want %t", got.Bool("--cleanup"), tt.wantCleanup)
			}
			if got.Int("--after") != tt.wantAfter {
				t.Fatalf("Int(--after) = %d, want %d", got.Int("--after"), tt.wantAfter)
			}
			if len(got.Positionals()) != len(tt.wantPositionals) {
				t.Fatalf("len(Positionals()) = %d, want %d", len(got.Positionals()), len(tt.wantPositionals))
			}
			for i := range tt.wantPositionals {
				if got.Positionals()[i] != tt.wantPositionals[i] {
					t.Fatalf("Positionals()[%d] = %q, want %q", i, got.Positionals()[i], tt.wantPositionals[i])
				}
			}
			for name, want := range tt.wantSeen {
				if got.Seen(name) != want {
					t.Fatalf("Seen(%q) = %t, want %t", name, got.Seen(name), want)
				}
			}
		})
	}
}
