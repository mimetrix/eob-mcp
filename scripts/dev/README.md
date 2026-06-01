# `scripts/dev/` — local-developer-only helpers

These are Mac-side scripts for reaching the in-cluster eob-mcp Service
and the tawon-dashboard from `localhost`. They are not deployed,
imported, or referenced by any production code. Safe to ignore unless
you're developing against a remote XC site.

## `xc-tunnels.sh` + `local.xc-tunnels.plist`

A launchd-supervised SSH tunnel that keeps three forwards alive on
your Mac at all times:

```
localhost:18443  →  eob-mcp HTTP/MCP    (Service ClusterIP 10.3.1.11:8443)
localhost:19443  →  eob-mcp gRPC        (Service ClusterIP 10.3.1.11:9443)
localhost:8789  →  tawon-dashboard     (master-0 hostIP  172.31.44.247:8789)
```

### Why the dashboard target is `hostIP`, not the Service ClusterIP

The dashboard Pod runs `hostNetwork: true` and binds on port 8789 on
the node. Its Service advertises 8888 with a target-port mismatch
(known Tawon CR bug — the CRD has no field to fix it cleanly). Curl
to the Service ClusterIP fails; curl to the host:hostPort works. So
the tunnel skips the Service and points at master-0 directly.

### Install (one-time)

```bash
# 1. Drop the script into your PATH and make it executable.
mkdir -p ~/bin
cp scripts/dev/xc-tunnels.sh ~/bin/
chmod +x ~/bin/xc-tunnels.sh

# 2. Drop the plist into LaunchAgents and edit the path inside it
#    to match your Mac username (launchd does not expand $HOME).
cp scripts/dev/local.xc-tunnels.plist ~/Library/LaunchAgents/
vim ~/Library/LaunchAgents/local.xc-tunnels.plist
#    ↳ change /Users/e.starin/bin/xc-tunnels.sh to /Users/<you>/bin/xc-tunnels.sh

# 3. Load it. Idempotent — safe to re-run.
launchctl unload ~/Library/LaunchAgents/local.xc-tunnels.plist 2>/dev/null
launchctl load   ~/Library/LaunchAgents/local.xc-tunnels.plist
```

### Verify

```bash
launchctl list | grep xc-tunnels
# expect a line like:  41234  0  local.xc-tunnels
# (PID present, exit code 0)

curl -s http://localhost:18443/healthz
# expect: ok

grpcurl -plaintext localhost:19443 list
# expect: eob.v1.EoBService + reflection services

curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8789/
# expect: 200
```

### Forensics if a tunnel drops

```bash
tail -f /tmp/xc-tunnels.err
launchctl list | grep xc-tunnels   # last exit code in column 2
```

`ssh` keepalives (`ServerAliveInterval=15`, `ServerAliveCountMax=3`)
mean a dead connection is killed within ~45s; launchd respawns within
5s (`ThrottleInterval`).

### Adding a fourth forward later

Edit `xc-tunnels.sh`, add another `-L localport:targetIP:targetPort`
line, then:

```bash
launchctl unload ~/Library/LaunchAgents/local.xc-tunnels.plist
launchctl load   ~/Library/LaunchAgents/local.xc-tunnels.plist
```

### Why these aren't in the main `deploy/` tree

This is dev-loop convenience for a particular developer's Mac. The
production answer is "expose the Service via an Ingress with TLS" —
see `TODO.md` Phase 1e. Until then, this tunnel is the bridge.
