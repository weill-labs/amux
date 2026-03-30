package listing

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

const ListCwdWidth = 36

type ListArgs struct {
	ShowCwd bool
}

func ParseListArgs(args []string) (ListArgs, error) {
	parsed := ListArgs{ShowCwd: true}
	for _, arg := range args {
		switch arg {
		case "--no-cwd":
			parsed.ShowCwd = false
		default:
			return ListArgs{}, fmt.Errorf("usage: list [--no-cwd]")
		}
	}
	return parsed, nil
}

type PaneEntry struct {
	PaneID        uint32
	Name          string
	Host          string
	WindowName    string
	Task          string
	Cwd           string
	GitBranch     string
	Idle          string
	PR            string
	KV            map[string]string
	TrackedPRs    []proto.TrackedPR
	TrackedIssues []proto.TrackedIssue
	Active        bool
	Lead          bool
}

func FormatPaneList(entries []PaneEntry, home string, showCwd bool) string {
	if len(entries) == 0 {
		return "No panes.\n"
	}

	var buf strings.Builder
	if showCwd {
		fmt.Fprintf(&buf, "%-6s %-20s %-15s %-30s %-9s %-36s %-10s %-12s %s\n", "PANE", "NAME", "HOST", "BRANCH", "IDLE", "CWD", "WINDOW", "TASK", "META")
	} else {
		fmt.Fprintf(&buf, "%-6s %-20s %-15s %-30s %-9s %-10s %-12s %s\n", "PANE", "NAME", "HOST", "BRANCH", "IDLE", "WINDOW", "TASK", "META")
	}
	for _, entry := range entries {
		fmt.Fprint(&buf, formatPaneListRow(entry, home, showCwd))
	}
	return buf.String()
}

func FormatPaneListBranch(entry PaneEntry) string {
	branch := entry.GitBranch
	if entry.PR != "" {
		branch += " #" + entry.PR
	}
	return branch
}

func formatPaneListRow(entry PaneEntry, home string, showCwd bool) string {
	active := " "
	if entry.Active {
		active = "*"
	}
	paneID := fmt.Sprintf("%s%d", active, entry.PaneID)
	branch := FormatPaneListBranch(entry)
	meta := formatPaneListMeta(entry)
	idle := entry.Idle
	if idle == "" {
		idle = "--"
	}
	if showCwd {
		return fmt.Sprintf("%-6s %-20s %-15s %-30s %-9s %-36s %-10s %-12s %s\n",
			paneID, entry.Name, entry.Host, branch, idle, FormatListCwd(entry.Cwd, home, ListCwdWidth), entry.WindowName, entry.Task, meta)
	}
	return fmt.Sprintf("%-6s %-20s %-15s %-30s %-9s %-10s %-12s %s\n",
		paneID, entry.Name, entry.Host, branch, idle, entry.WindowName, entry.Task, meta)
}

func formatPaneListMeta(entry PaneEntry) string {
	parts := make([]string, 0, len(entry.KV)+1)
	if entry.Lead {
		parts = append(parts, "lead")
	}

	if len(entry.KV) == 0 {
		return strings.Join(parts, " ")
	}

	keys := make([]string, 0, len(entry.KV))
	for key := range entry.KV {
		if key == "task" || key == "branch" || key == "pr" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, entry.KV[key]))
	}
	return strings.Join(parts, " ")
}

func FormatListCwd(cwd, home string, max int) string {
	if cwd == "" || max <= 0 {
		return ""
	}
	short := collapseListHome(cwd, home)
	if len([]rune(short)) <= max {
		return short
	}
	if strings.HasPrefix(short, "~/") {
		return truncateListPathSegments(short, "~/…/", max)
	}
	return truncateListPathSegments(short, "…/", max)
}

func collapseListHome(cwd, home string) string {
	if short, ok := collapseListHomePrefix(cwd, home); ok {
		return short
	}

	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return cwd
	}
	if short, ok := collapseListHomePrefix(cwd, resolvedHome); ok {
		return short
	}

	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return cwd
	}
	if short, ok := collapseListHomePrefix(resolvedCwd, resolvedHome); ok {
		return short
	}
	return cwd
}

func collapseListHomePrefix(cwd, home string) (string, bool) {
	if home == "" {
		return "", false
	}
	if cwd == home {
		return "~", true
	}
	if strings.HasPrefix(cwd, home+"/") {
		return "~" + strings.TrimPrefix(cwd, home), true
	}
	return "", false
}

func truncateListPathSegments(path, prefix string, max int) string {
	if len([]rune(prefix)) >= max {
		return truncateListPrefix(prefix, max)
	}

	remaining := max - len([]rune(prefix))
	parts := strings.Split(path, "/")
	switch {
	case strings.HasPrefix(path, "~/"):
		parts = parts[2:]
	case strings.HasPrefix(path, "/"):
		parts = parts[1:]
	}

	tail := ""
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := parts[i]
		if tail != "" {
			candidate += "/" + tail
		}
		if len([]rune(candidate)) > remaining {
			break
		}
		tail = candidate
	}
	if tail == "" && len(parts) > 0 {
		tail = truncateListTail(parts[len(parts)-1], remaining)
	}
	return prefix + tail
}

func truncateListPrefix(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func truncateListTail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[len(runes)-max:])
}

type SessionStatus struct {
	Total       int
	WindowCount int
	Zoomed      string
}

func FormatStatus(snap SessionStatus, buildVersion string) string {
	statusLine := fmt.Sprintf("windows: %d, panes: %d total", snap.WindowCount, snap.Total)
	if snap.Zoomed != "" {
		statusLine += fmt.Sprintf(", %s zoomed", snap.Zoomed)
	}
	if buildVersion != "" {
		statusLine += fmt.Sprintf(", build: %s", buildVersion)
	}
	return statusLine + "\n"
}

type WindowEntry struct {
	Index     int
	Name      string
	PaneCount int
	Active    bool
}

func FormatWindowList(entries []WindowEntry) string {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-6s %-20s %-8s\n", "WIN", "NAME", "PANES"))
	for _, entry := range entries {
		active := " "
		if entry.Active {
			active = "*"
		}
		output.WriteString(fmt.Sprintf("%-6s %-20s %-8d\n",
			fmt.Sprintf("%s%d:", active, entry.Index), entry.Name, entry.PaneCount))
	}
	return output.String()
}

type ClientEntry struct {
	ID           string
	DisplayPanes string
	Chooser      string
	Size         string
	SizeOwner    bool
	Capabilities string
}

func FormatClientList(entries []ClientEntry) string {
	if len(entries) == 0 {
		return "No clients attached.\n"
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-10s %-8s %-15s %-10s %-10s %s\n", "CLIENT", "OWNER", "SIZE", "DISPLAY_PANES", "CHOOSER", "CAPABILITIES"))
	for _, entry := range entries {
		owner := ""
		if entry.SizeOwner {
			owner = "*"
		}
		output.WriteString(fmt.Sprintf("%-10s %-8s %-15s %-10s %-10s %s\n",
			entry.ID, owner, entry.Size, entry.DisplayPanes, entry.Chooser, entry.Capabilities))
	}
	return output.String()
}
