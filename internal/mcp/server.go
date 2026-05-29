package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ToolHandler is the interface every registered tool implements.
//
// Name is the unique tool identifier (lowercase_snake_case by convention).
// Description is the human-readable summary shown in tools/list output.
// InputSchema is a JSON Schema describing the arguments accepted by Call.
// Call executes the tool with the given JSON arguments.
type ToolHandler interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Call(ctx context.Context, args json.RawMessage) (CallToolResult, error)
}

// Server is an MCP server with a tool registry. It is safe for
// concurrent use by multiple HTTP handlers.
type Server struct {
	name    string
	version string

	mu    sync.RWMutex
	tools map[string]ToolHandler
}

// NewServer constructs an MCP server. Name and version are reported in
// the initialize response's serverInfo block.
func NewServer(name, version string) *Server {
	return &Server{
		name:    name,
		version: version,
		tools:   make(map[string]ToolHandler),
	}
}

// RegisterTool registers a tool. Returns an error if the tool is nil, has
// an empty name, or a tool with the same name is already registered.
func (s *Server) RegisterTool(t ToolHandler) error {
	if t == nil {
		return errors.New("nil tool")
	}
	n := t.Name()
	if n == "" {
		return errors.New("tool name cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[n]; exists {
		return fmt.Errorf("tool %q already registered", n)
	}
	s.tools[n] = t
	return nil
}

// Dispatch routes a request to the appropriate method handler. Returns
// the response and a flag indicating whether the caller should send it
// (false for notifications, which have no ID).
func (s *Server) Dispatch(ctx context.Context, req Request) (Response, bool) {
	resp := Response{JSONRPC: "2.0", ID: req.ID}
	isNotification := isNotification(req)

	switch req.Method {
	case "initialize":
		var p InitializeParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
				return resp, !isNotification
			}
		}
		resp.Result = InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: ServerCapabilities{
				Tools:     &ToolsCapability{},
				Resources: &ResourcesCapability{},
			},
			ServerInfo: ServerInfo{Name: s.name, Version: s.version},
		}

	case "notifications/initialized":
		// Notification only; clients send after they receive initialize's response.
		return resp, false

	case "tools/list":
		s.mu.RLock()
		tools := make([]Tool, 0, len(s.tools))
		for _, t := range s.tools {
			tools = append(tools, Tool{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
		s.mu.RUnlock()
		// Stable ordering for reproducibility.
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		resp.Result = ToolsListResult{Tools: tools}

	case "tools/call":
		var p CallToolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
			return resp, !isNotification
		}
		s.mu.RLock()
		t, ok := s.tools[p.Name]
		s.mu.RUnlock()
		if !ok {
			resp.Error = &Error{
				Code:    ErrCodeMethodNotFound,
				Message: fmt.Sprintf("tool not found: %s", p.Name),
			}
			return resp, !isNotification
		}
		result, err := t.Call(ctx, p.Arguments)
		if err != nil {
			// Per MCP spec, tool execution errors are returned as a result with
			// isError=true rather than a JSON-RPC error. JSON-RPC errors are
			// reserved for protocol-level failures.
			resp.Result = CallToolResult{
				Content: []Content{{Type: "text", Text: err.Error()}},
				IsError: true,
			}
			return resp, !isNotification
		}
		resp.Result = result

	case "ping":
		resp.Result = struct{}{}

	default:
		resp.Error = &Error{
			Code:    ErrCodeMethodNotFound,
			Message: "method not found: " + req.Method,
		}
	}

	return resp, !isNotification
}

// isNotification reports whether a request lacks an id, indicating a
// notification under JSON-RPC 2.0.
func isNotification(req Request) bool {
	if len(req.ID) == 0 {
		return true
	}
	// Treat the literal `null` ID as a notification too.
	trimmed := req.ID
	// Strip ASCII whitespace.
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\n' || trimmed[0] == '\r') {
		trimmed = trimmed[1:]
	}
	return string(trimmed) == "null"
}
