package test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envWithHome(home string) []string {
	env := append([]string{}, os.Environ()...)
	replaced := false
	for i, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			env[i] = "HOME=" + home
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, "HOME="+home)
	}
	env = upsertEnv(env, "GOFLAGS", appendGoFlag(envValue(env, "GOFLAGS"), "-modcacherw"))
	return env
}

func envWithHomeAndBranch(t *testing.T, home, branch string, extra ...string) []string {
	t.Helper()

	env := envWithHome(home)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}

	fakeGitDir := t.TempDir()
	fakeGit := filepath.Join(fakeGitDir, "git")
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "rev-parse" && "${2:-}" == "--abbrev-ref" && "${3:-}" == "HEAD" ]]; then
	printf '%%s\n' %q
	exit 0
fi

exec %q "$@"
`, branch, gitPath)
	if err := os.WriteFile(fakeGit, []byte(script), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	pathValue := fakeGitDir
	replaced := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + fakeGitDir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, "PATH="+pathValue)
	}

	return append(env, extra...)
}

func prependPath(env []string, dir string) []string {
	pathValue := dir
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			return env
		}
	}
	return append(env, "PATH="+pathValue)
}

func newInstallTestToolDir(t *testing.T, fakeUID, installTerminfoBin string) string {
	t.Helper()

	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("look up go: %v", err)
	}
	idPath, err := exec.LookPath("id")
	if err != nil {
		t.Fatalf("look up id: %v", err)
	}

	dir := t.TempDir()
	installTerminfoCmd := "exit 0"
	if installTerminfoBin != "" {
		installTerminfoCmd = fmt.Sprintf("exec %q install-terminfo", installTerminfoBin)
	}
	writeTool := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}

	writeTool("go", fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "build" ]]; then
	out=""
	while [[ $# -gt 0 ]]; do
		case "$1" in
			-o)
				out="$2"
				shift 2
				;;
			*)
				shift
				;;
		esac
	done

	if [[ -z "$out" ]]; then
		echo "fake go build: missing -o" >&2
		exit 1
	fi

	cat >"$out" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "version" && "${2:-}" == "--json" ]]; then
	printf '{"build":"newbuild","checkpoint_version":2}\n'
	exit 0
fi

if [[ "${1:-}" == "install-terminfo" ]]; then
	%s
fi

printf 'unexpected args: %%s\n' "$*" >&2
exit 1
EOF
	chmod +x "$out"
	exit 0
fi

exec %q "$@"
`, installTerminfoCmd, goPath))
	writeTool("codesign", "#!/usr/bin/env bash\nexit 0\n")
	writeTool("xattr", "#!/usr/bin/env bash\nexit 0\n")
	writeTool("id", fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "-u" ]]; then
	printf '%%s\n' %q
	exit 0
fi

exec %q "$@"
`, fakeUID, idPath))

	return dir
}

func TestBuildInstallInstallsTerminfo(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dest := filepath.Join(t.TempDir(), "amux")
	toolDir := newInstallTestToolDir(t, fmt.Sprintf("%d", os.Getuid()), amuxBin)

	cmd := exec.Command("bash", repoPath(t, "scripts/install.sh"), dest)
	cmd.Dir = repoRoot(t)
	cmd.Env = prependPath(envWithHomeAndBranch(t, home, "main"), toolDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build install failed: %v\n%s", err, out)
	}

	verify := exec.Command("infocmp", "-A", filepath.Join(home, ".terminfo"), "amux")
	verify.Env = envWithHome(home)
	termOut, err := verify.CombinedOutput()
	if err != nil {
		t.Fatalf("infocmp amux failed: %v\n%s", err, termOut)
	}
	if !strings.Contains(string(termOut), "amux") {
		t.Fatalf("infocmp output missing amux entry:\n%s", termOut)
	}
}

func TestBuildInstallRewritesInvalidMetadata(t *testing.T) {
	t.Parallel()

	rootDir := repoRoot(t)
	dest := filepath.Join(t.TempDir(), "amux")
	metaPath := dest + ".install-meta"
	toolDir := newInstallTestToolDir(t, fmt.Sprintf("%d", os.Getuid()), amuxBin)
	if err := os.WriteFile(metaPath, []byte("not-valid-metadata\n"), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cmd := exec.Command("bash", repoPath(t, "scripts/install.sh"), dest)
	cmd.Dir = rootDir
	cmd.Env = prependPath(envWithHomeAndBranch(t, t.TempDir(), "main"), toolDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install with invalid metadata failed: %v\n%s", err, out)
	}

	meta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if !strings.Contains(string(meta), "source_repo="+rootDir) {
		t.Fatalf("expected metadata rewrite, got:\n%s", meta)
	}
}

func TestBuildInstallBlocksIncompatibleCheckpointHotReloadWithoutConfirmation(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dest := filepath.Join(t.TempDir(), "amux")
	fakeUID := "424242"
	if err := os.WriteFile(dest, []byte(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "version" && "${2:-}" == "--json" ]]; then
	printf '{"build":"oldbuild","checkpoint_version":1}\n'
	exit 0
fi

if [[ "${1:-}" == "-s" ]]; then
	shift 2
fi

if [[ "${1:-}" == "status" ]]; then
	printf 'windows: 1, panes: 1 total, build: oldbuild (checkpoint v1)\n'
	exit 0
fi

printf 'unexpected args: %s\n' "$*" >&2
exit 1
`), 0755); err != nil {
		t.Fatalf("write fake amux: %v", err)
	}

	socketDir := filepath.Join("/tmp", "amux-"+fakeUID)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	session := fmt.Sprintf("install-guard-%d", time.Now().UnixNano())
	socketPath := filepath.Join(socketDir, session)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()

	cmd := exec.Command("bash", repoPath(t, "scripts/install.sh"), dest)
	cmd.Dir = repoRoot(t)
	cmd.Env = prependPath(envWithHomeAndBranch(t, home, "main"), newInstallTestToolDir(t, fakeUID, ""))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install should fail without confirmation for incompatible live checkpoint\n%s", out)
	}
	output := string(out)
	if !strings.Contains(output, "checkpoint version") || !strings.Contains(output, "Proceed with install?") {
		t.Fatalf("install output = %q, want incompatible checkpoint warning with prompt", output)
	}

	destData, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read dest after failed install: %v", readErr)
	}
	if !strings.Contains(string(destData), "checkpoint_version\":1") {
		t.Fatalf("dest should remain the original fake binary after refusal, got:\n%s", destData)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func appendGoFlag(current, flag string) string {
	if current == "" {
		return flag
	}
	for _, existing := range strings.Fields(current) {
		if existing == flag {
			return current
		}
	}
	return current + " " + flag
}
