package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func TestRunServerDoesNotLateSetLogger(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	path := filepath.Join(filepath.Dir(thisFile), "internal", "cli", "server_bootstrap.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parser.ParseFile(%q): %v", path, err)
	}

	var runServerBody *ast.BlockStmt
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "RunServer" {
			continue
		}
		runServerBody = fn.Body
		break
	}
	if runServerBody == nil {
		t.Fatal("RunServer not found")
	}

	ast.Inspect(runServerBody, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "SetLogger" {
			return true
		}
		pos := fset.Position(call.Pos())
		t.Fatalf("RunServer still calls SetLogger at %s", pos)
		return false
	})
}

func TestServerBootstrapLogsServerStart(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	session := hermeticMainSession(t.Name())
	sockPath := server.SocketPath(session)
	_ = os.Remove(sockPath)
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(filepath.Join(server.SocketDir(), filepath.Base(sockPath)+".log"))
	})

	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe ready: %v", err)
	}
	defer readyRead.Close()

	shutdownRead, shutdownWrite, err := os.Pipe()
	if err != nil {
		readyWrite.Close()
		shutdownRead.Close()
		t.Fatalf("os.Pipe shutdown: %v", err)
	}
	defer shutdownRead.Close()

	cmdArgs := []string{"-test.run=TestMainCLISubprocessHelper", "--", "_server", session}
	cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
	cmd.ExtraFiles = []*os.File{readyWrite, shutdownWrite}
	cmd.Env = append(hermeticMainEnv(),
		"HOME="+t.TempDir(),
		"AMUX_READY_FD=3",
		"AMUX_SHUTDOWN_FD=4",
		"AMUX_NO_WATCH=1",
		"AMUX_DISABLE_META_REFRESH=1",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		readyWrite.Close()
		shutdownWrite.Close()
		t.Fatalf("cmd.Start(): %v", err)
	}
	readyWrite.Close()
	shutdownWrite.Close()

	if err := readyRead.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 64)
	n, err := readyRead.Read(buf)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("ready pipe read: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(string(buf[:n]), "ready") {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("ready pipe = %q, want ready\\n\noutput:\n%s", string(buf[:n]), out.String())
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("Process.Signal(os.Interrupt): %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("cmd.Wait(): %v\noutput:\n%s", err, out.String())
	}

	output := out.String()
	if !strings.Contains(output, `"event":"server_start"`) {
		t.Fatalf("output missing server_start event:\n%s", output)
	}
	if !strings.Contains(output, `"session":"`+session+`"`) {
		t.Fatalf("output missing session %q:\n%s", session, output)
	}
}
