# eob-mcp TODO

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

## Phase 1f² — scope narrowing (DONE 2026-06-01)

Decided to lock `eob-mcp`'s scope to "the typed, federated API to one
EoB site" — control plane (already shipped) plus three data-plane
stream RPCs (Phase 3 below). Decoding, aggregation, correlation,
templating, and prose synthesis are explicit non-goals; see
`docs/ARCHITECTURE.md` for the full statement. README, FLEET, and this
file updated to match.

## Phase 1g — per-RPC service tests for `resource_*`

Today only `ClusterIdentity` and the `imageTag` helper have direct
service-package tests. The five `resource_*` RPCs are exercised
indirectly through the MCP wrappers in `internal/tools/`, which gives
end-to-end coverage but no targeted unit coverage of the dynamic-client
paths.

- [ ] Add `internal/service/resource_*_test.go` files using
  `k8s.io/client-go/dynamic/fake` to back `*k8s.DynClient`. Cover at
  minimum: list with label selector, get NotFound mapping, apply
  dry-run shape, delete idempotency, schema CRD-not-found.

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

## Phase 3 — data plane (narrow)

Three RPCs, no more. Decoding/aggregation/correlation are out of
scope; see `docs/ARCHITECTURE.md`.

- [ ] Add `internal/streams/` with a `StreamReader` interface; concrete
  impl wraps `nats.go`. ~150 LOC.
- [ ] Add `internal/filter/` with a `Filter` interface; concrete impl
  wraps `itchyny/gojq`. ~80 LOC. **Filter syntax is jq, full stop —
  no invented expression language.**
- [ ] Add three RPCs to `proto/eob/v1/service.proto`:
  `StreamList`, `StreamStats`, `StreamRead`. Regenerate via `make proto`.
- [ ] Implement on `*service.Server` in `internal/service/stream_*.go`,
  each delegating to the backend interfaces above. Each method should
  stay under ~50 LOC; if anything goes over, that's a scope-leak signal.
- [ ] MCP wrappers in `internal/tools/stream.go` follow the existing
  proto-marshal pattern; no new logic.
- [ ] End-to-end test against an in-memory NATS server (`server.NewServer`
  from `nats-server`) — proves the wiring without needing a real cluster.

Total target: ~300 LOC across the package, plus tests.

## Phase 3-adjacent — `eob-decoder` sidecar (optional, separate repo)

If/when data-sovereignty constraints require in-cluster decoding, a
**separate small service** (not part of this repo) consumes raw Tawon
streams, dissects via `tshark`, and publishes decoded streams back to
JetStream. `eob-mcp` then reads the decoded streams like any other.
This is a deliberate separation — `eob-mcp` stays untouched.

Not on this repo's roadmap. Mentioned here so it doesn't accidentally
get pulled into our scope.
