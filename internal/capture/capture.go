package capture

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

// Request is the parsed shape of amux capture flags.
type Request struct {
	IncludeANSI     bool
	ColorMap        bool
	FormatJSON      bool
	DisplayMode     bool
	HistoryMode     bool
	RewrapSpecified bool
	RewrapRaw       string
	RewrapWidth     int
	PaneRef         string
}

// ParseArgs parses capture flags while preserving the existing loose CLI
// semantics: unknown positional args collapse to the last pane ref.
func ParseArgs(args []string) Request {
	var req Request
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ansi":
			req.IncludeANSI = true
		case "--colors":
			req.ColorMap = true
		case "--display":
			req.DisplayMode = true
		case "--history":
			req.HistoryMode = true
		case "--rewrap":
			req.RewrapSpecified = true
			if i+1 < len(args) {
				req.RewrapRaw = args[i+1]
				if width, err := strconv.Atoi(args[i+1]); err == nil {
					req.RewrapWidth = width
				}
				i++
			}
		case "--format":
			if i+1 < len(args) && args[i+1] == "json" {
				req.FormatJSON = true
				i++
			}
		default:
			req.PaneRef = args[i]
		}
	}
	return req
}

// ArgsForRequest reconstructs a normalized CLI arg list from a parsed request.
func ArgsForRequest(req Request) []string {
	args := make([]string, 0, 8)
	if req.IncludeANSI {
		args = append(args, "--ansi")
	}
	if req.ColorMap {
		args = append(args, "--colors")
	}
	if req.DisplayMode {
		args = append(args, "--display")
	}
	if req.HistoryMode {
		args = append(args, "--history")
	}
	if req.RewrapSpecified {
		args = append(args, "--rewrap")
		if req.RewrapRaw != "" {
			args = append(args, req.RewrapRaw)
		}
	}
	if req.FormatJSON {
		args = append(args, "--format", "json")
	}
	if req.PaneRef != "" {
		args = append(args, req.PaneRef)
	}
	return args
}

// ValidateScreenRequest applies the shared client-routed capture validation.
func ValidateScreenRequest(req Request) error {
	if (req.IncludeANSI && req.ColorMap) ||
		(req.IncludeANSI && req.FormatJSON) ||
		(req.ColorMap && req.FormatJSON) {
		return fmt.Errorf("--ansi, --colors, and --format json are mutually exclusive")
	}
	if req.DisplayMode && (req.IncludeANSI || req.ColorMap || req.FormatJSON || req.HistoryMode || req.PaneRef != "") {
		return fmt.Errorf("--display is mutually exclusive with other flags")
	}
	if req.RewrapSpecified {
		return fmt.Errorf("--rewrap requires --history")
	}
	return nil
}

// ValidateHistoryRequest applies the shared server-owned history capture validation.
func ValidateHistoryRequest(req Request) error {
	if !req.HistoryMode {
		return fmt.Errorf("internal error: captureHistory called without --history")
	}
	if req.IncludeANSI || req.ColorMap || req.DisplayMode {
		return fmt.Errorf("--history is mutually exclusive with --ansi, --colors, and --display")
	}
	if req.RewrapSpecified && req.RewrapWidth <= 0 {
		return fmt.Errorf("--rewrap requires a positive integer width")
	}
	if req.PaneRef == "" {
		return fmt.Errorf("--history requires a pane target")
	}
	return nil
}

// PaneInput holds the shared capture-pane fields assembled by both the client
// and server capture paths.
type PaneInput struct {
	ID            uint32
	Name          string
	Active        bool
	Lead          bool
	Zoomed        bool
	Host          string
	Task          string
	Color         string
	ColumnIndex   int
	ConnStatus    string
	Cwd           string
	GitBranch     string
	PR            string
	KV            map[string]string
	TrackedPRs    []proto.TrackedPR
	TrackedIssues []proto.TrackedIssue
	Cursor        proto.CaptureCursor
	Terminal      *proto.CaptureTerminal
	Content       []string
	History       []string
}

const lowerHexDigits = "0123456789abcdef"

func hexColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()

	var buf [6]byte
	appendHexByte(buf[0:2], uint8(r>>8))
	appendHexByte(buf[2:4], uint8(g>>8))
	appendHexByte(buf[4:6], uint8(b>>8))
	return string(buf[:])
}

func appendHexByte(dst []byte, v uint8) {
	dst[0] = lowerHexDigits[v>>4]
	dst[1] = lowerHexDigits[v&0x0f]
}

func CursorFromState(col, row int, hidden bool, state proto.TerminalState) proto.CaptureCursor {
	return proto.CaptureCursor{
		Col:      col,
		Row:      row,
		Hidden:   hidden,
		Style:    state.CursorStyle,
		Blinking: state.CursorBlinking,
	}
}

func TerminalFromState(state proto.TerminalState) *proto.CaptureTerminal {
	palette := make([]string, len(state.Palette))
	for i, c := range state.Palette {
		palette[i] = hexColor(c)
	}

	terminal := &proto.CaptureTerminal{
		AltScreen:       state.AltScreen,
		ForegroundColor: hexColor(state.ForegroundColor),
		BackgroundColor: hexColor(state.BackgroundColor),
		CursorColor:     hexColor(state.CursorColor),
		Mouse: &proto.CaptureMouseProtocol{
			Tracking: state.Mouse.TrackingName(),
			SGR:      state.Mouse.SGR,
		},
		Palette: palette,
	}
	if state.HyperlinkURL != "" || state.HyperlinkParams != "" {
		terminal.Hyperlink = &proto.CaptureHyperlink{
			URL:    state.HyperlinkURL,
			Params: state.HyperlinkParams,
		}
	}
	return terminal
}

func cloneTerminal(terminal *proto.CaptureTerminal) *proto.CaptureTerminal {
	if terminal == nil {
		return nil
	}
	cloned := *terminal
	if terminal.Mouse != nil {
		mouse := *terminal.Mouse
		cloned.Mouse = &mouse
	}
	if terminal.Hyperlink != nil {
		hyperlink := *terminal.Hyperlink
		cloned.Hyperlink = &hyperlink
	}
	cloned.Palette = append([]string(nil), terminal.Palette...)
	return &cloned
}

// BuildPane builds the common proto.CapturePane shape shared by both capture paths.
func BuildPane(input PaneInput, agentStatus map[uint32]proto.PaneAgentStatus) proto.CapturePane {
	cp := proto.CapturePane{
		ID:          input.ID,
		Name:        input.Name,
		Active:      input.Active,
		Lead:        input.Lead,
		Zoomed:      input.Zoomed,
		Host:        input.Host,
		Task:        input.Task,
		Color:       input.Color,
		ColumnIndex: input.ColumnIndex,
		Meta: proto.CaptureMeta{
			Task:          input.Task,
			GitBranch:     input.GitBranch,
			PR:            input.PR,
			KV:            cloneStringMap(input.KV),
			TrackedPRs:    proto.CloneTrackedPRs(input.TrackedPRs),
			TrackedIssues: proto.CloneTrackedIssues(input.TrackedIssues),
		},
		ConnStatus: input.ConnStatus,
		Cwd:        input.Cwd,
		GitBranch:  input.GitBranch,
		PR:         input.PR,
		Cursor:     input.Cursor,
		Terminal:   cloneTerminal(input.Terminal),
		Content:    append([]string(nil), input.Content...),
		History:    append([]string(nil), input.History...),
	}
	cp.ApplyAgentStatus(agentStatus)
	return cp
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func marshalIndented(v any) string {
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}

// JSONErrorOutput builds a capture-shaped JSON error payload.
func JSONErrorOutput(singlePane bool, code, message string) string {
	errPayload := &proto.CaptureError{Code: code, Message: message}
	if singlePane {
		return marshalIndented(proto.CapturePane{Error: errPayload})
	}
	return marshalIndented(proto.CaptureJSON{Error: errPayload})
}

// ValidateJSONOutput rejects blank, invalid, and empty-object JSON responses.
func ValidateJSONOutput(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("capture response was empty")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil {
		return fmt.Errorf("capture response was not valid JSON: %w", err)
	}
	if len(body) == 0 {
		return fmt.Errorf("capture response was an empty JSON object")
	}
	return nil
}

// TrimOuterBlankRows removes leading and trailing all-whitespace rows from a
// pane capture while preserving interior blank lines.
func TrimOuterBlankRows(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}

	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}

	return append([]string(nil), lines[start:end]...)
}

// TrimTrailingBlankRows removes trailing all-whitespace rows while preserving
// leading blank lines and interior blank lines.
func TrimTrailingBlankRows(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return append([]string(nil), lines[:end]...)
}
