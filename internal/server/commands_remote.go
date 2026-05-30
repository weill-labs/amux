package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	listingcmd "github.com/weill-labs/amux/internal/server/commands/listing"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

const (
	remoteCommandUsage   = "usage: remote <add|list|rm|panes|status|attach|detach|resize> ..."
	remoteAddUsage       = "usage: remote add <name> --ssh <target> --socket <path> [--session <name>]"
	remoteListUsage      = "usage: remote list"
	remoteStatusUsage    = "usage: remote status"
	remoteRmUsage        = "usage: remote rm <name>"
	remotePanesUsage     = "usage: remote panes <name>"
	remoteAttachUsage    = "usage: remote attach (<name>|<name>:<pane-name>)"
	remoteDetachUsage    = "usage: remote detach <local-pane>"
	remoteResizeUsage    = "usage: remote resize <local-pane>"
	remoteCommandTimeout = 10 * time.Second
)

type remoteAddArgs struct {
	name string
	host config.Host
}

type remoteMirrorTarget struct {
	paneID   uint32
	paneName string
	ref      checkpoint.RemoteRef
	cols     int
	rows     int
}

type remotePaneGeometry struct {
	id        uint32
	name      string
	cell      proto.CellSnapshot
	path      []layoutPathStep
	window    string
	zoomed    bool
	active    bool
	lead      bool
	snapshot  proto.PaneSnapshot
	leafFound bool
}

type layoutPathStep struct {
	dir   int
	index int
	count int
}

type remoteResizeStep struct {
	direction string
	delta     int
}

func cmdRemote(ctx *CommandContext) {
	ctx.applyCommandResult(runRemoteCommand(ctx))
}

func runRemoteCommand(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) == 0 {
		return commandpkg.Result{Err: errors.New(remoteCommandUsage)}
	}
	switch ctx.Args[0] {
	case "add":
		return runRemoteAdd(ctx)
	case "list":
		return runRemoteList(ctx)
	case "status":
		return runRemoteStatus(ctx)
	case "rm":
		return runRemoteRm(ctx)
	case "panes":
		return runRemotePanes(ctx)
	case "attach":
		return runRemoteAttach(ctx)
	case "detach":
		return runRemoteDetach(ctx)
	case "resize":
		return runRemoteResize(ctx)
	default:
		return commandpkg.Result{Err: errors.New(remoteCommandUsage)}
	}
}

func runRemoteAdd(ctx *CommandContext) commandpkg.Result {
	parsed, err := parseRemoteAddArgs(ctx.Args[1:])
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	cfg, err := loadRemoteConfig()
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if cfg.Remote.Hosts == nil {
		cfg.Remote.Hosts = make(map[string]config.Host)
	}
	cfg.Remote.Hosts[parsed.name] = parsed.host
	if err := config.ValidateRemoteHosts(cfg.Remote.Hosts); err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := saveRemoteConfig(ctx, cfg); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Added remote %s\n", parsed.name)}
}

func runRemoteList(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 1 {
		return commandpkg.Result{Err: errors.New(remoteListUsage)}
	}
	cfg, err := loadRemoteConfig()
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if len(cfg.Remote.Hosts) == 0 {
		return commandpkg.Result{Output: "No remotes.\n"}
	}

	names := sortedRemoteHostNames(cfg.Remote.Hosts)
	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %-30s %-14s %-36s %s\n", "NAME", "SSH", "SESSION", "SOCKET", "HEALTH")
	snaps := remoteMirrorSnapshots(ctx)
	for _, name := range names {
		host := cfg.Remote.Hosts[name]
		fmt.Fprintf(&b, "%-20s %-30s %-14s %-36s %s\n",
			name, host.SSH, remoteCommandSession(host), host.SocketPath, remoteHostHealth(name, snaps))
	}
	return commandpkg.Result{Output: b.String()}
}

func runRemoteStatus(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 1 {
		return commandpkg.Result{Err: errors.New(remoteStatusUsage)}
	}
	cfg, err := loadRemoteConfig()
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if len(cfg.Remote.Hosts) == 0 {
		return commandpkg.Result{Output: "No remotes.\n"}
	}

	return commandpkg.Result{Output: formatRemoteStatus(cfg.Remote.Hosts, remoteMirrorSnapshots(ctx))}
}

// formatRemoteStatus renders the `remote status` table: one block per
// configured host with its overall health plus a row per active mirror
// (remote pane name, remote pane ID, connection state). Pure function so the
// rendering is unit-testable without touching disk config or a live session.
func formatRemoteStatus(hosts map[string]config.Host, snaps []mirrorpkg.Snapshot) string {
	byHost := make(map[string][]mirrorpkg.Snapshot)
	for _, snap := range snaps {
		byHost[snap.RemoteRef.Host] = append(byHost[snap.RemoteRef.Host], snap)
	}

	b := strings.Builder{} // local accumulator (not a package-level var)
	fmt.Fprintf(&b, "%-20s %-14s %-20s %-10s %s\n", "HOST", "HEALTH", "PANE", "ID", "STATE")
	for _, name := range sortedRemoteHostNames(hosts) {
		health := remoteHostHealth(name, snaps)
		mirrors := byHost[name]
		if len(mirrors) == 0 {
			fmt.Fprintf(&b, "%-20s %-14s %-20s %-10s %s\n", name, health, "-", "-", "-")
			continue
		}
		sortMirrorSnapshots(mirrors)
		for i, snap := range mirrors {
			hostCol, healthCol := name, health
			if i > 0 {
				// Repeat host/health only on the first row for readability.
				hostCol, healthCol = "", ""
			}
			state := string(snap.State)
			if snap.LastError != "" {
				state = fmt.Sprintf("%s (%s)", state, snap.LastError)
			}
			fmt.Fprintf(&b, "%-20s %-14s %-20s %-10s %s\n",
				hostCol, healthCol, snap.RemoteRef.PaneName, remotePaneIDLabel(snap.RemotePaneID), state)
		}
	}
	return b.String()
}

func sortMirrorSnapshots(snaps []mirrorpkg.Snapshot) {
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].RemoteRef.PaneName < snaps[j].RemoteRef.PaneName
	})
}

func remotePaneIDLabel(id uint32) string {
	if id == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", id)
}

func runRemoteRm(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 2 {
		return commandpkg.Result{Err: errors.New(remoteRmUsage)}
	}
	name := ctx.Args[1]
	cfg, err := loadRemoteConfig()
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if _, ok := cfg.Remote.Hosts[name]; !ok {
		return commandpkg.Result{Err: fmt.Errorf("remote %q not found", name)}
	}
	delete(cfg.Remote.Hosts, name)
	if err := saveRemoteConfig(ctx, cfg); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Removed remote %s\n", name)}
}

func runRemotePanes(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 2 {
		return commandpkg.Result{Err: errors.New(remotePanesUsage)}
	}
	host, err := lookupRemoteHost(ctx.Args[1])
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	layout, err := listRemotePanes(ctx.context(), host)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	home, _ := os.UserHomeDir()
	return commandpkg.Result{Output: listingcmd.FormatPaneList(remoteLayoutPaneEntries(layout), home, true)}
}

func runRemoteAttach(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 2 {
		return commandpkg.Result{Err: errors.New(remoteAttachUsage)}
	}
	hostName, paneName, ok := strings.Cut(ctx.Args[1], ":")
	if !ok {
		return runRemoteAttachChooser(ctx, ctx.Args[1])
	}
	if hostName == "" || paneName == "" {
		return commandpkg.Result{Err: errors.New(remoteAttachUsage)}
	}
	host, err := lookupRemoteHost(hostName)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	ref := checkpoint.RemoteRef{
		Host:     hostName,
		Session:  remoteCommandSession(host),
		PaneName: paneName,
	}
	return toCommandResult(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
		w := mctx.activeWindow()
		if w == nil || w.ActivePane == nil {
			return commandMutationResult{err: fmt.Errorf("no active pane")}
		}
		pane, err := mctx.prepareMirrorPane(mux.PaneMeta{}, ref, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}
		if _, err := w.SplitPaneWithOptions(w.ActivePane.ID, mux.SplitHorizontal, pane, mux.SplitOptions{}); err != nil {
			mctx.removePane(pane.ID)
			mctx.ScheduleClose(pane)
			return commandMutationResult{err: err}
		}
		if err := mctx.trackMirrorPane(pane, ref); err != nil {
			if cell := w.Root.FindPane(pane.ID); cell != nil {
				_ = w.ClosePane(pane.ID)
			}
			mctx.removePane(pane.ID)
			mctx.ScheduleClose(pane)
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Attached %s:%s as %s\n", hostName, paneName, pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func runRemoteAttachChooser(ctx *CommandContext, hostName string) commandpkg.Result {
	if strings.TrimSpace(hostName) == "" || strings.HasPrefix(hostName, "-") {
		return commandpkg.Result{Err: errors.New(remoteAttachUsage)}
	}
	if ctx == nil || ctx.Sess == nil {
		return commandpkg.Result{Err: fmt.Errorf("no client attached")}
	}
	client, err := ctx.Sess.queryUIClientContext(ctx.context(), "", "")
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	host, err := lookupRemoteHost(hostName)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	layout, err := listRemotePanes(ctx.context(), host)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := client.client.Send(&Message{
		Type: MsgTypeChooser,
		Chooser: &proto.ChooserRequest{
			Kind:   proto.ChooserKindRemotePanes,
			Host:   hostName,
			Layout: layout,
		},
	}); err != nil {
		return commandpkg.Result{Err: err}
	}
	_ = client.client.Flush()
	return commandpkg.Result{Output: fmt.Sprintf("Opened remote pane chooser for %s\n", hostName)}
}

func runRemoteDetach(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 2 {
		return commandpkg.Result{Err: errors.New(remoteDetachUsage)}
	}
	target, err := queryRemoteMirrorTarget(ctx, ctx.Args[1])
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return toCommandResult(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
		removed := mctx.softClosePane(target.paneID)
		if removed.pane == nil {
			return commandMutationResult{}
		}
		mctx.appendPaneLog(paneLogEventExit, removed.pane, "detached")
		mctx.emitEvent(Event{
			Type:     EventPaneExit,
			PaneID:   removed.pane.ID,
			PaneName: removed.paneName,
			Host:     removed.pane.Meta.Host,
			Reason:   "detached",
		})
		return commandMutationResult{
			output:          fmt.Sprintf("Detached mirror %s\n", removed.paneName),
			broadcastLayout: removed.broadcastLayout,
		}
	}))
}

func runRemoteResize(ctx *CommandContext) commandpkg.Result {
	if len(ctx.Args) != 2 {
		return commandpkg.Result{Err: errors.New(remoteResizeUsage)}
	}
	target, err := queryRemoteMirrorTarget(ctx, ctx.Args[1])
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	host, err := lookupRemoteHost(target.ref.Host)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	layout, err := listRemotePanes(ctx.context(), host)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	remotePane, err := remoteGeometryForPane(layout, target.ref.PaneName)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	steps, err := planRemoteResize(remotePane, target.cols, target.rows)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if len(steps) == 0 {
		return commandpkg.Result{Output: fmt.Sprintf("Remote pane %s:%s already matches local size\n", target.ref.Host, target.ref.PaneName)}
	}
	for _, step := range steps {
		if _, err := runRemoteOneShotCommand(ctx.context(), host, "resize-pane", []string{
			target.ref.PaneName,
			step.direction,
			strconv.Itoa(step.delta),
		}); err != nil {
			return commandpkg.Result{Err: err}
		}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Resized remote pane %s:%s to %dx%d\n", target.ref.Host, target.ref.PaneName, target.cols, target.rows)}
}

func runRemoteKill(ctx *CommandContext, opts killCommandArgs) commandpkg.Result {
	target, err := queryRemoteMirrorTarget(ctx, opts.paneRef)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	host, err := lookupRemoteHost(target.ref.Host)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if _, err := runRemoteOneShotCommand(ctx.context(), host, "kill", []string{target.ref.PaneName}); err != nil {
		return commandpkg.Result{Err: err}
	}
	return toCommandResult(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
		removed := mctx.softClosePane(target.paneID)
		if removed.pane == nil {
			return commandMutationResult{}
		}
		mctx.appendPaneLog(paneLogEventExit, removed.pane, "remote killed")
		mctx.emitEvent(Event{
			Type:     EventPaneExit,
			PaneID:   removed.pane.ID,
			PaneName: removed.paneName,
			Host:     removed.pane.Meta.Host,
			Reason:   "remote killed",
		})
		return commandMutationResult{
			output:          fmt.Sprintf("Killed remote pane %s:%s and detached %s\n", target.ref.Host, target.ref.PaneName, removed.paneName),
			broadcastLayout: removed.broadcastLayout,
		}
	}))
}

func parseRemoteAddArgs(args []string) (remoteAddArgs, error) {
	if len(args) < 5 {
		return remoteAddArgs{}, errors.New(remoteAddUsage)
	}
	parsed := remoteAddArgs{name: args[0]}
	if parsed.name == "" || strings.HasPrefix(parsed.name, "-") {
		return remoteAddArgs{}, errors.New(remoteAddUsage)
	}
	parsed.host.Session = DefaultSessionName
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--ssh":
			if i+1 >= len(args) {
				return remoteAddArgs{}, errors.New(remoteAddUsage)
			}
			parsed.host.SSH = args[i+1]
			i++
		case "--socket":
			if i+1 >= len(args) {
				return remoteAddArgs{}, errors.New(remoteAddUsage)
			}
			parsed.host.SocketPath = args[i+1]
			i++
		case "--session":
			if i+1 >= len(args) {
				return remoteAddArgs{}, errors.New(remoteAddUsage)
			}
			parsed.host.Session = args[i+1]
			i++
		default:
			return remoteAddArgs{}, errors.New(remoteAddUsage)
		}
	}
	if parsed.host.SSH == "" || parsed.host.SocketPath == "" || parsed.host.Session == "" {
		return remoteAddArgs{}, errors.New(remoteAddUsage)
	}
	return parsed, nil
}

func loadRemoteConfig() (*config.Config, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil, err
	}
	if cfg.Remote.Hosts == nil {
		cfg.Remote.Hosts = make(map[string]config.Host)
	}
	return cfg, nil
}

func saveRemoteConfig(ctx *CommandContext, cfg *config.Config) error {
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		return err
	}
	if ctx != nil && ctx.Srv != nil {
		ctx.Srv.ConfigureMirrors(cfg.Remote.Hosts, nil)
	}
	return nil
}

func lookupRemoteHost(name string) (config.Host, error) {
	cfg, err := loadRemoteConfig()
	if err != nil {
		return config.Host{}, err
	}
	host, ok := cfg.Remote.Hosts[name]
	if !ok {
		return config.Host{}, fmt.Errorf("remote %q not found", name)
	}
	return host, nil
}

func sortedRemoteHostNames(hosts map[string]config.Host) []string {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func remoteMirrorSnapshots(ctx *CommandContext) []mirrorpkg.Snapshot {
	if ctx == nil || ctx.Sess == nil || ctx.Sess.mirror == nil {
		return nil
	}
	return ctx.Sess.mirror.Snapshots()
}

func remoteHostHealth(host string, snaps []mirrorpkg.Snapshot) string {
	counts := make(map[mirrorpkg.State]int)
	for _, snap := range snaps {
		if snap.RemoteRef.Host == host {
			counts[snap.State]++
		}
	}
	if len(counts) == 0 {
		return "idle"
	}
	states := make([]string, 0, len(counts))
	for state, count := range counts {
		if count == 1 {
			states = append(states, string(state))
		} else {
			states = append(states, fmt.Sprintf("%s(%d)", state, count))
		}
	}
	sort.Strings(states)
	return strings.Join(states, ",")
}

func remoteCommandSession(host config.Host) string {
	if strings.TrimSpace(host.Session) != "" {
		return strings.TrimSpace(host.Session)
	}
	return DefaultSessionName
}

func listRemotePanes(parent context.Context, host config.Host) (*proto.LayoutSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, remoteCommandTimeout)
	defer cancel()
	conn, err := remote.SSHDialer{}.Dial(ctx, host)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return remote.ListPanes(ctx, conn, remoteCommandSession(host))
}

func runRemoteOneShotCommand(parent context.Context, host config.Host, name string, args []string) (*proto.Message, error) {
	ctx, cancel := context.WithTimeout(parent, remoteCommandTimeout)
	defer cancel()
	link := remote.NewLink(host, nil)
	if err := link.Connect(ctx); err != nil {
		return nil, err
	}
	defer link.Close()
	if err := link.WriteMsg(&proto.Message{Type: proto.MsgTypeCommand, CmdName: name, CmdArgs: args}); err != nil {
		return nil, err
	}
	for {
		msg, err := link.ReadMsg()
		if err != nil {
			return nil, err
		}
		if msg.Type != proto.MsgTypeCmdResult {
			continue
		}
		if msg.CmdErr != "" {
			return nil, fmt.Errorf("%s", msg.CmdErr)
		}
		return msg, nil
	}
}

func queryRemoteMirrorTarget(ctx *CommandContext, paneRef string) (remoteMirrorTarget, error) {
	return enqueueSessionQueryOnState(ctx.context(), ctx.Sess, func(sess *Session) (remoteMirrorTarget, error) {
		var (
			pane *mux.Pane
			w    *mux.Window
			err  error
		)
		if paneRef == "" {
			w = sess.windowForActor(ctx.ActorPaneID)
			if w == nil || w.ActivePane == nil {
				return remoteMirrorTarget{}, fmt.Errorf("no active pane")
			}
			pane = w.ActivePane
		} else {
			pane, w, err = sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
			if err != nil {
				return remoteMirrorTarget{}, err
			}
		}
		if sess.mirror == nil {
			return remoteMirrorTarget{}, fmt.Errorf("mirror manager is not configured")
		}
		ref, ok := sess.mirror.RemoteRef(pane.ID)
		if !ok || ref == nil {
			return remoteMirrorTarget{}, fmt.Errorf("pane %s is not a remote mirror", pane.Meta.Name)
		}
		target := remoteMirrorTarget{
			paneID:   pane.ID,
			paneName: pane.Meta.Name,
			ref:      *ref,
		}
		if w != nil {
			if cell := w.Root.FindPane(pane.ID); cell != nil {
				target.cols = cell.W
				target.rows = mux.PaneContentHeight(cell.H)
			}
		}
		if target.cols <= 0 || target.rows <= 0 {
			target.cols, target.rows = pane.EmulatorSize()
		}
		return target, nil
	})
}

func remoteLayoutPaneEntries(layout *proto.LayoutSnapshot) []listingcmd.PaneEntry {
	if layout == nil {
		return nil
	}
	if len(layout.Windows) > 0 {
		var entries []listingcmd.PaneEntry
		for _, win := range layout.Windows {
			panes := paneSnapshotsByID(win.Panes)
			leaves := leafCellIDs(win.Root)
			for _, id := range leaves {
				pane, ok := panes[id]
				if !ok {
					continue
				}
				entries = append(entries, listingEntryFromPaneSnapshot(pane, win.Name, win.Zoomed, id == win.ActivePaneID, id == win.LeadPaneID))
			}
		}
		return entries
	}
	panes := paneSnapshotsByID(layout.Panes)
	leaves := leafCellIDs(layout.Root)
	entries := make([]listingcmd.PaneEntry, 0, len(leaves))
	for _, id := range leaves {
		pane, ok := panes[id]
		if !ok {
			continue
		}
		entries = append(entries, listingEntryFromPaneSnapshot(pane, "", layout.ZoomedPaneID != 0, id == layout.ActivePaneID, id == layout.LeadPaneID))
	}
	return entries
}

func listingEntryFromPaneSnapshot(pane proto.PaneSnapshot, windowName string, windowZoomed, active, lead bool) listingcmd.PaneEntry {
	return listingcmd.PaneEntry{
		PaneID:        pane.ID,
		Name:          pane.Name,
		Host:          pane.Host,
		WindowName:    windowName,
		WindowZoomed:  windowZoomed,
		Task:          pane.Task,
		GitBranch:     pane.GitBranch,
		PR:            pane.PR,
		KV:            pane.KV,
		TrackedPRs:    pane.TrackedPRs,
		TrackedIssues: pane.TrackedIssues,
		Active:        active,
		Lead:          lead || pane.Lead,
	}
}

func paneSnapshotsByID(panes []proto.PaneSnapshot) map[uint32]proto.PaneSnapshot {
	out := make(map[uint32]proto.PaneSnapshot, len(panes))
	for _, pane := range panes {
		out[pane.ID] = pane
	}
	return out
}

func leafCellIDs(root proto.CellSnapshot) []uint32 {
	var ids []uint32
	var walk func(proto.CellSnapshot)
	walk = func(cell proto.CellSnapshot) {
		if cell.IsLeaf {
			if cell.PaneID != 0 {
				ids = append(ids, cell.PaneID)
			}
			return
		}
		for _, child := range cell.Children {
			walk(child)
		}
	}
	walk(root)
	return ids
}

func remoteGeometryForPane(layout *proto.LayoutSnapshot, paneName string) (remotePaneGeometry, error) {
	if layout == nil {
		return remotePaneGeometry{}, fmt.Errorf("remote layout is empty")
	}
	var matches []remotePaneGeometry
	if len(layout.Windows) > 0 {
		for _, win := range layout.Windows {
			panes := paneSnapshotsByID(win.Panes)
			for _, geo := range geometriesForRoot(win.Root, panes, paneName) {
				geo.window = win.Name
				geo.zoomed = win.Zoomed
				geo.active = geo.id == win.ActivePaneID
				geo.lead = geo.id == win.LeadPaneID
				matches = append(matches, geo)
			}
		}
	} else {
		panes := paneSnapshotsByID(layout.Panes)
		for _, geo := range geometriesForRoot(layout.Root, panes, paneName) {
			geo.zoomed = layout.ZoomedPaneID != 0
			geo.active = geo.id == layout.ActivePaneID
			geo.lead = geo.id == layout.LeadPaneID
			matches = append(matches, geo)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return remotePaneGeometry{}, fmt.Errorf("pane name %q not found", paneName)
	default:
		return remotePaneGeometry{}, fmt.Errorf("pane name %q is ambiguous", paneName)
	}
}

func geometriesForRoot(root proto.CellSnapshot, panes map[uint32]proto.PaneSnapshot, paneName string) []remotePaneGeometry {
	var matches []remotePaneGeometry
	var walk func(proto.CellSnapshot, []layoutPathStep)
	walk = func(cell proto.CellSnapshot, path []layoutPathStep) {
		if cell.IsLeaf {
			pane, ok := panes[cell.PaneID]
			if ok && pane.Name == paneName {
				matches = append(matches, remotePaneGeometry{
					id:        pane.ID,
					name:      pane.Name,
					cell:      cell,
					path:      append([]layoutPathStep(nil), path...),
					snapshot:  pane,
					leafFound: true,
				})
			}
			return
		}
		for i, child := range cell.Children {
			next := append(append([]layoutPathStep(nil), path...), layoutPathStep{
				dir:   cell.Dir,
				index: i,
				count: len(cell.Children),
			})
			walk(child, next)
		}
	}
	walk(root, nil)
	return matches
}

func planRemoteResize(geo remotePaneGeometry, desiredCols, desiredRows int) ([]remoteResizeStep, error) {
	var steps []remoteResizeStep
	if desiredCols <= 0 || desiredRows <= 0 {
		return nil, fmt.Errorf("local mirror size is invalid")
	}
	if delta := desiredCols - geo.cell.W; delta != 0 {
		direction, ok := resizeDirectionForAxis(geo.path, int(mux.SplitVertical), delta > 0)
		if !ok {
			return nil, fmt.Errorf("remote pane %s cannot be resized horizontally", geo.name)
		}
		steps = append(steps, remoteResizeStep{direction: direction, delta: abs(delta)})
	}
	remoteRows := mux.PaneContentHeight(geo.cell.H)
	if delta := desiredRows - remoteRows; delta != 0 {
		direction, ok := resizeDirectionForAxis(geo.path, int(mux.SplitHorizontal), delta > 0)
		if !ok {
			return nil, fmt.Errorf("remote pane %s cannot be resized vertically", geo.name)
		}
		steps = append(steps, remoteResizeStep{direction: direction, delta: abs(delta)})
	}
	return steps, nil
}

func resizeDirectionForAxis(path []layoutPathStep, axis int, grow bool) (string, bool) {
	for i := len(path) - 1; i >= 0; i-- {
		step := path[i]
		if step.dir != axis || step.count < 2 {
			continue
		}
		targetIsLast := step.index == step.count-1
		switch axis {
		case int(mux.SplitVertical):
			if grow {
				if targetIsLast {
					return "left", true
				}
				return "right", true
			}
			if targetIsLast {
				return "right", true
			}
			return "left", true
		case int(mux.SplitHorizontal):
			if grow {
				if targetIsLast {
					return "up", true
				}
				return "down", true
			}
			if targetIsLast {
				return "down", true
			}
			return "up", true
		}
	}
	return "", false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
