package capture

import (
	"encoding/json"
	"fmt"
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
	ID         uint32
	Name       string
	Active     bool
	Minimized  bool
	Zoomed     bool
	Host       string
	Task       string
	Color      string
	ConnStatus string
	Cwd        string
	GitBranch  string
	PR         string
	PRs        []int
	Issues     []string
	Cursor     proto.CaptureCursor
	Content    []string
	History    []string
}

// BuildPane builds the common proto.CapturePane shape shared by both capture paths.
func BuildPane(input PaneInput, agentStatus map[uint32]proto.PaneAgentStatus) proto.CapturePane {
	cp := proto.CapturePane{
		ID:        input.ID,
		Name:      input.Name,
		Active:    input.Active,
		Minimized: input.Minimized,
		Zoomed:    input.Zoomed,
		Host:      input.Host,
		Task:      input.Task,
		Color:     input.Color,
		Meta: proto.CaptureMeta{
			Task:      input.Task,
			GitBranch: input.GitBranch,
			PR:        input.PR,
			PRs:       append([]int(nil), input.PRs...),
			Issues:    append([]string(nil), input.Issues...),
		},
		ConnStatus: input.ConnStatus,
		Cwd:        input.Cwd,
		GitBranch:  input.GitBranch,
		PR:         input.PR,
		Cursor:     input.Cursor,
		Content:    append([]string(nil), input.Content...),
		History:    append([]string(nil), input.History...),
	}
	cp.ApplyAgentStatus(agentStatus)
	return cp
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
