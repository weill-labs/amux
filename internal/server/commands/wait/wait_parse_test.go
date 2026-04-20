package wait

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestParseWaitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantAfter    uint64
		wantAfterSet bool
		wantTimeout  time.Duration
		wantErr      string
	}{
		{
			name:         "defaults",
			wantTimeout:  3 * time.Second,
			wantAfterSet: false,
		},
		{
			name:         "after and timeout",
			args:         []string{"--after", "7", "--timeout", "25ms"},
			wantAfter:    7,
			wantAfterSet: true,
			wantTimeout:  25 * time.Millisecond,
		},
		{
			name:    "missing after value",
			args:    []string{"--after"},
			wantErr: "missing value for --after",
		},
		{
			name:    "invalid after value",
			args:    []string{"--after", "bogus"},
			wantErr: "invalid value for --after: bogus",
		},
		{
			name:    "missing timeout value",
			args:    []string{"--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout value",
			args:    []string{"--timeout", "later"},
			wantErr: "invalid value for --timeout: later",
		},
		{
			name:    "unknown flag",
			args:    []string{"--wat"},
			wantErr: "unknown flag: --wat",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			afterGen, afterSet, timeout, err := ParseWaitArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseWaitArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWaitArgs(%v): %v", tt.args, err)
			}
			if afterGen != tt.wantAfter || afterSet != tt.wantAfterSet || timeout != tt.wantTimeout {
				t.Fatalf("parsed = (%d, %t, %v), want (%d, %t, %v)", afterGen, afterSet, timeout, tt.wantAfter, tt.wantAfterSet, tt.wantTimeout)
			}
		})
	}
}

func TestParseWaitArgsWithDefault(t *testing.T) {
	t.Parallel()

	afterGen, afterSet, timeout, err := ParseWaitArgsWithDefault(nil, 15*time.Second)
	if err != nil {
		t.Fatalf("ParseWaitArgsWithDefault(nil): %v", err)
	}
	if afterGen != 0 || afterSet || timeout != 15*time.Second {
		t.Fatalf("parsed = (%d, %t, %v), want (0, false, 15s)", afterGen, afterSet, timeout)
	}
}

func TestParseTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		startIdx    int
		defaultTime time.Duration
		want        time.Duration
		wantErr     string
	}{
		{
			name:        "default timeout",
			args:        []string{"pane-1"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			want:        5 * time.Second,
		},
		{
			name:        "explicit timeout",
			args:        []string{"pane-1", "--timeout", "25ms"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			want:        25 * time.Millisecond,
		},
		{
			name:        "invalid timeout",
			args:        []string{"pane-1", "--timeout", "later"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			wantErr:     "invalid value for --timeout: later",
		},
		{
			name:        "missing timeout value",
			args:        []string{"pane-1", "--timeout"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			wantErr:     "missing value for --timeout",
		},
		{
			name:        "unknown flag",
			args:        []string{"pane-1", "--bogus"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			wantErr:     "unknown flag: --bogus",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTimeout(tt.args, tt.startIdx, tt.defaultTime)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseTimeout(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTimeout(%v): %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseUIGenArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantClient string
		wantErr    string
	}{
		{name: "no args"},
		{name: "client", args: []string{"--client", "client-2"}, wantClient: "client-2"},
		{name: "missing client value", args: []string{"--client"}, wantErr: "missing value for --client"},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: "unknown flag: --wat"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseUIGenArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseUIGenArgs(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUIGenArgs(%v): %v", tt.args, err)
			}
			if got != tt.wantClient {
				t.Fatalf("client = %q, want %q", got, tt.wantClient)
			}
		})
	}
}

func TestParseWaitUIArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		wantEvent string
		wantID    string
		wantAfter uint64
		wantSet   bool
		wantDur   time.Duration
		wantErr   string
	}{
		{name: "missing event", wantErr: "usage: wait ui"},
		{name: "missing client value", args: []string{"input-idle", "--client"}, wantErr: "missing value for --client"},
		{name: "missing after value", args: []string{"input-idle", "--after"}, wantErr: "missing value for --after"},
		{name: "invalid after value", args: []string{"input-idle", "--after", "abc"}, wantErr: "invalid value for --after: abc"},
		{name: "missing timeout value", args: []string{"input-idle", "--timeout"}, wantErr: "missing value for --timeout"},
		{name: "invalid timeout", args: []string{"input-idle", "--timeout", "later"}, wantErr: "invalid value for --timeout: later"},
		{name: "unknown flag", args: []string{"input-idle", "--wat"}, wantErr: "unknown flag: --wat"},
		{name: "all flags", args: []string{"input-idle", "--client", "client-3", "--after", "9", "--timeout", "250ms"}, wantEvent: "input-idle", wantID: "client-3", wantAfter: 9, wantSet: true, wantDur: 250 * time.Millisecond},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event, clientID, afterGen, afterSet, timeout, err := ParseWaitUIArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseWaitUIArgs(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWaitUIArgs(%v): %v", tt.args, err)
			}
			if event != tt.wantEvent || clientID != tt.wantID || afterGen != tt.wantAfter || afterSet != tt.wantSet || timeout != tt.wantDur {
				t.Fatalf("parsed = (%q, %q, %d, %t, %v), want (%q, %q, %d, %t, %v)", event, clientID, afterGen, afterSet, timeout, tt.wantEvent, tt.wantID, tt.wantAfter, tt.wantSet, tt.wantDur)
			}
		})
	}
}

func TestWaitBusyForegroundProcessGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status mux.ForegroundJobState
		want   int
	}{
		{
			name:   "idle",
			status: mux.ForegroundJobState{Idle: true, ForegroundProcessGroup: 12},
			want:   0,
		},
		{
			name:   "no foreground process group",
			status: mux.ForegroundJobState{Idle: false},
			want:   0,
		},
		{
			name:   "foreground process group is returned",
			status: mux.ForegroundJobState{ForegroundProcessGroup: 56},
			want:   56,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := WaitBusyForegroundProcessGroup(tt.status); got != tt.want {
				t.Fatalf("WaitBusyForegroundProcessGroup(%+v) = %d, want %d", tt.status, got, tt.want)
			}
		})
	}
}

func TestWaitBusyReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		candidatePID int
		status       mux.ForegroundJobState
		wantNext     int
		wantReady    bool
	}{
		{
			name:         "zero candidate does not satisfy readiness",
			candidatePID: 0,
			status:       mux.ForegroundJobState{ForegroundProcessGroup: 91},
			wantNext:     91,
			wantReady:    false,
		},
		{
			name:         "different foreground process group updates candidate",
			candidatePID: 91,
			status:       mux.ForegroundJobState{ForegroundProcessGroup: 104},
			wantNext:     104,
			wantReady:    false,
		},
		{
			name:         "same foreground process group is ready",
			candidatePID: 104,
			status:       mux.ForegroundJobState{ForegroundProcessGroup: 104},
			wantNext:     104,
			wantReady:    true,
		},
		{
			name:         "idle clears candidate",
			candidatePID: 104,
			status:       mux.ForegroundJobState{Idle: true, ForegroundProcessGroup: 104},
			wantNext:     0,
			wantReady:    false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotNext, gotReady := WaitBusyReady(tt.candidatePID, tt.status)
			if gotNext != tt.wantNext || gotReady != tt.wantReady {
				t.Fatalf("WaitBusyReady(%d, %+v) = (%d, %t), want (%d, %t)", tt.candidatePID, tt.status, gotNext, gotReady, tt.wantNext, tt.wantReady)
			}
		})
	}
}
