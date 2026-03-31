package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDeps{}, os.Getenv))
}

type options struct {
	notify           bool
	claudeLoginRegex string
	repoOverride     string
}

type commandDeps interface {
	LookPath(name string) error
	PaneList() (string, error)
	PaneCapture(pane string) (string, error)
	SendKeys(pane string, message string) error
	PRList(repoOverride string) (string, error)
	PRChecks(prNumber int, repoOverride string) (string, int, error)
	PRReviews(repo string, prNumber int) (string, error)
	PRIssueComments(repo string, prNumber int) (string, error)
}

type realDeps struct{}

type pullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Mergeable string `json:"mergeable"`
}

type paneCapture struct {
	Idle           *bool  `json:"idle"`
	CurrentCommand string `json:"current_command"`
}

type review struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
}

type issueComment struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type claudeSignal struct {
	Body string
	At   string
}

type paneState struct {
	State          string
	CurrentCommand string
}

func run(args []string, stdout, stderr io.Writer, deps commandDeps, getenv func(string) string) int {
	opts, exitCode, ok := parseOptions(args, stderr)
	if !ok {
		return exitCode
	}

	for _, cmd := range []string{"amux", "gh"} {
		if err := deps.LookPath(cmd); err != nil {
			die(stderr, "missing required command: %s", cmd)
			return 2
		}
	}

	paneList, err := deps.PaneList()
	if err != nil {
		die(stderr, "failed to list panes")
		return 2
	}
	prToPanes := parsePanePRMap(paneList)

	prJSON, err := deps.PRList(opts.repoOverride)
	if err != nil {
		die(stderr, "failed to query open PRs")
		return 2
	}

	prs := []pullRequest{}
	if err := json.Unmarshal([]byte(prJSON), &prs); err != nil {
		die(stderr, "failed to parse open PRs")
		return 2
	}

	readyCount := 0
	for _, pr := range prs {
		if pr.Mergeable != "MERGEABLE" {
			continue
		}

		checksJSON, status, err := deps.PRChecks(pr.Number, opts.repoOverride)
		if err != nil && status != 0 && status != 8 {
			continue
		}
		if !requiredChecksPass(checksJSON) {
			continue
		}

		repoSlug := repoSlugForPR(pr.URL, opts.repoOverride, getenv("GH_REPO"))
		if repoSlug == "" || repoSlug == pr.URL {
			continue
		}

		reviewsJSON, reviewsErr := deps.PRReviews(repoSlug, pr.Number)
		if reviewsErr != nil {
			reviewsJSON = ""
		}
		commentsJSON, commentsErr := deps.PRIssueComments(repoSlug, pr.Number)
		if commentsErr != nil {
			commentsJSON = ""
		}

		reviewBody := latestClaudeSignalBody(reviewsJSON, commentsJSON, opts.claudeLoginRegex)
		if !reviewEndsWithLGTM(reviewBody) {
			continue
		}

		readyCount++
		panes := prToPanes[pr.Number]
		if len(panes) == 0 {
			fmt.Fprintf(stdout, "PR #%d %q owner=orphaned state=unknown review=LGTM notify=skipped-orphaned\n", pr.Number, pr.Title)
			continue
		}

		for _, pane := range panes {
			state := paneState{State: "unknown"}
			captureJSON, err := deps.PaneCapture(pane)
			if err == nil {
				state = parsePaneState(captureJSON)
			}

			notifyState := "disabled"
			if opts.notify {
				switch state.State {
				case "idle":
					if err := deps.SendKeys(pane, readyMessage(pr.Number)); err == nil {
						notifyState = "sent"
					} else {
						notifyState = "send-error"
					}
				case "busy":
					notifyState = "skipped-busy"
				default:
					notifyState = "skipped-unknown"
				}
			}

			displayState := state.State
			if state.State == "busy" && state.CurrentCommand != "" {
				displayState = fmt.Sprintf("busy(%s)", state.CurrentCommand)
			}

			fmt.Fprintf(stdout, "PR #%d %q owner=%s state=%s review=LGTM notify=%s\n", pr.Number, pr.Title, pane, displayState, notifyState)
		}
	}

	if readyCount == 0 {
		fmt.Fprintln(stdout, "No open PRs are ready for human merge.")
		return 0
	}

	fmt.Fprintf(stdout, "Found %d open PR(s) ready for human merge.\n", readyCount)
	return 0
}

func parseOptions(args []string, stderr io.Writer) (options, int, bool) {
	opts := options{
		notify:           true,
		claudeLoginRegex: "claude",
	}

	for len(args) > 0 {
		switch args[0] {
		case "--no-notify":
			opts.notify = false
			args = args[1:]
		case "--claude-login-regex":
			if len(args) < 2 {
				usage(stderr)
				return opts, 2, false
			}
			opts.claudeLoginRegex = args[1]
			args = args[2:]
		case "--repo", "-R":
			if len(args) < 2 {
				usage(stderr)
				return opts, 2, false
			}
			opts.repoOverride = args[1]
			args = args[2:]
		case "-h", "--help":
			usage(stderr)
			return opts, 0, false
		default:
			usage(stderr)
			return opts, 2, false
		}
	}

	return opts, 0, true
}

func usage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: check-pr-ready [--no-notify] [--claude-login-regex REGEX] [--repo OWNER/REPO]")
}

func parsePanePRMap(paneList string) map[int][]string {
	prToPanes := map[int][]string{}
	prsPattern := regexp.MustCompile(`prs=\[([^\]]*)\]`)

	lines := strings.Split(paneList, "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pane := fields[1]
		matches := prsPattern.FindStringSubmatch(line)
		if len(matches) != 2 {
			continue
		}
		for _, rawPR := range strings.Split(matches[1], ",") {
			rawPR = strings.TrimSpace(rawPR)
			if rawPR == "" {
				continue
			}
			prNumber, err := strconv.Atoi(rawPR)
			if err != nil {
				continue
			}
			if !containsPane(prToPanes[prNumber], pane) {
				prToPanes[prNumber] = append(prToPanes[prNumber], pane)
			}
		}
	}

	return prToPanes
}

func containsPane(panes []string, pane string) bool {
	for _, existing := range panes {
		if existing == pane {
			return true
		}
	}
	return false
}

func repoSlugForPR(url, repoOverride, envRepo string) string {
	if repoOverride != "" {
		return repoOverride
	}
	if envRepo != "" {
		return envRepo
	}
	return repoFromURL(url)
}

func repoFromURL(url string) string {
	matches := regexp.MustCompile(`^https?://[^/]+/([^/]+/[^/]+)/pull/[0-9]+/?$`).FindStringSubmatch(url)
	if len(matches) != 2 {
		return url
	}
	return matches[1]
}

func parsePaneState(captureJSON string) paneState {
	capture := paneCapture{}
	if err := json.Unmarshal([]byte(captureJSON), &capture); err != nil {
		return paneState{State: "unknown"}
	}
	if capture.Idle != nil && *capture.Idle {
		return paneState{State: "idle"}
	}
	return paneState{
		State:          "busy",
		CurrentCommand: capture.CurrentCommand,
	}
}

func requiredChecksPass(checksJSON string) bool {
	checks := []struct {
		Bucket string `json:"bucket"`
	}{}
	if err := json.Unmarshal([]byte(checksJSON), &checks); err != nil {
		return false
	}
	if len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		switch strings.ToLower(check.Bucket) {
		case "pass", "skipping":
		default:
			return false
		}
	}
	return true
}

func latestMatchingReviewBody(reviewsJSON, loginPattern string) string {
	signal, ok := latestMatchingReviewSignal(reviewsJSON, loginPattern)
	if !ok {
		return ""
	}
	return signal.Body
}

func latestMatchingReviewSignal(reviewsJSON, loginPattern string) (claudeSignal, bool) {
	if reviewsJSON == "" {
		return claudeSignal{}, false
	}
	loginRE, err := regexp.Compile("(?i)" + loginPattern)
	if err != nil {
		return claudeSignal{}, false
	}

	reviews, err := flattenReviews(reviewsJSON)
	if err != nil {
		return claudeSignal{}, false
	}

	filtered := make([]claudeSignal, 0, len(reviews))
	for _, review := range reviews {
		if loginRE.MatchString(review.User.Login) {
			filtered = append(filtered, claudeSignal{
				Body: review.Body,
				At:   review.SubmittedAt,
			})
		}
	}
	if len(filtered) == 0 {
		return claudeSignal{}, false
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].At < filtered[j].At
	})
	return filtered[len(filtered)-1], true
}

func flattenReviews(reviewsJSON string) ([]review, error) {
	raws := []json.RawMessage{}
	if err := json.Unmarshal([]byte(reviewsJSON), &raws); err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, nil
	}

	first := bytes.TrimSpace(raws[0])
	if len(first) > 0 && first[0] == '[' {
		all := []review{}
		for _, raw := range raws {
			page := []review{}
			if err := json.Unmarshal(raw, &page); err != nil {
				return nil, err
			}
			all = append(all, page...)
		}
		return all, nil
	}

	flat := []review{}
	if err := json.Unmarshal([]byte(reviewsJSON), &flat); err != nil {
		return nil, err
	}
	return flat, nil
}

func latestMatchingIssueCommentSignal(commentsJSON, loginPattern string) (claudeSignal, bool) {
	if commentsJSON == "" {
		return claudeSignal{}, false
	}
	loginRE, err := regexp.Compile("(?i)" + loginPattern)
	if err != nil {
		return claudeSignal{}, false
	}

	comments, err := flattenIssueComments(commentsJSON)
	if err != nil {
		return claudeSignal{}, false
	}

	filtered := make([]claudeSignal, 0, len(comments))
	for _, comment := range comments {
		login := comment.User.Login
		if !(loginRE.MatchString(login) || login == "github-actions[bot]" || login == "github-actions") {
			continue
		}
		if !strings.HasPrefix(comment.Body, "**Claude finished ") {
			continue
		}
		filtered = append(filtered, claudeSignal{
			Body: comment.Body,
			At:   comment.CreatedAt,
		})
	}
	if len(filtered) == 0 {
		return claudeSignal{}, false
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].At < filtered[j].At
	})
	return filtered[len(filtered)-1], true
}

func latestClaudeSignalBody(reviewsJSON, commentsJSON, loginPattern string) string {
	signals := make([]claudeSignal, 0, 2)
	if signal, ok := latestMatchingReviewSignal(reviewsJSON, loginPattern); ok {
		signals = append(signals, signal)
	}
	if signal, ok := latestMatchingIssueCommentSignal(commentsJSON, loginPattern); ok {
		signals = append(signals, signal)
	}
	if len(signals) == 0 {
		return ""
	}

	sort.Slice(signals, func(i, j int) bool {
		return signals[i].At < signals[j].At
	})
	return signals[len(signals)-1].Body
}

func flattenIssueComments(commentsJSON string) ([]issueComment, error) {
	raws := []json.RawMessage{}
	if err := json.Unmarshal([]byte(commentsJSON), &raws); err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, nil
	}

	first := bytes.TrimSpace(raws[0])
	if len(first) > 0 && first[0] == '[' {
		all := []issueComment{}
		for _, raw := range raws {
			page := []issueComment{}
			if err := json.Unmarshal(raw, &page); err != nil {
				return nil, err
			}
			all = append(all, page...)
		}
		return all, nil
	}

	flat := []issueComment{}
	if err := json.Unmarshal([]byte(commentsJSON), &flat); err != nil {
		return nil, err
	}
	return flat, nil
}

func reviewEndsWithLGTM(body string) bool {
	lgtmSuffixPattern := regexp.MustCompile(`(^|[^[:alnum:]_])LGTM$`)
	body = strings.ReplaceAll(body, "\r", "")
	body = strings.TrimRightFunc(body, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	return lgtmSuffixPattern.MatchString(body)
}

func readyMessage(prNumber int) string {
	return fmt.Sprintf("PR #%d is ready for human merge. CI is green, Claude left LGTM, and there are no merge conflicts.", prNumber)
}

func die(stderr io.Writer, format string, args ...any) {
	fmt.Fprintf(stderr, "check-pr-ready: "+format+"\n", args...)
}

func (realDeps) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func (realDeps) PaneList() (string, error) {
	return runCommand("amux", "list", "--no-cwd")
}

func (realDeps) PaneCapture(pane string) (string, error) {
	return runCommand("amux", "capture", "--format", "json", pane)
}

func (realDeps) SendKeys(pane string, message string) error {
	_, err := runCommand("amux", "send-keys", pane, message, "Enter")
	return err
}

func (realDeps) PRList(repoOverride string) (string, error) {
	args := []string{"pr", "list"}
	if repoOverride != "" {
		args = append(args, "-R", repoOverride)
	}
	args = append(args, "--limit", "200", "--json", "number,title,url,mergeable")
	return runCommand("gh", args...)
}

func (realDeps) PRChecks(prNumber int, repoOverride string) (string, int, error) {
	args := []string{"pr", "checks", strconv.Itoa(prNumber)}
	if repoOverride != "" {
		args = append(args, "-R", repoOverride)
	}
	args = append(args, "--required", "--json", "bucket,name,state")
	return runCommandWithStatus(nil, "gh", args...)
}

func (realDeps) PRReviews(repo string, prNumber int) (string, error) {
	path := fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews?per_page=100", prNumber)
	return runCommandWithEnv([]string{"GH_REPO=" + repo}, "gh", "api", "--paginate", "--slurp", path)
}

func (realDeps) PRIssueComments(repo string, prNumber int) (string, error) {
	path := fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments?per_page=100", prNumber)
	return runCommandWithEnv([]string{"GH_REPO=" + repo}, "gh", "api", "--paginate", "--slurp", path)
}

func runCommand(name string, args ...string) (string, error) {
	out, _, err := runCommandWithStatus(nil, name, args...)
	return out, err
}

func runCommandWithEnv(extraEnv []string, name string, args ...string) (string, error) {
	out, _, err := runCommandWithStatus(extraEnv, name, args...)
	return out, err
}

func runCommandWithStatus(extraEnv []string, name string, args ...string) (string, int, error) {
	cmd := exec.Command(name, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0, nil
	}

	exitErr := &exec.ExitError{}
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode(), err
	}
	return string(out), -1, err
}
