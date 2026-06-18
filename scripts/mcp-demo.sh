#!/usr/bin/env bash
# mcp-demo.sh — drive eob-mcp as an MCP client and SHOW the full JSON-RPC
# interaction: every client→server request and server→client response.
#
#   MCP_URL=http://localhost:18443/mcp ./mcp-demo.sh   # via eob-tun (tunnel)
#   MCP_URL=http://10.3.1.11:8443/mcp  ./mcp-demo.sh   # in-cluster (on a node)
#
# Default URL is the tunnel endpoint. Override per the comments above.
set -uo pipefail
URL="${MCP_URL:-http://localhost:18443/mcp}"
pp() { python3 -m json.tool 2>/dev/null || cat; }

rpc() {
  local desc="$1" body="$2"
  echo "════════════════════════════════════════ $desc"
  echo "  ▶ client → server:"
  echo "$body" | pp | sed 's/^/    /'
  echo "  ◀ server → client:"
  curl -s -X POST "$URL" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    --max-time 30 -d "$body" | pp | sed 's/^/    /'
  echo
}

echo "MCP endpoint: $URL"
echo
rpc "1. initialize (handshake)" \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"mcp-demo","version":"0.1"},"capabilities":{}}}'
rpc "2. tools/list (what does the server expose?)" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
rpc "3. tools/call cluster_identity" \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cluster_identity","arguments":{}}}'
rpc "4. tools/call trace_health" \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"trace_health","arguments":{}}}'
rpc "5. tools/call east_west_graph (tap+aggregate+resolve)" \
  '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"east_west_graph","arguments":{"window_seconds":8,"max_edges":8}}}'
