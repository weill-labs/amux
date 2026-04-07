package mux

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestProcessCommandOutputReturnsStdout(t *testing.T) {
	t.Parallel()

	out, err := processCommandOutput("sh", "-c", "printf ok")
	if err != nil {
		t.Fatalf("processCommandOutput() error = %v, want nil", err)
	}
	if got := string(out); got != "ok" {
		t.Fatalf("processCommandOutput() = %q, want %q", got, "ok")
	}
}

func TestProcessCommandOutputTimesOutWhenDescendantKeepsPipeOpen(t *testing.T) {
	t.Parallel()

	start := time.Now()
	_, err := processCommandOutput("sh", "-c", "sleep 30 & printf ok")
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("processCommandOutput() error = %v, want %v", err, exec.ErrWaitDelay)
	}
	if elapsed := time.Since(start); elapsed > 2*processTimeout {
		t.Fatalf("processCommandOutput() took %v, want <= %v", elapsed, 2*processTimeout)
	}
}

func TestShellOnlyChildChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		shellName string
		children  []int
		names     map[int]string
		tree      map[int][]int
		want      bool
	}{
		{
			name:      "empty shell name returns false",
			shellName: "",
			children:  []int{1},
			names:     map[int]string{1: "bash"},
			tree:      map[int][]int{},
			want:      false,
		},
		{
			name:      "non singleton child list returns false",
			shellName: "bash",
			children:  []int{1, 2},
			names:     map[int]string{1: "bash", 2: "bash"},
			tree:      map[int][]int{},
			want:      false,
		},
		{
			name:      "same name chain terminates successfully",
			shellName: "bash",
			children:  []int{1},
			names:     map[int]string{1: "bash", 2: "bash"},
			tree:      map[int][]int{1: {2}},
			want:      true,
		},
		{
			name:      "name mismatch returns false",
			shellName: "bash",
			children:  []int{1},
			names:     map[int]string{1: "zsh"},
			tree:      map[int][]int{},
			want:      false,
		},
		{
			name:      "max depth exceeded returns false",
			shellName: "bash",
			children:  []int{1},
			names: map[int]string{
				1: "bash",
				2: "bash",
				3: "bash",
				4: "bash",
				5: "bash",
				6: "bash",
				7: "bash",
				8: "bash",
			},
			tree: map[int][]int{
				1: {2},
				2: {3},
				3: {4},
				4: {5},
				5: {6},
				6: {7},
				7: {8},
				8: {9},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := shellOnlyChildChainWithLookups(tt.shellName, tt.children,
				func(pid int) string {
					return tt.names[pid]
				},
				func(pid int) []int {
					return tt.tree[pid]
				},
			)
			if got != tt.want {
				t.Fatalf("shellOnlyChildChainWithLookups(%q, %v) = %v, want %v", tt.shellName, tt.children, got, tt.want)
			}
		})
	}
}

func TestShellOnlyForegroundChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		shellPID  int
		shellName string
		leaderPID int
		names     map[int]string
		parents   map[int]int
		want      bool
	}{
		{
			name:      "empty shell name returns false",
			shellPID:  1,
			shellName: "",
			leaderPID: 2,
			names:     map[int]string{2: "bash"},
			parents:   map[int]int{2: 1},
			want:      false,
		},
		{
			name:      "matching shell-only chain returns true",
			shellPID:  1,
			shellName: "bash",
			leaderPID: 3,
			names:     map[int]string{1: "bash", 2: "bash", 3: "bash"},
			parents:   map[int]int{3: 2, 2: 1},
			want:      true,
		},
		{
			name:      "name mismatch returns false",
			shellPID:  1,
			shellName: "bash",
			leaderPID: 3,
			names:     map[int]string{1: "bash", 2: "bash", 3: "sleep"},
			parents:   map[int]int{3: 2, 2: 1},
			want:      false,
		},
		{
			name:      "missing parent chain returns false",
			shellPID:  1,
			shellName: "bash",
			leaderPID: 3,
			names:     map[int]string{3: "bash"},
			parents:   map[int]int{},
			want:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := shellOnlyForegroundChainWithLookups(
				tt.shellPID,
				tt.shellName,
				tt.leaderPID,
				func(pid int) string { return tt.names[pid] },
				func(pid int) int { return tt.parents[pid] },
			)
			if got != tt.want {
				t.Fatalf("shellOnlyForegroundChainWithLookups(%d, %q, %d) = %v, want %v", tt.shellPID, tt.shellName, tt.leaderPID, got, tt.want)
			}
		})
	}
}
