package test

import "testing"

func TestPrepareInnerAmuxEnvDefaultsToNoWatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "adds no-watch when absent",
			in:   []string{"AMUX_EXIT_UNATTACHED=0"},
			want: []string{"AMUX_EXIT_UNATTACHED=0", "AMUX_NO_WATCH=1"},
		},
		{
			name: "preserves explicit watch override",
			in:   []string{"AMUX_EXIT_UNATTACHED=0", "AMUX_NO_WATCH=0"},
			want: []string{"AMUX_EXIT_UNATTACHED=0", "AMUX_NO_WATCH=0"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := prepareInnerAmuxEnv(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("prepareInnerAmuxEnv() len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("prepareInnerAmuxEnv()[%d] = %q, want %q (full=%v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestNeedsNestedHarnessStartupLock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		binPath string
		envVars []string
		want    bool
	}{
		{
			name:    "shared binary with explicit watch needs lock",
			binPath: amuxBin,
			envVars: []string{"AMUX_NO_WATCH=0"},
			want:    true,
		},
		{
			name:    "shared binary defaults to no watch",
			binPath: amuxBin,
			envVars: nil,
			want:    false,
		},
		{
			name:    "private watched binary does not need shared lock",
			binPath: "/tmp/private-amux",
			envVars: []string{"AMUX_NO_WATCH=0"},
			want:    false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := needsNestedHarnessStartupLock(tt.binPath, prepareInnerAmuxEnv(tt.envVars))
			if got != tt.want {
				t.Fatalf("needsNestedHarnessStartupLock(%q, %v) = %t, want %t", tt.binPath, tt.envVars, got, tt.want)
			}
		})
	}
}

func TestBuildInnerAmuxLaunchCommand(t *testing.T) {
	t.Parallel()

	got := buildInnerAmuxLaunchCommand("/tmp/amux", "t-1234", "/tmp/work tree", []string{"AMUX_NO_WATCH=1", "AMUX_REPEAT_TIMEOUT=30s"})
	want := `cd "/tmp/work tree" && AMUX_NO_WATCH=1 AMUX_REPEAT_TIMEOUT=30s "/tmp/amux" -s t-1234`
	if got != want {
		t.Fatalf("buildInnerAmuxLaunchCommand() = %q, want %q", got, want)
	}
}
