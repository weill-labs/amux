package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseInstallScriptInstallsExplicitVersionArchive(t *testing.T) {
	t.Parallel()

	const version = "1.2.3"
	script := "#!/bin/sh\necho explicit-version\n"
	archive := fakeReleaseInstallArchive(t, script)
	checksums := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), releaseArchiveName(version))

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case releaseArchivePath(version):
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		case releaseChecksumsPath(version):
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	home := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("bash", repoPath(t, "scripts/install-release.sh"))
	cmd.Dir = repoRoot(t)
	cmd.Env = append(envWithHome(home),
		"AMUX_INSTALL_BASE_URL="+ts.URL,
		"AMUX_INSTALL_VERSION="+version,
		"AMUX_INSTALL_BIN_DIR="+binDir,
		"AMUX_INSTALL_SKIP_TERMINFO=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-release failed: %v\n%s", err, out)
	}

	installed := filepath.Join(binDir, "amux")
	verifyInstalledScript(t, installed, "explicit-version\n")
}

func TestReleaseInstallScriptResolvesLatestReleaseRedirect(t *testing.T) {
	t.Parallel()

	const version = "2.0.1"
	script := "#!/bin/sh\necho latest-version\n"
	archive := fakeReleaseInstallArchive(t, script)
	checksums := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), releaseArchiveName(version))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "http://"+r.Host+"/releases/tag/v"+version, http.StatusFound)
		case fmt.Sprintf("/releases/tag/v%s", version):
			_, _ = w.Write([]byte("ok"))
		case releaseArchivePath(version):
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		case releaseChecksumsPath(version):
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	home := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("bash", repoPath(t, "scripts/install-release.sh"))
	cmd.Dir = repoRoot(t)
	cmd.Env = append(envWithHome(home),
		"AMUX_INSTALL_BASE_URL="+ts.URL,
		"AMUX_INSTALL_BIN_DIR="+binDir,
		"AMUX_INSTALL_SKIP_TERMINFO=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-release failed: %v\n%s", err, out)
	}

	installed := filepath.Join(binDir, "amux")
	verifyInstalledScript(t, installed, "latest-version\n")
}

func releaseArchiveName(version string) string {
	return fmt.Sprintf("amux_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
}

func releaseArchivePath(version string) string {
	return fmt.Sprintf("/releases/download/v%s/%s", version, releaseArchiveName(version))
}

func releaseChecksumsPath(version string) string {
	return fmt.Sprintf("/releases/download/v%s/checksums.txt", version)
}

func fakeReleaseInstallArchive(t *testing.T, content string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	data := []byte(content)
	if err := tw.WriteHeader(&tar.Header{
		Name: "amux",
		Mode: 0755,
		Size: int64(len(data)),
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	return buf.Bytes()
}

func verifyInstalledScript(t *testing.T, path, wantOutput string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Fatalf("installed binary should be executable, mode=%v", info.Mode())
	}

	out, err := exec.Command(path).CombinedOutput()
	if err != nil {
		t.Fatalf("run installed binary: %v\n%s", err, out)
	}
	if string(out) != wantOutput {
		t.Fatalf("installed binary output = %q, want %q", string(out), wantOutput)
	}
}
