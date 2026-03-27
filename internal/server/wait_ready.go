package server

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/mux"
)

const (
	waitReadyUsage = "usage: wait ready <pane> [--timeout <duration>] [--continue-known-dialogs]"

	codexTrustDialogQuestion = "Do you trust the contents of this directory?"
	codexTrustDialogWarning  = "higher risk of prompt injection."
)

type waitReadyOptions struct {
	timeout              time.Duration
	continueKnownDialogs bool
}

type sendKeysWaitTarget int

const (
	sendKeysNoWait sendKeysWaitTarget = iota
	sendKeysWaitReady
	sendKeysWaitInputIdle
)

type sendKeysOptions struct {
	waitTarget           sendKeysWaitTarget
	waitTimeout          time.Duration
	continueKnownDialogs bool
	delayFinal           time.Duration
	hexMode              bool
	keys                 []string
}

type paneReadinessState int

const (
	paneReadinessPending paneReadinessState = iota
	paneReadinessReady
	paneReadinessBlockedKnownDialog
)

type paneReadiness struct {
	state paneReadinessState
}

func cmdWaitReady(ctx *CommandContext) {
	paneRef, opts, err := parseWaitReadyArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := waitForPaneReady(ctx.Sess, paneRef, pane, opts); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply("ready\n")
}

func parseWaitReadyArgs(args []string) (string, waitReadyOptions, error) {
	if len(args) < 1 {
		return "", waitReadyOptions{}, fmt.Errorf(waitReadyUsage)
	}

	opts := waitReadyOptions{timeout: 10 * time.Second}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--continue-known-dialogs":
			opts.continueKnownDialogs = true
		case "--timeout":
			if i+1 >= len(args) {
				return "", waitReadyOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitReadyOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.timeout = timeout
		default:
			return "", waitReadyOptions{}, fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	return args[0], opts, nil
}

func parseSendKeysArgs(args []string) (sendKeysOptions, error) {
	opts := sendKeysOptions{waitTimeout: 10 * time.Second}
	timeoutSet := false
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--wait":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --wait")
			}
			i++
			switch args[i] {
			case "ready":
				opts.waitTarget = sendKeysWaitReady
			case "ui=input-idle":
				opts.waitTarget = sendKeysWaitInputIdle
			default:
				return sendKeysOptions{}, fmt.Errorf("send-keys: unsupported --wait target %q (want ready or ui=input-idle)", args[i])
			}
			i++
		case "--wait-ready":
			return sendKeysOptions{}, fmt.Errorf("send-keys: --wait-ready was removed; use --wait ready")
		case "--continue-known-dialogs":
			opts.continueKnownDialogs = true
			i++
		case "--timeout":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return sendKeysOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.waitTimeout = timeout
			timeoutSet = true
			i++
		case "--delay-final":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --delay-final")
			}
			i++
			delay, err := time.ParseDuration(args[i])
			if err != nil {
				return sendKeysOptions{}, fmt.Errorf("invalid delay-final: %s", args[i])
			}
			opts.delayFinal = delay
			i++
		case "--hex":
			opts.hexMode = true
			i++
		default:
			opts.keys = append(opts.keys, args[i:]...)
			i = len(args)
		}
	}

	if opts.continueKnownDialogs && opts.waitTarget != sendKeysWaitReady {
		return sendKeysOptions{}, fmt.Errorf("send-keys: --continue-known-dialogs requires --wait ready")
	}
	if timeoutSet && opts.waitTarget == sendKeysNoWait {
		return sendKeysOptions{}, fmt.Errorf("send-keys: --timeout requires --wait ready or --wait ui=input-idle")
	}
	return opts, nil
}

func waitForPaneReady(sess *Session, paneRef string, paneRefData resolvedPaneRef, opts waitReadyOptions) error {
	outputCh := sess.enqueuePaneOutputSubscribe(paneRefData.paneID)
	if outputCh == nil {
		return fmt.Errorf("session shutting down")
	}
	defer sess.enqueuePaneOutputUnsubscribe(paneRefData.paneID, outputCh)

	timeoutTimer := time.NewTimer(opts.timeout)
	defer timeoutTimer.Stop()

	continuedKnownDialog := false
	for {
		readiness, err := inspectPaneReadiness(sess, paneRefData.paneID)
		if err != nil {
			return fmt.Errorf("pane %q disappeared while waiting to become ready", paneRef)
		}

		switch readiness.state {
		case paneReadinessReady:
			return nil
		case paneReadinessBlockedKnownDialog:
			if !opts.continueKnownDialogs {
				return fmt.Errorf("Codex trust dialog is blocking input in %s; rerun with --continue-known-dialogs or trust the repo first", paneRef)
			}
			if !continuedKnownDialog {
				enterChunks, err := encodeKeyChunks(false, []string{"Enter"})
				if err != nil {
					return err
				}
				if err := sess.enqueuePacedPaneInput(paneRefData.pane, enterChunks); err != nil {
					return err
				}
				continuedKnownDialog = true
			}
		}

		select {
		case <-outputCh:
		case <-timeoutTimer.C:
			return fmt.Errorf("timeout waiting for %s to become ready", paneRef)
		}
	}
}

func inspectPaneReadiness(sess *Session, paneID uint32) (paneReadiness, error) {
	pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		return sess.findPaneByID(paneID), nil
	})
	if err != nil {
		return paneReadiness{}, err
	}
	if pane == nil {
		return paneReadiness{}, fmt.Errorf("pane missing")
	}

	snap := pane.CaptureSnapshot()
	return classifyPaneReadiness(snap), nil
}

func classifyPaneReadiness(snap mux.CaptureSnapshot) paneReadiness {
	if isCodexTrustDialog(snap.Content) {
		return paneReadiness{state: paneReadinessBlockedKnownDialog}
	}

	cursorRow, ok := readinessCursorRow(snap)
	if !ok || cursorRow < 0 || cursorRow >= len(snap.Content) {
		return paneReadiness{state: paneReadinessPending}
	}

	line := strings.TrimLeftFunc(snap.Content[cursorRow], unicode.IsSpace)
	if !isReadyPromptLine(line) {
		return paneReadiness{state: paneReadinessPending}
	}
	return paneReadiness{state: paneReadinessReady}
}

func isCodexTrustDialog(lines []string) bool {
	foundQuestion := false
	foundWarning := false
	for _, line := range lines {
		if strings.Contains(line, codexTrustDialogQuestion) {
			foundQuestion = true
		}
		if strings.Contains(line, codexTrustDialogWarning) {
			foundWarning = true
		}
	}
	return foundQuestion && foundWarning
}

func readinessCursorRow(snap mux.CaptureSnapshot) (int, bool) {
	if !snap.CursorHidden {
		return snap.CursorRow, true
	}
	if snap.HasCursorBlock {
		return snap.CursorBlockRow, true
	}
	return 0, false
}

func isReadyPromptLine(line string) bool {
	if line == "" {
		return false
	}
	if isNumberedPromptOption(line) {
		return false
	}
	return strings.HasPrefix(line, ">") || strings.HasPrefix(line, "›") || strings.HasPrefix(line, "❯")
}

func isNumberedPromptOption(line string) bool {
	if line == "" {
		return false
	}
	if !(strings.HasPrefix(line, ">") || strings.HasPrefix(line, "›") || strings.HasPrefix(line, "❯")) {
		return false
	}

	_, size := utf8.DecodeRuneInString(line)
	rest := strings.TrimLeftFunc(line[size:], unicode.IsSpace)
	if rest == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsDigit(r)
}
