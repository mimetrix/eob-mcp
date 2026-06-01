#!/bin/zsh
# Maintains the SSH forwards that expose XC services to localhost.
#
#   localhost:18443 → eob-mcp HTTP/MCP    (Service ClusterIP 10.3.1.11:8443)
#   localhost:19443 → eob-mcp gRPC        (Service ClusterIP 10.3.1.11:9443)
#   localhost:8789 → tawon-dashboard     (master-0 hostIP 172.31.44.247:8789)
#
# Why not just use the dashboard's Service ClusterIP? Because the dashboard
# runs hostNetwork=true on master-0 and listens on :8789, but the Service
# advertises :8888 with a port mismatch (known Tawon CR bug). Curl to the
# Service IP returns connection refused; curl to the hostIP:hostPort works.
# So this script targets the hostIP for dashboard only — the eob-mcp pair
# works fine via Service since eob-mcp's Service port↔targetPort map is correct.
#
# Designed to be supervised by launchd via ~/Library/LaunchAgents/local.xc-tunnels.plist.
# Runs in the foreground; if ssh exits, launchd respawns this script.
#
# When run interactively (not under launchd) it prints a banner so you know
# the "silent hang" that follows is just ssh -N forwarding ports as designed.

is_tty=0
if [ -t 1 ]; then is_tty=1; fi

if [ "$is_tty" = 1 ]; then
    cat <<BANNER
xc-tunnels: connecting xcuser@3.147.217.91 ...
  localhost:18443  →  eob-mcp HTTP/MCP    (10.3.1.11:8443)
  localhost:19443  →  eob-mcp gRPC        (10.3.1.11:9443)
  localhost:8789   →  tawon-dashboard     (172.31.44.247:8789)

ssh will now go silent — that is NOT a hang, it is the working state.
Verify from another terminal:
  curl -s http://localhost:18443/healthz                  # ok
  grpcurl -plaintext localhost:19443 list                 # eob.v1.EoBService
  curl -s -o /dev/null -w "%{http_code}\\n" http://localhost:8789/   # 200

Ctrl-C here to tear down.
BANNER
fi

exec /usr/bin/ssh -N -T \
  -i "$HOME/.ssh/id_ed25519_xc" \
  -o ServerAliveInterval=15 \
  -o ServerAliveCountMax=3 \
  -o ExitOnForwardFailure=yes \
  -o StrictHostKeyChecking=accept-new \
  -L 18443:10.3.1.11:8443 \
  -L 19443:10.3.1.11:9443 \
  -L 8789:172.31.44.247:8789 \
  xcuser@3.147.217.91
