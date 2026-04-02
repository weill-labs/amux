package server

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	inputcmd "github.com/weill-labs/amux/internal/server/commands/input"
	keyscmd "github.com/weill-labs/amux/internal/server/commands/input/keys"
)

// tokenKeyGap is a small pacing gap before injected submit/control keys.
// Some interactive TUIs only react correctly when Enter or Ctrl-key input
// arrives on a later input tick rather than in the same burst as preceding text.
const tokenKeyGap = 50 * time.Millisecond

const (
	broadcastUsage              = "usage: broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>..."
	sendKeysUsage               = "usage: send-keys <pane> [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..."
	typeKeysUsage               = "usage: type-keys [--wait ui=input-idle] [--timeout <duration>] [--hex] <keys>..."
	defaultCommandUIWaitTimeout = 5 * time.Second
)

type encodedKeyChunk struct {
	data        []byte
	paceBefore  bool
	delayBefore time.Duration
}

func parseKey(key string) []byte {
	return keyscmd.ParseKey(key)
}

func parseKeyArgs(args []string) (hexMode bool, keys []string) {
	return keyscmd.ParseKeyArgs(args)
}

func encodeKeyChunks(hexMode bool, keys []string) ([]encodedKeyChunk, error) {
	chunks, err := keyscmd.EncodeChunks(hexMode, keys)
	if err != nil {
		return nil, err
	}
	out := make([]encodedKeyChunk, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, encodedKeyChunk{
			data:       chunk.Data,
			paceBefore: chunk.PaceBefore,
		})
	}
	return out, nil
}

func pacedKeyToken(key string) bool {
	return keyscmd.PacedKeyToken(key)
}

func totalEncodedKeyBytes(chunks []encodedKeyChunk) int {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk.data)
	}
	return total
}

type typeKeysOptions struct {
	waitInputIdle bool
	waitTimeout   time.Duration
	hexMode       bool
	keys          []string
}

type inputCommandContext struct {
	*CommandContext
}

func parseTypeKeysArgs(args []string) (typeKeysOptions, error) {
	opts := typeKeysOptions{waitTimeout: defaultCommandUIWaitTimeout}
	timeoutSet := false

	for i := 0; i < len(args); {
		switch args[i] {
		case "--wait":
			if i+1 >= len(args) {
				return typeKeysOptions{}, fmt.Errorf("missing value for --wait")
			}
			i++
			if args[i] != "ui=input-idle" {
				return typeKeysOptions{}, fmt.Errorf("type-keys: unsupported --wait target %q (want ui=input-idle)", args[i])
			}
			opts.waitInputIdle = true
			i++
		case "--timeout":
			if i+1 >= len(args) {
				return typeKeysOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return typeKeysOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.waitTimeout = timeout
			timeoutSet = true
			i++
		case "--hex":
			opts.hexMode = true
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return typeKeysOptions{}, fmt.Errorf("unknown flag: %s", args[i])
			}
			opts.keys = append(opts.keys, args[i:]...)
			i = len(args)
		}
	}

	if timeoutSet && !opts.waitInputIdle {
		return typeKeysOptions{}, fmt.Errorf("type-keys: --timeout requires --wait ui=input-idle")
	}

	return opts, nil
}

func (ctx inputCommandContext) SendKeys(actorPaneID uint32, args []string) (string, int, error) {
	if len(args) < 2 {
		return "", 0, fmt.Errorf(sendKeysUsage)
	}
	opts, err := parseSendKeysArgs(args[1:])
	if err != nil {
		return "", 0, err
	}
	if len(opts.keys) == 0 {
		return "", 0, fmt.Errorf(sendKeysUsage)
	}
	chunks, err := encodeKeyChunks(opts.hexMode, opts.keys)
	if err != nil {
		return "", 0, err
	}
	applyFinalDelay(chunks, opts.delayFinal)
	pane, err := ctx.Sess.queryResolvedPaneForActor(actorPaneID, args[0])
	if err != nil {
		return "", 0, err
	}
	switch opts.waitTarget {
	case sendKeysWaitReady:
		if err := waitForPaneReady(ctx.Sess, args[0], pane, waitReadyOptions{timeout: opts.waitTimeout}); err != nil {
			return "", 0, err
		}
		if err := enqueueSendKeysInput(ctx.Sess, pane, chunks, opts, nil); err != nil {
			return "", 0, err
		}
	case sendKeysWaitInputIdle:
		uiWait, err := ctx.Sess.queryUIClient(opts.requestedClientID, proto.UIEventInputIdle)
		if err != nil {
			return "", 0, err
		}
		if err := enqueueSendKeysInput(ctx.Sess, pane, chunks, opts, &uiWait); err != nil {
			return "", 0, err
		}
	default:
		if err := enqueueSendKeysInput(ctx.Sess, pane, chunks, opts, nil); err != nil {
			return "", 0, err
		}
	}
	return pane.paneName, totalEncodedKeyBytes(chunks), nil
}

func cmdSendKeys(ctx *CommandContext) {
	ctx.applyCommandResult(inputcmd.SendKeys(inputCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func enqueueSendKeysInput(sess *Session, pane resolvedPaneRef, chunks []encodedKeyChunk, opts sendKeysOptions, uiWait *uiClientSnapshot) error {
	if opts.transport == sendKeysViaClient {
		if uiWait == nil {
			var err error
			uiWait, err = querySendKeysClient(sess, opts.requestedClientID)
			if err != nil {
				return err
			}
		}
		return enqueueTargetedClientKeys(sess, uiWait.client, pane.pane, chunks, uiWait, opts.waitTimeout)
	}
	return sess.enqueuePacedPaneInput(pane.pane, chunks)
}

func querySendKeysClient(sess *Session, requestedClientID string) (*uiClientSnapshot, error) {
	snap, err := sess.queryUIClient(requestedClientID, "")
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

func applyFinalDelay(chunks []encodedKeyChunk, delay time.Duration) {
	if delay <= 0 || len(chunks) == 0 {
		return
	}
	last := &chunks[len(chunks)-1]
	last.delayBefore = delay
	last.paceBefore = false
}

func enqueueTargetedClientKeys(sess *Session, client *clientConn, pane *mux.Pane, chunks []encodedKeyChunk, uiWait *uiClientSnapshot, waitTimeout time.Duration) error {
	if err := client.enqueueTypeKeysToPane(pane.ID, chunks); err != nil {
		return err
	}
	if uiWait == nil {
		return nil
	}
	return waitForNextUIEvent(sess, *uiWait, proto.UIEventInputIdle, waitTimeout)
}

type broadcastCommandArgs struct {
	paneRefs     []string
	windowRef    string
	matchPattern string
	hexMode      bool
	keys         []string
}

func (ctx inputCommandContext) Broadcast(actorPaneID uint32, args []string) ([]string, int, error) {
	parsed, err := parseBroadcastCommandArgs(args)
	if err != nil {
		return nil, 0, err
	}

	chunks, err := encodeKeyChunks(parsed.hexMode, parsed.keys)
	if err != nil {
		return nil, 0, err
	}

	targets, err := resolveBroadcastTargetsForActor(ctx.Sess, actorPaneID, parsed)
	if err != nil {
		return nil, 0, err
	}

	if err := enqueueBroadcastInput(ctx.Sess, targets, chunks); err != nil {
		return nil, 0, err
	}

	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.paneName)
	}
	return names, totalEncodedKeyBytes(chunks), nil
}

func cmdBroadcast(ctx *CommandContext) {
	ctx.applyCommandResult(inputcmd.Broadcast(inputCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func parseBroadcastCommandArgs(args []string) (broadcastCommandArgs, error) {
	if len(args) == 0 {
		return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
	}

	var parsed broadcastCommandArgs
	var keyArgs []string
	selectorCount := 0

	for i := 0; i < len(args); {
		switch args[i] {
		case "--":
			i++
			keyArgs = append(keyArgs, args[i:]...)
			i = len(args)
		case "--panes":
			if i+1 >= len(args) {
				return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
			}
			selectorCount++
			parsed.paneRefs = splitBroadcastPaneRefs(args[i+1])
			if len(parsed.paneRefs) == 0 {
				return broadcastCommandArgs{}, fmt.Errorf("broadcast: no panes specified")
			}
			i += 2
		case "--window":
			if i+1 >= len(args) {
				return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
			}
			selectorCount++
			parsed.windowRef = args[i+1]
			i += 2
		case "--match":
			if i+1 >= len(args) {
				return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
			}
			selectorCount++
			parsed.matchPattern = args[i+1]
			i += 2
		default:
			keyArgs = append(keyArgs, args[i:]...)
			i = len(args)
		}
	}

	if selectorCount == 0 {
		return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
	}
	if selectorCount != 1 {
		return broadcastCommandArgs{}, fmt.Errorf("broadcast: specify exactly one of --panes, --window, or --match")
	}

	parsed.hexMode, parsed.keys = parseKeyArgs(keyArgs)
	if len(parsed.keys) == 0 {
		return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
	}

	return parsed, nil
}

func splitBroadcastPaneRefs(raw string) []string {
	var refs []string
	for _, ref := range strings.Split(raw, ",") {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func resolveBroadcastTargets(sess *Session, args broadcastCommandArgs) ([]resolvedPaneRef, error) {
	return resolveBroadcastTargetsForActor(sess, 0, args)
}

func resolveBroadcastTargetsForActor(sess *Session, actorPaneID uint32, args broadcastCommandArgs) ([]resolvedPaneRef, error) {
	return enqueueSessionQuery(sess, func(sess *Session) ([]resolvedPaneRef, error) {
		switch {
		case len(args.paneRefs) > 0:
			return resolveBroadcastPaneRefs(sess, actorPaneID, args.paneRefs)
		case args.windowRef != "":
			return resolveBroadcastWindowTargets(sess, args.windowRef)
		case args.matchPattern != "":
			return resolveBroadcastMatchTargets(sess, args.matchPattern)
		default:
			return nil, fmt.Errorf(broadcastUsage)
		}
	})
}

func resolveBroadcastPaneRefs(sess *Session, actorPaneID uint32, refs []string) ([]resolvedPaneRef, error) {
	targets := make([]resolvedPaneRef, 0, len(refs))
	seen := make(map[uint32]struct{}, len(refs))
	for _, ref := range refs {
		pane, window, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
		if err != nil {
			return nil, err
		}
		targets = appendBroadcastTarget(targets, seen, pane, window)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("broadcast: no panes specified")
	}
	return targets, nil
}

func resolveBroadcastWindowTargets(sess *Session, ref string) ([]resolvedPaneRef, error) {
	window := sess.resolveWindow(ref)
	if window == nil {
		return nil, fmt.Errorf("window %q not found", ref)
	}

	targets := make([]resolvedPaneRef, 0, window.PaneCount())
	seen := make(map[uint32]struct{}, window.PaneCount())
	window.Root.Walk(func(cell *mux.LayoutCell) {
		if cell.Pane == nil {
			return
		}
		targets = appendBroadcastTarget(targets, seen, cell.Pane, window)
	})
	if len(targets) == 0 {
		return nil, fmt.Errorf("broadcast: window %q has no panes", ref)
	}
	return targets, nil
}

func resolveBroadcastMatchTargets(sess *Session, pattern string) ([]resolvedPaneRef, error) {
	targets := make([]resolvedPaneRef, 0, len(sess.Panes))
	seen := make(map[uint32]struct{}, len(sess.Panes))
	for _, pane := range sess.Panes {
		matched, err := filepath.Match(pattern, pane.Meta.Name)
		if err != nil {
			return nil, fmt.Errorf("broadcast: invalid match pattern %q: %v", pattern, err)
		}
		if !matched {
			continue
		}
		targets = appendBroadcastTarget(targets, seen, pane, sess.findWindowByPaneID(pane.ID))
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("broadcast: no panes match %q", pattern)
	}
	return targets, nil
}

func appendBroadcastTarget(targets []resolvedPaneRef, seen map[uint32]struct{}, pane *mux.Pane, window *mux.Window) []resolvedPaneRef {
	if pane == nil {
		return targets
	}
	if _, ok := seen[pane.ID]; ok {
		return targets
	}
	seen[pane.ID] = struct{}{}

	target := resolvedPaneRef{
		pane:     pane,
		window:   window,
		paneID:   pane.ID,
		paneName: pane.Meta.Name,
	}
	if window != nil {
		target.windowID = window.ID
	}
	return append(targets, target)
}

func enqueueBroadcastInput(sess *Session, targets []resolvedPaneRef, chunks []encodedKeyChunk) error {
	var wg sync.WaitGroup
	errs := make(chan string, len(targets))

	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sess.enqueuePacedPaneInput(target.pane, chunks); err != nil {
				errs <- fmt.Sprintf("%s: %v", target.paneName, err)
			}
		}()
	}

	wg.Wait()
	close(errs)

	if len(errs) == 0 {
		return nil
	}

	failures := make([]string, 0, len(errs))
	for err := range errs {
		failures = append(failures, err)
	}
	slices.Sort(failures)
	return fmt.Errorf("broadcast: failed for %d/%d panes: %s", len(failures), len(targets), strings.Join(failures, ", "))
}

func (ctx inputCommandContext) TypeKeys(args []string) (int, error) {
	opts, err := parseTypeKeysArgs(args)
	if err != nil {
		return 0, err
	}
	if len(opts.keys) == 0 {
		return 0, fmt.Errorf(typeKeysUsage)
	}
	chunks, err := encodeKeyChunks(opts.hexMode, opts.keys)
	if err != nil {
		return 0, err
	}

	var (
		client *clientConn
		uiWait uiClientSnapshot
	)
	if opts.waitInputIdle {
		uiWait, err = ctx.Sess.queryUIClient("", proto.UIEventInputIdle)
		if err != nil {
			return 0, err
		}
		client = uiWait.client
	} else {
		client, err = ctx.Sess.queryFirstClient()
		if err != nil {
			return 0, err
		}
	}

	if err := client.enqueueTypeKeys(chunks); err != nil {
		return 0, err
	}
	if opts.waitInputIdle {
		if err := waitForNextUIEvent(ctx.Sess, uiWait, proto.UIEventInputIdle, opts.waitTimeout); err != nil {
			return 0, err
		}
	}
	return totalEncodedKeyBytes(chunks), nil
}

func cmdTypeKeys(ctx *CommandContext) {
	ctx.applyCommandResult(inputcmd.TypeKeys(inputCommandContext{ctx}, ctx.Args))
}
