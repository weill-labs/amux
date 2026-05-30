package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type CommandRunner interface {
	RunCommand(context.Context, string, []string) (string, error)
}

type ServerOptions struct {
	Runner        CommandRunner
	ServerName    string
	ServerVersion string
}

type Server struct {
	runner        CommandRunner
	serverName    string
	serverVersion string
	tools         []Tool
}

func NewServer(opts ServerOptions) *Server {
	name := opts.ServerName
	if name == "" {
		name = "amux"
	}
	version := opts.ServerVersion
	if version == "" {
		version = "dev"
	}
	return &Server{
		runner:        opts.Runner,
		serverName:    name,
		serverVersion: version,
		tools:         MailboxTools(),
	}
}

func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(w)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			if err := encoder.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &RPCError{Code: -32700, Message: "Parse error: " + err.Error()},
			}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}

		resp := s.handleRequest(ctx, req)
		if err := encoder.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if req.JSONRPC != "2.0" {
		resp.Error = &RPCError{Code: -32600, Message: "Invalid Request: jsonrpc must be \"2.0\""}
		return resp
	}

	switch req.Method {
	case "initialize":
		resp.Result = s.initializeResult(req.Params)
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.tools}
	case "tools/call":
		var params CallToolParams
		if len(req.Params) == 0 {
			resp.Error = rpcInvalidParams("tools/call params are required")
			return resp
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = rpcInvalidParams("invalid tools/call params: " + err.Error())
			return resp
		}
		result, rpcErr := s.CallTool(ctx, params)
		if rpcErr != nil {
			resp.Error = rpcErr
			return resp
		}
		resp.Result = result
	default:
		resp.Error = rpcMethodNotFound(req.Method)
	}
	return resp
}

func (s *Server) initializeResult(params json.RawMessage) map[string]any {
	protocolVersion := ProtocolVersion
	if len(params) > 0 {
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &initParams); err == nil && initParams.ProtocolVersion != "" {
			protocolVersion = initParams.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    s.serverName,
			"version": s.serverVersion,
		},
	}
}

func (s *Server) CallTool(ctx context.Context, params CallToolParams) (CallToolResult, *RPCError) {
	if params.Name == "" {
		return CallToolResult{}, rpcInvalidParams("tool name is required")
	}
	if s.runner == nil {
		return toolErrorResult("amux command runner is not configured"), nil
	}

	command, args, wrapKey, rpcErr := buildToolCommand(params.Name, params.Arguments)
	if rpcErr != nil {
		return CallToolResult{}, rpcErr
	}
	output, err := s.runner.RunCommand(ctx, command, args)
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	return decodeToolOutput(output, wrapKey), nil
}

func buildToolCommand(name string, args map[string]any) (string, []string, string, *RPCError) {
	switch name {
	case "amux_mailbox_send":
		return buildSendCommand(args)
	case "amux_mailbox_inbox":
		return buildInboxCommand(args)
	case "amux_mailbox_read":
		return buildReadCommand(args)
	case "amux_mailbox_ack":
		return buildAckCommand(args)
	case "amux_mailbox_watch":
		return buildWatchCommand(args)
	default:
		return "", nil, "", rpcInvalidParams("Unknown tool: " + name)
	}
}

func buildSendCommand(args map[string]any) (string, []string, string, *RPCError) {
	from, rpcErr := requiredString(args, "from")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	to, rpcErr := requiredStringList(args, "to")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	body, rpcErr := requiredString(args, "body")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}

	out := []string{"send", "--from", from, "--to", strings.Join(to, ",")}
	if subject, ok, rpcErr := optionalString(args, "subject"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--subject", subject)
	}
	if topics, ok, rpcErr := optionalStringList(args, "topics"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		for _, topic := range topics {
			out = append(out, "--topic", topic)
		}
	}
	if groups, ok, rpcErr := optionalStringList(args, "groups"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		for _, group := range groups {
			out = append(out, "--group", group)
		}
	}
	if metadata, ok, rpcErr := optionalJSONObject(args, "metadata"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--metadata", metadata)
	}
	if replyTo, ok, rpcErr := optionalString(args, "reply_to"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--reply-to", replyTo)
	}
	out = append(out, "--body", body, "--format", "json")
	return "msg", out, "", nil
}

func buildInboxCommand(args map[string]any) (string, []string, string, *RPCError) {
	pane, rpcErr := requiredString(args, "pane")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	out := []string{"inbox", pane}
	if unread, ok, rpcErr := optionalBool(args, "unread"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok && unread {
		out = append(out, "--unread")
	}
	out = append(out, "--format", "json")
	return "msg", out, "messages", nil
}

func buildReadCommand(args map[string]any) (string, []string, string, *RPCError) {
	id, rpcErr := requiredString(args, "id")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	pane, rpcErr := requiredString(args, "pane")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	out := []string{"read", id, "--for", pane}
	if peek, ok, rpcErr := optionalBool(args, "peek"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok && peek {
		out = append(out, "--peek")
	}
	out = append(out, "--format", "json")
	return "msg", out, "", nil
}

func buildAckCommand(args map[string]any) (string, []string, string, *RPCError) {
	id, rpcErr := requiredString(args, "id")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	pane, rpcErr := requiredString(args, "pane")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	out := []string{"ack", id, "--for", pane}
	if status, ok, rpcErr := optionalString(args, "status"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--status", status)
	}
	if note, ok, rpcErr := optionalString(args, "note"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--note", note)
	}
	out = append(out, "--format", "json")
	return "msg", out, "", nil
}

func buildWatchCommand(args map[string]any) (string, []string, string, *RPCError) {
	pane, rpcErr := requiredString(args, "pane")
	if rpcErr != nil {
		return "", nil, "", rpcErr
	}
	out := []string{"msg", pane}
	if topic, ok, rpcErr := optionalString(args, "topic"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--topic", topic)
	}
	if after, ok, rpcErr := optionalString(args, "after"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--after", after)
	}
	if timeoutMS, ok, rpcErr := optionalInt64(args, "timeout_ms"); rpcErr != nil {
		return "", nil, "", rpcErr
	} else if ok {
		out = append(out, "--timeout", strconv.FormatInt(timeoutMS, 10)+"ms")
	}
	out = append(out, "--format", "json")
	return "wait", out, "", nil
}

func decodeToolOutput(output, wrapKey string) CallToolResult {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return toolErrorResult("amux command returned empty JSON output")
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return toolErrorResult("amux command returned invalid JSON output: " + err.Error())
	}

	structured, ok := decoded.(map[string]any)
	if !ok {
		if wrapKey == "" {
			wrapKey = "result"
		}
		structured = map[string]any{wrapKey: decoded}
	}
	textBytes, err := json.Marshal(structured)
	if err != nil {
		return toolErrorResult("amux command output could not be encoded: " + err.Error())
	}
	return CallToolResult{
		Content: []ContentBlock{{
			Type: "text",
			Text: string(textBytes),
		}},
		StructuredContent: structured,
	}
}

func toolErrorResult(message string) CallToolResult {
	return CallToolResult{
		Content: []ContentBlock{{
			Type: "text",
			Text: message,
		}},
		IsError: true,
	}
}

func requiredString(args map[string]any, key string) (string, *RPCError) {
	value, ok, rpcErr := optionalString(args, key)
	if rpcErr != nil {
		return "", rpcErr
	}
	if !ok {
		return "", rpcInvalidParams(fmt.Sprintf("argument %q is required", key))
	}
	return value, nil
}

func optionalString(args map[string]any, key string) (string, bool, *RPCError) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", false, nil
	}
	s, ok := value.(string)
	if !ok {
		return "", false, rpcInvalidParams(fmt.Sprintf("argument %q must be a string", key))
	}
	return s, true, nil
}

func requiredStringList(args map[string]any, key string) ([]string, *RPCError) {
	values, ok, rpcErr := optionalStringList(args, key)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if !ok {
		return nil, rpcInvalidParams(fmt.Sprintf("argument %q is required", key))
	}
	if len(values) == 0 {
		return nil, rpcInvalidParams(fmt.Sprintf("argument %q must include at least one value", key))
	}
	return values, nil
}

func optionalStringList(args map[string]any, key string) ([]string, bool, *RPCError) {
	value, ok := args[key]
	if !ok || value == nil {
		return nil, false, nil
	}
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...), true, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false, rpcInvalidParams(fmt.Sprintf("argument %q item %d must be a string", key, i))
			}
			out = append(out, s)
		}
		return out, true, nil
	default:
		return nil, false, rpcInvalidParams(fmt.Sprintf("argument %q must be an array of strings", key))
	}
}

func optionalBool(args map[string]any, key string) (bool, bool, *RPCError) {
	value, ok := args[key]
	if !ok || value == nil {
		return false, false, nil
	}
	b, ok := value.(bool)
	if !ok {
		return false, false, rpcInvalidParams(fmt.Sprintf("argument %q must be a boolean", key))
	}
	return b, true, nil
}

func optionalJSONObject(args map[string]any, key string) (string, bool, *RPCError) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", false, nil
	}
	switch value.(type) {
	case map[string]any, map[string]json.RawMessage:
	default:
		return "", false, rpcInvalidParams(fmt.Sprintf("argument %q must be a JSON object", key))
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", false, rpcInvalidParams(fmt.Sprintf("argument %q could not be encoded as JSON: %v", key, err))
	}
	return string(data), true, nil
}

func optionalInt64(args map[string]any, key string) (int64, bool, *RPCError) {
	value, ok := args[key]
	if !ok || value == nil {
		return 0, false, nil
	}
	var n int64
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int64:
		n = v
	case float64:
		if math.Trunc(v) != v {
			return 0, false, rpcInvalidParams(fmt.Sprintf("argument %q must be an integer", key))
		}
		n = int64(v)
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return 0, false, rpcInvalidParams(fmt.Sprintf("argument %q must be an integer", key))
		}
		n = parsed
	default:
		return 0, false, rpcInvalidParams(fmt.Sprintf("argument %q must be an integer", key))
	}
	if n < 0 {
		return 0, false, rpcInvalidParams(fmt.Sprintf("argument %q must be non-negative", key))
	}
	return n, true, nil
}
