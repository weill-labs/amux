package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

const linearGraphQLEndpoint = "https://api.linear.app/graphql"

// TrackedMetaResolver resolves external completion state for tracked PRs and issues.
type TrackedMetaResolver interface {
	ResolvePR(cwd string, number int) (proto.TrackedStatus, error)
	ResolveIssue(id string) (proto.TrackedStatus, error)
	TransitionIssueToStartedIfNeeded(id string) error
}

type externalTrackedMetaResolver struct {
	runGH          func(dir string, args ...string) ([]byte, error)
	httpClient     *http.Client
	linearToken    string
	linearEndpoint string
}

type trackedPaneRefreshSnapshot struct {
	paneID        uint32
	cwd           string
	pid           int
	trackedPRs    []proto.TrackedPR
	trackedIssues []proto.TrackedIssue
}

type trackedPRRefreshResult struct {
	number    int
	status    proto.TrackedStatus
	checkedAt string
	err       error
}

type trackedIssueRefreshResult struct {
	id        string
	status    proto.TrackedStatus
	checkedAt string
	err       error
}

type linearIssueResponse struct {
	Data struct {
		Issue *struct {
			State struct {
				Type string `json:"type"`
			} `json:"state"`
		} `json:"issue"`
	} `json:"data"`
	Errors []linearGraphQLError `json:"errors"`
}

type linearGraphQLError struct {
	Message string `json:"message"`
}

type linearStartedStateQueryResponse struct {
	Data struct {
		Issue *struct {
			State struct {
				Type string `json:"type"`
			} `json:"state"`
			Team struct {
				States struct {
					Nodes []linearWorkflowState `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	} `json:"data"`
	Errors []linearGraphQLError `json:"errors"`
}

type linearWorkflowState struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

type linearIssueUpdateResponse struct {
	Data struct {
		IssueUpdate struct {
			Success bool `json:"success"`
			Issue   *struct {
				ID string `json:"id"`
			} `json:"issue"`
		} `json:"issueUpdate"`
	} `json:"data"`
	Errors []linearGraphQLError `json:"errors"`
}

func NewExternalTrackedMetaResolver() TrackedMetaResolver {
	return &externalTrackedMetaResolver{
		runGH: runGHCommand,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		linearToken:    strings.TrimSpace(os.Getenv("LINEAR_API_KEY")),
		linearEndpoint: linearGraphQLEndpoint,
	}
}

func runGHCommand(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

func (r *externalTrackedMetaResolver) ResolvePR(cwd string, number int) (proto.TrackedStatus, error) {
	if strings.TrimSpace(cwd) == "" {
		return proto.TrackedStatusUnknown, fmt.Errorf("pane cwd is unavailable")
	}

	out, err := r.runGH(cwd, "pr", "view", strconv.Itoa(number), "--json", "mergedAt", "--jq", ".mergedAt")
	if err != nil {
		return proto.TrackedStatusUnknown, fmt.Errorf("gh pr view %d: %w", number, err)
	}

	mergedAt := strings.TrimSpace(string(out))
	if mergedAt != "" && mergedAt != "null" {
		return proto.TrackedStatusCompleted, nil
	}
	return proto.TrackedStatusActive, nil
}

func (r *externalTrackedMetaResolver) ResolveIssue(id string) (proto.TrackedStatus, error) {
	decoded := linearIssueResponse{}
	if err := r.doLinearGraphQL(`query($id: String!) { issue(id: $id) { state { type } } }`, map[string]string{
		"id": id,
	}, &decoded); err != nil {
		return proto.TrackedStatusUnknown, fmt.Errorf("Linear issue %s: %w", id, err)
	}
	if msg := firstLinearGraphQLError(decoded.Errors); msg != "" {
		return proto.TrackedStatusUnknown, fmt.Errorf("Linear issue %s: %s", id, msg)
	}
	if decoded.Data.Issue == nil {
		return proto.TrackedStatusUnknown, fmt.Errorf("Linear issue %s: not found", id)
	}
	if strings.EqualFold(decoded.Data.Issue.State.Type, "completed") {
		return proto.TrackedStatusCompleted, nil
	}
	return proto.TrackedStatusActive, nil
}

func (r *externalTrackedMetaResolver) TransitionIssueToStartedIfNeeded(id string) error {
	decoded := linearStartedStateQueryResponse{}
	if err := r.doLinearGraphQL(`query TeamStartedStatuses($id: String!) {
  issue(id: $id) {
    state {
      type
    }
    team {
      states(filter: { type: { eq: "started" } }) {
        nodes {
          id
          type
          position
        }
      }
    }
  }
}`, map[string]string{
		"id": id,
	}, &decoded); err != nil {
		return fmt.Errorf("Linear issue %s: %w", id, err)
	}
	if msg := firstLinearGraphQLError(decoded.Errors); msg != "" {
		return fmt.Errorf("Linear issue %s: %s", id, msg)
	}
	if decoded.Data.Issue == nil {
		return fmt.Errorf("Linear issue %s: not found", id)
	}
	if !linearIssueNeedsStartedTransition(decoded.Data.Issue.State.Type) {
		return nil
	}

	nextStateID := firstStartedWorkflowStateID(decoded.Data.Issue.Team.States.Nodes)
	if nextStateID == "" {
		return fmt.Errorf("Linear issue %s: no started state found", id)
	}

	update := linearIssueUpdateResponse{}
	if err := r.doLinearGraphQL(`mutation IssueUpdate($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) {
    success
    issue {
      id
    }
  }
}`, map[string]string{
		"id":      id,
		"stateId": nextStateID,
	}, &update); err != nil {
		return fmt.Errorf("Linear issue %s: %w", id, err)
	}
	if msg := firstLinearGraphQLError(update.Errors); msg != "" {
		return fmt.Errorf("Linear issue %s: %s", id, msg)
	}
	if !update.Data.IssueUpdate.Success || update.Data.IssueUpdate.Issue == nil {
		return fmt.Errorf("Linear issue %s: issueUpdate failed", id)
	}
	return nil
}

func (r *externalTrackedMetaResolver) doLinearGraphQL(query string, variables any, out any) error {
	if strings.TrimSpace(r.linearToken) == "" {
		return fmt.Errorf("LINEAR_API_KEY is not set")
	}

	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Linear request: %w", err)
	}

	endpoint := r.linearEndpoint
	if endpoint == "" {
		endpoint = linearGraphQLEndpoint
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Linear request: %w", err)
	}
	req.Header.Set("Authorization", r.linearToken)
	req.Header.Set("Content-Type", "application/json")

	client := r.httpClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode Linear response: %w", err)
	}
	return nil
}

func firstStartedWorkflowStateID(states []linearWorkflowState) string {
	filtered := make([]linearWorkflowState, 0, len(states))
	for _, state := range states {
		if strings.EqualFold(strings.TrimSpace(state.Type), "started") {
			filtered = append(filtered, state)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	sorted := append([]linearWorkflowState(nil), filtered...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Position == sorted[j].Position {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].Position < sorted[j].Position
	})
	return sorted[0].ID
}

func firstLinearGraphQLError(errors []linearGraphQLError) string {
	if len(errors) == 0 {
		return ""
	}
	return errors[0].Message
}

func linearIssueNeedsStartedTransition(stateType string) bool {
	switch strings.ToLower(strings.TrimSpace(stateType)) {
	case "backlog", "unstarted":
		return true
	default:
		return false
	}
}

func (snap trackedPaneRefreshSnapshot) resolvedCwd() string {
	if snap.cwd != "" {
		return snap.cwd
	}
	if snap.pid != 0 {
		return mux.PaneCwd(snap.pid)
	}
	return ""
}

func (s *Session) queryTrackedPaneRefreshSnapshot(actorPaneID uint32, ref string) (trackedPaneRefreshSnapshot, error) {
	return enqueueSessionQuery(s, func(sess *Session) (trackedPaneRefreshSnapshot, error) {
		if ref == "" {
			w := sess.windowForActor(actorPaneID)
			if w == nil || w.ActivePane == nil {
				return trackedPaneRefreshSnapshot{}, fmt.Errorf("no active pane")
			}
			pane := w.ActivePane
			return trackedPaneRefreshSnapshot{
				paneID:        pane.ID,
				cwd:           effectivePaneCwd(pane),
				pid:           pane.ProcessPid(),
				trackedPRs:    proto.CloneTrackedPRs(pane.Meta.TrackedPRs),
				trackedIssues: proto.CloneTrackedIssues(pane.Meta.TrackedIssues),
			}, nil
		} else {
			pane, _, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
			if err != nil {
				return trackedPaneRefreshSnapshot{}, err
			}
			return trackedPaneRefreshSnapshot{
				paneID:        pane.ID,
				cwd:           effectivePaneCwd(pane),
				pid:           pane.ProcessPid(),
				trackedPRs:    proto.CloneTrackedPRs(pane.Meta.TrackedPRs),
				trackedIssues: proto.CloneTrackedIssues(pane.Meta.TrackedIssues),
			}, nil
		}
	})
}

func (s *Session) queryAllTrackedPaneRefreshSnapshots() ([]trackedPaneRefreshSnapshot, error) {
	return enqueueSessionQuery(s, func(sess *Session) ([]trackedPaneRefreshSnapshot, error) {
		out := make([]trackedPaneRefreshSnapshot, 0, len(sess.Panes))
		for _, pane := range sess.Panes {
			if len(pane.Meta.TrackedPRs) == 0 && len(pane.Meta.TrackedIssues) == 0 {
				continue
			}
			out = append(out, trackedPaneRefreshSnapshot{
				paneID:        pane.ID,
				cwd:           effectivePaneCwd(pane),
				pid:           pane.ProcessPid(),
				trackedPRs:    proto.CloneTrackedPRs(pane.Meta.TrackedPRs),
				trackedIssues: proto.CloneTrackedIssues(pane.Meta.TrackedIssues),
			})
		}
		return out, nil
	})
}

func (s *Session) refreshTrackedMetaForPaneRef(actorPaneID uint32, ref string) error {
	if s.TrackedMetaResolver == nil {
		return nil
	}

	snap, err := s.queryTrackedPaneRefreshSnapshot(actorPaneID, ref)
	if err != nil {
		return err
	}
	return s.refreshTrackedMetaSnapshot(snap)
}

func (s *Session) refreshTrackedMetaSnapshot(snap trackedPaneRefreshSnapshot) error {
	if s.TrackedMetaResolver == nil {
		return nil
	}
	if len(snap.trackedPRs) == 0 && len(snap.trackedIssues) == 0 {
		return nil
	}

	cwd := snap.resolvedCwd()
	prResults := make([]trackedPRRefreshResult, 0, len(snap.trackedPRs))
	for _, ref := range snap.trackedPRs {
		checkedAt := s.clock().Now().UTC().Format(time.RFC3339Nano)
		status, err := s.TrackedMetaResolver.ResolvePR(cwd, ref.Number)
		prResults = append(prResults, trackedPRRefreshResult{
			number:    ref.Number,
			status:    status,
			checkedAt: checkedAt,
			err:       err,
		})
	}

	issueResults := make([]trackedIssueRefreshResult, 0, len(snap.trackedIssues))
	for _, ref := range snap.trackedIssues {
		checkedAt := s.clock().Now().UTC().Format(time.RFC3339Nano)
		status, err := s.TrackedMetaResolver.ResolveIssue(ref.ID)
		issueResults = append(issueResults, trackedIssueRefreshResult{
			id:        ref.ID,
			status:    status,
			checkedAt: checkedAt,
			err:       err,
		})
	}

	res := s.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane := sess.findPaneByID(snap.paneID)
		if pane == nil {
			return commandMutationResult{}
		}

		changed := applyTrackedMetaRefreshResults(&pane.Meta, prResults, issueResults)
		return commandMutationResult{broadcastLayout: changed}
	})
	if res.err == errSessionShuttingDown {
		return nil
	}
	return res.err
}

func applyTrackedMetaRefreshResults(meta *mux.PaneMeta, prResults []trackedPRRefreshResult, issueResults []trackedIssueRefreshResult) bool {
	changed := false

	prByNumber := make(map[int]trackedPRRefreshResult, len(prResults))
	for _, result := range prResults {
		prByNumber[result.number] = result
	}
	for i := range meta.TrackedPRs {
		result, ok := prByNumber[meta.TrackedPRs[i].Number]
		if !ok {
			continue
		}
		next := meta.TrackedPRs[i]
		if result.err != nil {
			if next.Status == "" {
				next.Status = proto.TrackedStatusUnknown
			}
			next.Stale = true
			next.CheckedAt = result.checkedAt
		} else {
			next.Status = result.status
			next.Stale = false
			next.CheckedAt = result.checkedAt
		}
		if next != meta.TrackedPRs[i] {
			meta.TrackedPRs[i] = next
			changed = true
		}
	}

	issueByID := make(map[string]trackedIssueRefreshResult, len(issueResults))
	for _, result := range issueResults {
		issueByID[result.id] = result
	}
	for i := range meta.TrackedIssues {
		result, ok := issueByID[meta.TrackedIssues[i].ID]
		if !ok {
			continue
		}
		next := meta.TrackedIssues[i]
		if result.err != nil {
			if next.Status == "" {
				next.Status = proto.TrackedStatusUnknown
			}
			next.Stale = true
			next.CheckedAt = result.checkedAt
		} else {
			next.Status = result.status
			next.Stale = false
			next.CheckedAt = result.checkedAt
		}
		if next != meta.TrackedIssues[i] {
			meta.TrackedIssues[i] = next
			changed = true
		}
	}

	return changed
}

func (s *Session) refreshTrackedMetaAsync() {
	if s == nil || s.TrackedMetaResolver == nil {
		return
	}

	go func() {
		snaps, err := s.queryAllTrackedPaneRefreshSnapshots()
		if err != nil {
			return
		}
		for _, snap := range snaps {
			_ = s.refreshTrackedMetaSnapshot(snap)
		}
	}()
}

func (s *Session) transitionTrackedIssuesToStartedAsync(issueIDs []string) {
	if s == nil || s.TrackedMetaResolver == nil || len(issueIDs) == 0 {
		return
	}

	unique := make([]string, 0, len(issueIDs))
	seen := make(map[string]struct{}, len(issueIDs))
	for _, id := range issueIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return
	}

	go func() {
		for _, id := range unique {
			_ = s.TrackedMetaResolver.TransitionIssueToStartedIfNeeded(id)
		}
	}()
}

func (s *Server) SetTrackedMetaResolver(resolver TrackedMetaResolver) {
	if s == nil {
		return
	}
	for _, sess := range s.sessions {
		sess.TrackedMetaResolver = resolver
	}
}

func (s *Server) RefreshTrackedMetaAsync() {
	if s == nil {
		return
	}
	for _, sess := range s.sessions {
		sess.refreshTrackedMetaAsync()
	}
}
