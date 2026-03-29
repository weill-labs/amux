package server

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	listingcmd "github.com/weill-labs/amux/internal/server/commands/listing"
)

func toListingPaneEntry(entry paneListEntry) listingcmd.PaneEntry {
	return listingcmd.PaneEntry{
		PaneID:        entry.paneID,
		Name:          entry.name,
		Host:          entry.host,
		WindowName:    entry.windowName,
		Task:          entry.task,
		Cwd:           entry.cwd,
		GitBranch:     entry.gitBranch,
		Idle:          entry.idle,
		PR:            entry.pr,
		TrackedPRs:    proto.CloneTrackedPRs(entry.prs),
		TrackedIssues: proto.CloneTrackedIssues(entry.issues),
		Active:        entry.active,
		Lead:          entry.lead,
	}
}

func toListingPaneEntries(entries []paneListEntry) []listingcmd.PaneEntry {
	out := make([]listingcmd.PaneEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, toListingPaneEntry(entry))
	}
	return out
}

func toListingStatus(snap sessionStatusSnapshot) listingcmd.SessionStatus {
	return listingcmd.SessionStatus{
		Total:       snap.total,
		WindowCount: snap.windowCount,
		Zoomed:      snap.zoomed,
	}
}

func toListingWindowEntries(entries []windowListEntry) []listingcmd.WindowEntry {
	out := make([]listingcmd.WindowEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, listingcmd.WindowEntry{
			Index:     entry.index,
			Name:      entry.name,
			PaneCount: entry.paneCount,
			Active:    entry.active,
		})
	}
	return out
}

func toListingClientEntries(entries []clientListEntry) []listingcmd.ClientEntry {
	out := make([]listingcmd.ClientEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, listingcmd.ClientEntry{
			ID:           entry.id,
			DisplayPanes: entry.displayPanes,
			Chooser:      entry.chooser,
			Size:         entry.size,
			SizeOwner:    entry.sizeOwner,
			Capabilities: entry.capabilities,
		})
	}
	return out
}

func formatPaneList(entries []paneListEntry, home string, showCwd bool) string {
	return listingcmd.FormatPaneList(toListingPaneEntries(entries), home, showCwd)
}

func formatPaneListBranch(entry paneListEntry) string {
	return listingcmd.FormatPaneListBranch(toListingPaneEntry(entry))
}

func formatListCwd(cwd, home string, max int) string {
	return listingcmd.FormatListCwd(cwd, home, max)
}

func cmdList(ctx *CommandContext) {
	args, err := listingcmd.ParseListArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	entries, err := ctx.Sess.queryPaneList()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	home, _ := os.UserHomeDir()
	ctx.reply(formatPaneList(entries, home, args.ShowCwd))
}

func cmdStatus(ctx *CommandContext) {
	snap, err := ctx.Sess.querySessionStatus()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(listingcmd.FormatStatus(toListingStatus(snap), BuildVersion))
}

func cmdListWindows(ctx *CommandContext) {
	entries, err := ctx.Sess.queryWindowList()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(listingcmd.FormatWindowList(toListingWindowEntries(entries)))
}

func cmdListClients(ctx *CommandContext) {
	clients, err := ctx.Sess.queryClientList()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(listingcmd.FormatClientList(toListingClientEntries(clients)))
}

func cmdConnectionLog(ctx *CommandContext) {
	entries, err := ctx.Sess.queryConnectionLog()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(entries) == 0 {
		ctx.reply("No client connections recorded.\n")
		return
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-30s %-8s %-10s %-6s %-6s %s\n", "TS", "EVENT", "CLIENT", "COLS", "ROWS", "REASON"))
	for _, entry := range entries {
		reason := entry.DisconnectReason
		if reason == "" {
			reason = "-"
		}
		output.WriteString(fmt.Sprintf(
			"%-30s %-8s %-10s %-6d %-6d %s\n",
			entry.Timestamp.UTC().Format(time.RFC3339Nano),
			entry.Event,
			entry.ClientID,
			entry.Cols,
			entry.Rows,
			reason,
		))
	}
	ctx.reply(output.String())
}

func cmdPaneLog(ctx *CommandContext) {
	entries, err := ctx.Sess.queryPaneLog()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(entries) == 0 {
		ctx.reply("No pane lifecycle events recorded.\n")
		return
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-30s %-8s %-5s %-12s %-10s %-40s %-24s %s\n", "TS", "EVENT", "ID", "PANE", "HOST", "CWD", "GIT_BRANCH", "REASON"))
	for _, entry := range entries {
		cwd := entry.Cwd
		if cwd == "" {
			cwd = "-"
		}
		gitBranch := entry.GitBranch
		if gitBranch == "" {
			gitBranch = "-"
		}
		reason := entry.ExitReason
		if reason == "" {
			reason = "-"
		}
		output.WriteString(fmt.Sprintf(
			"%-30s %-8s %-5d %-12s %-10s %-40s %-24s %s\n",
			entry.Timestamp.UTC().Format(time.RFC3339Nano),
			entry.Event,
			entry.PaneID,
			entry.PaneName,
			entry.Host,
			cwd,
			gitBranch,
			reason,
		))
	}
	ctx.reply(output.String())
}
