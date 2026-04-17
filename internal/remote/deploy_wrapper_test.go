package remote

import (
	"os"
	"testing"
)

func TestShouldDeploy(t *testing.T) {
	// Cannot use t.Parallel — subtests use t.Setenv which modifies process env.

	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name      string
		buildHash string
		deploy    *bool
		envVar    string
		want      bool
	}{
		{name: "default enabled", buildHash: "abc1234", deploy: nil, want: true},
		{name: "explicit true", buildHash: "abc1234", deploy: boolPtr(true), want: true},
		{name: "explicit false", buildHash: "abc1234", deploy: boolPtr(false), want: false},
		{name: "empty build hash", buildHash: "", deploy: nil, want: false},
		{name: "env var set", buildHash: "abc1234", deploy: nil, envVar: "1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar != "" {
				t.Setenv("AMUX_NO_DEPLOY", tt.envVar)
			} else {
				os.Unsetenv("AMUX_NO_DEPLOY")
			}

			hc := &HostConn{buildHash: tt.buildHash}
			hc.config.Deploy = tt.deploy
			if got := hc.shouldDeploy(); got != tt.want {
				t.Errorf("shouldDeploy() = %v, want %v", got, tt.want)
			}
		})
	}
}
