package client

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

const attachBootstrapPeakHeapSubprocessEnv = "AMUX_TEST_ATTACH_BOOTSTRAP_PEAK_HEAP"

func TestReadAttachBootstrapKeepsPeakHeapUnderBound(t *testing.T) {
	t.Parallel()

	if os.Getenv(attachBootstrapPeakHeapSubprocessEnv) == "1" {
		runReadAttachBootstrapPeakHeapTest(t)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestReadAttachBootstrapKeepsPeakHeapUnderBound$", "-test.parallel=1")
	cmd.Env = append(os.Environ(), attachBootstrapPeakHeapSubprocessEnv+"=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("peak-heap subprocess failed: %v\n%s", err, output)
	}
}

func runReadAttachBootstrapPeakHeapTest(t *testing.T) {
	const (
		paneCount     = 32
		historyLines  = 128
		lineWidth     = 120
		layoutHeight  = 24
		peakHeapLimit = 256 << 20
		sessionName   = "bootstrap-memory"
	)

	stream := buildAttachBootstrapReplay(t, sessionName, paneCount, historyLines, lineWidth, layoutHeight)
	conn := &replayAttachConn{Reader: bytes.NewReader(stream)}
	cr := NewClientRenderer(lineWidth, layoutHeight+1)
	t.Cleanup(cr.renderer.Close)

	peakHeap, err := samplePeakHeapAlloc(func() error {
		return readAttachBootstrap(conn, proto.NewReader(conn), cr)
	})
	if err != nil {
		t.Fatalf("readAttachBootstrap: %v", err)
	}

	if got := len(cr.loadState().baseHistory); got != paneCount {
		t.Fatalf("base history pane count = %d, want %d", got, paneCount)
	}
	if got := len(cr.loadState().baseHistory[uint32(paneCount)]); got != historyLines {
		t.Fatalf("pane-%d history lines = %d, want %d", paneCount, got, historyLines)
	}
	t.Logf("attach bootstrap peak heap delta = %d MiB", peakHeap>>20)
	if peakHeap > peakHeapLimit {
		t.Fatalf("attach bootstrap peak heap = %d MiB, want <= %d MiB", peakHeap>>20, peakHeapLimit>>20)
	}
}

func buildAttachBootstrapReplay(t *testing.T, sessionName string, paneCount, historyLines, lineWidth, layoutHeight int) []byte {
	t.Helper()

	layout := memoryAttachLayoutSnapshot(sessionName, paneCount, lineWidth, layoutHeight)
	history := repeatedStyledHistory(historyLines, lineWidth)
	plainHistory := proto.StyledLineText(history)
	screen := []byte("\033[2J\033[H" + strings.Repeat("x", lineWidth))

	var buf bytes.Buffer
	writer := proto.NewWriter(&buf)
	writer.SetBinaryPaneHistory(true)
	if err := writer.WriteMsg(&proto.Message{Type: proto.MsgTypeLayout, Layout: layout}); err != nil {
		t.Fatalf("write layout: %v", err)
	}
	for i := 0; i < paneCount; i++ {
		paneID := uint32(i + 1)
		if err := writer.WriteMsg(&proto.Message{
			Type:          proto.MsgTypePaneHistory,
			PaneID:        paneID,
			History:       plainHistory,
			StyledHistory: history,
		}); err != nil {
			t.Fatalf("write pane history for pane %d: %v", paneID, err)
		}
		if err := writer.WriteMsg(&proto.Message{
			Type:     proto.MsgTypePaneOutput,
			PaneID:   paneID,
			PaneData: screen,
		}); err != nil {
			t.Fatalf("write pane output for pane %d: %v", paneID, err)
		}
	}
	if err := writer.WriteMsg(&proto.Message{Type: proto.MsgTypeBell}); err != nil {
		t.Fatalf("write correction terminator: %v", err)
	}
	return buf.Bytes()
}

func memoryAttachLayoutSnapshot(sessionName string, paneCount, width, height int) *proto.LayoutSnapshot {
	panes := make([]proto.PaneSnapshot, paneCount)
	windows := make([]proto.WindowSnapshot, paneCount)

	for i := 0; i < paneCount; i++ {
		paneID := uint32(i + 1)
		pane := proto.PaneSnapshot{
			ID:    paneID,
			Name:  fmt.Sprintf("pane-%d", paneID),
			Host:  "local",
			Color: "f5e0dc",
		}
		root := proto.CellSnapshot{
			X:      0,
			Y:      0,
			W:      width,
			H:      height,
			IsLeaf: true,
			Dir:    -1,
			PaneID: paneID,
		}
		panes[i] = pane
		windows[i] = proto.WindowSnapshot{
			ID:           paneID,
			Name:         fmt.Sprintf("window-%d", paneID),
			Index:        i + 1,
			ActivePaneID: paneID,
			Root:         root,
			Panes:        []proto.PaneSnapshot{pane},
		}
	}

	return &proto.LayoutSnapshot{
		SessionName:    sessionName,
		ActivePaneID:   1,
		Width:          width,
		Height:         height,
		Root:           windows[0].Root,
		Panes:          panes,
		Windows:        windows,
		ActiveWindowID: 1,
	}
}

func repeatedStyledHistory(lines, width int) []proto.StyledLine {
	lineText := strings.Repeat("h", width)
	lineCells := make([]proto.Cell, width)
	for i := 0; i < width; i++ {
		lineCells[i] = proto.Cell{Char: "h", Width: 1}
	}

	history := make([]proto.StyledLine, lines)
	for i := 0; i < lines; i++ {
		history[i] = proto.StyledLine{
			Text:  lineText,
			Cells: lineCells,
		}
	}
	return history
}

func samplePeakHeapAlloc(fn func() error) (uint64, error) {
	runtime.GC()
	runtime.GC()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	start := mem.HeapAlloc
	peak := start

	errCh := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		errCh <- fn()
	}()

	for {
		runtime.ReadMemStats(&mem)
		if mem.HeapAlloc > peak {
			peak = mem.HeapAlloc
		}

		select {
		case <-done:
			runtime.ReadMemStats(&mem)
			if mem.HeapAlloc > peak {
				peak = mem.HeapAlloc
			}
			if peak < start {
				return 0, <-errCh
			}
			return peak - start, <-errCh
		default:
			runtime.Gosched()
		}
	}
}

type replayAttachConn struct {
	*bytes.Reader
}

func (*replayAttachConn) Write([]byte) (int, error) { return 0, errors.New("unexpected write") }
func (*replayAttachConn) Close() error              { return nil }
func (*replayAttachConn) LocalAddr() net.Addr       { return stubAttachAddr("local") }
func (*replayAttachConn) RemoteAddr() net.Addr      { return stubAttachAddr("remote") }
func (*replayAttachConn) SetDeadline(time.Time) error {
	return nil
}
func (*replayAttachConn) SetReadDeadline(time.Time) error {
	return nil
}
func (*replayAttachConn) SetWriteDeadline(time.Time) error {
	return nil
}
