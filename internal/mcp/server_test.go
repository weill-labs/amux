package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type commandCall struct {
	name string
	args []string
}

type recordingRunner struct {
	output string
	err    error
	calls  []commandCall
}

func (r *recordingRunner) RunCommand(_ context.Context, name string, args []string) (string, error) {
	r.calls = append(r.calls, commandCall{name: name, args: append([]string(nil), args...)})
	return r.output, r.err
}

func TestMailboxToolsExposeTypedSchemas(t *testing.T) {
	t.Parallel()

	tools := MailboxTools()
	gotNames := make([]string, 0, len(tools))
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		gotNames = append(gotNames, tool.Name)
		byName[tool.Name] = tool
	}

	wantNames := []string{
		"amux_mailbox_send",
		"amux_mailbox_inbox",
		"amux_mailbox_read",
		"amux_mailbox_ack",
		"amux_mailbox_watch",
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("tool names = %#v, want %#v", gotNames, wantNames)
	}

	assertRequired(t, byName["amux_mailbox_send"].InputSchema, "from", "to", "body")
	assertPropertyType(t, byName["amux_mailbox_send"].InputSchema, "from", "string")
	assertPropertyType(t, byName["amux_mailbox_send"].InputSchema, "to", "array")
	assertPropertyType(t, byName["amux_mailbox_send"].InputSchema, "body", "string")

	assertRequired(t, byName["amux_mailbox_inbox"].InputSchema, "pane")
	assertPropertyType(t, byName["amux_mailbox_inbox"].InputSchema, "unread", "boolean")

	assertRequired(t, byName["amux_mailbox_read"].InputSchema, "id", "pane")
	assertPropertyType(t, byName["amux_mailbox_read"].InputSchema, "peek", "boolean")

	assertRequired(t, byName["amux_mailbox_ack"].InputSchema, "id", "pane")
	assertPropertyType(t, byName["amux_mailbox_ack"].InputSchema, "status", "string")

	assertRequired(t, byName["amux_mailbox_watch"].InputSchema, "pane")
	assertPropertyType(t, byName["amux_mailbox_watch"].InputSchema, "timeout_ms", "integer")
}

func TestCallToolDispatchesMailboxCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		output   string
		wantCall commandCall
	}{
		{
			name:   "send",
			tool:   "amux_mailbox_send",
			output: `{"id":"msg-000001","subject":"Review","thread_id":"msg-000001"}` + "\n",
			args: map[string]any{
				"from":    "pane-1",
				"to":      []any{"pane-2", "pane-3"},
				"subject": "Review",
				"topics":  []any{"build", "review"},
				"groups":  []any{"agents"},
				"body":    "please review",
			},
			wantCall: commandCall{
				name: "msg",
				args: []string{"send", "--from", "pane-1", "--to", "pane-2,pane-3", "--subject", "Review", "--topic", "build", "--topic", "review", "--group", "agents", "--body", "please review", "--format", "json"},
			},
		},
		{
			name:   "inbox",
			tool:   "amux_mailbox_inbox",
			output: `[{"id":"msg-000001","subject":"Review"}]` + "\n",
			args: map[string]any{
				"pane":   "pane-2",
				"unread": true,
			},
			wantCall: commandCall{name: "msg", args: []string{"inbox", "pane-2", "--unread", "--format", "json"}},
		},
		{
			name:   "read",
			tool:   "amux_mailbox_read",
			output: `{"id":"msg-000001","body":"please review"}` + "\n",
			args: map[string]any{
				"id":   "msg-000001",
				"pane": "pane-2",
				"peek": true,
			},
			wantCall: commandCall{name: "msg", args: []string{"read", "msg-000001", "--for", "pane-2", "--peek", "--format", "json"}},
		},
		{
			name:   "ack",
			tool:   "amux_mailbox_ack",
			output: `{"id":"msg-000001","delivery":{"ack_status":"ok"}}` + "\n",
			args: map[string]any{
				"id":     "msg-000001",
				"pane":   "pane-2",
				"status": "ok",
				"note":   "done",
			},
			wantCall: commandCall{name: "msg", args: []string{"ack", "msg-000001", "--for", "pane-2", "--status", "ok", "--note", "done", "--format", "json"}},
		},
		{
			name:   "watch",
			tool:   "amux_mailbox_watch",
			output: `{"id":"msg-000002","subject":"Ready"}` + "\n",
			args: map[string]any{
				"pane":       "pane-2",
				"topic":      "review",
				"after":      "msg-000001",
				"timeout_ms": float64(2500),
			},
			wantCall: commandCall{name: "wait", args: []string{"msg", "pane-2", "--topic", "review", "--after", "msg-000001", "--timeout", "2500ms", "--format", "json"}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := &recordingRunner{output: tt.output}
			server := NewServer(ServerOptions{Runner: runner})
			result, rpcErr := server.CallTool(context.Background(), CallToolParams{Name: tt.tool, Arguments: tt.args})
			if rpcErr != nil {
				t.Fatalf("CallTool() rpc error = %+v", rpcErr)
			}
			if result.IsError {
				t.Fatalf("CallTool() returned tool error: %+v", result)
			}
			if len(result.Content) != 1 || result.Content[0].Type != "text" || !strings.Contains(result.Content[0].Text, `"id"`) {
				t.Fatalf("content = %+v, want JSON text content", result.Content)
			}
			if result.StructuredContent == nil {
				t.Fatal("structuredContent is nil")
			}
			if !reflect.DeepEqual(runner.calls, []commandCall{tt.wantCall}) {
				t.Fatalf("runner calls = %#v, want %#v", runner.calls, []commandCall{tt.wantCall})
			}
		})
	}
}

func TestCallToolReturnsToolExecutionErrorsInResult(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{err: errors.New(`pane "missing" not found`)}
	server := NewServer(ServerOptions{Runner: runner})

	result, rpcErr := server.CallTool(context.Background(), CallToolParams{
		Name:      "amux_mailbox_inbox",
		Arguments: map[string]any{"pane": "missing"},
	})
	if rpcErr != nil {
		t.Fatalf("CallTool() rpc error = %+v, want tool result error", rpcErr)
	}
	if !result.IsError {
		t.Fatalf("CallTool() isError = false, want true")
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `pane "missing" not found`) {
		t.Fatalf("error content = %+v, want actionable command error", result.Content)
	}
}

func TestCallToolRejectsUnknownToolsAsProtocolErrors(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Runner: &recordingRunner{}})

	_, rpcErr := server.CallTool(context.Background(), CallToolParams{Name: "missing_tool"})
	if rpcErr == nil {
		t.Fatal("CallTool() error = nil, want protocol error")
	}
	if rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "Unknown tool: missing_tool") {
		t.Fatalf("rpc error = %+v, want unknown tool invalid params", rpcErr)
	}
}

func TestServeStdioHandlesJSONRPCRequests(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: `[{"id":"msg-000001"}]` + "\n"}
	server := NewServer(ServerOptions{Runner: runner, ServerName: "amux-test", ServerVersion: "test"})
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"amux_mailbox_inbox","arguments":{"pane":"pane-2"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve(): %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("response lines = %d, want 3\n%s", len(lines), out.String())
	}
	assertJSONRPCResultHas(t, lines[0], "serverInfo")
	assertJSONRPCResultHas(t, lines[1], "tools")
	assertJSONRPCResultHas(t, lines[2], "content")
}

func assertRequired(t *testing.T, schema map[string]any, names ...string) {
	t.Helper()

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema required = %#v, want []string", schema["required"])
	}
	for _, name := range names {
		found := false
		for _, requiredName := range required {
			if requiredName == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("schema required = %#v, missing %q", required, name)
		}
	}
}

func assertPropertyType(t *testing.T, schema map[string]any, name, wantType string) {
	t.Helper()

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v, want object", schema["properties"])
	}
	property, ok := properties[name].(map[string]any)
	if !ok {
		t.Fatalf("schema property %q = %#v, want object", name, properties[name])
	}
	if got := property["type"]; got != wantType {
		t.Fatalf("schema property %q type = %#v, want %q", name, got, wantType)
	}
}

func assertJSONRPCResultHas(t *testing.T, line, key string) {
	t.Helper()

	var response struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	if _, ok := response.Result[key]; !ok {
		t.Fatalf("response result = %#v, missing %q", response.Result, key)
	}
}
