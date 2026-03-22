//go:build !race

package server

import (
	"bytes"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

func newRecordingPane(sess *Session, id uint32, name string, sink *bytes.Buffer) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		_, _ = sink.Write(data)
		return len(data), nil
	})
}

func newStandaloneProxyPane(id uint32, name string) *mux.Pane {
	return mux.NewProxyPaneWithScrollback(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
}

func mustReadMessage(t *testing.T, conn net.Conn) *Message {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	msg, err := ReadMsg(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("reset deadline: %v", err)
	}
	return msg
}

func TestCommandStatusListAndWindowNavigation(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p2.Meta.Minimized = true
	p2.Meta.Task = "build"
	p2.Meta.GitBranch = "feature/test"
	p2.Meta.PR = "123"
	w1 := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	w1.ZoomedPaneID = p1.ID

	p3 := newTestPane(sess, 3, "pane-3")
	w2 := newTestWindowWithPanes(t, sess, 2, "logs", p3)

	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = []*mux.Pane{p1, p2, p3}

	listRes := runTestCommand(t, srv, sess, "list")
	if listRes.cmdErr != "" {
		t.Fatalf("list error: %s", listRes.cmdErr)
	}
	if !strings.Contains(listRes.output, "PANE") || !strings.Contains(listRes.output, "pane-2") || !strings.Contains(listRes.output, "feature/test #123") {
		t.Fatalf("unexpected list output:\n%s", listRes.output)
	}

	statusRes := runTestCommand(t, srv, sess, "status")
	if statusRes.cmdErr != "" {
		t.Fatalf("status error: %s", statusRes.cmdErr)
	}
	if !strings.Contains(statusRes.output, "windows: 2, panes: 3 total, 2 active, 1 minimized") || !strings.Contains(statusRes.output, "pane-1 zoomed") {
		t.Fatalf("unexpected status output: %q", statusRes.output)
	}

	windowsRes := runTestCommand(t, srv, sess, "list-windows")
	if windowsRes.cmdErr != "" {
		t.Fatalf("list-windows error: %s", windowsRes.cmdErr)
	}
	if !strings.Contains(windowsRes.output, "*1:") || !strings.Contains(windowsRes.output, "logs") {
		t.Fatalf("unexpected list-windows output:\n%s", windowsRes.output)
	}

	usageRes := runTestCommand(t, srv, sess, "select-window")
	if usageRes.cmdErr != "usage: select-window <index|name>" {
		t.Fatalf("select-window usage error = %q", usageRes.cmdErr)
	}

	selectRes := runTestCommand(t, srv, sess, "select-window", "logs")
	if selectRes.cmdErr != "" || !strings.Contains(selectRes.output, "Switched window") {
		t.Fatalf("select-window result = %#v", selectRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 { return sess.ActiveWindowID }); got != w2.ID {
		t.Fatalf("active window = %d, want %d", got, w2.ID)
	}

	nextRes := runTestCommand(t, srv, sess, "next-window")
	if nextRes.cmdErr != "" || !strings.Contains(nextRes.output, "Next window") {
		t.Fatalf("next-window result = %#v", nextRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 { return sess.ActiveWindowID }); got != w1.ID {
		t.Fatalf("active window after next = %d, want %d", got, w1.ID)
	}

	prevRes := runTestCommand(t, srv, sess, "prev-window")
	if prevRes.cmdErr != "" || !strings.Contains(prevRes.output, "Previous window") {
		t.Fatalf("prev-window result = %#v", prevRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 { return sess.ActiveWindowID }); got != w2.ID {
		t.Fatalf("active window after prev = %d, want %d", got, w2.ID)
	}
}

func TestCommandPaneMutationsAndMetadata(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	var sink bytes.Buffer
	p1 := newRecordingPane(sess, 1, "pane-1", &sink)
	p2 := newTestPane(sess, 2, "pane-2")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	zoomRes := runTestCommand(t, srv, sess, "zoom")
	if zoomRes.cmdErr != "" || !strings.Contains(zoomRes.output, "Zoomed pane-2") {
		t.Fatalf("zoom result = %#v", zoomRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 { return sess.ActiveWindow().ZoomedPaneID }); got != p2.ID {
		t.Fatalf("zoomed pane = %d, want %d", got, p2.ID)
	}

	unzoomRes := runTestCommand(t, srv, sess, "zoom")
	if unzoomRes.cmdErr != "" || !strings.Contains(unzoomRes.output, "Unzoomed pane-2") {
		t.Fatalf("unzoom result = %#v", unzoomRes)
	}

	minimizeRes := runTestCommand(t, srv, sess, "minimize", "pane-1")
	if minimizeRes.cmdErr != "" || !strings.Contains(minimizeRes.output, "Minimized pane-1") {
		t.Fatalf("minimize result = %#v", minimizeRes)
	}
	if !mustSessionQuery(t, sess, func(sess *Session) bool { return sess.findPaneByID(p1.ID).Meta.Minimized }) {
		t.Fatal("pane-1 should be minimized")
	}

	restoreRes := runTestCommand(t, srv, sess, "restore", "pane-1")
	if restoreRes.cmdErr != "" || !strings.Contains(restoreRes.output, "Restored pane-1") {
		t.Fatalf("restore result = %#v", restoreRes)
	}
	if mustSessionQuery(t, sess, func(sess *Session) bool { return sess.findPaneByID(p1.ID).Meta.Minimized }) {
		t.Fatal("pane-1 should be restored")
	}

	copyModeRes := runTestCommand(t, srv, sess, "copy-mode", "pane-1")
	if copyModeRes.cmdErr != "" || !strings.Contains(copyModeRes.output, "Copy mode entered for pane-1") {
		t.Fatalf("copy-mode result = %#v", copyModeRes)
	}

	copyModeActiveRes := runTestCommand(t, srv, sess, "copy-mode")
	if copyModeActiveRes.cmdErr != "" || !strings.Contains(copyModeActiveRes.output, "Copy mode entered for pane-2") {
		t.Fatalf("copy-mode active result = %#v", copyModeActiveRes)
	}

	sendKeysRes := runTestCommand(t, srv, sess, "send-keys", "pane-1", "hello")
	if sendKeysRes.cmdErr != "" || !strings.Contains(sendKeysRes.output, "Sent 5 bytes to pane-1") {
		t.Fatalf("send-keys result = %#v", sendKeysRes)
	}
	if got := sink.String(); got != "hello" {
		t.Fatalf("recorded pane input = %q, want %q", got, "hello")
	}

	sendHexErr := runTestCommand(t, srv, sess, "send-keys", "pane-1", "--hex", "zz")
	if sendHexErr.cmdErr != "invalid hex: zz" {
		t.Fatalf("send-keys hex error = %q", sendHexErr.cmdErr)
	}

	resizeBorderUsage := runTestCommand(t, srv, sess, "resize-border", "10")
	if resizeBorderUsage.cmdErr != "usage: resize-border <x> <y> <delta>" {
		t.Fatalf("resize-border usage error = %q", resizeBorderUsage.cmdErr)
	}

	resizeBorderErr := runTestCommand(t, srv, sess, "resize-border", "x", "y", "z")
	if resizeBorderErr.cmdErr != "resize-border: invalid arguments" {
		t.Fatalf("resize-border invalid args error = %q", resizeBorderErr.cmdErr)
	}

	resizeBorderRes := runTestCommand(t, srv, sess, "resize-border", "0", "11", "1")
	if resizeBorderRes.cmdErr != "" {
		t.Fatalf("resize-border error: %s", resizeBorderRes.cmdErr)
	}

	resizeActiveUsage := runTestCommand(t, srv, sess, "resize-active", "left")
	if resizeActiveUsage.cmdErr != "usage: resize-active <direction> <delta>" {
		t.Fatalf("resize-active usage error = %q", resizeActiveUsage.cmdErr)
	}

	resizeActiveErr := runTestCommand(t, srv, sess, "resize-active", "left", "bad")
	if resizeActiveErr.cmdErr != "resize-active: invalid delta" {
		t.Fatalf("resize-active invalid delta error = %q", resizeActiveErr.cmdErr)
	}

	resizeActiveRes := runTestCommand(t, srv, sess, "resize-active", "up", "1")
	if resizeActiveRes.cmdErr != "" {
		t.Fatalf("resize-active error: %s", resizeActiveRes.cmdErr)
	}

	resizePaneUsage := runTestCommand(t, srv, sess, "resize-pane", "pane-1")
	if resizePaneUsage.cmdErr != "usage: resize-pane <pane> <direction> [delta]" {
		t.Fatalf("resize-pane usage error = %q", resizePaneUsage.cmdErr)
	}

	resizePaneDirErr := runTestCommand(t, srv, sess, "resize-pane", "pane-1", "sideways")
	if resizePaneDirErr.cmdErr != `resize-pane: invalid direction "sideways" (use left/right/up/down)` {
		t.Fatalf("resize-pane direction error = %q", resizePaneDirErr.cmdErr)
	}

	resizePaneDeltaErr := runTestCommand(t, srv, sess, "resize-pane", "pane-1", "down", "0")
	if resizePaneDeltaErr.cmdErr != "resize-pane: invalid delta" {
		t.Fatalf("resize-pane delta error = %q", resizePaneDeltaErr.cmdErr)
	}

	resizePaneRes := runTestCommand(t, srv, sess, "resize-pane", "pane-1", "down", "2")
	if resizePaneRes.cmdErr != "" || !strings.Contains(resizePaneRes.output, "Resized pane-1 down by 2") {
		t.Fatalf("resize-pane result = %#v", resizePaneRes)
	}

	swapErr := runTestCommand(t, srv, sess, "swap", "pane-1")
	if swapErr.cmdErr != "usage: swap <pane1> <pane2> | swap forward | swap backward" {
		t.Fatalf("swap usage error = %q", swapErr.cmdErr)
	}

	swapRes := runTestCommand(t, srv, sess, "swap", "forward")
	if swapRes.cmdErr != "" || !strings.Contains(swapRes.output, "Swapped") {
		t.Fatalf("swap result = %#v", swapRes)
	}

	rotateRes := runTestCommand(t, srv, sess, "rotate", "--reverse")
	if rotateRes.cmdErr != "" || !strings.Contains(rotateRes.output, "Rotated") {
		t.Fatalf("rotate result = %#v", rotateRes)
	}

	setMetaUsage := runTestCommand(t, srv, sess, "set-meta", "pane-1")
	if setMetaUsage.cmdErr != "usage: set-meta <pane> key=value [key=value...]" {
		t.Fatalf("set-meta usage error = %q", setMetaUsage.cmdErr)
	}

	setMetaErr := runTestCommand(t, srv, sess, "set-meta", "pane-1", "nope")
	if setMetaErr.cmdErr != `invalid key=value: "nope"` {
		t.Fatalf("set-meta invalid kv error = %q", setMetaErr.cmdErr)
	}

	setMetaUnknown := runTestCommand(t, srv, sess, "set-meta", "pane-1", "unknown=value")
	if setMetaUnknown.cmdErr != `unknown meta key: "unknown" (valid: task, pr, branch)` {
		t.Fatalf("set-meta unknown key error = %q", setMetaUnknown.cmdErr)
	}

	setMetaRes := runTestCommand(t, srv, sess, "set-meta", "pane-1", "task=ship", "pr=456", "branch=feature/x")
	if setMetaRes.cmdErr != "" {
		t.Fatalf("set-meta error: %s", setMetaRes.cmdErr)
	}
	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta { return sess.findPaneByID(p1.ID).Meta })
	if meta.Task != "ship" || meta.PR != "456" || meta.GitBranch != "feature/x" {
		t.Fatalf("pane metadata = %#v", meta)
	}

	clearBranchRes := runTestCommand(t, srv, sess, "set-meta", "pane-1", "branch=")
	if clearBranchRes.cmdErr != "" {
		t.Fatalf("set-meta clear branch error: %s", clearBranchRes.cmdErr)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) string { return sess.findPaneByID(p1.ID).Meta.GitBranch }); got != "" {
		t.Fatalf("branch after clear = %q, want empty", got)
	}

	addMetaUsage := runTestCommand(t, srv, sess, "add-meta", "pane-1")
	if addMetaUsage.cmdErr != "usage: add-meta <pane> key=value [key=value...]" {
		t.Fatalf("add-meta usage error = %q", addMetaUsage.cmdErr)
	}

	addMetaInvalid := runTestCommand(t, srv, sess, "add-meta", "pane-1", "nope")
	if addMetaInvalid.cmdErr != `invalid key=value: "nope"` {
		t.Fatalf("add-meta invalid kv error = %q", addMetaInvalid.cmdErr)
	}

	addMetaBadPR := runTestCommand(t, srv, sess, "add-meta", "pane-1", "pr=abc")
	if addMetaBadPR.cmdErr != `invalid pr value: "abc"` {
		t.Fatalf("add-meta invalid pr error = %q", addMetaBadPR.cmdErr)
	}

	addMetaUnknown := runTestCommand(t, srv, sess, "add-meta", "pane-1", "task=ship")
	if addMetaUnknown.cmdErr != `unknown meta key: "task" (valid: pr, issue)` {
		t.Fatalf("add-meta unknown key error = %q", addMetaUnknown.cmdErr)
	}

	addMetaRes := runTestCommand(t, srv, sess, "add-meta", "pane-1", "pr=42", "issue=LAB-338", "pr=42", "issue=LAB-338")
	if addMetaRes.cmdErr != "" {
		t.Fatalf("add-meta error: %s", addMetaRes.cmdErr)
	}
	meta = mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta { return sess.findPaneByID(p1.ID).Meta })
	if prs := reflect.ValueOf(meta).FieldByName("PRs"); !prs.IsValid() || prs.Len() != 1 || prs.Index(0).Int() != 42 {
		t.Fatalf("pane PRs = %#v, want [42]", prs)
	}
	if issues := reflect.ValueOf(meta).FieldByName("Issues"); !issues.IsValid() || issues.Len() != 1 || issues.Index(0).String() != "LAB-338" {
		t.Fatalf("pane Issues = %#v, want [LAB-338]", issues)
	}

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		p := sess.findPaneByID(p1.ID)
		reflect.ValueOf(&p.Meta).Elem().FieldByName("PRs").Set(reflect.ValueOf([]int{42, 42, 73}))
		reflect.ValueOf(&p.Meta).Elem().FieldByName("Issues").Set(reflect.ValueOf([]string{"LAB-338", "LAB-338", "LAB-412"}))
		return struct{}{}
	})

	rmMetaUsage := runTestCommand(t, srv, sess, "rm-meta", "pane-1")
	if rmMetaUsage.cmdErr != "usage: rm-meta <pane> key=value [key=value...]" {
		t.Fatalf("rm-meta usage error = %q", rmMetaUsage.cmdErr)
	}

	rmMetaInvalid := runTestCommand(t, srv, sess, "rm-meta", "pane-1", "nope")
	if rmMetaInvalid.cmdErr != `invalid key=value: "nope"` {
		t.Fatalf("rm-meta invalid kv error = %q", rmMetaInvalid.cmdErr)
	}

	rmMetaBadPR := runTestCommand(t, srv, sess, "rm-meta", "pane-1", "pr=abc")
	if rmMetaBadPR.cmdErr != `invalid pr value: "abc"` {
		t.Fatalf("rm-meta invalid pr error = %q", rmMetaBadPR.cmdErr)
	}

	rmMetaUnknown := runTestCommand(t, srv, sess, "rm-meta", "pane-1", "task=ship")
	if rmMetaUnknown.cmdErr != `unknown meta key: "task" (valid: pr, issue)` {
		t.Fatalf("rm-meta unknown key error = %q", rmMetaUnknown.cmdErr)
	}

	rmMetaRes := runTestCommand(t, srv, sess, "rm-meta", "pane-1", "pr=42", "issue=LAB-338")
	if rmMetaRes.cmdErr != "" {
		t.Fatalf("rm-meta error: %s", rmMetaRes.cmdErr)
	}
	meta = mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta { return sess.findPaneByID(p1.ID).Meta })
	if prs := reflect.ValueOf(meta).FieldByName("PRs"); !prs.IsValid() || prs.Len() != 1 || prs.Index(0).Int() != 73 {
		t.Fatalf("pane PRs after remove = %#v, want [73]", prs)
	}
	if issues := reflect.ValueOf(meta).FieldByName("Issues"); !issues.IsValid() || issues.Len() != 1 || issues.Index(0).String() != "LAB-412" {
		t.Fatalf("pane Issues after remove = %#v, want [LAB-412]", issues)
	}

	assertSessionLayoutConsistent(t, sess)
}

func TestCommandCaptureHistoryAndWaitCommands(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newStandaloneProxyPane(1, "pane-1")
	p1.FeedOutput([]byte("alpha\r\nbeta\r\nmarker"))

	w := newTestWindowWithPanes(t, sess, 1, "main", p1)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	sess.hookGen.Store(6)
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.idle.MarkIdle(p1.ID)
		return struct{}{}
	})
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.hookResults = []hookResultRecord{{
			Generation: 6,
			Event:      "on-idle",
			PaneName:   "pane-1",
			Success:    true,
		}}
		return struct{}{}
	})

	captureUsage := runTestCommand(t, srv, sess, "capture", "--history")
	if captureUsage.cmdErr != "--history requires a pane target" {
		t.Fatalf("capture usage error = %q", captureUsage.cmdErr)
	}

	captureRes := runTestCommand(t, srv, sess, "capture", "--history", "pane-1")
	if captureRes.cmdErr != "" || !strings.Contains(captureRes.output, "alpha") || !strings.Contains(captureRes.output, "marker") {
		t.Fatalf("capture history result = %#v", captureRes)
	}

	waitForUsage := runTestCommand(t, srv, sess, "wait-for", "pane-1")
	if waitForUsage.cmdErr != "usage: wait-for <pane> <substring> [--timeout <duration>]" {
		t.Fatalf("wait-for usage error = %q", waitForUsage.cmdErr)
	}

	waitForRes := runTestCommand(t, srv, sess, "wait-for", "pane-1", "marker", "--timeout", "1ms")
	if waitForRes.cmdErr != "" || strings.TrimSpace(waitForRes.output) != "matched" {
		t.Fatalf("wait-for result = %#v", waitForRes)
	}

	waitIdleUsage := runTestCommand(t, srv, sess, "wait-idle")
	if waitIdleUsage.cmdErr != "usage: wait-idle <pane> [--timeout <duration>]" {
		t.Fatalf("wait-idle usage error = %q", waitIdleUsage.cmdErr)
	}

	waitIdleRes := runTestCommand(t, srv, sess, "wait-idle", "pane-1", "--timeout", "1ms")
	if waitIdleRes.cmdErr != "" || strings.TrimSpace(waitIdleRes.output) != "idle" {
		t.Fatalf("wait-idle result = %#v", waitIdleRes)
	}

	waitBusyUsage := runTestCommand(t, srv, sess, "wait-busy")
	if waitBusyUsage.cmdErr != "usage: wait-busy <pane> [--timeout <duration>]" {
		t.Fatalf("wait-busy usage error = %q", waitBusyUsage.cmdErr)
	}

	waitBusyRes := runTestCommand(t, srv, sess, "wait-busy", "pane-1", "--timeout", "1ms")
	if !strings.Contains(waitBusyRes.cmdErr, "timeout waiting for pane-1 to become busy") {
		t.Fatalf("wait-busy timeout error = %q", waitBusyRes.cmdErr)
	}

	hookGenRes := runTestCommand(t, srv, sess, "hook-gen")
	if hookGenRes.cmdErr != "" || strings.TrimSpace(hookGenRes.output) != "6" {
		t.Fatalf("hook-gen result = %#v", hookGenRes)
	}

	waitHookRes := runTestCommand(t, srv, sess, "wait-hook", "on-idle", "--pane", "pane-1", "--after", "5", "--timeout", "1ms")
	if waitHookRes.cmdErr != "" || strings.TrimSpace(waitHookRes.output) != "6 on-idle pane-1 success" {
		t.Fatalf("wait-hook result = %#v", waitHookRes)
	}
}

func TestCommandWaitHooksClientsAndTypeKeys(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}
	sess.generation.Store(7)
	sess.clipboardGen.Store(5)
	sess.lastClipboardB64 = "clip-data"

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	uiClient := NewClientConn(serverConn)
	defer uiClient.Close()
	uiClient.ID = "client-1"
	uiClient.cols = 100
	uiClient.rows = 30
	uiClient.displayPanesShown = true
	uiClient.chooserMode = "tree"
	uiClient.uiGeneration = 2
	uiClient.setNegotiatedCapabilities(proto.ClientCapabilities{KittyKeyboard: true, Hyperlinks: true})
	uiClient.initTypeKeyQueue()

	sess.clients = []*ClientConn{uiClient}
	sess.sizeClient.Store(uiClient)

	genRes := runTestCommand(t, srv, sess, "generation")
	if genRes.cmdErr != "" || strings.TrimSpace(genRes.output) != "7" {
		t.Fatalf("generation result = %#v", genRes)
	}

	waitLayoutRes := runTestCommand(t, srv, sess, "wait-layout", "--after", "6", "--timeout", "1ms")
	if waitLayoutRes.cmdErr != "" || strings.TrimSpace(waitLayoutRes.output) != "7" {
		t.Fatalf("wait-layout result = %#v", waitLayoutRes)
	}

	waitLayoutTimeout := runTestCommand(t, srv, sess, "wait-layout", "--after", "99", "--timeout", "1ms")
	if !strings.Contains(waitLayoutTimeout.cmdErr, "timeout waiting for generation > 99") {
		t.Fatalf("wait-layout timeout error = %q", waitLayoutTimeout.cmdErr)
	}

	clipGenRes := runTestCommand(t, srv, sess, "clipboard-gen")
	if clipGenRes.cmdErr != "" || strings.TrimSpace(clipGenRes.output) != "5" {
		t.Fatalf("clipboard-gen result = %#v", clipGenRes)
	}

	waitClipboardRes := runTestCommand(t, srv, sess, "wait-clipboard", "--after", "4", "--timeout", "1ms")
	if waitClipboardRes.cmdErr != "" || strings.TrimSpace(waitClipboardRes.output) != "clip-data" {
		t.Fatalf("wait-clipboard result = %#v", waitClipboardRes)
	}

	waitClipboardTimeout := runTestCommand(t, srv, sess, "wait-clipboard", "--after", "99", "--timeout", "1ms")
	if waitClipboardTimeout.cmdErr != "timeout waiting for clipboard event" {
		t.Fatalf("wait-clipboard timeout error = %q", waitClipboardTimeout.cmdErr)
	}

	uiGenRes := runTestCommand(t, srv, sess, "ui-gen", "--client", "client-1")
	if uiGenRes.cmdErr != "" || strings.TrimSpace(uiGenRes.output) != "2" {
		t.Fatalf("ui-gen result = %#v", uiGenRes)
	}

	waitUIInvalid := runTestCommand(t, srv, sess, "wait-ui", "totally-unknown")
	if waitUIInvalid.cmdErr != "unknown ui event: totally-unknown" {
		t.Fatalf("wait-ui invalid error = %q", waitUIInvalid.cmdErr)
	}

	waitUISuccess := runTestCommand(t, srv, sess, "wait-ui", proto.UIEventDisplayPanesShown, "--after", "1", "--timeout", "1ms")
	if waitUISuccess.cmdErr != "" || strings.TrimSpace(waitUISuccess.output) != proto.UIEventDisplayPanesShown {
		t.Fatalf("wait-ui success result = %#v", waitUISuccess)
	}

	setHookUsage := runTestCommand(t, srv, sess, "set-hook", "on-idle")
	if setHookUsage.cmdErr != "usage: set-hook <event> <command>" {
		t.Fatalf("set-hook usage error = %q", setHookUsage.cmdErr)
	}

	setHookRes := runTestCommand(t, srv, sess, "set-hook", "on-idle", "echo hook")
	if setHookRes.cmdErr != "" || !strings.Contains(setHookRes.output, "Hook added: on-idle") {
		t.Fatalf("set-hook result = %#v", setHookRes)
	}

	listHooksRes := runTestCommand(t, srv, sess, "list-hooks")
	if listHooksRes.cmdErr != "" || !strings.Contains(listHooksRes.output, "on-idle:") || !strings.Contains(listHooksRes.output, "echo hook") {
		t.Fatalf("list-hooks result = %#v", listHooksRes)
	}

	unsetHookErr := runTestCommand(t, srv, sess, "unset-hook", "on-idle", "bad")
	if unsetHookErr.cmdErr != "invalid index: bad" {
		t.Fatalf("unset-hook invalid index error = %q", unsetHookErr.cmdErr)
	}

	unsetHookRes := runTestCommand(t, srv, sess, "unset-hook", "on-idle")
	if unsetHookRes.cmdErr != "" || !strings.Contains(unsetHookRes.output, "Removed all hooks for on-idle") {
		t.Fatalf("unset-hook result = %#v", unsetHookRes)
	}

	listClientsRes := runTestCommand(t, srv, sess, "list-clients")
	if listClientsRes.cmdErr != "" || !strings.Contains(listClientsRes.output, "client-1") || !strings.Contains(listClientsRes.output, "100x30") {
		t.Fatalf("list-clients result = %#v", listClientsRes)
	}

	type typeKeyRead struct {
		msg *Message
		err error
	}
	readCh := make(chan typeKeyRead, 1)
	go func() {
		if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			readCh <- typeKeyRead{err: err}
			return
		}
		msg, err := ReadMsg(clientConn)
		readCh <- typeKeyRead{msg: msg, err: err}
	}()

	typeKeysRes := runTestCommand(t, srv, sess, "type-keys", "ab")
	if typeKeysRes.cmdErr != "" || !strings.Contains(typeKeysRes.output, "Typed 2 bytes") {
		t.Fatalf("type-keys result = %#v", typeKeysRes)
	}

	read := <-readCh
	if read.err != nil {
		t.Fatalf("reading type-keys message: %v", read.err)
	}
	if read.msg.Type != MsgTypeTypeKeys || string(read.msg.Input) != "ab" {
		t.Fatalf("type-keys message = %#v", read.msg)
	}
}

func TestCommandSplitSpawnKillAndEvents(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	p1.Start()
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}

	spawnUsage := runTestCommand(t, srv, sess, "spawn", "--task", "build")
	if spawnUsage.cmdErr != "--name is required" {
		t.Fatalf("spawn usage error = %q", spawnUsage.cmdErr)
	}

	splitRes := runTestCommand(t, srv, sess, "split", "root", "v")
	if splitRes.cmdErr != "" || !strings.Contains(splitRes.output, "Split vertical: new pane") {
		t.Fatalf("split result = %#v", splitRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 2 {
		t.Fatalf("pane count after split = %d, want 2", got)
	}

	spawnRes := runTestCommand(t, srv, sess, "spawn", "--name", "worker-1", "--task", "build")
	if spawnRes.cmdErr != "" || !strings.Contains(spawnRes.output, "Spawned worker-1") {
		t.Fatalf("spawn result = %#v", spawnRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 3 {
		t.Fatalf("pane count after spawn = %d, want 3", got)
	}

	focusRes := runTestCommand(t, srv, sess, "focus", "pane-1")
	if focusRes.cmdErr != "" || !strings.Contains(focusRes.output, "Focused pane-1") {
		t.Fatalf("focus result = %#v", focusRes)
	}

	killRes := runTestCommand(t, srv, sess, "kill", "worker-1")
	if killRes.cmdErr != "" || !strings.Contains(killRes.output, "Killed worker-1") {
		t.Fatalf("kill result = %#v", killRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 2 {
		t.Fatalf("pane count after kill = %d, want 2", got)
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	cc := NewClientConn(serverConn)
	defer cc.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.handleCommand(srv, sess, &Message{
			Type:    MsgTypeCommand,
			CmdName: "events",
			CmdArgs: []string{"--filter", "layout", "--throttle", "0s"},
		})
	}()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.emitEvent(Event{Type: EventLayout, Generation: 9})
		return struct{}{}
	})

	msg := mustReadMessage(t, clientConn)
	if msg.Type != MsgTypeCmdResult || !strings.Contains(msg.CmdOutput, `"type":"layout"`) {
		t.Fatalf("events message = %#v", msg)
	}

	_ = clientConn.Close()
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.emitEvent(Event{Type: EventLayout, Generation: 10})
		return struct{}{}
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("events command did not exit after client disconnect")
	}
}

func TestCommandSplitParsesDirectionFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		setup      func(t *testing.T, srv *Server, sess *Session)
		wantErr    string
		wantOutput string
		wantDir    mux.SplitDir
		wantPanes  int
	}{
		{
			name:       "vertical flag",
			args:       []string{"--vertical"},
			wantOutput: "Split vertical: new pane",
			wantDir:    mux.SplitVertical,
			wantPanes:  2,
		},
		{
			name:       "horizontal flag",
			args:       []string{"--horizontal"},
			wantOutput: "Split horizontal: new pane",
			wantDir:    mux.SplitHorizontal,
			wantPanes:  2,
		},
		{
			name: "root vertical flag",
			args: []string{"root", "--vertical"},
			setup: func(t *testing.T, srv *Server, sess *Session) {
				res := runTestCommand(t, srv, sess, "split")
				if res.cmdErr != "" {
					t.Fatalf("initial split failed: %s", res.cmdErr)
				}
			},
			wantOutput: "Split vertical: new pane",
			wantDir:    mux.SplitVertical,
			wantPanes:  3,
		},
		{
			name:       "legacy vertical shorthand",
			args:       []string{"v"},
			wantOutput: "Split vertical: new pane",
			wantDir:    mux.SplitVertical,
			wantPanes:  2,
		},
		{
			name:    "conflicting directions",
			args:    []string{"--vertical", "--horizontal"},
			wantErr: "conflicting split directions",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			p1, err := sess.createPane(srv, 80, 23)
			if err != nil {
				t.Fatalf("createPane: %v", err)
			}
			p1.Start()
			w := mux.NewWindow(p1, 80, 23)
			w.ID = 1
			w.Name = "main"
			sess.Windows = []*mux.Window{w}
			sess.ActiveWindowID = w.ID
			sess.Panes = []*mux.Pane{p1}

			if tt.setup != nil {
				tt.setup(t, srv, sess)
			}

			res := runTestCommand(t, srv, sess, "split", tt.args...)
			if tt.wantErr != "" {
				if !strings.Contains(res.cmdErr, tt.wantErr) {
					t.Fatalf("split %v error = %q, want substring %q", tt.args, res.cmdErr, tt.wantErr)
				}
				return
			}
			if res.cmdErr != "" {
				t.Fatalf("split %v cmdErr = %q", tt.args, res.cmdErr)
			}
			if !strings.Contains(res.output, tt.wantOutput) {
				t.Fatalf("split %v output = %q, want substring %q", tt.args, res.output, tt.wantOutput)
			}

			if got := mustSessionQuery(t, sess, func(sess *Session) mux.SplitDir {
				return sess.ActiveWindow().Root.Dir
			}); got != tt.wantDir {
				t.Fatalf("split %v root dir = %v, want %v", tt.args, got, tt.wantDir)
			}
			if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != tt.wantPanes {
				t.Fatalf("split %v pane count = %d, want %d", tt.args, got, tt.wantPanes)
			}
		})
	}
}

func TestFlushPendingOutputEventsAndHelpers(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	cc := NewClientConn(serverConn)
	defer cc.Close()

	ctx := &CommandContext{CC: cc}
	pending := map[uint32][]byte{
		7: []byte(`{"type":"output","pane_id":7,"text":"later"}`),
		3: []byte(`{"type":"output","pane_id":3,"text":"first"}`),
	}

	type flushRead struct {
		msgs []*Message
		err  error
	}
	readCh := make(chan flushRead, 1)
	go func() {
		if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			readCh <- flushRead{err: err}
			return
		}
		msg1, err := ReadMsg(clientConn)
		if err != nil {
			readCh <- flushRead{err: err}
			return
		}
		msg2, err := ReadMsg(clientConn)
		readCh <- flushRead{msgs: []*Message{msg1, msg2}, err: err}
	}()

	if err := flushPendingOutputEvents(ctx, pending); err != nil {
		t.Fatalf("flushPendingOutputEvents: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after flush = %d, want 0", len(pending))
	}

	read := <-readCh
	if read.err != nil {
		t.Fatalf("reading flushed messages: %v", read.err)
	}
	msg1 := read.msgs[0]
	msg2 := read.msgs[1]
	if !strings.Contains(msg1.CmdOutput, `"pane_id":3`) || !strings.Contains(msg2.CmdOutput, `"pane_id":7`) {
		t.Fatalf("flush order = %#v then %#v", msg1, msg2)
	}

	if got := dirName(mux.SplitVertical); got != "vertical" {
		t.Fatalf("dirName(vertical) = %q", got)
	}
	if got := dirName(mux.SplitHorizontal); got != "horizontal" {
		t.Fatalf("dirName(horizontal) = %q", got)
	}
}

func TestCommandHostsAndRemoteErrors(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	hostsNil := runTestCommand(t, srv, sess, "hosts")
	if hostsNil.cmdErr != "" || hostsNil.output != "No remote hosts configured.\n" {
		t.Fatalf("hosts without manager = %#v", hostsNil)
	}

	disconnectUsage := runTestCommand(t, srv, sess, "disconnect")
	if disconnectUsage.cmdErr != "usage: disconnect <host>" {
		t.Fatalf("disconnect usage error = %q", disconnectUsage.cmdErr)
	}

	reconnectUsage := runTestCommand(t, srv, sess, "reconnect")
	if reconnectUsage.cmdErr != "usage: reconnect <host>" {
		t.Fatalf("reconnect usage error = %q", reconnectUsage.cmdErr)
	}

	cfg := &config.Config{
		Hosts: map[string]config.Host{
			"remote-a": {Type: "remote", Address: "10.0.0.1", User: "ubuntu"},
			"local":    {Type: "local"},
		},
	}
	sess.RemoteManager = remote.NewManager(cfg, "")

	hostsRes := runTestCommand(t, srv, sess, "hosts")
	if hostsRes.cmdErr != "" || !strings.Contains(hostsRes.output, "remote-a") || !strings.Contains(hostsRes.output, "disconnected") {
		t.Fatalf("hosts result = %#v", hostsRes)
	}

	disconnectRes := runTestCommand(t, srv, sess, "disconnect", "remote-a")
	if disconnectRes.cmdErr != `host "remote-a" not connected` {
		t.Fatalf("disconnect result = %#v", disconnectRes)
	}

	reconnectRes := runTestCommand(t, srv, sess, "reconnect", "remote-a")
	if reconnectRes.cmdErr != `host "remote-a" not known` {
		t.Fatalf("reconnect result = %#v", reconnectRes)
	}
}

func TestSessionWindowHelpers(t *testing.T) {
	t.Parallel()

	sess := &Session{}
	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")

	w1 := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	w2 := newTestWindowWithPanes(t, sess, 2, "logs", p3)
	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID

	if got := sess.ResolveWindow("2"); got != w2 {
		t.Fatalf("ResolveWindow(2) = %#v, want logs window", got)
	}
	if got := sess.ResolveWindow("main"); got != w1 {
		t.Fatalf("ResolveWindow(main) = %#v, want main window", got)
	}
	if got := sess.ResolveWindow("lo"); got != w2 {
		t.Fatalf("ResolveWindow(lo) = %#v, want logs window", got)
	}

	sess.NextWindow()
	if sess.ActiveWindowID != w2.ID {
		t.Fatalf("NextWindow active = %d, want %d", sess.ActiveWindowID, w2.ID)
	}
	sess.PrevWindow()
	if sess.ActiveWindowID != w1.ID {
		t.Fatalf("PrevWindow active = %d, want %d", sess.ActiveWindowID, w1.ID)
	}

	if got := sess.closePaneInWindow(p3.ID); got != "logs" {
		t.Fatalf("closePaneInWindow(last pane) = %q, want %q", got, "logs")
	}
	if len(sess.Windows) != 1 || sess.ActiveWindowID != w1.ID {
		t.Fatalf("windows after closing logs = %d active=%d", len(sess.Windows), sess.ActiveWindowID)
	}

	if got := sess.closePaneInWindow(p2.ID); got != "" {
		t.Fatalf("closePaneInWindow(non-last) = %q, want empty", got)
	}
	if w1.PaneCount() != 1 {
		t.Fatalf("main pane count after close = %d, want 1", w1.PaneCount())
	}

	sess.RemoveWindow(w1.ID)
	if len(sess.Windows) != 0 {
		t.Fatalf("RemoveWindow left %d windows, want 0", len(sess.Windows))
	}
}

func TestParseKeyArgsAndEncodeKeyChunks(t *testing.T) {
	t.Parallel()

	hexMode, keys := parseKeyArgs([]string{"--hex", "61", "62"})
	if !hexMode || len(keys) != 2 || keys[0] != "61" || keys[1] != "62" {
		t.Fatalf("parseKeyArgs() = (%v, %v)", hexMode, keys)
	}

	hexChunks, err := encodeKeyChunks(true, []string{"61", "0d"})
	if err != nil {
		t.Fatalf("encodeKeyChunks(hex): %v", err)
	}
	if len(hexChunks) != 2 || string(hexChunks[0].data) != "a" || string(hexChunks[1].data) != "\r" {
		t.Fatalf("hex chunks = %#v", hexChunks)
	}

	plainChunks, err := encodeKeyChunks(false, []string{"hello", "Enter", "C-c"})
	if err != nil {
		t.Fatalf("encodeKeyChunks(plain): %v", err)
	}
	if len(plainChunks) != 3 {
		t.Fatalf("plain chunk count = %d, want 3", len(plainChunks))
	}
	if string(plainChunks[0].data) != "hello" || plainChunks[0].paceBefore {
		t.Fatalf("plain chunk[0] = %#v", plainChunks[0])
	}
	if string(plainChunks[1].data) != "\r" || !plainChunks[1].paceBefore {
		t.Fatalf("plain chunk[1] = %#v", plainChunks[1])
	}
	if len(plainChunks[2].data) != 1 || plainChunks[2].data[0] != 3 || !plainChunks[2].paceBefore {
		t.Fatalf("plain chunk[2] = %#v", plainChunks[2])
	}

	if _, err := encodeKeyChunks(true, []string{"zz"}); err == nil || err.Error() != "invalid hex: zz" {
		t.Fatalf("encodeKeyChunks invalid hex error = %v", err)
	}
}
