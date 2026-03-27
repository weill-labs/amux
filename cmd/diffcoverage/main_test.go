package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/diffcoverage"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		analyze    analyzeFunc
		detect     func() (string, error)
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "success with explicit module path",
			args: []string{"--base", "origin/main", "--module", "github.com/weill-labs/amux", "--profile", "merged-coverage.txt", "--target", "70"},
			analyze: func(baseRef, profilePath, modulePath string) (diffcoverage.Result, error) {
				if baseRef != "origin/main" || profilePath != "merged-coverage.txt" || modulePath != "github.com/weill-labs/amux" {
					t.Fatalf("analyze args = (%q, %q, %q)", baseRef, profilePath, modulePath)
				}
				return diffcoverage.Result{
					CoveredLines:    7,
					ExecutableLines: 10,
				}, nil
			},
			detect: func() (string, error) {
				t.Fatal("detectModulePath should not be called when --module is set")
				return "", nil
			},
			wantExit:   0,
			wantStdout: "Diff coverage against origin/main: 70.00% (7/10 executable changed lines covered; target 70.00%)",
		},
		{
			name: "below target prints uncovered lines",
			args: []string{"--module", "github.com/weill-labs/amux"},
			analyze: func(baseRef, profilePath, modulePath string) (diffcoverage.Result, error) {
				return diffcoverage.Result{
					CoveredLines:    1,
					ExecutableLines: 2,
					Uncovered: []diffcoverage.ChangedLine{{
						File: "internal/server/example.go",
						Line: 42,
						Text: "return err",
					}},
				}, nil
			},
			detect:     func() (string, error) { return "", nil },
			wantExit:   1,
			wantStdout: "Uncovered changed lines:\n  internal/server/example.go:42: return err",
			wantStderr: "diff coverage is below target",
		},
		{
			name: "detect module path failure",
			args: nil,
			analyze: func(baseRef, profilePath, modulePath string) (diffcoverage.Result, error) {
				t.Fatal("analyze should not run when module detection fails")
				return diffcoverage.Result{}, nil
			},
			detect: func() (string, error) {
				return "", errors.New("boom")
			},
			wantExit:   1,
			wantStderr: "detect module path: boom",
		},
		{
			name: "no executable lines succeeds",
			args: []string{"--module", "github.com/weill-labs/amux"},
			analyze: func(baseRef, profilePath, modulePath string) (diffcoverage.Result, error) {
				return diffcoverage.Result{}, nil
			},
			detect:     func() (string, error) { return "", nil },
			wantExit:   0,
			wantStdout: "Diff coverage against origin/main: no executable changed Go lines found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := run(tt.args, &stdout, &stderr, tt.analyze, tt.detect)
			if exitCode != tt.wantExit {
				t.Fatalf("run exit code = %d, want %d", exitCode, tt.wantExit)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Fatalf("stdout = %q, want substring %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
