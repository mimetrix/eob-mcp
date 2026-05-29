package mcp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// MaxRequestBytes caps the size of an inbound MCP request body. 1 MiB is
// generous for tool calls; raise only with a documented reason.
const MaxRequestBytes = 1 << 20 // 1 MiB

// ServeHTTP implements http.Handler. It accepts POST requests carrying a
// JSON-RPC 2.0 envelope, dispatches them, and returns the response (or
// 202 Accepted for notifications).
//
// SSE-streamed responses (text/event-stream) are not yet implemented.
// They will be added in a later phase for long-running tool calls; the
// current single-response mode is spec-compliant for short tools.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("mcp handler panic recovered",
				"path", r.URL.Path,
				"panic", rec,
				"stack", string(debug.Stack()),
			)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, nil, ErrCodeParse, "parse error: "+err.Error())
		return
	}

	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidReq, "jsonrpc must be 2.0")
		return
	}
	if req.Method == "" {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidReq, "method is required")
		return
	}

	resp, hasResponse := s.Dispatch(r.Context(), req)
	if !hasResponse {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("mcp response encode failed", "err", err)
	}
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: msg},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("mcp error response encode failed", "err", err)
	}
}
