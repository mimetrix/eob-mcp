# eob-mcp TODO

## Phase 1e — durable MCP connection (DONE 2026-06-02)

Two flavors now ship under `scripts/dev/`:

- `xc-tunnels.sh` + `local.xc-tunnels.plist` — Mac, launchd-supervised
  (shipped earlier).
- `tunnel.sh` — portable (Linux/BSD/Mac), no launchd dependency, with
  `up`/`down`/`status`/`logs` subcommands. Uses `autossh` when present,
  falls back to plain `ssh`. Smoke-tested end-to-end against
  srikan-tf-test-0.

Either is suitable for dev consumers. An Ingress-shaped path is the
real answer for fleet consoles consuming many sites — not on the
single-site repo's roadmap; the choice (cert-manager + Ingress, F5 XC
HTTPLoadBalancer, or SPIFFE-fronted) is downstream.

## Phase 1g — service-package RPC tests (DONE 2026-06-02)

Originally framed as "tests for `resource_*`," which had already
landed (six test files, 20+ tests). The remaining service-test gap
was `ClusterIdentity` and `EoBHealth`, both at 0% coverage. Now:

- `cluster_identity_test.go` — RPC happy path, no-kube degraded path,
  operator-deployment-missing path, eob_version fallback chain
  (version label → manager image → first-container image).
- `eob_health_test.go` — happy/degraded/absent component statuses,
  directives aggregation + sort, agents-per-node grouping including
  the `<pending>` sentinel.

`internal/service` coverage: 50% → 80.4%.

## Phase 3 — data plane (DONE 2026-05-30; commit 1dd8042)

Shipped before the auto-discovery work. The three RPCs (`StreamList`,
`StreamStats`, `StreamRead`), the `streams.Reader` abstraction with a
nats.go/jetstream implementation, and the `filter.Filter` gojq wrapper
are all in `internal/streams/`, `internal/filter/`, and
`internal/service/stream_*.go`. End-to-end test against embedded NATS
in `stream_e2e_test.go`. The checklist in this file was never
ticked off; see commit history for the actual shape.

## Phase 1j — auto-discover NATS + site identity (DONE 2026-06-02)

Two install-time fragilities retired:

- `EOB_NATS_URL`-unset now triggers in-cluster Service discovery by
  label (`app=tawon-streamstore`) in `EOB_TAWON_NAMESPACE`, with a
  name-pattern fallback. The deploy manifest no longer bakes in the
  chart's per-install hex suffix.
- `EOB_SITE_ID` / `EOB_TENANT` / `EOB_REGION` back-fill from
  `/etc/resolv.conf` on F5 XC sites using the canonical
  `<site>.<tenant>.tenant.local` + `<region>.compute.internal`
  search-domain pattern. Explicit env still wins.

Both: env values continue to take precedence when set, so existing
deploys keep working unchanged. The bare `EDIT ME` env entries in
`deploy/k8s/eob-mcp.yaml` are removed.

## Phase 1d — proto-first dual mode (DONE)

Shipped 2026-05-30. The seven existing tools were migrated end-to-end
to a single `*service.Server` defined by `proto/eob/v1/service.proto`.
Both MCP and gRPC front doors are wired to the same in-process service.
TLS hooks (off by default, mTLS-capable) and the gRPC Service port
(`:9443` in cluster → `:19443` on the host) are in place. See
`README.md` for the configuration matrix and `FLEET.md` for how
federation consumes the gRPC surface.

## Phase 1f — file the Claude Code `/mcp` bug

`claude mcp add` modifies `~/.claude.json` successfully and `claude
mcp list` shows the new server connected, but the `/mcp` slash command
in an already-running Claude Code session continues to report "No MCP
servers configured" until the session is restarted. Repro and
suggested fix already drafted; file at
https://github.com/anthropics/claude-code/issues when convenient.

## Phase 1f² — scope narrowing (DONE 2026-06-01)

Decided to lock `eob-mcp`'s scope to "the typed, federated API to one
EoB site" — control plane (already shipped) plus three data-plane
stream RPCs (Phase 3 below). Decoding, aggregation, correlation,
templating, and prose synthesis are explicit non-goals; see
`docs/ARCHITECTURE.md` for the full statement. README, FLEET, and this
file updated to match.

## Phase 1h — cert provisioning for production TLS

TLS code path is done; the operational piece left is choosing how
certs land in the Pod.

- [ ] Decide between cert-manager (recommended; the manifest's
  commented Secret layout matches cert-manager's `Certificate` output
  natively), a static externally-managed Secret, or SPIFFE workload
  identity if the F5 XC site already runs a SPIRE agent.
- [ ] Add a cert hot-reload story (currently restart-to-rotate) once
  the source is chosen.

## Phase 1i — dev image / observability polish

- [ ] Add `grpcurl` to `Dockerfile.claude-code` (single-binary layer
  alongside `helm`, `gh`, `yq`, `buf`) so the dev-loop image can
  exercise the gRPC surface without host-side tooling.
- [ ] Add `tshark` to the dev image too — useful for ad-hoc pcap
  workflows that aren't routed through `eob-mcp`.
- [ ] Replace `readyz` stub with a real readiness check (kube
  reachability, optional NATS reachability) once Phase 3 lands and
  NATS becomes a hard dependency.

## Phase 3-adjacent — `eob-decoder` sidecar (optional, separate repo)

If/when data-sovereignty constraints require in-cluster decoding, a
**separate small service** (not part of this repo) consumes raw Tawon
streams, dissects via `tshark`, and publishes decoded streams back to
JetStream. `eob-mcp` then reads the decoded streams like any other.
This is a deliberate separation — `eob-mcp` stays untouched.

Not on this repo's roadmap. Mentioned here so it doesn't accidentally
get pulled into our scope.
