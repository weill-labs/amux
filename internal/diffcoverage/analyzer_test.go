package diffcoverage

import (
	"os"
	"strings"
	"testing"
)

func TestAnalyzeContents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		diff              string
		profile           string
		wantCovered       int
		wantExecutable    int
		wantIgnored       int
		wantUncoveredLine int
	}{
		{
			name: "counts covered and uncovered changed lines",
			diff: strings.TrimSpace(`
diff --git a/internal/server/example.go b/internal/server/example.go
index 1111111..2222222 100644
--- a/internal/server/example.go
+++ b/internal/server/example.go
@@ -9,0 +10,4 @@
+func example() {
+	if broken {
+		panic("boom")
+	}
`),
			profile: strings.TrimSpace(`
mode: atomic
github.com/weill-labs/amux/internal/server/example.go:10.1,10.16 1 1
github.com/weill-labs/amux/internal/server/example.go:11.2,12.17 1 0
github.com/weill-labs/amux/internal/server/example.go:13.2,13.3 1 0
`),
			wantCovered:       1,
			wantExecutable:    4,
			wantIgnored:       0,
			wantUncoveredLine: 11,
		},
		{
			name: "ignores lines with no coverage entry",
			diff: strings.TrimSpace(`
diff --git a/internal/server/comment_only.go b/internal/server/comment_only.go
index 1111111..2222222 100644
--- a/internal/server/comment_only.go
+++ b/internal/server/comment_only.go
@@ -1,0 +2,2 @@
+// document why this path exists
+
`),
			profile: strings.TrimSpace(`
mode: atomic
`),
			wantCovered:       0,
			wantExecutable:    0,
			wantIgnored:       2,
			wantUncoveredLine: 0,
		},
		{
			name: "ignores non-go files in the diff",
			diff: strings.TrimSpace(`
diff --git a/README.md b/README.md
index 1111111..2222222 100644
--- a/README.md
+++ b/README.md
@@ -1,0 +2,1 @@
+new line
`),
			profile: strings.TrimSpace(`
mode: atomic
github.com/weill-labs/amux/internal/server/example.go:10.1,11.14 1 1
`),
			wantCovered:       0,
			wantExecutable:    0,
			wantIgnored:       0,
			wantUncoveredLine: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := AnalyzeContents([]byte(tt.diff), []byte(tt.profile), "github.com/weill-labs/amux")
			if err != nil {
				t.Fatalf("AnalyzeContents error = %v", err)
			}
			if result.CoveredLines != tt.wantCovered {
				t.Fatalf("CoveredLines = %d, want %d", result.CoveredLines, tt.wantCovered)
			}
			if result.ExecutableLines != tt.wantExecutable {
				t.Fatalf("ExecutableLines = %d, want %d", result.ExecutableLines, tt.wantExecutable)
			}
			if result.IgnoredLines != tt.wantIgnored {
				t.Fatalf("IgnoredLines = %d, want %d", result.IgnoredLines, tt.wantIgnored)
			}

			if tt.wantUncoveredLine == 0 {
				if len(result.Uncovered) != 0 {
					t.Fatalf("Uncovered = %#v, want none", result.Uncovered)
				}
				return
			}
			if len(result.Uncovered) == 0 {
				t.Fatal("want uncovered line, got none")
			}
			if result.Uncovered[0].Line != tt.wantUncoveredLine {
				t.Fatalf("first uncovered line = %d, want %d", result.Uncovered[0].Line, tt.wantUncoveredLine)
			}
		})
	}
}

func TestResultPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result Result
		want   float64
	}{
		{
			name: "normal percentage",
			result: Result{
				CoveredLines:    7,
				ExecutableLines: 10,
			},
			want: 70,
		},
		{
			name:   "no executable lines counts as full coverage",
			result: Result{},
			want:   100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.result.Percent(); got != tt.want {
				t.Fatalf("Percent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnalyze(t *testing.T) {
	t.Parallel()

	profilePath := t.TempDir() + "/coverage.out"
	if err := os.WriteFile(profilePath, []byte("mode: atomic\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", profilePath, err)
	}

	result, err := Analyze("HEAD", profilePath, "github.com/weill-labs/amux")
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.ExecutableLines != 0 {
		t.Fatalf("ExecutableLines = %d, want 0", result.ExecutableLines)
	}

	_, err = Analyze("refs/heads/does-not-exist", profilePath, "github.com/weill-labs/amux")
	if err == nil || !strings.Contains(err.Error(), "git diff against refs/heads/does-not-exist") {
		t.Fatalf("Analyze() error = %v, want missing ref error", err)
	}

	_, err = Analyze("HEAD", profilePath+".missing", "github.com/weill-labs/amux")
	if err == nil || !strings.Contains(err.Error(), "read "+profilePath+".missing") {
		t.Fatalf("Analyze() error = %v, want missing profile error", err)
	}
}

func TestAnalyzeContentsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		diff    string
		profile string
		wantErr string
	}{
		{
			name: "invalid diff hunk",
			diff: strings.TrimSpace(`
diff --git a/internal/server/example.go b/internal/server/example.go
--- a/internal/server/example.go
+++ b/internal/server/example.go
@@ bad @@
+func example() {}
`),
			profile: "mode: atomic\n",
			wantErr: `parse diff hunk: "@@ bad @@"`,
		},
		{
			name: "invalid profile line",
			diff: strings.TrimSpace(`
diff --git a/internal/server/example.go b/internal/server/example.go
--- a/internal/server/example.go
+++ b/internal/server/example.go
@@ -1,0 +1,1 @@
+func example() {}
`),
			profile: "mode: atomic\nbroken\n",
			wantErr: `parse coverprofile line: "broken"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := AnalyzeContents([]byte(tt.diff), []byte(tt.profile), "github.com/weill-labs/amux")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("AnalyzeContents() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseChangedLines(t *testing.T) {
	t.Parallel()

	diff := strings.TrimSpace(`
diff --git a/internal/server/example.go b/internal/server/example.go
--- a/internal/server/example.go
+++ b/internal/server/example.go
@@ -1,2 +1,3 @@
 func keep() {}
+func added() {}
 func keepToo() {}
`)

	changed, err := parseChangedLines([]byte(diff))
	if err != nil {
		t.Fatalf("parseChangedLines() error = %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("len(changed) = %d, want 1", len(changed))
	}
	if changed[0].Line != 2 {
		t.Fatalf("changed line = %d, want 2", changed[0].Line)
	}

	_, err = parseChangedLines([]byte(strings.TrimSpace(`
diff --git a/internal/server/example.go b/internal/server/example.go
--- a/internal/server/example.go
+++ b/internal/server/example.go
@@ -1,0 +999999999999999999999999,1 @@
+func example() {}
`)))
	if err == nil || !strings.Contains(err.Error(), `parse diff line "999999999999999999999999"`) {
		t.Fatalf("parseChangedLines() error = %v, want oversized line error", err)
	}
}

func TestParseCoverageProfileErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile string
		wantErr string
	}{
		{
			name:    "missing path separator",
			profile: "mode: atomic\nbroken 1 1\n",
			wantErr: `parse coverprofile path: "broken 1 1"`,
		},
		{
			name:    "missing range comma",
			profile: "mode: atomic\ngithub.com/weill-labs/amux/internal/server/example.go:10.1 1 1\n",
			wantErr: `parse coverprofile range "10.1"`,
		},
		{
			name:    "missing line column separator",
			profile: "mode: atomic\ngithub.com/weill-labs/amux/internal/server/example.go:10,11.1 1 1\n",
			wantErr: `parse coverprofile line ref "10"`,
		},
		{
			name:    "invalid line number",
			profile: "mode: atomic\ngithub.com/weill-labs/amux/internal/server/example.go:ten.1,11.1 1 1\n",
			wantErr: `parse coverprofile line "ten"`,
		},
		{
			name:    "invalid hit count",
			profile: "mode: atomic\ngithub.com/weill-labs/amux/internal/server/example.go:10.1,11.1 1 nope\n",
			wantErr: `parse coverprofile count "nope"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseCoverageProfile([]byte(tt.profile), "github.com/weill-labs/amux")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseCoverageProfile() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
