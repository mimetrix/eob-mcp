#!/usr/bin/env bash
# Portable SSH-tunnel manager for eob-mcp dev access.
#
# Sibling to xc-tunnels.sh + local.xc-tunnels.plist (Mac/launchd path);
# this script runs on Linux, BSD, or Mac without launchd. Uses autossh
# when present for resilient reconnects; falls back to plain ssh.
#
# Forwards (same as the launchd setup):
#   localhost:18443 -> eob-mcp HTTP/MCP   (Service ClusterIP 10.3.1.11:8443)
#   localhost:19443 -> eob-mcp gRPC       (Service ClusterIP 10.3.1.11:9443)
#   localhost:8789  -> tawon-dashboard    (master-0 hostIP 172.31.44.247:8789)
#
# Usage:
#   tunnel.sh up          run in foreground (Ctrl-C to stop)
#   tunnel.sh up -d       run as a background daemon
#   tunnel.sh status      report whether the daemon is running
#   tunnel.sh down        stop the daemon
#   tunnel.sh logs        tail the daemon log (daemon mode only)
#
# Env overrides:
#   REMOTE_USER       SSH user      (default: xcuser)
#   REMOTE_HOST       SSH host      (default: 3.147.217.91)
#   SSH_KEY           identity file (default: $HOME/.ssh/id_ed25519_xc)
#   MCP_SVC           HTTP target   (default: 10.3.1.11:8443)
#   GRPC_SVC          gRPC target   (default: 10.3.1.11:9443)
#   DASHBOARD_HOST    dash target   (default: 172.31.44.247:8789)
#   STATE_DIR         pid+log dir   (default: $XDG_RUNTIME_DIR or /tmp)

set -euo pipefail

REMOTE_USER="${REMOTE_USER:-xcuser}"
REMOTE_HOST="${REMOTE_HOST:-3.147.217.91}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_ed25519_xc}"
MCP_SVC="${MCP_SVC:-10.3.1.11:8443}"
GRPC_SVC="${GRPC_SVC:-10.3.1.11:9443}"
DASHBOARD_HOST="${DASHBOARD_HOST:-172.31.44.247:8789}"

STATE_DIR="${STATE_DIR:-${XDG_RUNTIME_DIR:-/tmp}}"
PIDFILE="$STATE_DIR/eob-mcp-tunnel.pid"
LOGFILE="$STATE_DIR/eob-mcp-tunnel.log"

usage() {
    sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

log()  { echo "[tunnel] $*" >&2; }
fail() { echo "ERROR: $*" >&2; exit 1; }

# Build the ssh command line shared across autossh/ssh, foreground/daemon.
ssh_args=(
    -N -T
    -i "$SSH_KEY"
    -o ServerAliveInterval=15
    -o ServerAliveCountMax=3
    -o ExitOnForwardFailure=yes
    -o StrictHostKeyChecking=accept-new
    -L "18443:$MCP_SVC"
    -L "19443:$GRPC_SVC"
    -L "8789:$DASHBOARD_HOST"
    "$REMOTE_USER@$REMOTE_HOST"
)

# Pick the supervisor: autossh if present (auto-restart on failure),
# otherwise plain ssh. autossh -M 0 delegates connection liveness to
# ssh's ServerAlive* options (above) instead of autossh's own port.
choose_supervisor() {
    if command -v autossh >/dev/null 2>&1; then
        supervisor="autossh"
        supervisor_args=("-M" "0")
    else
        supervisor="ssh"
        supervisor_args=()
    fi
}

print_banner() {
    cat <<BANNER
tunnel: connecting $REMOTE_USER@$REMOTE_HOST ...
  localhost:18443  ->  eob-mcp HTTP/MCP    ($MCP_SVC)
  localhost:19443  ->  eob-mcp gRPC        ($GRPC_SVC)
  localhost:8789   ->  tawon-dashboard     ($DASHBOARD_HOST)

Supervisor: $supervisor
ssh will now go silent; that is the working state, not a hang.
Verify from another terminal:
  curl -s http://localhost:18443/healthz                          # ok
  grpcurl -plaintext localhost:19443 list                         # eob.v1.EoBService
  curl -s -o /dev/null -w "%{http_code}\\n" http://localhost:8789/  # 200

Ctrl-C to stop (foreground mode); 'tunnel.sh down' (daemon mode).
BANNER
}

cmd_up() {
    local daemon=0
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -d|--daemon) daemon=1; shift ;;
            -h|--help)   usage 0 ;;
            *)           fail "unknown flag: $1" ;;
        esac
    done

    [ -f "$SSH_KEY" ] || fail "SSH key not found: $SSH_KEY"

    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        fail "daemon already running (pid $(cat "$PIDFILE")); run 'tunnel.sh down' first"
    fi

    choose_supervisor
    mkdir -p "$STATE_DIR"

    if [ "$daemon" -eq 1 ]; then
        log "starting in background, log -> $LOGFILE"
        nohup "$supervisor" "${supervisor_args[@]}" "${ssh_args[@]}" \
            >"$LOGFILE" 2>&1 &
        echo "$!" >"$PIDFILE"
        sleep 2
        if ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
            rm -f "$PIDFILE"
            fail "daemon died on startup; check $LOGFILE"
        fi
        log "daemon up (pid $(cat "$PIDFILE")); 'tunnel.sh status' to verify"
    else
        print_banner
        exec "$supervisor" "${supervisor_args[@]}" "${ssh_args[@]}"
    fi
}

cmd_down() {
    if [ ! -f "$PIDFILE" ]; then
        log "no pidfile; nothing to stop"
        return 0
    fi
    pid=$(cat "$PIDFILE")
    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid"
        # Give the supervisor up to 5s to exit cleanly before SIGKILL.
        for _ in 1 2 3 4 5; do
            kill -0 "$pid" 2>/dev/null || break
            sleep 1
        done
        if kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
        fi
        log "stopped (pid $pid)"
    else
        log "stale pidfile (pid $pid not running); removing"
    fi
    rm -f "$PIDFILE"
}

cmd_status() {
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "running (pid $(cat "$PIDFILE"))"
        ss -tln 2>/dev/null | awk 'NR==1 || /:18443|:19443|:8789/' || true
    else
        [ -f "$PIDFILE" ] && rm -f "$PIDFILE"
        echo "stopped"
        return 1
    fi
}

cmd_logs() {
    [ -f "$LOGFILE" ] || fail "no log file at $LOGFILE (daemon not started?)"
    exec tail -F "$LOGFILE"
}

case "${1:-}" in
    up)     shift; cmd_up "$@" ;;
    down)   cmd_down ;;
    status) cmd_status ;;
    logs)   cmd_logs ;;
    -h|--help|"") usage 0 ;;
    *)      fail "unknown subcommand: $1 (try -h)" ;;
esac
