package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/weill-labs/amux/internal/diffcoverage"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, diffcoverage.Analyze, detectModulePath))
}

type analyzeFunc func(baseRef, profilePath, modulePath string) (diffcoverage.Result, error)

func run(args []string, stdout, stderr io.Writer, analyze analyzeFunc, detectModulePath func() (string, error)) int {
	fs := flag.NewFlagSet("diffcoverage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		baseRef    = fs.String("base", "origin/main", "git base ref for the diff")
		modulePath = fs.String("module", "", "go module path used in coverage profiles")
		profile    = fs.String("profile", "merged-coverage.txt", "coverprofile to analyze")
		target     = fs.Float64("target", 70, "minimum acceptable diff coverage percentage")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *modulePath == "" {
		detected, err := detectModulePath()
		if err != nil {
			fatalf(stderr, "detect module path: %v", err)
			return 1
		}
		*modulePath = detected
	}

	result, err := analyze(*baseRef, *profile, *modulePath)
	if err != nil {
		fatalf(stderr, "analyze diff coverage: %v", err)
		return 1
	}

	if result.ExecutableLines == 0 {
		fmt.Fprintf(stdout, "Diff coverage against %s: no executable changed Go lines found\n", *baseRef)
		return 0
	}

	fmt.Fprintf(
		stdout,
		"Diff coverage against %s: %.2f%% (%d/%d executable changed lines covered; target %.2f%%)\n",
		*baseRef,
		result.Percent(),
		result.CoveredLines,
		result.ExecutableLines,
		*target,
	)
	if len(result.Uncovered) > 0 {
		fmt.Fprintln(stdout, "Uncovered changed lines:")
		for _, line := range result.Uncovered {
			fmt.Fprintf(stdout, "  %s:%d: %s\n", line.File, line.Line, strings.TrimSpace(line.Text))
		}
	}

	if result.Percent() < *target {
		fatalf(stderr, "diff coverage is below target")
		return 1
	}
	return 0
}

func detectModulePath() (string, error) {
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fatalf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format+"\n", args...)
}
