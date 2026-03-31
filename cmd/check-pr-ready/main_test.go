package main

import (
	"bytes"
	"reflect"
	"testing"
)

func TestParsePanePRMap(t *testing.T) {
	t.Parallel()

	input := `PANE   NAME                 HOST            BRANCH                         WINDOW     TASK         META
 7     pane-7               local           feature/pr-422                 main       worker       prs=[422]
 8     pane-8               local           feature/pr-422b                main       worker       prs=[422,500]
 7     pane-7               local           feature/pr-422                 main       worker       prs=[422]
 9     pane-9               local           feature/none                   main       worker       nope
`

	got := parsePanePRMap(input)
	want := map[int][]string{
		422: {"pane-7", "pane-8"},
		500: {"pane-8"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePanePRMap() = %#v, want %#v", got, want)
	}
}

func TestLatestMatchingReviewBody(t *testing.T) {
	t.Parallel()

	reviewsJSON := `[
  [
    {"user":{"login":"claude[bot]"},"body":"Earlier approval\n\nLGTM","submitted_at":"2026-03-28T10:00:00Z"},
    {"user":{"login":"reviewer"},"body":"human note","submitted_at":"2026-03-28T11:00:00Z"}
  ],
  [
    {"user":{"login":"Claude Code"},"body":"Found one more issue to fix.","submitted_at":"2026-03-28T12:00:00Z"}
  ]
]`

	got := latestMatchingReviewBody(reviewsJSON, "claude")
	want := "Found one more issue to fix."
	if got != want {
		t.Fatalf("latestMatchingReviewBody() = %q, want %q", got, want)
	}
}

func TestLatestMatchingIssueCommentSignal(t *testing.T) {
	t.Parallel()

	commentsJSON := `[
  [
    {"user":{"login":"github-actions[bot]"},"body":"**Claude finished @cweill's task in 1m**\n\n### Findings\n\n**Blocking: old finding**","created_at":"2026-03-28T10:00:00Z"}
  ],
  [
    {"user":{"login":"codecov"},"body":"coverage looks good","created_at":"2026-03-28T11:00:00Z"},
    {"user":{"login":"github-actions[bot]"},"body":"**Claude finished @cweill's task in 45s**\n\n### Review\n\nNo blocking issues.\n\nLGTM","created_at":"2026-03-28T12:00:00Z"}
  ]
]`

	got, ok := latestMatchingIssueCommentSignal(commentsJSON, "claude")
	if !ok {
		t.Fatal("latestMatchingIssueCommentSignal() = no match, want latest Claude issue comment")
	}
	if got.At != "2026-03-28T12:00:00Z" {
		t.Fatalf("latestMatchingIssueCommentSignal() at = %q, want %q", got.At, "2026-03-28T12:00:00Z")
	}
	if got.Body != "**Claude finished @cweill's task in 45s**\n\n### Review\n\nNo blocking issues.\n\nLGTM" {
		t.Fatalf("latestMatchingIssueCommentSignal() body = %q, want latest LGTM comment", got.Body)
	}
}

func TestLatestClaudeSignalBodyPrefersNewestAcrossReviewsAndIssueComments(t *testing.T) {
	t.Parallel()

	reviewsJSON := `[{"user":{"login":"claude[bot]"},"body":"Looks good.\n\nLGTM","submitted_at":"2026-03-28T10:00:00Z"}]`
	commentsJSON := `[{"user":{"login":"github-actions[bot]"},"body":"**Claude finished @cweill's task in 50s**\n\n### Findings\n\n**Blocking: one more fix**","created_at":"2026-03-28T11:00:00Z"}]`

	got := latestClaudeSignalBody(reviewsJSON, commentsJSON, "claude")
	want := "**Claude finished @cweill's task in 50s**\n\n### Findings\n\n**Blocking: one more fix**"
	if got != want {
		t.Fatalf("latestClaudeSignalBody() = %q, want %q", got, want)
	}
}

func TestReviewEndsWithLGTM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "plain suffix", body: "Looks good.\n\nLGTM", want: true},
		{name: "punctuation before suffix", body: "Done: LGTM", want: true},
		{name: "trailing whitespace", body: "Looks good.\r\n\r\nLGTM \n", want: true},
		{name: "embedded token only", body: "LGTM-ish", want: false},
		{name: "later text", body: "LGTM\nwith extra text", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := reviewEndsWithLGTM(tt.body); got != tt.want {
				t.Fatalf("reviewEndsWithLGTM(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestRequiredChecksPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want bool
	}{
		{
			name: "pass and skipping buckets pass",
			json: `[{"bucket":"pass"},{"bucket":"skipping"}]`,
			want: true,
		},
		{
			name: "pending bucket blocks readiness",
			json: `[{"bucket":"pass"},{"bucket":"pending"}]`,
			want: false,
		},
		{
			name: "empty array blocks readiness",
			json: `[]`,
			want: false,
		},
		{
			name: "invalid json blocks readiness",
			json: `nope`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := requiredChecksPass(tt.json); got != tt.want {
				t.Fatalf("requiredChecksPass(%q) = %v, want %v", tt.json, got, tt.want)
			}
		})
	}
}

func TestRepoSlugForPR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		url          string
		repoOverride string
		envRepo      string
		want         string
	}{
		{
			name:         "explicit override wins",
			url:          "https://github.com/weill-labs/amux/pull/422",
			repoOverride: "other/repo",
			envRepo:      "env/repo",
			want:         "other/repo",
		},
		{
			name:    "gh repo env is next",
			url:     "https://github.com/weill-labs/amux/pull/422",
			envRepo: "env/repo",
			want:    "env/repo",
		},
		{
			name: "url fallback",
			url:  "https://github.com/weill-labs/amux/pull/422",
			want: "weill-labs/amux",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := repoSlugForPR(tt.url, tt.repoOverride, tt.envRepo); got != tt.want {
				t.Fatalf("repoSlugForPR() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		want       options
		wantExit   int
		wantOK     bool
		wantStderr string
	}{
		{
			name: "defaults",
			want: options{
				notify:           true,
				claudeLoginRegex: "claude",
			},
			wantExit: 0,
			wantOK:   true,
		},
		{
			name: "flags",
			args: []string{"--no-notify", "--claude-login-regex", "Claude Code", "-R", "weill-labs/amux"},
			want: options{
				notify:           false,
				claudeLoginRegex: "Claude Code",
				repoOverride:     "weill-labs/amux",
			},
			wantExit: 0,
			wantOK:   true,
		},
		{
			name:       "help",
			args:       []string{"--help"},
			want:       options{notify: true, claudeLoginRegex: "claude"},
			wantExit:   0,
			wantOK:     false,
			wantStderr: "usage: check-pr-ready",
		},
		{
			name:       "missing value",
			args:       []string{"--repo"},
			want:       options{notify: true, claudeLoginRegex: "claude"},
			wantExit:   2,
			wantOK:     false,
			wantStderr: "usage: check-pr-ready",
		},
		{
			name:       "unknown flag",
			args:       []string{"--bogus"},
			want:       options{notify: true, claudeLoginRegex: "claude"},
			wantExit:   2,
			wantOK:     false,
			wantStderr: "usage: check-pr-ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			got, exitCode, ok := parseOptions(tt.args, &stderr)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseOptions() options = %#v, want %#v", got, tt.want)
			}
			if exitCode != tt.wantExit {
				t.Fatalf("parseOptions() exit = %d, want %d", exitCode, tt.wantExit)
			}
			if ok != tt.wantOK {
				t.Fatalf("parseOptions() ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantStderr != "" && !bytes.Contains(stderr.Bytes(), []byte(tt.wantStderr)) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
