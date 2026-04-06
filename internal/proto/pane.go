package proto

import "image/color"

// DefaultScrollbackLines is the retained history limit used by amux for pane
// scrollback on both server and client emulators.
const DefaultScrollbackLines = 10000

// MouseTrackingMode is the pane's current application mouse-tracking mode.
type MouseTrackingMode int

const (
	MouseTrackingNone MouseTrackingMode = iota
	MouseTrackingStandard
	MouseTrackingButton
	MouseTrackingAny
)

// MouseProtocol describes how a pane wants mouse events encoded.
type MouseProtocol struct {
	Tracking MouseTrackingMode
	SGR      bool
}

// Enabled reports whether the pane currently accepts mouse events.
func (p MouseProtocol) Enabled() bool {
	return p.Tracking != MouseTrackingNone
}

// TrackingName returns a stable string form for JSON capture and events.
func (p MouseProtocol) TrackingName() string {
	switch p.Tracking {
	case MouseTrackingStandard:
		return "standard"
	case MouseTrackingButton:
		return "button"
	case MouseTrackingAny:
		return "any"
	default:
		return "none"
	}
}

// TerminalState is the pane's non-text terminal metadata at a point in time.
type TerminalState struct {
	AltScreen       bool
	Mouse           MouseProtocol
	ForegroundColor color.Color
	BackgroundColor color.Color
	CursorColor     color.Color
	CursorStyle     string
	CursorBlinking  bool
	HyperlinkURL    string
	HyperlinkParams string
	Palette         []color.Color
}

// PaneMeta holds amux metadata for a pane.
type PaneMeta struct {
	Name          string            `json:"name"`
	Host          string            `json:"host"`
	Task          string            `json:"task,omitempty"`
	KV            map[string]string `json:"kv,omitempty"`
	Remote        string            `json:"remote,omitempty"`
	Color         string            `json:"color"`
	Dormant       bool              `json:"dormant,omitempty"`    // pane is in Session.Panes but not in any window layout (e.g., SSH takeover host)
	Dir           string            `json:"dir,omitempty"`        // working directory override for new shell
	GitBranch     string            `json:"git_branch,omitempty"` // cached git branch (auto-detected or manually set)
	PR            string            `json:"pr,omitempty"`         // PR number (set via escape sequence or CLI)
	TrackedPRs    []TrackedPR       `json:"tracked_prs,omitempty"`
	TrackedIssues []TrackedIssue    `json:"tracked_issues,omitempty"`
}

// CaptureHistoryLine is one retained scrollback row plus the width it was
// wrapped at when it entered live scrollback.
type CaptureHistoryLine struct {
	Text        string
	SourceWidth int
	Filled      bool
}

// StyledLine is one frozen line of text plus optional per-cell styling.
// Cells may be nil when only plain text is available.
type StyledLine struct {
	Text  string
	Cells []Cell
}

// CloneStyledLines deep-copies styled lines and their cell slices.
func CloneStyledLines(src []StyledLine) []StyledLine {
	if len(src) == 0 {
		return nil
	}
	dst := make([]StyledLine, len(src))
	for i, line := range src {
		dst[i].Text = line.Text
		if len(line.Cells) != 0 {
			dst[i].Cells = append([]Cell(nil), line.Cells...)
		}
	}
	return dst
}

// StyledLineText returns the text content for each styled line.
func StyledLineText(lines []StyledLine) []string {
	if len(lines) == 0 {
		return nil
	}
	text := make([]string, len(lines))
	for i, line := range lines {
		text[i] = line.Text
	}
	return text
}

// CaptureSnapshot is a consistent plain-text snapshot of a pane's retained
// history, visible screen, and cursor state.
type CaptureSnapshot struct {
	BaseHistory    []string
	LiveHistory    []CaptureHistoryLine
	History        []string
	ContentRows    []CaptureHistoryLine
	Content        []string
	Terminal       TerminalState
	Width          int
	CursorCol      int
	CursorRow      int
	CursorHidden   bool
	CursorBlockCol int
	CursorBlockRow int
	HasCursorBlock bool
}
