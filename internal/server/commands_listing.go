package server

import (
	"os"

	"github.com/weill-labs/amux/internal/mux"
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
		KV:            mux.CloneMetaKV(entry.kv),
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

func toListingConnectionLogEntries(entries []ConnectionLogEntry) []listingcmd.ConnectionLogEntry {
	out := make([]listingcmd.ConnectionLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, listingcmd.ConnectionLogEntry{
			Timestamp:        entry.Timestamp,
			Event:            entry.Event,
			ClientID:         entry.ClientID,
			Cols:             entry.Cols,
			Rows:             entry.Rows,
			DisconnectReason: entry.DisconnectReason,
		})
	}
	return out
}

func toListingPaneLogEntries(entries []PaneLogEntry) []listingcmd.PaneLogEntry {
	out := make([]listingcmd.PaneLogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, listingcmd.PaneLogEntry{
			Timestamp:  entry.Timestamp,
			Event:      entry.Event,
			PaneID:     entry.PaneID,
			PaneName:   entry.PaneName,
			Host:       entry.Host,
			Cwd:        entry.Cwd,
			GitBranch:  entry.GitBranch,
			ExitReason: entry.ExitReason,
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

type listingCommandContext struct {
	*CommandContext
}

func (ctx listingCommandContext) HomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func (ctx listingCommandContext) BuildVersion() string {
	return BuildVersion
}

func (ctx listingCommandContext) QueryPaneList() ([]listingcmd.PaneEntry, error) {
	entries, err := ctx.Sess.queryPaneList()
	if err != nil {
		return nil, err
	}
	return toListingPaneEntries(entries), nil
}

func (ctx listingCommandContext) QuerySessionStatus() (listingcmd.SessionStatus, error) {
	snap, err := ctx.Sess.querySessionStatus()
	if err != nil {
		return listingcmd.SessionStatus{}, err
	}
	return toListingStatus(snap), nil
}

func (ctx listingCommandContext) QueryWindowList() ([]listingcmd.WindowEntry, error) {
	entries, err := ctx.Sess.queryWindowList()
	if err != nil {
		return nil, err
	}
	return toListingWindowEntries(entries), nil
}

func (ctx listingCommandContext) QueryClientList() ([]listingcmd.ClientEntry, error) {
	clients, err := ctx.Sess.queryClientList()
	if err != nil {
		return nil, err
	}
	return toListingClientEntries(clients), nil
}

func (ctx listingCommandContext) QueryConnectionLog() ([]listingcmd.ConnectionLogEntry, error) {
	entries, err := ctx.Sess.queryConnectionLog()
	if err != nil {
		return nil, err
	}
	return toListingConnectionLogEntries(entries), nil
}

func (ctx listingCommandContext) QueryPaneLog() ([]listingcmd.PaneLogEntry, error) {
	entries, err := ctx.Sess.queryPaneLog()
	if err != nil {
		return nil, err
	}
	return toListingPaneLogEntries(entries), nil
}

func cmdList(ctx *CommandContext) {
	ctx.applyCommandResult(listingcmd.List(listingCommandContext{ctx}, ctx.Args))
}

func cmdStatus(ctx *CommandContext) {
	ctx.applyCommandResult(listingcmd.Status(listingCommandContext{ctx}, ctx.Args))
}

func cmdListWindows(ctx *CommandContext) {
	ctx.applyCommandResult(listingcmd.ListWindows(listingCommandContext{ctx}, ctx.Args))
}

func cmdListClients(ctx *CommandContext) {
	ctx.applyCommandResult(listingcmd.ListClients(listingCommandContext{ctx}, ctx.Args))
}

func cmdConnectionLog(ctx *CommandContext) {
	ctx.applyCommandResult(listingcmd.ConnectionLog(listingCommandContext{ctx}, ctx.Args))
}

func cmdPaneLog(ctx *CommandContext) {
	ctx.applyCommandResult(listingcmd.PaneLog(listingCommandContext{ctx}, ctx.Args))
}
