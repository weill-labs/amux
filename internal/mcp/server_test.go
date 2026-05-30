package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	serverpkg "github.com/weill-labs/amux/internal/server"
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
				"from":     "pane-1",
				"to":       []any{"pane-2", "pane-3"},
				"subject":  "Review",
				"topics":   []any{"build", "review"},
				"groups":   []any{"agents"},
				"metadata": map[string]any{"priority": "high"},
				"reply_to": "msg-000000",
				"body":     "please review",
			},
			wantCall: commandCall{
				name: "msg",
				args: []string{"send", "--from", "pane-1", "--to", "pane-2,pane-3", "--subject", "Review", "--topic", "build", "--topic", "review", "--group", "agents", "--metadata", `{"priority":"high"}`, "--reply-to", "msg-000000", "--body", "please review", "--format", "json"},
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

func TestCallToolRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tool    string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "missing required string",
			tool:    "amux_mailbox_read",
			args:    map[string]any{"pane": "pane-2"},
			wantErr: `argument "id" is required`,
		},
		{
			name:    "empty recipient list",
			tool:    "amux_mailbox_send",
			args:    map[string]any{"from": "pane-1", "to": []any{}, "body": "body"},
			wantErr: `argument "to" must include at least one value`,
		},
		{
			name:    "wrong list item type",
			tool:    "amux_mailbox_send",
			args:    map[string]any{"from": "pane-1", "to": []any{"pane-2", 3}, "body": "body"},
			wantErr: `argument "to" item 1 must be a string`,
		},
		{
			name:    "wrong boolean type",
			tool:    "amux_mailbox_inbox",
			args:    map[string]any{"pane": "pane-2", "unread": "true"},
			wantErr: `argument "unread" must be a boolean`,
		},
		{
			name:    "metadata must be object",
			tool:    "amux_mailbox_send",
			args:    map[string]any{"from": "pane-1", "to": []string{"pane-2"}, "body": "body", "metadata": "nope"},
			wantErr: `argument "metadata" must be a JSON object`,
		},
		{
			name:    "fractional timeout",
			tool:    "amux_mailbox_watch",
			args:    map[string]any{"pane": "pane-2", "timeout_ms": 1.5},
			wantErr: `argument "timeout_ms" must be an integer`,
		},
		{
			name:    "negative timeout",
			tool:    "amux_mailbox_watch",
			args:    map[string]any{"pane": "pane-2", "timeout_ms": -1},
			wantErr: `argument "timeout_ms" must be non-negative`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := NewServer(ServerOptions{Runner: &recordingRunner{}})
			_, rpcErr := server.CallTool(context.Background(), CallToolParams{Name: tt.tool, Arguments: tt.args})
			if rpcErr == nil {
				t.Fatal("CallTool() error = nil, want argument error")
			}
			if !strings.Contains(rpcErr.Message, tt.wantErr) {
				t.Fatalf("CallTool() error = %q, want substring %q", rpcErr.Message, tt.wantErr)
			}
		})
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

func TestCallToolHandlesInternalOutputErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		output  string
		wantErr string
	}{
		{name: "empty output", output: "", wantErr: "empty JSON output"},
		{name: "invalid json output", output: "{", wantErr: "invalid JSON output"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := NewServer(ServerOptions{Runner: &recordingRunner{output: tt.output}})
			result, rpcErr := server.CallTool(context.Background(), CallToolParams{
				Name:      "amux_mailbox_inbox",
				Arguments: map[string]any{"pane": "pane-2"},
			})
			if rpcErr != nil {
				t.Fatalf("CallTool() rpc error = %+v, want tool result error", rpcErr)
			}
			if !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, tt.wantErr) {
				t.Fatalf("CallTool() result = %+v, want tool error containing %q", result, tt.wantErr)
			}
		})
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

func TestServeStdioReportsProtocolErrors(t *testing.T) {
	t.Parallel()

	server := NewServer(ServerOptions{Runner: &recordingRunner{}})
	input := strings.Join([]string{
		`{`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":"bad"}`,
		`{"jsonrpc":"2.0","id":5,"method":"missing"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve(): %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("response lines = %d, want 6\n%s", len(lines), out.String())
	}
	assertJSONRPCErrorContains(t, lines[0], -32700, "Parse error")
	assertJSONRPCErrorContains(t, lines[1], -32600, "jsonrpc")
	assertJSONRPCResultHas(t, lines[2], "")
	assertJSONRPCErrorContains(t, lines[3], -32602, "params are required")
	assertJSONRPCErrorContains(t, lines[4], -32602, "invalid tools/call params")
	assertJSONRPCErrorContains(t, lines[5], -32601, "Method not found")
}

func TestServerCommandRunnerRunCommand(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverErr := make(chan error, 1)
	go func() {
		reader := serverpkg.NewReader(serverConn)
		writer := serverpkg.NewWriter(serverConn)
		msg, err := reader.ReadMsg()
		if err != nil {
			serverErr <- err
			return
		}
		if msg.Type != serverpkg.MsgTypeCommand || msg.CmdName != "msg" || !reflect.DeepEqual(msg.CmdArgs, []string{"inbox", "pane-2"}) || msg.ActorPaneID != 42 {
			serverErr <- errors.New("unexpected command message")
			return
		}
		if err := writer.WriteMsg(&serverpkg.Message{Type: serverpkg.MsgTypeNotify}); err != nil {
			serverErr <- err
			return
		}
		if err := writer.WriteMsg(&serverpkg.Message{Type: serverpkg.MsgTypeCmdResult, CmdOutput: `{"ok":true}` + "\n"}); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	runner := ServerCommandRunner{
		SessionName: "test-session",
		ActorPaneID: 42,
		DialUnix: func(path string) (net.Conn, error) {
			if !strings.Contains(path, "test-session") {
				t.Fatalf("dial path = %q, want session socket path", path)
			}
			return clientConn, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	output, err := runner.RunCommand(ctx, "msg", []string{"inbox", "pane-2"})
	if err != nil {
		t.Fatalf("RunCommand(): %v", err)
	}
	if output != `{"ok":true}`+"\n" {
		t.Fatalf("RunCommand() output = %q, want JSON output", output)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server side: %v", err)
	}
}

func TestServerCommandRunnerErrors(t *testing.T) {
	t.Parallel()

	t.Run("dial error", func(t *testing.T) {
		t.Parallel()

		runner := ServerCommandRunner{
			SessionName: "test-session",
			DialUnix: func(string) (net.Conn, error) {
				return nil, errors.New("boom")
			},
		}
		if _, err := runner.RunCommand(context.Background(), "msg", nil); err == nil || !strings.Contains(err.Error(), "connecting to server: boom") {
			t.Fatalf("RunCommand() error = %v, want dial context", err)
		}
	})

	t.Run("command error", func(t *testing.T) {
		t.Parallel()

		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		go func() {
			_, _ = serverpkg.NewReader(serverConn).ReadMsg()
			_ = serverpkg.NewWriter(serverConn).WriteMsg(&serverpkg.Message{Type: serverpkg.MsgTypeCmdResult, CmdErr: `pane "missing" not found`})
		}()
		runner := ServerCommandRunner{
			SessionName: "test-session",
			DialUnix: func(string) (net.Conn, error) {
				return clientConn, nil
			},
		}
		if _, err := runner.RunCommand(context.Background(), "msg", nil); err == nil || !strings.Contains(err.Error(), `pane "missing" not found`) {
			t.Fatalf("RunCommand() error = %v, want command error", err)
		}
	})
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
	if key == "" {
		if response.Result == nil {
			t.Fatalf("response result = nil, want object")
		}
		return
	}
	if _, ok := response.Result[key]; !ok {
		t.Fatalf("response result = %#v, missing %q", response.Result, key)
	}
}

func assertJSONRPCErrorContains(t *testing.T, line string, code int, message string) {
	t.Helper()

	var response struct {
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	if response.Error == nil {
		t.Fatalf("response error = nil, want code %d", code)
	}
	if response.Error.Code != code || !strings.Contains(response.Error.Message, message) {
		t.Fatalf("response error = %+v, want code %d containing %q", response.Error, code, message)
	}
}
