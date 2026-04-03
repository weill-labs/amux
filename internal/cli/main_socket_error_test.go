package cli

import (
	"strings"
	"testing"
)

func TestMainStatusReportsDialErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*testing.T)
		wantErr string
	}{
		{name: "missing socket", prepare: prepareHermeticMissingSocket, wantErr: "no such file or directory"},
		{name: "stale socket", prepare: prepareHermeticStaleSocket, wantErr: "connection refused"},
		{name: "permission denied", prepare: prepareHermeticPermissionDeniedSocket, wantErr: "permission denied"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.prepare(t)

			out, code := runHermeticMain(t, "status")
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, out)
			}
			assertMainCommandConnectError(t, out, "status")
			if !strings.Contains(out, tt.wantErr) {
				t.Fatalf("output = %q, want substring %q", out, tt.wantErr)
			}
		})
	}
}

func TestMainEventsReportsInitialDialError(t *testing.T) {
	t.Parallel()

	prepareHermeticPermissionDeniedSocket(t)

	out, code := runHermeticMain(t, "events", "--no-reconnect")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	assertMainCommandConnectError(t, out, "events")
	if !strings.Contains(out, "permission denied") {
		t.Fatalf("output = %q, want permission denied", out)
	}
}
