# eob-mcp TODO

## Phase 1d — model-vs-reality gaps in eob_health

Surfaced by the first in-cluster deploy on srikan-tf-test-0. The
plumbing is fine (HTTP, MCP, RBAC, k8s client all work end-to-end);
the workload names and shape are wrong.

- [ ] **Operator lookup** — actual Deployment is
  `tawon-operator-controller-manager` in namespace `operators`, not
  `tawon-operator`. Switch from name-based `Get` to label-based
  selection: `app.kubernetes.io/name=tawon-operator,
  app.kubernetes.io/component=manager`. Files: `internal/tools/health.go`
  (tawonOperatorDeployment const), `internal/tools/identity.go` (same
  const reused). Update tests in `health_test.go` and `identity_test.go`.

- [ ] **eob_version source** — operator carries
  `app.kubernetes.io/version=v2.39.36-rc1` as a Deployment label.
  Prefer that over scraping the image tag; fall back to image tag if
  the label is missing. File: `internal/tools/identity.go` eobVersion().

- [ ] **Drop webhook component** — no separate `tawon-webhook`
  Deployment exists in this build; admission webhook is served by the
  operator binary itself. Remove the `webhook` field from the
  `eob_health` snapshot and the `tawonWebhookDeployment` constant.
  Update `TestEoBHealth_*` assertions in `health_test.go`.

- [ ] **Agent — switch from name to label, allow many** — agent
  DaemonSets are named per-directive
  (`tawon-directive-payload-process-name-coredns-2cc2` style) so the
  name varies and there can be N of them. List DaemonSets in the
  Tawon namespace filtered by `app.kubernetes.io/name=tawon-directive`,
  aggregate ready/desired across all of them, and report each one in
  the snapshot (e.g. `agents: { "<ds-name>": {kind, ready, desired,
  status} }`). Pods carry the same label; use it for the per-node map
  too (replace the `app=tawon-agent` selector). Files:
  `internal/tools/health.go` agentReadinessByNode() + daemonSetStatus(),
  `health_test.go`.

After these land, `eob_health` should report ok/ok/ok on the existing
cluster instead of three "absent" lines.

## Phase 1e — durable MCP connection

Validation tunnel is fragile (dies when the dev session ends).

- [ ] Either expose `eob-mcp` Service via Ingress (TLS termination
  somewhere) **or** ship a small `scripts/tunnel.sh` that establishes
  an autossh-backed `localhost:18443 -> svc:8443` tunnel on demand for
  Claude Code clients. The latter is fine for the dev loop; the
  former is needed before any fleet console talks to multiple sites.

## Phase 1f — file the Claude Code `/mcp` bug

`claude mcp add` modifies `~/.claude.json` successfully and `claude
mcp list` shows the new server connected, but the `/mcp` slash command
in an already-running Claude Code session continues to report "No MCP
servers configured" until the session is restarted. Repro and
suggested fix already drafted; file at
https://github.com/anthropics/claude-code/issues when convenient.
