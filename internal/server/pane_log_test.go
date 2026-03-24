package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPaneLogKeepsLastEntriesInOrder(t *testing.T) {
	t.Parallel()

	log := newPaneLog(3)
	base := time.Date(2026, time.March, 23, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		log.Append(PaneLogEntry{
			Timestamp:  base.Add(time.Duration(i) * time.Second),
			Event:      paneLogEventExit,
			PaneID:     uint32(i),
			PaneName:   fmt.Sprintf("pane-%d", i),
			Host:       "local",
			Cwd:        fmt.Sprintf("/tmp/pane-%d", i),
			GitBranch:  fmt.Sprintf("branch-%d", i),
			ExitReason: fmt.Sprintf("reason-%d", i),
		})
	}

	got := log.Snapshot()
	if len(got) != 3 {
		t.Fatalf("len(snapshot) = %d, want 3", len(got))
	}

	for i, want := range []struct {
		name      string
		cwd       string
		gitBranch string
		reason    string
	}{
		{name: "pane-3", cwd: "/tmp/pane-3", gitBranch: "branch-3", reason: "reason-3"},
		{name: "pane-4", cwd: "/tmp/pane-4", gitBranch: "branch-4", reason: "reason-4"},
		{name: "pane-5", cwd: "/tmp/pane-5", gitBranch: "branch-5", reason: "reason-5"},
	} {
		if got[i].PaneName != want.name {
			t.Fatalf("snapshot[%d].PaneName = %q, want %q", i, got[i].PaneName, want.name)
		}
		if got[i].Cwd != want.cwd {
			t.Fatalf("snapshot[%d].Cwd = %q, want %q", i, got[i].Cwd, want.cwd)
		}
		if got[i].GitBranch != want.gitBranch {
			t.Fatalf("snapshot[%d].GitBranch = %q, want %q", i, got[i].GitBranch, want.gitBranch)
		}
		if got[i].ExitReason != want.reason {
			t.Fatalf("snapshot[%d].ExitReason = %q, want %q", i, got[i].ExitReason, want.reason)
		}
	}
}

func TestAppendPaneLogSnapshotsExitContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		event          string
		liveCwd        string
		metaDir        string
		gitBranch      string
		wantCwd        string
		wantGitBranch  string
		wantExitReason string
	}{
		{
			name:           "exit prefers live cwd",
			event:          paneLogEventExit,
			liveCwd:        "/tmp/live-cwd",
			metaDir:        "/tmp/meta-dir",
			gitBranch:      "feat/live",
			wantCwd:        "/tmp/live-cwd",
			wantGitBranch:  "feat/live",
			wantExitReason: "killed",
		},
		{
			name:           "exit falls back to meta dir",
			event:          paneLogEventExit,
			metaDir:        "/tmp/meta-dir",
			gitBranch:      "feat/fallback",
			wantCwd:        "/tmp/meta-dir",
			wantGitBranch:  "feat/fallback",
			wantExitReason: "killed",
		},
		{
			name:           "create leaves context empty",
			event:          paneLogEventCreate,
			liveCwd:        "/tmp/live-cwd",
			metaDir:        "/tmp/meta-dir",
			gitBranch:      "feat/create",
			wantExitReason: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			pane := newTestPane(sess, 1, "pane-1")
			if tt.liveCwd != "" {
				pane.ApplyCwdBranch(tt.liveCwd, "")
			}
			pane.Meta.Dir = tt.metaDir
			pane.Meta.GitBranch = tt.gitBranch

			sess.appendPaneLog(tt.event, pane, tt.wantExitReason)
			got := sess.ensurePaneLog().Snapshot()
			if len(got) != 1 {
				t.Fatalf("len(snapshot) = %d, want 1", len(got))
			}
			if got[0].Cwd != tt.wantCwd {
				t.Fatalf("snapshot[0].Cwd = %q, want %q", got[0].Cwd, tt.wantCwd)
			}
			if got[0].GitBranch != tt.wantGitBranch {
				t.Fatalf("snapshot[0].GitBranch = %q, want %q", got[0].GitBranch, tt.wantGitBranch)
			}
			if got[0].ExitReason != tt.wantExitReason {
				t.Fatalf("snapshot[0].ExitReason = %q, want %q", got[0].ExitReason, tt.wantExitReason)
			}
		})
	}
}

func TestCmdPaneLogIncludesExitContextColumns(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	sess.paneLog = newPaneLog(10)
	sess.paneLog.Append(PaneLogEntry{
		Timestamp: time.Date(2026, time.March, 23, 12, 0, 0, 0, time.UTC),
		Event:     paneLogEventCreate,
		PaneID:    1,
		PaneName:  "pane-1",
		Host:      "local",
	})
	sess.paneLog.Append(PaneLogEntry{
		Timestamp:  time.Date(2026, time.March, 23, 12, 0, 1, 0, time.UTC),
		Event:      paneLogEventExit,
		PaneID:     2,
		PaneName:   "pane-2",
		Host:       "local",
		Cwd:        "/tmp/lab-404",
		GitBranch:  "feat/lab-404",
		ExitReason: "killed",
	})

	res := runTestCommand(t, srv, sess, "pane-log")
	if res.cmdErr != "" {
		t.Fatalf("pane-log error: %s", res.cmdErr)
	}

	lines := strings.Split(strings.TrimSpace(res.output), "\n")
	if len(lines) != 3 {
		t.Fatalf("pane-log lines = %d, want 3\n%s", len(lines), res.output)
	}

	if got := strings.Fields(lines[0]); len(got) != 8 || got[5] != "CWD" || got[6] != "GIT_BRANCH" || got[7] != "REASON" {
		t.Fatalf("pane-log header = %q, want CWD/GIT_BRANCH/REASON columns", lines[0])
	}

	createFields := strings.Fields(lines[1])
	if len(createFields) != 8 {
		t.Fatalf("create row fields = %v, want 8 columns", createFields)
	}
	if createFields[1] != paneLogEventCreate || createFields[5] != "-" || createFields[6] != "-" || createFields[7] != "-" {
		t.Fatalf("create row = %v, want empty context rendered as -", createFields)
	}

	exitFields := strings.Fields(lines[2])
	if len(exitFields) != 8 {
		t.Fatalf("exit row fields = %v, want 8 columns", exitFields)
	}
	if exitFields[1] != paneLogEventExit || exitFields[5] != "/tmp/lab-404" || exitFields[6] != "feat/lab-404" || exitFields[7] != "killed" {
		t.Fatalf("exit row = %v, want full cwd, git branch, and reason", exitFields)
	}
}

func TestPaneLogExitReason(t *testing.T) {
	t.Parallel()

	log := newPaneLog(10)
	log.Append(PaneLogEntry{
		Event:      paneLogEventCreate,
		PaneName:   "pane-1",
		Host:       "local",
		ExitReason: "",
	})
	log.Append(PaneLogEntry{
		Event:      paneLogEventExit,
		PaneName:   "pane-1",
		Host:       "local",
		ExitReason: "exit 0",
	})

	got := log.Snapshot()
	if len(got) != 2 {
		t.Fatalf("len(snapshot) = %d, want 2", len(got))
	}
	if got[0].ExitReason != "" {
		t.Errorf("create entry reason = %q, want empty", got[0].ExitReason)
	}
	if got[1].ExitReason != "exit 0" {
		t.Errorf("exit entry reason = %q, want %q", got[1].ExitReason, "exit 0")
	}
}

func TestPaneLogNilSafe(t *testing.T) {
	t.Parallel()

	var log *PaneLog
	log.Append(PaneLogEntry{Event: paneLogEventCreate}) // must not panic
	if got := log.Snapshot(); got != nil {
		t.Fatalf("nil log snapshot = %v, want nil", got)
	}
}
