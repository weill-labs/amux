package test

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
	"github.com/weill-labs/amux/internal/server"
)

// headlessClient is a lightweight attached client that maintains emulators
// and responds to MsgTypeCaptureRequest. It runs without a terminal —
// used by ServerHarness so capture always routes through a client.
type headlessClient struct {
	conn         net.Conn
	mu           sync.Mutex
	emulators    map[uint32]mux.TerminalEmulator
	paneInfo     map[uint32]proto.PaneSnapshot
	layout       *mux.LayoutCell
	activePaneID uint32
	zoomedPaneID uint32
	sessionName  string
	width        int
	height       int
	windows      []proto.WindowSnapshot // for JSON capture window info
	activeWinID  uint32
	done         chan struct{}
}

// newHeadlessClient attaches to the server and starts a background message
// loop. The connection stays alive until close() is called.
func newHeadlessClient(sockPath, session string, cols, rows int) (*headlessClient, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: session,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		conn.Close()
		return nil, err
	}

	hc := &headlessClient{
		conn:      conn,
		emulators: make(map[uint32]mux.TerminalEmulator),
		paneInfo:  make(map[uint32]proto.PaneSnapshot),
		width:     cols,
		height:    rows,
		done:      make(chan struct{}),
	}

	go hc.readLoop()
	return hc, nil
}

func (hc *headlessClient) close() {
	hc.conn.Close()
	<-hc.done
}

func (hc *headlessClient) readLoop() {
	defer close(hc.done)
	for {
		msg, err := server.ReadMsg(hc.conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case server.MsgTypeLayout:
			hc.handleLayout(msg.Layout)
		case server.MsgTypePaneOutput:
			hc.handlePaneOutput(msg.PaneID, msg.PaneData)
		case server.MsgTypeCaptureRequest:
			resp := hc.handleCapture(msg.CmdArgs, msg.AgentStatus)
			server.WriteMsg(hc.conn, resp)
		case server.MsgTypeExit:
			return
		}
	}
}

func (hc *headlessClient) handleLayout(snap *proto.LayoutSnapshot) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.sessionName = snap.SessionName
	hc.activePaneID = snap.ActivePaneID
	hc.zoomedPaneID = snap.ZoomedPaneID
	hc.width = snap.Width
	hc.height = snap.Height + render.GlobalBarHeight

	allPanes := snap.Panes
	activeRoot := snap.Root
	if len(snap.Windows) > 0 {
		allPanes = nil
		hc.windows = snap.Windows
		hc.activeWinID = snap.ActiveWindowID
		for _, ws := range snap.Windows {
			allPanes = append(allPanes, ws.Panes...)
			if ws.ID == snap.ActiveWindowID {
				activeRoot = ws.Root
				hc.activePaneID = ws.ActivePaneID
			}
		}
	}

	newIDs := make(map[uint32]bool, len(allPanes))
	for _, ps := range allPanes {
		newIDs[ps.ID] = true
		hc.paneInfo[ps.ID] = ps
	}

	for _, ps := range allPanes {
		if _, exists := hc.emulators[ps.ID]; !exists {
			var w, h int
			if ps.Minimized && ps.EmuWidth > 0 && ps.EmuHeight > 0 {
				w, h = ps.EmuWidth, ps.EmuHeight
			} else {
				w, h = findCellDimensions(snap, activeRoot, ps.ID)
			}
			hc.emulators[ps.ID] = mux.NewVTEmulatorWithDrain(w, h)
		}
	}

	for id := range hc.emulators {
		if !newIDs[id] {
			delete(hc.emulators, id)
			delete(hc.paneInfo, id)
		}
	}

	hc.layout = mux.RebuildLayout(activeRoot)

	hc.layout.Walk(func(cell *mux.LayoutCell) {
		if emu, ok := hc.emulators[cell.PaneID]; ok {
			if info, ok := hc.paneInfo[cell.PaneID]; ok && info.Minimized {
				return
			}
			emu.Resize(cell.W, mux.PaneContentHeight(cell.H))
		}
	})

	if hc.zoomedPaneID != 0 {
		if emu, ok := hc.emulators[hc.zoomedPaneID]; ok {
			layoutH := hc.height - render.GlobalBarHeight
			emu.Resize(hc.width, mux.PaneContentHeight(layoutH))
		}
	}
}

func (hc *headlessClient) handlePaneOutput(paneID uint32, data []byte) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if emu, ok := hc.emulators[paneID]; ok {
		emu.Write(data)
	}
}

func (hc *headlessClient) handleCapture(args []string, agentStatus map[uint32]proto.PaneAgentStatus) *server.Message {
	includeANSI := false
	colorMap := false
	formatJSON := false
	var paneRef string
	for _, arg := range args {
		switch arg {
		case "--ansi":
			includeANSI = true
		case "--colors":
			colorMap = true
		case "--format":
		case "json":
			formatJSON = true
		default:
			paneRef = arg
		}
	}

	flagCount := 0
	if includeANSI {
		flagCount++
	}
	if colorMap {
		flagCount++
	}
	if formatJSON {
		flagCount++
	}
	if flagCount > 1 {
		return &server.Message{Type: server.MsgTypeCaptureResponse,
			CmdErr: "--ansi, --colors, and --format json are mutually exclusive"}
	}

	if paneRef != "" {
		if colorMap {
			return &server.Message{Type: server.MsgTypeCaptureResponse,
				CmdErr: "--colors is only supported for full screen capture"}
		}
		paneID := hc.resolvePaneID(paneRef)
		if paneID == 0 {
			return &server.Message{Type: server.MsgTypeCaptureResponse,
				CmdErr: fmt.Sprintf("pane %q not found", paneRef)}
		}
		var out string
		if formatJSON {
			out = hc.capturePaneJSON(paneID, agentStatus)
		} else {
			out = hc.capturePaneText(paneID, includeANSI)
		}
		return &server.Message{Type: server.MsgTypeCaptureResponse, CmdOutput: out + "\n"}
	}

	var out string
	if formatJSON {
		out = hc.captureJSON(agentStatus) + "\n"
	} else if colorMap {
		out = hc.captureColorMap()
	} else {
		out = hc.captureScreen(!includeANSI)
	}
	return &server.Message{Type: server.MsgTypeCaptureResponse, CmdOutput: out}
}

// ---------------------------------------------------------------------------
// Capture rendering — mirrors ClientRenderer capture methods
// ---------------------------------------------------------------------------

func (hc *headlessClient) captureScreen(stripANSI bool) string {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.layout == nil {
		return ""
	}

	root, activePaneID := hc.captureRoot()
	comp := render.NewCompositor(hc.width, hc.height, hc.sessionName)
	raw := string(comp.RenderFull(root, activePaneID, hc.paneLookup))

	if stripANSI {
		return render.MaterializeGrid(raw, hc.width, hc.height)
	}
	return raw
}

func (hc *headlessClient) captureColorMap() string {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.layout == nil {
		return ""
	}

	root, activePaneID := hc.captureRoot()
	comp := render.NewCompositor(hc.width, hc.height, hc.sessionName)
	raw := string(comp.RenderFull(root, activePaneID, hc.paneLookup))
	return render.ExtractColorMap(raw, hc.width, hc.height) + "\n"
}

func (hc *headlessClient) captureJSON(agentStatus map[uint32]proto.PaneAgentStatus) string {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.layout == nil {
		return "{}"
	}

	root := hc.layout
	layoutH := hc.height - render.GlobalBarHeight
	if hc.zoomedPaneID != 0 {
		root = mux.NewLeafByID(hc.zoomedPaneID, 0, 0, hc.width, layoutH)
	}

	capture := proto.CaptureJSON{
		Session: hc.sessionName,
		Width:   hc.width,
		Height:  layoutH,
	}
	// Populate window info from the active window snapshot
	for _, ws := range hc.windows {
		if ws.ID == hc.activeWinID {
			capture.Window = proto.CaptureWindow{
				ID:    ws.ID,
				Name:  ws.Name,
				Index: ws.Index,
			}
			break
		}
	}

	root.Walk(func(c *mux.LayoutCell) {
		paneID := c.CellPaneID()
		if paneID == 0 {
			return
		}
		emu, ok := hc.emulators[paneID]
		if !ok {
			return
		}
		info, ok := hc.paneInfo[paneID]
		if !ok {
			return
		}
		col, row := emu.CursorPosition()
		cp := proto.CapturePane{
			ID:        info.ID,
			Name:      info.Name,
			Active:    info.ID == hc.activePaneID,
			Minimized: info.Minimized,
			Zoomed:    info.ID == hc.zoomedPaneID,
			Host:      info.Host,
			Task:      info.Task,
			Color:     info.Color,
			Position: &proto.CapturePos{
				X: c.X, Y: c.Y, Width: c.W, Height: c.H,
			},
			Cursor: proto.CaptureCursor{Col: col, Row: row, Hidden: emu.CursorHidden()},
			Content: emuContentLines(emu),
		}
		if st, ok := agentStatus[paneID]; ok {
			cp.Idle = st.Idle
			cp.IdleSince = st.IdleSince
			cp.CurrentCommand = st.CurrentCommand
			if st.ChildPIDs != nil { cp.ChildPIDs = st.ChildPIDs } else { cp.ChildPIDs = []int{} }
		}
		capture.Panes = append(capture.Panes, cp)
	})

	out, _ := json.MarshalIndent(capture, "", "  ")
	return string(out)
}

func (hc *headlessClient) capturePaneText(paneID uint32, includeANSI bool) string {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	emu, ok := hc.emulators[paneID]
	if !ok {
		return ""
	}
	if includeANSI {
		return emu.Render()
	}
	return strings.Join(emuContentLines(emu), "\n")
}

func (hc *headlessClient) capturePaneJSON(paneID uint32, agentStatus map[uint32]proto.PaneAgentStatus) string {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	emu, ok := hc.emulators[paneID]
	if !ok {
		return "{}"
	}
	info, ok := hc.paneInfo[paneID]
	if !ok {
		return "{}"
	}
	col, row := emu.CursorPosition()
	cp := proto.CapturePane{
		ID: info.ID, Name: info.Name,
		Active: info.ID == hc.activePaneID, Minimized: info.Minimized,
		Zoomed: info.ID == hc.zoomedPaneID, Host: info.Host,
		Task: info.Task, Color: info.Color,
		Cursor:  proto.CaptureCursor{Col: col, Row: row, Hidden: emu.CursorHidden()},
		Content: emuContentLines(emu),
	}
	if st, ok := agentStatus[paneID]; ok {
		cp.Idle = st.Idle
		cp.IdleSince = st.IdleSince
		cp.CurrentCommand = st.CurrentCommand
		if st.ChildPIDs != nil { cp.ChildPIDs = st.ChildPIDs } else { cp.ChildPIDs = []int{} }
	}
	out, _ := json.MarshalIndent(cp, "", "  ")
	return string(out)
}

func (hc *headlessClient) resolvePaneID(ref string) uint32 {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if id, err := fmt.Sscanf(ref, "%d", new(uint32)); err == nil && id == 1 {
		var n uint32
		fmt.Sscanf(ref, "%d", &n)
		if _, ok := hc.paneInfo[n]; ok {
			return n
		}
	}
	var prefixMatch uint32
	for _, info := range hc.paneInfo {
		if info.Name == ref {
			return info.ID
		}
		if strings.HasPrefix(info.Name, ref) {
			prefixMatch = info.ID
		}
	}
	return prefixMatch
}

// captureRoot returns the layout root for capture. Caller must hold hc.mu.
func (hc *headlessClient) captureRoot() (*mux.LayoutCell, uint32) {
	root := hc.layout
	if hc.zoomedPaneID != 0 {
		layoutH := hc.height - render.GlobalBarHeight
		root = mux.NewLeafByID(hc.zoomedPaneID, 0, 0, hc.width, layoutH)
	}
	return root, hc.activePaneID
}

// paneLookup returns PaneData for rendering. Caller must hold hc.mu.
func (hc *headlessClient) paneLookup(paneID uint32) render.PaneData {
	emu, ok := hc.emulators[paneID]
	if !ok {
		return nil
	}
	info, ok := hc.paneInfo[paneID]
	if !ok {
		return nil
	}
	return &headlessPaneData{emu: emu, info: info}
}

// headlessPaneData adapts emulator + snapshot for the PaneData interface.
type headlessPaneData struct {
	emu  mux.TerminalEmulator
	info proto.PaneSnapshot
}

func (p *headlessPaneData) RenderScreen(active bool) string {
	if !active {
		return p.emu.RenderWithoutCursorBlock()
	}
	return p.emu.Render()
}
func (p *headlessPaneData) CursorPos() (int, int)  { return p.emu.CursorPosition() }
func (p *headlessPaneData) CursorHidden() bool      { return p.emu.CursorHidden() }
func (p *headlessPaneData) HasCursorBlock() bool     { return p.emu.HasCursorBlock() }
func (p *headlessPaneData) ID() uint32               { return p.info.ID }
func (p *headlessPaneData) Name() string             { return p.info.Name }
func (p *headlessPaneData) Host() string             { return p.info.Host }
func (p *headlessPaneData) Task() string             { return p.info.Task }
func (p *headlessPaneData) Color() string            { return p.info.Color }
func (p *headlessPaneData) Minimized() bool          { return p.info.Minimized }
func (p *headlessPaneData) Idle() bool               { return p.info.Idle }
func (p *headlessPaneData) InCopyMode() bool         { return false }
func (p *headlessPaneData) CopyModeSearch() string   { return "" }

// findCellDimensions finds a pane's dimensions in the layout snapshot.
func findCellDimensions(snap *proto.LayoutSnapshot, activeRoot proto.CellSnapshot, paneID uint32) (int, int) {
	if cell := findCellByID(activeRoot, paneID); cell != nil {
		return cell.W, mux.PaneContentHeight(cell.H)
	}
	for _, ws := range snap.Windows {
		if cell := findCellByID(ws.Root, paneID); cell != nil {
			return cell.W, mux.PaneContentHeight(cell.H)
		}
	}
	return snap.Width, mux.PaneContentHeight(snap.Height)
}

func findCellByID(cs proto.CellSnapshot, paneID uint32) *proto.CellSnapshot {
	if cs.IsLeaf && cs.PaneID == paneID {
		return &cs
	}
	for i := range cs.Children {
		if found := findCellByID(cs.Children[i], paneID); found != nil {
			return found
		}
	}
	return nil
}

// emuContentLines returns plain-text screen lines from an emulator.
func emuContentLines(emu mux.TerminalEmulator) []string {
	_, rows := emu.Size()
	rendered := emu.Render()
	all := strings.Split(rendered, "\n")
	result := make([]string, rows)
	for i := 0; i < rows && i < len(all); i++ {
		result[i] = mux.StripANSI(strings.TrimRight(all[i], " "))
	}
	return result
}
