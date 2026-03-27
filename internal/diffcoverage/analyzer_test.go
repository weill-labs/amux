package diffcoverage

import (
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
