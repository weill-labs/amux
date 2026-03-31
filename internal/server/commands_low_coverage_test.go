package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newRecordingPane(sess *Session, id uint32, name string, sink interface{ Write([]byte) (int, error) }) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.AccentColor(id - 1),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		_, _ = sink.Write(data)
		return len(data), nil
	})
}

func newStandaloneProxyPane(id uint32, name string) *mux.Pane {
	return mux.NewProxyPaneWithScrollback(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.AccentColor(id - 1),
	}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
}

func testCaptureHexColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
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
	p2.Meta.Task = "build"
	p2.Meta.GitBranch = "feature/test"
	p2.Meta.PR = "123"
	w1 := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	w1.ZoomedPaneID = p1.ID

	p3 := newTestPane(sess, 3, "pane-3")
	w2 := newTestWindowWithPanes(t, sess, 2, "logs", p3)

	setSessionLayoutForTest(t, sess, w1.ID, []*mux.Window{w1, w2}, p1, p2, p3)

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
	if !strings.Contains(statusRes.output, "windows: 2, panes: 3 total") || !strings.Contains(statusRes.output, "pane-1 zoomed") {
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

	var sink lockedBuffer
	p1 := newRecordingPane(sess, 1, "pane-1", &sink)
	p2 := newTestPane(sess, 2, "pane-2")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)

	zoomRes := runTestCommand(t, srv, sess, "zoom")
	if zoomRes.cmdErr != "" || !strings.Contains(zoomRes.output, "Zoomed pane-2") {
		t.Fatalf("zoom result = %#v", zoomRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) uint32 { return sess.activeWindow().ZoomedPaneID }); got != p2.ID {
		t.Fatalf("zoomed pane = %d, want %d", got, p2.ID)
	}

	unzoomRes := runTestCommand(t, srv, sess, "zoom")
	if unzoomRes.cmdErr != "" || !strings.Contains(unzoomRes.output, "Unzoomed pane-2") {
		t.Fatalf("unzoom result = %#v", unzoomRes)
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

	setKVUsage := runTestCommand(t, srv, sess, "set-kv", "pane-1")
	if setKVUsage.cmdErr != "usage: set-kv <pane> key=value [key=value...]" {
		t.Fatalf("set-kv usage error = %q", setKVUsage.cmdErr)
	}

	setKVErr := runTestCommand(t, srv, sess, "set-kv", "pane-1", "nope")
	if setKVErr.cmdErr != `invalid key=value: "nope"` {
		t.Fatalf("set-kv invalid kv error = %q", setKVErr.cmdErr)
	}

	setKVRes := runTestCommand(t, srv, sess, "set-kv", "pane-1", "foo=bar", "task=ship", "branch=feature/x", "pr=456")
	if setKVRes.cmdErr != "" {
		t.Fatalf("set-kv error: %s", setKVRes.cmdErr)
	}
	meta := mustSessionQuery(t, sess, func(sess *Session) mux.PaneMeta { return sess.findPaneByID(p1.ID).Meta })
	if meta.Task != "ship" || meta.PR != "456" || meta.GitBranch != "feature/x" {
		t.Fatalf("pane metadata after set-kv = %#v", meta)
	}
	kvs := reflect.ValueOf(meta).FieldByName("KV")
	if !kvs.IsValid() {
		t.Fatal("PaneMeta.KV field missing")
	}
	if got := kvs.MapIndex(reflect.ValueOf("foo")); !got.IsValid() || got.String() != "bar" {
		t.Fatalf("KV[foo] = %#v, want bar", got)
	}

	getKVRes := runTestCommand(t, srv, sess, "get-kv", "pane-1")
	if getKVRes.cmdErr != "" {
		t.Fatalf("get-kv error: %s", getKVRes.cmdErr)
	}
	for _, want := range []string{"branch=feature/x", "foo=bar", "pr=456", "task=ship"} {
		if !strings.Contains(getKVRes.output, want) {
			t.Fatalf("get-kv output missing %q:\n%s", want, getKVRes.output)
		}
	}

	getKVSubset := runTestCommand(t, srv, sess, "get-kv", "pane-1", "foo", "task")
	if getKVSubset.cmdErr != "" {
		t.Fatalf("get-kv subset error: %s", getKVSubset.cmdErr)
	}
	if strings.Contains(getKVSubset.output, "branch=feature/x") || strings.Contains(getKVSubset.output, "pr=456") {
		t.Fatalf("get-kv subset output should only contain requested keys, got:\n%s", getKVSubset.output)
	}
	for _, want := range []string{"foo=bar", "task=ship"} {
		if !strings.Contains(getKVSubset.output, want) {
			t.Fatalf("get-kv subset output missing %q:\n%s", want, getKVSubset.output)
		}
	}

	rmKVUsage := runTestCommand(t, srv, sess, "rm-kv", "pane-1")
	if rmKVUsage.cmdErr != "usage: rm-kv <pane> key [key...]" {
		t.Fatalf("rm-kv usage error = %q", rmKVUsage.cmdErr)
	}

	rmKVRes := runTestCommand(t, srv, sess, "rm-kv", "pane-1", "task")
	if rmKVRes.cmdErr != "" {
		t.Fatalf("rm-kv error: %s", rmKVRes.cmdErr)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) string { return sess.findPaneByID(p1.ID).Meta.Task }); got != "" {
		t.Fatalf("task after rm-kv = %q, want empty", got)
	}
	getKVAfterRmKV := runTestCommand(t, srv, sess, "get-kv", "pane-1")
	if strings.Contains(getKVAfterRmKV.output, "task=") {
		t.Fatalf("get-kv after rm-kv should not include task key:\n%s", getKVAfterRmKV.output)
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
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)

	captureUsage := runTestCommand(t, srv, sess, "capture", "--history")
	if captureUsage.cmdErr != "--history requires a pane target" {
		t.Fatalf("capture usage error = %q", captureUsage.cmdErr)
	}

	captureRes := runTestCommand(t, srv, sess, "capture", "--history", "pane-1")
	if captureRes.cmdErr != "" || !strings.Contains(captureRes.output, "alpha") || !strings.Contains(captureRes.output, "marker") {
		t.Fatalf("capture history result = %#v", captureRes)
	}

	waitForUsage := runTestCommand(t, srv, sess, "wait", "content", "pane-1")
	if waitForUsage.cmdErr != "usage: wait content <pane> <substring> [--timeout <duration>]" {
		t.Fatalf("wait-for usage error = %q", waitForUsage.cmdErr)
	}

	waitForRes := runTestCommand(t, srv, sess, "wait", "content", "pane-1", "marker", "--timeout", "1ms")
	if waitForRes.cmdErr != "" || strings.TrimSpace(waitForRes.output) != "matched" {
		t.Fatalf("wait-for result = %#v", waitForRes)
	}

	waitIdleUsage := runTestCommand(t, srv, sess, "wait", "idle")
	if waitIdleUsage.cmdErr != "usage: wait idle <pane> [--settle <duration>] [--timeout <duration>]" {
		t.Fatalf("wait-idle usage error = %q", waitIdleUsage.cmdErr)
	}

	waitIdleRes := runTestCommand(t, srv, sess, "wait", "idle", "pane-1", "--settle", "0s", "--timeout", "1ms")
	if waitIdleRes.cmdErr != "" || strings.TrimSpace(waitIdleRes.output) != "idle" {
		t.Fatalf("wait-idle result = %#v", waitIdleRes)
	}

	waitExitedRes := runTestCommand(t, srv, sess, "wait", "exited", "pane-1", "--timeout", "1ms")
	if waitExitedRes.cmdErr != "" || strings.TrimSpace(waitExitedRes.output) != "exited" {
		t.Fatalf("wait-exited result = %#v", waitExitedRes)
	}

	waitBusyUsage := runTestCommand(t, srv, sess, "wait", "busy")
	if waitBusyUsage.cmdErr != "usage: wait busy <pane> [--timeout <duration>]" {
		t.Fatalf("wait-busy usage error = %q", waitBusyUsage.cmdErr)
	}

	waitBusyRes := runTestCommand(t, srv, sess, "wait", "busy", "pane-1", "--timeout", "1ms")
	if !strings.Contains(waitBusyRes.cmdErr, "timeout waiting for pane-1 to become busy") {
		t.Fatalf("wait-busy timeout error = %q", waitBusyRes.cmdErr)
	}
}

func TestCommandCaptureJSONIncludesTerminalMetadata(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newStandaloneProxyPane(1, "pane-1")
	p1.FeedOutput([]byte(
		"\x1b]10;#112233\x07" +
			"\x1b]11;#445566\x07" +
			"\x1b]12;#778899\x07" +
			"\x1b]8;;https://example.com\x07" +
			"\x1b[6 q" +
			"\x1b[?1049h",
	))

	w := newTestWindowWithPanes(t, sess, 1, "main", p1)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1}

	res := runTestCommand(t, srv, sess, "capture", "--format", "json", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q", res.cmdErr)
	}

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(res.output), &pane); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, res.output)
	}

	if pane.Cursor.Style != "bar" {
		t.Fatalf("cursor style = %q, want bar", pane.Cursor.Style)
	}
	if pane.Cursor.Blinking {
		t.Fatal("cursor blinking = true, want false for DECSCUSR 6")
	}
	if pane.Terminal == nil {
		t.Fatal("terminal metadata should be present")
	}
	if !pane.Terminal.AltScreen {
		t.Fatal("alt_screen = false, want true")
	}
	if pane.Terminal.ForegroundColor != "112233" {
		t.Fatalf("foreground_color = %q, want 112233", pane.Terminal.ForegroundColor)
	}
	if pane.Terminal.BackgroundColor != "445566" {
		t.Fatalf("background_color = %q, want 445566", pane.Terminal.BackgroundColor)
	}
	if pane.Terminal.CursorColor != "778899" {
		t.Fatalf("cursor_color = %q, want 778899", pane.Terminal.CursorColor)
	}
	if pane.Terminal.Hyperlink == nil || pane.Terminal.Hyperlink.URL != "https://example.com" {
		t.Fatalf("hyperlink = %+v, want active https://example.com", pane.Terminal.Hyperlink)
	}
	if pane.Terminal.Mouse == nil {
		t.Fatal("mouse metadata should be present")
	}
	if pane.Terminal.Mouse.Tracking != "none" {
		t.Fatalf("mouse tracking = %q, want none", pane.Terminal.Mouse.Tracking)
	}
	if got := len(pane.Terminal.Palette); got != 256 {
		t.Fatalf("palette len = %d, want 256", got)
	}
	if got, want := pane.Terminal.Palette[2], testCaptureHexColor(ansi.IndexedColor(2)); got != want {
		t.Fatalf("palette[2] = %q, want %q", got, want)
	}
}

func TestCommandWaitClientsAndTypeKeys(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)
	sess.generation.Store(7)
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.waiters.setClipboardStateForTest(5, "clip-data")
	})

	serverConn, peerConn := net.Pipe()
	defer serverConn.Close()
	defer peerConn.Close()

	uiClient := newClientConn(serverConn)
	defer uiClient.Close()
	uiClient.ID = "client-1"
	uiClient.cols = 100
	uiClient.rows = 30
	uiClient.displayPanesShown = true
	uiClient.chooserMode = "tree"
	uiClient.uiGeneration = 2
	uiClient.setNegotiatedCapabilities(proto.ClientCapabilities{KittyKeyboard: true, Hyperlinks: true})
	uiClient.initTypeKeyQueue()

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(uiClient)
		sess.ensureClientManager().setSizeOwnerForTest(uiClient)
	})

	genRes := runTestCommand(t, srv, sess, "cursor", "layout")
	if genRes.cmdErr != "" || strings.TrimSpace(genRes.output) != "7" {
		t.Fatalf("generation result = %#v", genRes)
	}

	layoutJSONRes := runTestCommand(t, srv, sess, "_layout-json")
	if layoutJSONRes.cmdErr != "" {
		t.Fatalf("_layout-json result = %#v", layoutJSONRes)
	}
	var layout proto.LayoutSnapshot
	if err := json.Unmarshal([]byte(layoutJSONRes.output), &layout); err != nil {
		t.Fatalf("json.Unmarshal(_layout-json): %v\noutput:\n%s", err, layoutJSONRes.output)
	}
	if layout.ActivePaneID != 1 || len(layout.Panes) != 1 || layout.Panes[0].Name != "pane-1" {
		t.Fatalf("_layout-json snapshot = %+v, want active pane-1", layout)
	}

	waitLayoutRes := runTestCommand(t, srv, sess, "wait", "layout", "--after", "6", "--timeout", "1ms")
	if waitLayoutRes.cmdErr != "" || strings.TrimSpace(waitLayoutRes.output) != "7" {
		t.Fatalf("wait-layout result = %#v", waitLayoutRes)
	}

	waitLayoutTimeout := runTestCommand(t, srv, sess, "wait", "layout", "--after", "99", "--timeout", "1ms")
	if !strings.Contains(waitLayoutTimeout.cmdErr, "timeout waiting for generation > 99") {
		t.Fatalf("wait-layout timeout error = %q", waitLayoutTimeout.cmdErr)
	}

	clipGenRes := runTestCommand(t, srv, sess, "cursor", "clipboard")
	if clipGenRes.cmdErr != "" || strings.TrimSpace(clipGenRes.output) != "5" {
		t.Fatalf("clipboard-gen result = %#v", clipGenRes)
	}

	waitClipboardRes := runTestCommand(t, srv, sess, "wait", "clipboard", "--after", "4", "--timeout", "1ms")
	if waitClipboardRes.cmdErr != "" || strings.TrimSpace(waitClipboardRes.output) != "clip-data" {
		t.Fatalf("wait-clipboard result = %#v", waitClipboardRes)
	}

	waitClipboardTimeout := runTestCommand(t, srv, sess, "wait", "clipboard", "--after", "99", "--timeout", "1ms")
	if waitClipboardTimeout.cmdErr != "timeout waiting for clipboard event" {
		t.Fatalf("wait-clipboard timeout error = %q", waitClipboardTimeout.cmdErr)
	}

	uiGenRes := runTestCommand(t, srv, sess, "cursor", "ui", "--client", "client-1")
	if uiGenRes.cmdErr != "" || strings.TrimSpace(uiGenRes.output) != "2" {
		t.Fatalf("ui-gen result = %#v", uiGenRes)
	}

	waitUIInvalid := runTestCommand(t, srv, sess, "wait", "ui", "totally-unknown")
	if waitUIInvalid.cmdErr != "unknown ui event: totally-unknown" {
		t.Fatalf("wait-ui invalid error = %q", waitUIInvalid.cmdErr)
	}

	waitUISuccess := runTestCommand(t, srv, sess, "wait", "ui", proto.UIEventDisplayPanesShown, "--after", "1", "--timeout", "1ms")
	if waitUISuccess.cmdErr != "" || strings.TrimSpace(waitUISuccess.output) != proto.UIEventDisplayPanesShown {
		t.Fatalf("wait-ui success result = %#v", waitUISuccess)
	}

	for _, command := range []string{"set-hook", "unset-hook", "list-hooks", "delegate", "refresh-meta"} {
		res := runTestCommand(t, srv, sess, command)
		if res.cmdErr != "unknown command: "+command {
			t.Fatalf("%s result = %#v", command, res)
		}
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
		if err := peerConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			readCh <- typeKeyRead{err: err}
			return
		}
		msg, err := ReadMsg(peerConn)
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

	p1 := mustCreatePane(t, sess, srv, 80, 23)
	p1.Start()
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)

	spawnUsage := runTestCommand(t, srv, sess, "spawn", "--task", "build")
	if spawnUsage.cmdErr != "" || !strings.Contains(spawnUsage.output, "Spawned") {
		t.Fatalf("spawn result = %#v", spawnUsage)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 2 {
		t.Fatalf("pane count after unnamed spawn = %d, want 2", got)
	}

	splitRes := runTestCommand(t, srv, sess, "split", "pane-1", "root", "v")
	if splitRes.cmdErr != "" || !strings.Contains(splitRes.output, "Split vertical: new pane") {
		t.Fatalf("split result = %#v", splitRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 3 {
		t.Fatalf("pane count after split = %d, want 3", got)
	}

	spawnRes := runTestCommand(t, srv, sess, "spawn", "--name", "worker-1", "--task", "build")
	if spawnRes.cmdErr != "" || !strings.Contains(spawnRes.output, "Spawned worker-1") {
		t.Fatalf("spawn result = %#v", spawnRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 4 {
		t.Fatalf("pane count after spawn = %d, want 4", got)
	}

	focusRes := runTestCommand(t, srv, sess, "focus", "pane-1")
	if focusRes.cmdErr != "" || !strings.Contains(focusRes.output, "Focused pane-1") {
		t.Fatalf("focus result = %#v", focusRes)
	}

	killRes := runTestCommand(t, srv, sess, "kill", "worker-1")
	if killRes.cmdErr != "" || !strings.Contains(killRes.output, "Killed worker-1") {
		t.Fatalf("kill result = %#v", killRes)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 3 {
		t.Fatalf("pane count after kill = %d, want 3", got)
	}

	serverConn, peerConn := net.Pipe()
	defer serverConn.Close()
	defer peerConn.Close()

	cc := newClientConn(serverConn)
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

	initial := readCmdResultEventWithTimeout(t, peerConn, 3*time.Second)
	if initial.Type != EventLayout {
		t.Fatalf("initial events message = %+v, want layout", initial)
	}

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.emitEvent(Event{Type: EventLayout, Generation: 9})
		return struct{}{}
	})

	ev := readCmdResultEventWithTimeout(t, peerConn, 3*time.Second)
	if ev.Type != EventLayout || ev.Generation != 9 {
		t.Fatalf("events message = %+v", ev)
	}

	_ = peerConn.Close()
	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.emitEvent(Event{Type: EventLayout, Generation: 10})
		return struct{}{}
	})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("events command did not exit after client disconnect")
	}
}

func TestCommandSplitAndSpawnKeepZoomAndFocus(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := mustCreatePane(t, sess, srv, 80, 23)
	p1.Start()
	w := mux.NewWindow(p1, 80, 23)
	w.ID = 1
	w.Name = "main"
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)

	splitRes := runTestCommand(t, srv, sess, "split", "pane-1")
	if splitRes.cmdErr != "" {
		t.Fatalf("initial split failed: %s", splitRes.cmdErr)
	}
	zoomRes := runTestCommand(t, srv, sess, "zoom", "pane-1")
	if zoomRes.cmdErr != "" {
		t.Fatalf("zoom failed: %s", zoomRes.cmdErr)
	}

	keptFocusSplit := runTestCommand(t, srv, sess, "split", "pane-1", "--name", "bg-split")
	if keptFocusSplit.cmdErr != "" {
		t.Fatalf("split failed: %s", keptFocusSplit.cmdErr)
	}
	stateAfterSplit := mustSessionQuery(t, sess, func(sess *Session) struct {
		activeID uint32
		zoomedID uint32
		hasPane  bool
	} {
		w := sess.activeWindow()
		return struct {
			activeID uint32
			zoomedID uint32
			hasPane  bool
		}{
			activeID: w.ActivePane.ID,
			zoomedID: w.ZoomedPaneID,
			hasPane: func() bool {
				_, err := sess.findPaneByRef("bg-split")
				return err == nil
			}(),
		}
	})
	if stateAfterSplit.activeID != p1.ID || stateAfterSplit.zoomedID != p1.ID || !stateAfterSplit.hasPane {
		t.Fatalf("state after split = %+v, want active pane-1, zoomed pane-1, bg-split present", stateAfterSplit)
	}

	keptFocusSpawn := runTestCommand(t, srv, sess, "spawn", "--name", "bg-worker", "--task", "build")
	if keptFocusSpawn.cmdErr != "" {
		t.Fatalf("spawn failed: %s", keptFocusSpawn.cmdErr)
	}
	stateAfterSpawn := mustSessionQuery(t, sess, func(sess *Session) struct {
		activeID uint32
		zoomedID uint32
		task     string
	} {
		w := sess.activeWindow()
		pane, _ := sess.findPaneByRef("bg-worker")
		task := ""
		if pane != nil {
			task = pane.Meta.Task
		}
		return struct {
			activeID uint32
			zoomedID uint32
			task     string
		}{
			activeID: w.ActivePane.ID,
			zoomedID: w.ZoomedPaneID,
			task:     task,
		}
	})
	if stateAfterSpawn.activeID != p1.ID || stateAfterSpawn.zoomedID != p1.ID || stateAfterSpawn.task != "build" {
		t.Fatalf("state after spawn = %+v, want active pane-1, zoomed pane-1, build task", stateAfterSpawn)
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
			args:       []string{"pane-1", "--vertical"},
			wantOutput: "Split vertical: new pane",
			wantDir:    mux.SplitVertical,
			wantPanes:  2,
		},
		{
			name:       "horizontal flag",
			args:       []string{"pane-1", "--horizontal"},
			wantOutput: "Split horizontal: new pane",
			wantDir:    mux.SplitHorizontal,
			wantPanes:  2,
		},
		{
			name: "root vertical flag",
			args: []string{"pane-1", "root", "--vertical"},
			setup: func(t *testing.T, srv *Server, sess *Session) {
				res := runTestCommand(t, srv, sess, "split", "pane-1")
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
			args:       []string{"pane-1", "v"},
			wantOutput: "Split vertical: new pane",
			wantDir:    mux.SplitVertical,
			wantPanes:  2,
		},
		{
			name:    "conflicting directions",
			args:    []string{"pane-1", "--vertical", "--horizontal"},
			wantErr: "conflicting split directions",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			p1 := mustCreatePane(t, sess, srv, 80, 23)
			p1.Start()
			w := mux.NewWindow(p1, 80, 23)
			w.ID = 1
			w.Name = "main"
			setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)

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
				return sess.activeWindow().Root.Dir
			}); got != tt.wantDir {
				t.Fatalf("split %v root dir = %v, want %v", tt.args, got, tt.wantDir)
			}
			if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != tt.wantPanes {
				t.Fatalf("split %v pane count = %d, want %d", tt.args, got, tt.wantPanes)
			}
		})
	}
}

func TestCommandSplitTargetsExplicitInactivePane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      string
		args         []string
		wantActiveID uint32
		newPaneName  string
	}{
		{
			name:         "pure split keeps the existing active pane",
			command:      "split",
			args:         []string{"pane-1", "--name", "target-split"},
			wantActiveID: 2,
			newPaneName:  "target-split",
		},
		{
			name:         "split --focus activates the new pane",
			command:      "split",
			args:         []string{"pane-1", "--name", "flag-focus-target", "--focus"},
			wantActiveID: 3,
			newPaneName:  "flag-focus-target",
		},
		{
			name:         "split-focus activates the new pane",
			command:      "split-focus",
			args:         []string{"pane-1", "--name", "focus-target"},
			wantActiveID: 3,
			newPaneName:  "focus-target",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			p1 := mustCreatePane(t, sess, srv, 80, 23)
			p1.Start()
			w := mux.NewWindow(p1, 80, 23)
			w.ID = 1
			w.Name = "main"
			setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)

			res := runTestCommand(t, srv, sess, "split", "pane-1")
			if res.cmdErr != "" {
				t.Fatalf("initial split failed: %s", res.cmdErr)
			}
			res = runTestCommand(t, srv, sess, "focus", "pane-2")
			if res.cmdErr != "" {
				t.Fatalf("focus pane-2 failed: %s", res.cmdErr)
			}

			res = runTestCommand(t, srv, sess, tt.command, tt.args...)
			if res.cmdErr != "" {
				t.Fatalf("%s %v failed: %s", tt.command, tt.args, res.cmdErr)
			}

			state := mustSessionQuery(t, sess, func(sess *Session) struct {
				activeID uint32
				p1Y      int
				p2Y      int
				p3Y      int
			} {
				w := sess.activeWindow()
				newPane, err := sess.findPaneByRef(tt.newPaneName)
				if err != nil {
					return struct {
						activeID uint32
						p1Y      int
						p2Y      int
						p3Y      int
					}{activeID: w.ActivePane.ID, p1Y: -1, p2Y: -1, p3Y: -1}
				}
				c1 := w.Root.FindPane(1)
				c2 := w.Root.FindPane(2)
				c3 := w.Root.FindPane(newPane.ID)
				return struct {
					activeID uint32
					p1Y      int
					p2Y      int
					p3Y      int
				}{
					activeID: w.ActivePane.ID,
					p1Y:      c1.Y,
					p2Y:      c2.Y,
					p3Y:      c3.Y,
				}
			})

			if state.activeID != tt.wantActiveID {
				t.Fatalf("active pane after split = %d, want %d", state.activeID, tt.wantActiveID)
			}
			if !(state.p1Y < state.p3Y && state.p3Y < state.p2Y) {
				t.Fatalf("explicit split should land between pane-1 and pane-2, got y positions pane-1=%d new=%d pane-2=%d", state.p1Y, state.p3Y, state.p2Y)
			}
		})
	}
}

func TestCommandSpawnFocusModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      string
		args         []string
		wantActiveID uint32
	}{
		{
			name:         "spawn keeps the existing active pane",
			command:      "spawn",
			args:         []string{"--name", "worker-pure", "--task", "build"},
			wantActiveID: 1,
		},
		{
			name:         "spawn --focus activates the new pane",
			command:      "spawn",
			args:         []string{"--name", "worker-flag-focus", "--task", "build", "--focus"},
			wantActiveID: 2,
		},
		{
			name:         "spawn-focus activates the new pane",
			command:      "spawn-focus",
			args:         []string{"--name", "worker-focus", "--task", "build"},
			wantActiveID: 2,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			p1 := mustCreatePane(t, sess, srv, 80, 23)
			p1.Start()
			w := mux.NewWindow(p1, 80, 23)
			w.ID = 1
			w.Name = "main"
			setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1)

			res := runTestCommand(t, srv, sess, tt.command, tt.args...)
			if res.cmdErr != "" {
				t.Fatalf("%s %v failed: %s", tt.command, tt.args, res.cmdErr)
			}

			state := mustSessionQuery(t, sess, func(sess *Session) struct {
				activeID uint32
				hasPane  bool
			} {
				w := sess.activeWindow()
				return struct {
					activeID uint32
					hasPane  bool
				}{
					activeID: w.ActivePane.ID,
					hasPane: func() bool {
						_, err := sess.findPaneByRef(tt.args[1])
						return err == nil
					}(),
				}
			})
			if state.activeID != tt.wantActiveID || !state.hasPane {
				t.Fatalf("%s state = %+v, want active %d with spawned pane present", tt.command, state, tt.wantActiveID)
			}
		})
	}
}

func TestFlushPendingOutputEventsAndHelpers(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	defer serverConn.Close()
	defer peerConn.Close()

	cc := newClientConn(serverConn)
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
		if err := peerConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			readCh <- flushRead{err: err}
			return
		}
		msg1, err := ReadMsg(peerConn)
		if err != nil {
			readCh <- flushRead{err: err}
			return
		}
		msg2, err := ReadMsg(peerConn)
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
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.configurePaneTransport(&stubPaneTransport{
			hostStatusByName: map[string]proto.ConnState{
				"remote-a": proto.Disconnected,
			},
			disconnectErrs: map[string]error{
				"remote-a": fmt.Errorf(`host "remote-a" not connected`),
			},
			reconnectErrs: map[string]error{
				"remote-a": fmt.Errorf(`host "remote-a" not known`),
			},
		}, cfg.HostColor)
	})

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

	if got := sess.resolveWindow("2"); got != w2 {
		t.Fatalf("resolveWindow(2) = %#v, want logs window", got)
	}
	if got := sess.resolveWindow("main"); got != w1 {
		t.Fatalf("resolveWindow(main) = %#v, want main window", got)
	}
	if got := sess.resolveWindow("lo"); got != w2 {
		t.Fatalf("resolveWindow(lo) = %#v, want logs window", got)
	}

	sess.nextWindow()
	if sess.ActiveWindowID != w2.ID {
		t.Fatalf("nextWindow active = %d, want %d", sess.ActiveWindowID, w2.ID)
	}
	sess.prevWindow()
	if sess.ActiveWindowID != w1.ID {
		t.Fatalf("prevWindow active = %d, want %d", sess.ActiveWindowID, w1.ID)
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

	sess.removeWindow(w1.ID)
	if len(sess.Windows) != 0 {
		t.Fatalf("removeWindow left %d windows, want 0", len(sess.Windows))
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
