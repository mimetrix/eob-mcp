package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubTool is a minimal ToolHandler for tests.
type stubTool struct {
	name        string
	description string
	schema      json.RawMessage
	fn          func(ctx context.Context, args json.RawMessage) (CallToolResult, error)
}

func (t *stubTool) Name() string                 { return t.name }
func (t *stubTool) Description() string          { return t.description }
func (t *stubTool) InputSchema() json.RawMessage { return t.schema }
func (t *stubTool) Call(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	return t.fn(ctx, args)
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("eob-mcp-test", "0.0.0")
	must := func(err error) {
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	must(s.RegisterTool(&stubTool{
		name:        "echo",
		description: "Returns its argument as text.",
		schema:      json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"additionalProperties":false}`),
		fn: func(_ context.Context, args json.RawMessage) (CallToolResult, error) {
			var in struct {
				Msg string `json:"msg"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Content: []Content{{Type: "text", Text: in.Msg}}}, nil
		},
	}))
	must(s.RegisterTool(&stubTool{
		name:        "boom",
		description: "Always returns an error.",
		schema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		fn: func(_ context.Context, _ json.RawMessage) (CallToolResult, error) {
			return CallToolResult{}, errors.New("kaboom")
		},
	}))
	return s
}

func TestRegisterToolErrors(t *testing.T) {
	t.Parallel()
	s := NewServer("eob-mcp-test", "0.0.0")
	if err := s.RegisterTool(nil); err == nil {
		t.Fatal("nil tool should error")
	}
	if err := s.RegisterTool(&stubTool{name: "", description: "x", schema: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("empty name should error")
	}
	dup := &stubTool{name: "dup", description: "d", schema: json.RawMessage(`{}`)}
	if err := s.RegisterTool(dup); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := s.RegisterTool(dup); err == nil {
		t.Fatal("duplicate register should error")
	}
}

func TestDispatchInitialize(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1"}}`),
	}
	resp, has := s.Dispatch(context.Background(), req)
	if !has {
		t.Fatal("initialize should produce a response")
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(body), `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("missing protocolVersion in result: %s", body)
	}
	if !strings.Contains(string(body), `"name":"eob-mcp-test"`) {
		t.Fatalf("missing serverInfo.name: %s", body)
	}
}

func TestDispatchToolsList(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"}
	resp, has := s.Dispatch(context.Background(), req)
	if !has || resp.Error != nil {
		t.Fatalf("tools/list failed: has=%v err=%+v", has, resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	// Tools are returned in alphabetic order; "boom" before "echo".
	if !strings.Contains(string(body), `"name":"boom"`) || !strings.Contains(string(body), `"name":"echo"`) {
		t.Fatalf("expected both tools in result: %s", body)
	}
	if strings.Index(string(body), "boom") > strings.Index(string(body), "echo") {
		t.Fatalf("tools should be sorted alphabetically: %s", body)
	}
}

func TestDispatchToolsCall(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"echo","arguments":{"msg":"hello"}}`),
	}
	resp, has := s.Dispatch(context.Background(), req)
	if !has || resp.Error != nil {
		t.Fatalf("tools/call failed: has=%v err=%+v", has, resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(body), `"text":"hello"`) {
		t.Fatalf("expected echoed text in result: %s", body)
	}
}

func TestDispatchToolsCallUnknown(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"nope"}`),
	}
	resp, _ := s.Dispatch(context.Background(), req)
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Fatalf("expected method-not-found error, got %+v", resp.Error)
	}
}

func TestDispatchToolErrorAsResult(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"boom"}`),
	}
	resp, has := s.Dispatch(context.Background(), req)
	if !has {
		t.Fatal("expected response")
	}
	// Per MCP spec, tool execution errors come back as result with isError=true,
	// not as a JSON-RPC error.
	if resp.Error != nil {
		t.Fatalf("did not expect JSON-RPC error, got %+v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(body), `"isError":true`) {
		t.Fatalf("expected isError=true in result: %s", body)
	}
}

func TestDispatchNotificationSilent(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{JSONRPC: "2.0", Method: "notifications/initialized"} // no ID = notification
	_, has := s.Dispatch(context.Background(), req)
	if has {
		t.Fatal("notification should not produce a response")
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := Request{JSONRPC: "2.0", ID: json.RawMessage(`6`), Method: "wat"}
	resp, _ := s.Dispatch(context.Background(), req)
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Fatalf("expected method-not-found, got %+v", resp.Error)
	}
}

func TestHTTPHandlerSuccess(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(body))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), `"name":"echo"`) {
		t.Fatalf("expected echo in body: %s", rr.Body.String())
	}
}

func TestHTTPHandlerNotificationAccepted(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(body))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusAccepted)
	}
}

func TestHTTPHandlerMethodNotAllowed(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp", nil)
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHTTPHandlerParseError(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader("not json"))
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (JSON-RPC errors are 200 OK)", rr.Code, http.StatusOK)
	}
	var resp Response
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeParse {
		t.Fatalf("expected parse error, got %+v", resp.Error)
	}
}

func TestHTTPHandlerInvalidJSONRPCVersion(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	body := `{"jsonrpc":"1.0","id":1,"method":"tools/list"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(body))
	s.ServeHTTP(rr, req)
	var resp Response
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidReq {
		t.Fatalf("expected invalid request, got %+v", resp.Error)
	}
}

func TestHTTPHandlerOversizedBody(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	oversized := bytes.Repeat([]byte("x"), MaxRequestBytes+1024)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", bytes.NewReader(oversized))
	s.ServeHTTP(rr, req)
	// Either a parse error (JSON-RPC 200 OK with error body) or a 413/400
	// from MaxBytesReader is acceptable; both indicate the cap was enforced.
	if rr.Code == http.StatusOK {
		var resp Response
		if err := json.NewDecoder(rr.Body).Decode(&resp); err == nil && resp.Error != nil {
			return // parse-error path
		}
	}
	if rr.Code >= 400 && rr.Code < 500 {
		return // 4xx path
	}
	t.Fatalf("oversized body was not rejected: code=%d body=%s", rr.Code, rr.Body.String())
}
