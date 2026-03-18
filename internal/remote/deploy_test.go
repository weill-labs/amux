package remote

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseUnameArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantOS   string
		wantArch string
		wantErr  bool
	}{
		{name: "Linux x86_64", input: "Linux x86_64", wantOS: "linux", wantArch: "amd64"},
		{name: "Linux aarch64", input: "Linux aarch64", wantOS: "linux", wantArch: "arm64"},
		{name: "Darwin x86_64", input: "Darwin x86_64", wantOS: "darwin", wantArch: "amd64"},
		{name: "Darwin arm64", input: "Darwin arm64", wantOS: "darwin", wantArch: "arm64"},
		{name: "unknown OS", input: "FreeBSD amd64", wantErr: true},
		{name: "unknown arch", input: "Linux mips", wantErr: true},
		{name: "empty string", input: "", wantErr: true},
		{name: "single field", input: "Linux", wantErr: true},
		{name: "three fields", input: "Linux x86_64 extra", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotOS, gotArch, err := parseUnameArch(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseUnameArch(%q) = (%q, %q, nil), want error", tt.input, gotOS, gotArch)
				}
				return
			}
			if err != nil {
				t.Errorf("parseUnameArch(%q) returned error: %v", tt.input, err)
				return
			}
			if gotOS != tt.wantOS || gotArch != tt.wantArch {
				t.Errorf("parseUnameArch(%q) = (%q, %q), want (%q, %q)", tt.input, gotOS, gotArch, tt.wantOS, tt.wantArch)
			}
		})
	}
}

func TestDeployBinarySkipsWhenNoDeploySet(t *testing.T) {
	t.Setenv("AMUX_NO_DEPLOY", "1")

	// DeployBinary should return nil immediately without trying SSH
	err := DeployBinary(nil, "abc1234")
	if err != nil {
		t.Errorf("DeployBinary with AMUX_NO_DEPLOY=1 returned error: %v", err)
	}
}

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

func TestFindModuleRoot(t *testing.T) {
	t.Parallel()

	// Use the test file's source directory (known to be inside the module)
	_, thisFile, _, _ := runtime.Caller(0)
	thisDir := filepath.Dir(thisFile)

	root := findModuleRoot(thisDir)
	if root == "" {
		t.Fatalf("findModuleRoot(%q) returned empty string, expected to find go.mod", thisDir)
	}

	// go.mod should exist in the returned root
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("go.mod not found in returned root %q: %v", root, err)
	}

	// From root itself should return the same
	root2 := findModuleRoot(root)
	if root2 != root {
		t.Errorf("findModuleRoot(%q) = %q, want %q", root, root2, root)
	}

	// From non-existent deep path should return empty
	root3 := findModuleRoot("/nonexistent/deep/path")
	if root3 != "" {
		t.Errorf("findModuleRoot(\"/nonexistent/deep/path\") = %q, want empty", root3)
	}
}
