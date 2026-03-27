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
	keyscmd "github.com/weill-labs/amux/internal/server/commands/input/keys"
)

// tokenKeyGap is a small pacing gap before injected submit/control keys.
// Some interactive TUIs only react correctly when Enter or Ctrl-key input
// arrives on a later input tick rather than in the same burst as preceding text.
const tokenKeyGap = 50 * time.Millisecond

const (
	broadcastUsage              = "usage: broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>..."
	sendKeysUsage               = "usage: send-keys <pane> [--wait ready|ui=input-idle] [--continue-known-dialogs] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..."
	typeKeysUsage               = "usage: type-keys [--wait ui=input-idle] [--timeout <duration>] [--hex] <keys>..."
	delegateUsage               = "usage: delegate <pane> [--timeout <duration>] [--start-timeout <duration>] [--hex] <keys>..."
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

type delegateOptions struct {
	waitTimeout  time.Duration
	startTimeout time.Duration
	hexMode      bool
	keys         []string
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

func cmdSendKeys(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr(sendKeysUsage)
		return
	}
	opts, err := parseSendKeysArgs(ctx.Args[1:])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(opts.keys) == 0 {
		ctx.replyErr(sendKeysUsage)
		return
	}
	chunks, err := encodeKeyChunks(opts.hexMode, opts.keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	applyFinalDelay(chunks, opts.delayFinal)
	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	switch opts.waitTarget {
	case sendKeysWaitReady:
		if err := waitForPaneReady(ctx.Sess, ctx.Args[0], pane, waitReadyOptions{
			timeout:              opts.waitTimeout,
			continueKnownDialogs: opts.continueKnownDialogs,
		}); err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if err := ctx.Sess.enqueuePacedPaneInput(pane.pane, chunks); err != nil {
			ctx.replyErr(err.Error())
			return
		}
	case sendKeysWaitInputIdle:
		uiWait, err := ctx.Sess.queryUIClient("", proto.UIEventInputIdle)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if err := enqueueTargetedClientKeys(ctx.Sess, uiWait.client, pane.pane, chunks, &uiWait, opts.waitTimeout); err != nil {
			ctx.replyErr(err.Error())
			return
		}
	default:
		if err := ctx.Sess.enqueuePacedPaneInput(pane.pane, chunks); err != nil {
			ctx.replyErr(err.Error())
			return
		}
	}
	ctx.reply(fmt.Sprintf("Sent %d bytes to %s\n", totalEncodedKeyBytes(chunks), pane.paneName))
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
	return client.withInputTargetOverride(pane, func() error {
		if err := client.enqueueTypeKeys(chunks); err != nil {
			return err
		}
		if uiWait == nil {
			return nil
		}
		return waitForNextUIEvent(sess, *uiWait, proto.UIEventInputIdle, waitTimeout)
	})
}

func parseDelegateArgs(args []string) (delegateOptions, error) {
	opts := delegateOptions{
		waitTimeout:  defaultCommandUIWaitTimeout,
		startTimeout: 5 * time.Second,
	}

	for i := 0; i < len(args); {
		switch args[i] {
		case "--timeout":
			if i+1 >= len(args) {
				return delegateOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return delegateOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.waitTimeout = timeout
			i++
		case "--start-timeout":
			if i+1 >= len(args) {
				return delegateOptions{}, fmt.Errorf("missing value for --start-timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return delegateOptions{}, fmt.Errorf("invalid start-timeout: %s", args[i])
			}
			opts.startTimeout = timeout
			i++
		case "--hex":
			opts.hexMode = true
			i++
		default:
			opts.keys = append(opts.keys, args[i:]...)
			i = len(args)
		}
	}

	return opts, nil
}

func cmdDelegate(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr(delegateUsage)
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	opts, err := parseDelegateArgs(ctx.Args[1:])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(opts.keys) == 0 {
		ctx.replyErr(delegateUsage)
		return
	}

	chunks, err := encodeKeyChunks(opts.hexMode, opts.keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	uiWait, err := ctx.Sess.queryUIClient("", proto.UIEventInputIdle)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := enqueueTargetedClientKeys(ctx.Sess, uiWait.client, pane.pane, chunks, &uiWait, opts.waitTimeout); err != nil {
		ctx.replyErr(err.Error())
		return
	}

	enterChunks, err := encodeKeyChunks(false, []string{"Enter"})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := ctx.Sess.enqueuePacedPaneInput(pane.pane, enterChunks); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := waitForPaneBusy(ctx.Sess, pane.paneID, pane.paneName, opts.startTimeout); err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.reply(fmt.Sprintf("Delegated %d bytes to %s\n", totalEncodedKeyBytes(chunks), pane.paneName))
}

type broadcastCommandArgs struct {
	paneRefs     []string
	windowRef    string
	matchPattern string
	hexMode      bool
	keys         []string
}

func cmdBroadcast(ctx *CommandContext) {
	parsed, err := parseBroadcastCommandArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	chunks, err := encodeKeyChunks(parsed.hexMode, parsed.keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	targets, err := resolveBroadcastTargetsForActor(ctx.Sess, ctx.ActorPaneID, parsed)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	if err := enqueueBroadcastInput(ctx.Sess, targets, chunks); err != nil {
		ctx.replyErr(err.Error())
		return
	}

	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.paneName)
	}

	noun := "panes"
	if len(targets) == 1 {
		noun = "pane"
	}
	ctx.reply(fmt.Sprintf("Sent %d bytes to %d %s: %s\n", totalEncodedKeyBytes(chunks), len(targets), noun, strings.Join(names, ", ")))
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

func cmdTypeKeys(ctx *CommandContext) {
	opts, err := parseTypeKeysArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(opts.keys) == 0 {
		ctx.replyErr(typeKeysUsage)
		return
	}
	chunks, err := encodeKeyChunks(opts.hexMode, opts.keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	var (
		client *clientConn
		uiWait uiClientSnapshot
	)
	if opts.waitInputIdle {
		uiWait, err = ctx.Sess.queryUIClient("", proto.UIEventInputIdle)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		client = uiWait.client
	} else {
		client, err = ctx.Sess.queryFirstClient()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
	}

	if err := client.enqueueTypeKeys(chunks); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if opts.waitInputIdle {
		if err := waitForNextUIEvent(ctx.Sess, uiWait, proto.UIEventInputIdle, opts.waitTimeout); err != nil {
			ctx.replyErr(err.Error())
			return
		}
	}
	ctx.reply(fmt.Sprintf("Typed %d bytes\n", totalEncodedKeyBytes(chunks)))
}
