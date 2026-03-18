package remote

import (
	"fmt"
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

// --- SSH-backed tests (use in-process test SSH server) ---

// plantFakeAmux writes a shell script to $HOME/.local/bin/amux in the test
// SSH server's home directory. The script echoes the given hash, simulating
// an installed amux binary that reports its version.
func plantFakeAmux(t *testing.T, homeDir, hash string) {
	t.Helper()
	binDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("creating bin dir: %v", err)
	}
	script := filepath.Join(binDir, "amux")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+hash+"\n"), 0755); err != nil {
		t.Fatalf("writing fake amux: %v", err)
	}
}

func TestSSHOutput(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	out, err := sshOutput(ts.Client, "echo hello")
	if err != nil {
		t.Fatalf("sshOutput error: %v", err)
	}
	if out != "hello" {
		t.Errorf("sshOutput = %q, want hello", out)
	}
}

func TestSSHOutputError(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	_, err := sshOutput(ts.Client, "exit 1")
	if err == nil {
		t.Error("sshOutput with failing command should return error")
	}
}

func TestSSHRun(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	// sshRun ignores errors — should not panic on success or failure
	sshRun(ts.Client, "true")
	sshRun(ts.Client, "false")
}

func TestSSHRunErr(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	if err := sshRunErr(ts.Client, "true"); err != nil {
		t.Errorf("sshRunErr(true) = %v, want nil", err)
	}
	if err := sshRunErr(ts.Client, "false"); err == nil {
		t.Error("sshRunErr(false) should return error")
	}
}

func TestUploadBinary(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	// Create a temp file to upload
	tmpFile := filepath.Join(t.TempDir(), "test-binary")
	if err := os.WriteFile(tmpFile, []byte("#!/bin/sh\necho uploaded"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := uploadBinary(ts.Client, tmpFile); err != nil {
		t.Fatalf("uploadBinary error: %v", err)
	}

	// Verify the file was uploaded to $HOME/.local/bin/amux
	uploaded := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	data, err := os.ReadFile(uploaded)
	if err != nil {
		t.Fatalf("reading uploaded binary: %v", err)
	}
	if string(data) != "#!/bin/sh\necho uploaded" {
		t.Errorf("uploaded content = %q, want script content", string(data))
	}

	// Verify it's executable
	info, _ := os.Stat(uploaded)
	if info.Mode()&0111 == 0 {
		t.Error("uploaded binary should be executable")
	}
}

func TestUploadBinaryFileNotFound(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	err := uploadBinary(ts.Client, "/nonexistent/path/to/binary")
	if err == nil {
		t.Error("uploadBinary with nonexistent file should error")
	}
}

func TestRemoteBuildHashNotFound(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	// No amux binary on the "remote" — should return error
	_, err := remoteBuildHash(ts.Client)
	if err == nil {
		t.Error("remoteBuildHash should error when amux not found")
	}
}

func TestRemoteBuildHashFound(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	plantFakeAmux(t, ts.HomeDir, "fakehash123")

	hash, err := remoteBuildHash(ts.Client)
	if err != nil {
		t.Fatalf("remoteBuildHash error: %v", err)
	}
	if hash != "fakehash123" {
		t.Errorf("remoteBuildHash = %q, want fakehash123", hash)
	}
}

func TestDeployBinaryUpToDate(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	plantFakeAmux(t, ts.HomeDir, "abc1234")

	// Deploy with matching hash — should skip (no upload)
	err := DeployBinary(ts.Client, "abc1234")
	if err != nil {
		t.Errorf("DeployBinary with matching hash should succeed, got: %v", err)
	}
}

func TestDeployBinaryCrossArchFails(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	// Plant a fake uname that reports a different architecture,
	// triggering the cross-arch path in DeployBinary.
	binDir := filepath.Join(ts.HomeDir, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("creating bin dir: %v", err)
	}

	fakeArch := "Linux x86_64"
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		fakeArch = "Linux aarch64"
	}
	fakeUname := filepath.Join(binDir, "uname")
	if err := os.WriteFile(fakeUname, []byte(fmt.Sprintf("#!/bin/sh\necho '%s'\n", fakeArch)), 0755); err != nil {
		t.Fatalf("writing fake uname: %v", err)
	}

	// DeployBinary should attempt cross-compile (which will fail in test env
	// since go.mod can't be found from test binary path) and then fall through
	// to the GitHub release download (which will also fail). Both failing means
	// it returns a "cross-arch deploy failed" error.
	err := DeployBinary(ts.Client, "crosshash")
	if err == nil {
		t.Error("DeployBinary cross-arch should fail when both cross-compile and download fail")
	}
}

func TestDeployBinarySameArch(t *testing.T) {
	t.Parallel()
	ts := startTestSSH(t)

	// No amux on remote and the test SSH server runs on the same machine,
	// so uname -sm matches the local platform → same-arch upload path.
	// DeployBinary will upload the test binary via os.Executable().
	err := DeployBinary(ts.Client, "newhash")
	if err != nil {
		t.Errorf("DeployBinary same-arch should succeed, got: %v", err)
	}

	// Verify something was uploaded
	uploaded := filepath.Join(ts.HomeDir, ".local", "bin", "amux")
	if _, err := os.Stat(uploaded); err != nil {
		t.Errorf("expected binary at %s after deploy: %v", uploaded, err)
	}
}
