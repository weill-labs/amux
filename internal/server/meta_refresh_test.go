package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type stubTrackedMetaResolver struct {
	mu                     sync.Mutex
	prStatus               map[int]proto.TrackedStatus
	prErr                  map[int]error
	issueStatus            map[string]proto.TrackedStatus
	issueErr               map[string]error
	issueTransitionErr     map[string]error
	issueTransitionCalls   []string
	issueTransitionStarted chan string
	issueTransitionRelease <-chan struct{}
	prCalls                []stubPRCall
	issueCalls             []string
}

type stubPRCall struct {
	cwd    string
	number int
}

func (r *stubTrackedMetaResolver) ResolvePR(cwd string, number int) (proto.TrackedStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prCalls = append(r.prCalls, stubPRCall{cwd: cwd, number: number})
	if err := r.prErr[number]; err != nil {
		return proto.TrackedStatusUnknown, err
	}
	if status, ok := r.prStatus[number]; ok {
		return status, nil
	}
	return proto.TrackedStatusActive, nil
}

func (r *stubTrackedMetaResolver) ResolveIssue(id string) (proto.TrackedStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.issueCalls = append(r.issueCalls, id)
	if err := r.issueErr[id]; err != nil {
		return proto.TrackedStatusUnknown, err
	}
	if status, ok := r.issueStatus[id]; ok {
		return status, nil
	}
	return proto.TrackedStatusActive, nil
}

func (r *stubTrackedMetaResolver) TransitionIssueToStartedIfNeeded(id string) error {
	r.mu.Lock()
	r.issueTransitionCalls = append(r.issueTransitionCalls, id)
	err := r.issueTransitionErr[id]
	started := r.issueTransitionStarted
	release := r.issueTransitionRelease
	r.mu.Unlock()

	if started != nil {
		select {
		case started <- id:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return err
}

func (r *stubTrackedMetaResolver) prCallsSnapshot() []stubPRCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stubPRCall(nil), r.prCalls...)
}

func (r *stubTrackedMetaResolver) issueCallsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.issueCalls...)
}

func (r *stubTrackedMetaResolver) issueTransitionCallsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.issueTransitionCalls...)
}

func TestCmdRefreshMetaUsesActivePaneWhenPaneOmitted(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	resolver := &stubTrackedMetaResolver{
		prStatus: map[int]proto.TrackedStatus{
			42: proto.TrackedStatusCompleted,
		},
		issueStatus: map[string]proto.TrackedStatus{
			"LAB-450": proto.TrackedStatusCompleted,
		},
	}
	sess.TrackedMetaResolver = resolver

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/repo",
		TrackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusUnknown},
		},
		TrackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusUnknown},
		},
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	res := runTestCommand(t, srv, sess, "refresh-meta")
	if res.cmdErr != "" {
		t.Fatalf("refresh-meta error: %s", res.cmdErr)
	}

	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta {
		return sess.findPaneByID(pane.ID).Meta
	})
	if got := meta.TrackedPRs[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked PR status = %q, want completed", got)
	}
	if meta.TrackedPRs[0].Stale {
		t.Fatal("tracked PR should not be stale after successful refresh")
	}
	if meta.TrackedPRs[0].CheckedAt == "" {
		t.Fatal("tracked PR checked_at should be set")
	}
	if got := meta.TrackedIssues[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked issue status = %q, want completed", got)
	}
	if meta.TrackedIssues[0].Stale {
		t.Fatal("tracked issue should not be stale after successful refresh")
	}
	if meta.TrackedIssues[0].CheckedAt == "" {
		t.Fatal("tracked issue checked_at should be set")
	}

	prCalls := resolver.prCallsSnapshot()
	if len(prCalls) != 1 || prCalls[0].cwd != "/tmp/repo" || prCalls[0].number != 42 {
		t.Fatalf("resolver PR calls = %#v, want [/tmp/repo 42]", prCalls)
	}
	issueCalls := resolver.issueCallsSnapshot()
	if len(issueCalls) != 1 || issueCalls[0] != "LAB-450" {
		t.Fatalf("resolver issue calls = %#v, want [LAB-450]", issueCalls)
	}
}

func TestCmdRefreshMetaUsesExplicitPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	resolver := &stubTrackedMetaResolver{
		prStatus: map[int]proto.TrackedStatus{
			73: proto.TrackedStatusCompleted,
		},
	}
	sess.TrackedMetaResolver = resolver

	pane1 := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/active",
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	pane2 := newProxyPane(2, mux.PaneMeta{
		Name:  "pane-2",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(1),
		Dir:   "/tmp/target",
		TrackedPRs: []proto.TrackedPR{
			{Number: 73, Status: proto.TrackedStatusUnknown},
		},
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane1, pane2}
	window.ActivePane = pane1

	res := runTestCommand(t, srv, sess, "refresh-meta", "pane-2")
	if res.cmdErr != "" {
		t.Fatalf("refresh-meta error: %s", res.cmdErr)
	}

	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta {
		return sess.findPaneByID(pane2.ID).Meta
	})
	if got := meta.TrackedPRs[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked PR status = %q, want completed", got)
	}
	prCalls := resolver.prCallsSnapshot()
	if len(prCalls) != 1 || prCalls[0].cwd != "/tmp/target" || prCalls[0].number != 73 {
		t.Fatalf("resolver PR calls = %#v, want [/tmp/target 73]", prCalls)
	}
}

func TestCmdAddMetaReaddsRefreshExistingRefsAndMarksFailuresStale(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	resolver := &stubTrackedMetaResolver{
		prErr: map[int]error{
			42: errors.New("gh failed"),
		},
		issueErr: map[string]error{
			"LAB-450": errors.New("linear failed"),
		},
	}
	sess.TrackedMetaResolver = resolver

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/repo",
		TrackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusCompleted, CheckedAt: "old-pr"},
		},
		TrackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusActive, CheckedAt: "old-issue"},
		},
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	res := runTestCommand(t, srv, sess, "add-meta", "pane-1", "pr=42", "issue=LAB-450")
	if res.cmdErr != "" {
		t.Fatalf("add-meta error: %s", res.cmdErr)
	}

	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta {
		return sess.findPaneByID(pane.ID).Meta
	})
	if got := meta.TrackedPRs[0].Status; got != proto.TrackedStatusCompleted {
		t.Fatalf("tracked PR status after failed refresh = %q, want completed", got)
	}
	if !meta.TrackedPRs[0].Stale {
		t.Fatal("tracked PR should be stale after failed refresh")
	}
	if meta.TrackedPRs[0].CheckedAt == "" || meta.TrackedPRs[0].CheckedAt == "old-pr" {
		t.Fatalf("tracked PR checked_at = %q, want updated timestamp", meta.TrackedPRs[0].CheckedAt)
	}
	if got := meta.TrackedIssues[0].Status; got != proto.TrackedStatusActive {
		t.Fatalf("tracked issue status after failed refresh = %q, want active", got)
	}
	if !meta.TrackedIssues[0].Stale {
		t.Fatal("tracked issue should be stale after failed refresh")
	}
	if meta.TrackedIssues[0].CheckedAt == "" || meta.TrackedIssues[0].CheckedAt == "old-issue" {
		t.Fatalf("tracked issue checked_at = %q, want updated timestamp", meta.TrackedIssues[0].CheckedAt)
	}
}

func TestCmdAddMetaTransitionsTrackedIssuesAsync(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	release := make(chan struct{})
	resolver := &stubTrackedMetaResolver{
		issueTransitionStarted: make(chan string, 1),
		issueTransitionRelease: release,
	}
	sess.TrackedMetaResolver = resolver

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/repo",
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	done := make(chan struct {
		output string
		cmdErr string
	}, 1)
	go func() {
		done <- runTestCommand(t, srv, sess, "add-meta", "pane-1", "issue=LAB-488")
	}()

	select {
	case got := <-resolver.issueTransitionStarted:
		if got != "LAB-488" {
			t.Fatalf("transition started for %q, want LAB-488", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async issue transition")
	}

	select {
	case res := <-done:
		if res.cmdErr != "" {
			t.Fatalf("add-meta error: %s", res.cmdErr)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("add-meta blocked on issue transition")
	}
	close(release)

	if got := resolver.issueTransitionCallsSnapshot(); len(got) != 1 || got[0] != "LAB-488" {
		t.Fatalf("issue transition calls = %#v, want [LAB-488]", got)
	}

	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta {
		return sess.findPaneByID(pane.ID).Meta
	})
	if got := len(meta.TrackedIssues); got != 1 {
		t.Fatalf("tracked issues len = %d, want 1", got)
	}
	if got := meta.TrackedIssues[0].ID; got != "LAB-488" {
		t.Fatalf("tracked issue id = %q, want LAB-488", got)
	}
}

func TestCmdAddMetaSurfacesAsyncIssueTransitionFailures(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	resolver := &stubTrackedMetaResolver{
		issueTransitionErr: map[string]error{
			"LAB-488": errors.New("Linear issue LAB-488: LINEAR_API_KEY is not set"),
		},
	}
	sess.TrackedMetaResolver = resolver

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
		Dir:   "/tmp/repo",
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{window}
	sess.ActiveWindowID = window.ID
	sess.Panes = []*mux.Pane{pane}

	res := runTestCommand(t, srv, sess, "add-meta", "pane-1", "issue=LAB-488")
	if res.cmdErr != "" {
		t.Fatalf("add-meta error: %s", res.cmdErr)
	}

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) string {
			return sess.notice
		}) == "add-meta: Linear issue LAB-488: LINEAR_API_KEY is not set"
	})
}

func TestCmdRefreshMetaUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "refresh-meta", "pane-1", "extra")
	if got := res.cmdErr; got != "usage: refresh-meta [pane]" {
		t.Fatalf("refresh-meta usage error = %q, want usage string", got)
	}
}

func TestExternalTrackedMetaResolverResolvePR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cwd        string
		out        []byte
		err        error
		wantStatus proto.TrackedStatus
		wantErr    string
		wantCalls  int
	}{
		{
			name:       "missing cwd",
			wantStatus: proto.TrackedStatusUnknown,
			wantErr:    "pane cwd is unavailable",
		},
		{
			name:       "merged pr is completed",
			cwd:        "/tmp/repo",
			out:        []byte("2026-03-26T00:00:00Z\n"),
			wantStatus: proto.TrackedStatusCompleted,
			wantCalls:  1,
		},
		{
			name:       "null mergedAt stays active",
			cwd:        "/tmp/repo",
			out:        []byte("null\n"),
			wantStatus: proto.TrackedStatusActive,
			wantCalls:  1,
		},
		{
			name:       "gh failure bubbles up",
			cwd:        "/tmp/repo",
			err:        errors.New("boom"),
			wantStatus: proto.TrackedStatusUnknown,
			wantErr:    "gh pr view 42: boom",
			wantCalls:  1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			resolver := &externalTrackedMetaResolver{
				runGH: func(dir string, args ...string) ([]byte, error) {
					calls++
					if dir != tt.cwd {
						t.Fatalf("runGH dir = %q, want %q", dir, tt.cwd)
					}
					return tt.out, tt.err
				},
			}

			got, err := resolver.ResolvePR(tt.cwd, 42)
			if got != tt.wantStatus {
				t.Fatalf("ResolvePR() status = %q, want %q", got, tt.wantStatus)
			}
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ResolvePR() err = %v, want nil", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("ResolvePR() err = %v, want substring %q", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Fatalf("runGH calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestExternalTrackedMetaResolverResolveIssue(t *testing.T) {
	t.Parallel()

	t.Run("missing token", func(t *testing.T) {
		t.Parallel()

		resolver := &externalTrackedMetaResolver{}
		got, err := resolver.ResolveIssue("LAB-450")
		if got != proto.TrackedStatusUnknown {
			t.Fatalf("ResolveIssue() status = %q, want unknown", got)
		}
		if err == nil || !strings.Contains(err.Error(), "LINEAR_API_KEY is not set") {
			t.Fatalf("ResolveIssue() err = %v, want missing token error", err)
		}
	})

	tests := []struct {
		name       string
		statusCode int
		body       any
		wantStatus proto.TrackedStatus
		wantErr    string
	}{
		{
			name:       "completed issue",
			statusCode: http.StatusOK,
			body: map[string]any{
				"data": map[string]any{
					"issue": map[string]any{
						"state": map[string]any{"type": "completed"},
					},
				},
			},
			wantStatus: proto.TrackedStatusCompleted,
		},
		{
			name:       "non-completed issue stays active",
			statusCode: http.StatusOK,
			body: map[string]any{
				"data": map[string]any{
					"issue": map[string]any{
						"state": map[string]any{"type": "started"},
					},
				},
			},
			wantStatus: proto.TrackedStatusActive,
		},
		{
			name:       "graphql error is returned",
			statusCode: http.StatusOK,
			body: map[string]any{
				"errors": []map[string]any{{"message": "nope"}},
			},
			wantStatus: proto.TrackedStatusUnknown,
			wantErr:    "Linear issue LAB-450: nope",
		},
		{
			name:       "missing issue is returned",
			statusCode: http.StatusOK,
			body: map[string]any{
				"data": map[string]any{"issue": nil},
			},
			wantStatus: proto.TrackedStatusUnknown,
			wantErr:    "Linear issue LAB-450: not found",
		},
		{
			name:       "http error status is returned",
			statusCode: http.StatusBadGateway,
			body:       map[string]any{},
			wantStatus: proto.TrackedStatusUnknown,
			wantErr:    "unexpected status 502 Bad Gateway",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("request method = %s, want POST", r.Method)
				}
				if got := r.Header.Get("Authorization"); got != "token" {
					t.Fatalf("authorization header = %q, want token", got)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("ReadAll(request body): %v", err)
				}
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("Unmarshal(request body): %v", err)
				}
				variables, ok := payload["variables"].(map[string]any)
				if !ok || variables["id"] != "LAB-450" {
					t.Fatalf("request variables = %#v, want id LAB-450", payload["variables"])
				}

				w.WriteHeader(tt.statusCode)
				if err := json.NewEncoder(w).Encode(tt.body); err != nil {
					t.Fatalf("Encode(response body): %v", err)
				}
			}))
			defer srv.Close()

			resolver := &externalTrackedMetaResolver{
				httpClient:     srv.Client(),
				linearToken:    "token",
				linearEndpoint: srv.URL,
			}

			got, err := resolver.ResolveIssue("LAB-450")
			if got != tt.wantStatus {
				t.Fatalf("ResolveIssue() status = %q, want %q", got, tt.wantStatus)
			}
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ResolveIssue() err = %v, want nil", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("ResolveIssue() err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestExternalTrackedMetaResolverTransitionIssueToStartedIfNeeded(t *testing.T) {
	t.Parallel()

	t.Run("missing token", func(t *testing.T) {
		t.Parallel()

		resolver := &externalTrackedMetaResolver{}
		err := resolver.TransitionIssueToStartedIfNeeded("LAB-488")
		if err == nil || !strings.Contains(err.Error(), "LINEAR_API_KEY is not set") {
			t.Fatalf("TransitionIssueToStartedIfNeeded() err = %v, want missing token error", err)
		}
	})

	t.Run("transitions backlog issue to first started state", func(t *testing.T) {
		t.Parallel()

		var mutationStateID string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Query     string                 `json:"query"`
				Variables map[string]interface{} `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			switch {
			case strings.Contains(payload.Query, "issueUpdate"):
				stateID, _ := payload.Variables["stateId"].(string)
				mutationStateID = stateID
				_, _ = io.WriteString(w, `{"data":{"issueUpdate":{"success":true,"issue":{"id":"LAB-488"}}}}`)
			default:
				_, _ = io.WriteString(w, `{"data":{"issue":{"state":{"type":"backlog"},"team":{"states":{"nodes":[{"id":"started-later","type":"started","position":20},{"id":"backlog-1","type":"backlog","position":5},{"id":"started-first","type":"started","position":10}]}}}}}`)
			}
		}))
		defer server.Close()

		resolver := &externalTrackedMetaResolver{
			httpClient:     server.Client(),
			linearToken:    "token",
			linearEndpoint: server.URL,
		}

		if err := resolver.TransitionIssueToStartedIfNeeded("LAB-488"); err != nil {
			t.Fatalf("TransitionIssueToStartedIfNeeded() err = %v, want nil", err)
		}
		if mutationStateID != "started-first" {
			t.Fatalf("issueUpdate stateId = %q, want started-first", mutationStateID)
		}
	})

	t.Run("skips transition for started issue", func(t *testing.T) {
		t.Parallel()

		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			_, _ = io.WriteString(w, `{"data":{"issue":{"state":{"type":"started"},"team":{"states":{"nodes":[{"id":"started-first","type":"started","position":10}]}}}}}`)
		}))
		defer server.Close()

		resolver := &externalTrackedMetaResolver{
			httpClient:     server.Client(),
			linearToken:    "token",
			linearEndpoint: server.URL,
		}

		if err := resolver.TransitionIssueToStartedIfNeeded("LAB-488"); err != nil {
			t.Fatalf("TransitionIssueToStartedIfNeeded() err = %v, want nil", err)
		}
		if requests != 1 {
			t.Fatalf("Linear requests = %d, want 1 query and no mutation", requests)
		}
	})
}

func TestApplyTrackedMetaRefreshResults(t *testing.T) {
	t.Parallel()

	meta := mux.PaneMeta{
		TrackedPRs: []proto.TrackedPR{
			{Number: 42},
			{Number: 99, Status: proto.TrackedStatusCompleted, CheckedAt: "old-pr"},
		},
		TrackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusActive, CheckedAt: "old-issue"},
			{ID: "LAB-999", Status: proto.TrackedStatusCompleted, CheckedAt: "keep"},
		},
	}

	changed := applyTrackedMetaRefreshResults(&meta,
		[]trackedPRRefreshResult{
			{number: 42, err: errors.New("gh failed"), checkedAt: "pr-failed"},
			{number: 99, status: proto.TrackedStatusActive, checkedAt: "pr-ok"},
		},
		[]trackedIssueRefreshResult{
			{id: "LAB-450", err: errors.New("linear failed"), checkedAt: "issue-failed"},
		},
	)
	if !changed {
		t.Fatal("applyTrackedMetaRefreshResults() = false, want true")
	}

	if got := meta.TrackedPRs[0]; got.Status != proto.TrackedStatusUnknown || !got.Stale || got.CheckedAt != "pr-failed" {
		t.Fatalf("blank-status PR after failed refresh = %#v, want unknown+stale+checked_at", got)
	}
	if got := meta.TrackedPRs[1]; got.Status != proto.TrackedStatusActive || got.Stale || got.CheckedAt != "pr-ok" {
		t.Fatalf("successful PR refresh = %#v, want active+fresh+checked_at", got)
	}
	if got := meta.TrackedIssues[0]; got.Status != proto.TrackedStatusActive || !got.Stale || got.CheckedAt != "issue-failed" {
		t.Fatalf("failed issue refresh = %#v, want active preserved + stale + checked_at", got)
	}
	if got := meta.TrackedIssues[1]; got.Status != proto.TrackedStatusCompleted || got.CheckedAt != "keep" {
		t.Fatalf("unmatched issue should remain unchanged, got %#v", got)
	}

	unchanged := applyTrackedMetaRefreshResults(&meta, nil, nil)
	if unchanged {
		t.Fatal("applyTrackedMetaRefreshResults(nil, nil) = true, want false")
	}
}
