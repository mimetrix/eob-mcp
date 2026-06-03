# eob-mcp

**The typed, federated API to one EoB site.** Exposes Tawon's
primitives — CRD lifecycle, stream reads, health, identity — over MCP
and gRPC from a single in-process service. That's the entire scope.

**Status:** the per-cluster control plane (identity, health, generic
CRD CRUD) is shipping. The data plane (three stream RPCs over NATS
JetStream) is next. Decode and aggregation are explicit non-goals —
see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

**Why this exists:** two named customer asks — VZW's internal AI
effort (MCP front door) and ATT's multi-site federation (gRPC front
door) — both served by one canonical service. See
[`docs/MOTIVATION.md`](docs/MOTIVATION.md) for the customer-anchored
rationale.

---

## What it is

The Mantis EoB stack on a Kubernetes cluster (today: F5 XC Customer Edge
sites — see [`eob-xc-install`](https://github.com/mimetrix/XC-eBPF) for
that installer) gives you a CRD-driven capture/observation platform. The
existing UI is a per-cluster dashboard; the existing CLI is `kubectl` +
the JetStream API.

`eob-mcp` adds a third surface: a typed, federation-aware API to the
EoB primitives, consumable by any MCP or gRPC client (Claude Code, an
aggregator service, a custom dashboard backend, a shell pipeline). It
is **not** a replacement for the dashboard or `kubectl`, and it is
**not** a packet-analysis tool, an aggregation engine, or a workflow
orchestrator. It is the typed door to the platform; consumers compose
it with the other tools they already use.

## What it does and does not do

| Layer | Owns | Does NOT own |
|---|---|---|
| **EoB API surface** | ClusterDirective CRUD; stream reads; health; identity; federation envelope | — |
| **Packet dissection** | — | `tshark` / `sharkd` / `wireshark-common` |
| **Stream aggregation** | — | DuckDB / SQL / Pandas / chosen by consumer |
| **Filter / query language** | server-side filter on `StreamRead` uses **jq** (`itchyny/gojq`) — adopted, not invented | — |
| **Directive templating** | exposes the OpenAPI schema (`ResourceSchema`); the consumer renders YAML | template registry / convenience renderers |
| **Workflow orchestration** | — | the LLM / aggregator / human |
| **LLM-friendly prose** | — | the LLM, every time |

The principle: **eob-mcp does one thing — be the typed interface to
one EoB site. Everything else composes with it.**

## A drag-and-drop pcap, for example

If a user drops a pcap into a chat and asks for analysis, **`eob-mcp`
has no role** if the pcap is just a file unrelated to a Tawon
directive. `tshark` alone is sufficient.

`eob-mcp` earns its keep when the data came from EoB. Then:

1. `StreamList` / `StreamStats` / `StreamRead` give the consumer the
   raw envelopes Tawon wrote.
2. The consumer pipes those records to `tshark`, DuckDB, jq, or
   whatever — locally or via another in-cluster service.
3. The federation envelope tells the consumer which site each record
   came from, so an aggregator can fan out across the fleet without
   external metadata.

If decoded streams need to live in-cluster (data-sovereignty), the
right shape is a separate small sidecar service that reads the raw
stream, decodes via `tshark`, and writes a new JetStream stream. That
new stream is then visible to `eob-mcp` like any other — no change to
this server. See `docs/ARCHITECTURE.md` for the pipeline pattern.

## Architecture

```
        Claude Code / LLM agents          aggregator / federated services
                  │                                       │
                  ▼                                       ▼
            MCP (HTTP /mcp)                          gRPC (TCP)
                  │                                       │
                  └───────────────────┬───────────────────┘
                                      ▼
                  ┌───────────────────────────────────────┐
                  │            eob-mcp process            │
                  │                                       │
                  │   internal/tools/  (MCP wrappers)     │
                  │             │                         │
                  │             ▼                         │
                  │   internal/service.Server             │
                  │     (in-process gRPC impl —           │
                  │      single source of truth)          │
                  │             │                         │
                  └─────────────┼─────────────────────────┘
                                ▼
        ┌───────────────────────┴────────────────────────┐
        │                       │                        │
        ▼                       ▼                        ▼
   kube-apiserver         NATS JetStream           pod logs / metrics
   (Tawon CRDs +          (capture + payload        (future analysis
    ClusterDirective       streams — future          layer)
    operations)            analysis layer)
```

**Dual front door, one body.** A single `service.Server` (defined by
`proto/eob/v1/service.proto`) implements every RPC. The MCP listener
(`/mcp` over HTTP) and the gRPC listener serve it side-by-side. Adding
an RPC means adding one entry to the proto + one method on the service;
both front doors get it. Any future front door (OpenAI-style function
calling, agent-framework X, …) lives as a sibling package alongside
`internal/tools/`; the service is invariant.

Inside the server, just two halves:

- **Control plane (shipped):** identity, health, and generic CRD CRUD.
  The consumer reads `ResourceSchema` to learn the live CRD shape, then
  drives the directive lifecycle through `ResourceApply` / `ResourceList` /
  `ResourceGet` / `ResourceDelete`. Validation is the apiserver's job
  via dry-run apply; we relay.

- **Data plane (Phase 3, narrow):** three stream RPCs — `StreamList`,
  `StreamStats`, `StreamRead` — that return raw Tawon JSON envelopes
  with server-side jq filtering. Decoding, aggregation, and correlation
  are explicit non-goals; consumers compose those externally.

## What's shipped today

Seven RPCs, exposed identically over MCP and gRPC. Every response carries
a `cluster` envelope (`site_id`, `tenant`, `region`) so a federating
aggregator can disambiguate results across sites.

| RPC / tool | Purpose |
|---|---|
| `ClusterIdentity` / `cluster_identity` | Reports site_id, tenant, region, k8s_version, eob_version, mcp_version |
| `EoBHealth` / `eob_health` | Snapshot of operator, dashboard, streamstore, webhook, per-directive DaemonSets, per-node agent counts |
| `ResourceList` / `resource_list` | List Kubernetes resources by Kind (default group: `tawon.mantisnet.com`) |
| `ResourceGet` / `resource_get` | Fetch one resource as a `google.protobuf.Struct` |
| `ResourceApply` / `resource_apply` | Server-Side Apply for YAML/JSON manifests; `dry_run` and `force` supported |
| `ResourceDelete` / `resource_delete` | Idempotent delete; reports `"deleted"` or `"notFound"` |
| `ResourceSchema` / `resource_schema` | Returns the OpenAPI v3 schema for a given CRD |

Listeners are plaintext by default; TLS (and optional mTLS for service-to-service)
is wired in but off until `-tls-cert`/`-tls-key` are set. See **Configuration**
below.

## Tawon stream formats (reference — passed through unchanged)

Tawon writes structured JSON envelopes to JetStream, one stream per
directive. `eob-mcp` returns these envelopes **as-is** through
`StreamRead`; decoding the `payload` base64 is the consumer's job.

**Capture stream** (`type: "rawpacket"`):

```json
{
  "timestamp": "<ISO8601>",
  "src": { "name": "capture", "hostname": "master-N", ... },
  "data": [{
    "data": {
      "payload": "<base64 — full Ethernet frame, up to MTU>",
      "meta": { "interface": { "name": "vhost0", "addrs": [...] } }
    },
    "type": "rawpacket"
  }]
}
```

The inner `payload` is a standard L2 Ethernet frame; consumers feed it
to `tshark`, `gopacket`, or any pcap-aware tool.

**Payload stream** (`type: "payload"`):

```json
{
  "timestamp": "<ISO8601>",
  "src": { "name": "payload", "hostname": "master-N", ... },
  "data": [{
    "data": {
      "flowID": "...",
      "payload": "<base64 — L7 bytes only>",
      "length": <int>,
      "ts": <epoch_ns>,
      "direction": "RX" | "TX",
      "process": {
        "container": { "id", "name" },
        "pod":       { "id", "name", "namespace" },
        "pid", "ppid", "name", "cmd", "exe", "ns", "caps", "startedAt"
      },
      "net": { "selfAddr", "peerAddr", "selfPort", "peerPort",
               "proto": "TCP"|"UDP", "af": "INET" }
    },
    "type": "payload"
  }]
}
```

Payload streams carry full process attribution (pid, container, pod,
namespace), conversation correlation via `flowID`, direction (RX/TX),
nanosecond timestamps, and the raw L7 payload. Consumers running jq
filters on `StreamRead` target these fields directly with standard jq
syntax (e.g. `.data[].data.net.peerPort == 53`).

## Configuration

Flags (with defaults):

```
-listen          :8443    HTTP/MCP listener
-grpc-listen     :9443    gRPC listener (empty disables)
-tls-cert        ""       PEM cert path; empty leaves both listeners plaintext
-tls-key         ""       PEM key path (must be paired with -tls-cert)
-tls-client-ca   ""       PEM CA bundle; presence enables mTLS (verify client certs)
-log-level       info     debug | info | warn | error
```

Identity env vars (surface in `cluster_identity` output):

```
EOB_SITE_ID, EOB_TENANT, EOB_REGION
  auto-detected from /etc/resolv.conf on F5 XC CE sites
  (`<site>.<tenant>.tenant.local` + `<region>.compute.internal`).
  Set explicitly to override discovery or run off-XC.

EOB_OPERATOR_NAMESPACE       (default: operators)
EOB_TAWON_NAMESPACE          (default: tawon-operator)
EOB_OPERATOR_DEPLOYMENT_NAME (default: tawon-operator-controller-manager)
EOB_WEBHOOK_CONFIG_NAME      (default: eob-mutate)
EOB_DIRECTIVE_LABEL_SELECTOR (default: app.kubernetes.io/name=tawon-directive)
EOB_CRD_API_GROUP            (default: tawon.mantisnet.com)
EOB_FIELD_MANAGER            (default: eob-mcp)

EOB_NATS_URL
  when unset, the server discovers the chart-rendered Tawon
  streamstore Service by label (`app=tawon-streamstore`) in
  EOB_TAWON_NAMESPACE and uses `nats://<svc>.<ns>.svc:4222`.
  Set explicitly only to point at an externally-managed NATS.
```

TLS flag validation: both `-tls-cert` and `-tls-key` must be set together;
`-tls-client-ca` alone is rejected. When TLS is on, the same cert serves
both listeners.

## Tool surface (future)

Phase 3 adds three RPCs for the data plane. That is the entire
forthcoming surface — decoding, aggregation, correlation, and
templating are explicit non-goals. See `docs/ARCHITECTURE.md` for
the rationale.

### Data plane (Phase 3)

- `StreamList` — catalog of NATS JetStream streams for this site
  (name, message count, byte count, first/last timestamp).
- `StreamStats(stream, since, until)` — counts, bytes, rates for one
  stream over a time window. Cheap; no message bodies returned.
- `StreamRead(stream, since, until, limit, filter)` — return raw Tawon
  JSON envelopes within the window, optionally filtered server-side
  via a **jq expression** (`itchyny/gojq`). Bodies are returned
  unchanged; consumers decode the `payload` base64 themselves with
  `tshark` or equivalent.

### Resources

Read-only browsable via MCP `resources/list`:

- `eob://directives` — live list of `ClusterDirective` resources
- `eob://directives/{name}` — one directive's spec + status
- `eob://streams` — JetStream stream catalog
- `eob://docs/runbook` — the EoB install runbook (so Claude can answer
  doc-grounded questions in-context)

## Deployment

Container image, deployed into the **same Kubernetes cluster and the same
`tawon-operator` namespace** as the EoB workload pods (dashboard,
streamstore, agent DS). The Tawon operator itself runs in the adjacent
`operators` namespace; `eob-mcp`'s RBAC reaches into both. Designed as a
small Deployment — not a DaemonSet, not hostNetwork.

Why the same namespace: in-cluster NATS access to the streamstore
Service is a `*.tawon-operator.svc:4222` lookup that resolves trivially
when the client lives next door, simplifying network policy, and
inheriting the same site-infrastructure trust boundary.

| Property | Value |
|---|---|
| Image base | `gcr.io/distroless/static-debian12:nonroot` (target) |
| Image size (target) | ~15–20 MB |
| Replicas | 1 today; HA across masters once we lift hostNetwork |
| Network | ClusterIP service in `tawon-operator` namespace |
| MCP transport | HTTP JSON-RPC at `/mcp` (port 8443) |
| gRPC transport | HTTP/2 + protobuf (port 9443); reflection enabled |
| TLS | flag-gated, off by default. Same cert serves both listeners; `-tls-client-ca` enables mTLS. Cert-manager-friendly Secret layout (tls.crt, tls.key, ca.crt) — see `deploy/k8s/eob-mcp.yaml` for the commented volume mount. |
| Auth | Bearer token (k8s ServiceAccount projected) for MCP; mTLS available for gRPC |
| RBAC | scoped to Tawon CRDs in `tawon-operator` + `operators` |
| NATS access | `tawon-streamstore-*.tawon-operator.svc:4222` (in-cluster, future analysis layer) |
| Resources | requests 50m/128Mi, limits 250m/256Mi |

Pod and container SecurityContext: `runAsNonRoot`, `readOnlyRootFilesystem`,
`allowPrivilegeEscalation: false`, all capabilities dropped,
`seccompProfile: RuntimeDefault`, `GOMEMLIMIT` set to ~90% of memory
limit so the runtime trims allocations before OOM.

## Memory safety stance

Go is memory-safe by default but has escape hatches. We close them:

| Concern | Enforcement |
|---|---|
| `unsafe` / `reflect.UnsafePointer` | `forbidigo` linter, CI-gated |
| CGO | `CGO_ENABLED=0` in build; pure-Go binary |
| Data races | `go test -race` mandatory in CI |
| Nil dereferences | `nilaway` + `staticcheck` in CI |
| Unchecked errors | `errcheck` in CI |
| Known CVEs in deps | `govulncheck` in CI |
| Untrusted input parsers (NATS envelope, MCP request, gopacket bytes) | Native Go fuzzing, max-size limits at every boundary, panic recovery at goroutine boundaries |
| Container escape | distroless nonroot, read-only FS, drop ALL caps, RuntimeDefault seccomp |

All third-party libs (`nats.go`, `gopacket`, `miekg/dns`, `client-go`) are
pure Go. We do not link `libpcap` — capture bytes arrive pre-captured via
NATS, so the live-capture path of `gopacket` is unused.

## Repository layout

```
eob-mcp/
├── README.md                       # this file
├── FLEET.md                        # multi-cluster fleet architecture
├── TODO.md                         # open follow-ups
├── go.mod / go.sum
├── buf.yaml / buf.gen.yaml         # proto workspace + generator config
├── proto/eob/v1/service.proto      # service contract (7 RPCs + ClusterRef)
├── gen/go/eob/v1/                  # generated stubs (buf generate)
│   ├── service.pb.go               # messages
│   └── service_grpc.pb.go          # EoBServiceServer iface
├── cmd/eob-mcp/                    # entry point + HTTP/gRPC wiring
│   ├── main.go                     # listeners, TLS, shutdown
│   ├── grpc_test.go                # end-to-end gRPC smoke test
│   └── tls_test.go                 # TLS + flag-combo validation
├── internal/
│   ├── service/                    # in-process gRPC impl (single source of truth)
│   │   ├── service.go              # Server struct + New
│   │   ├── cluster_identity.go     # one RPC per file
│   │   ├── eob_health.go
│   │   ├── resource_common.go      # shared helpers (toStruct, clusterRef, ...)
│   │   ├── resource_list.go
│   │   ├── resource_get.go
│   │   ├── resource_apply.go
│   │   ├── resource_delete.go
│   │   └── resource_schema.go
│   ├── tools/                      # thin MCP-protocol wrappers around service
│   │   ├── identity.go             # cluster_identity
│   │   ├── health.go               # eob_health
│   │   └── resource.go             # 5 resource_* wrappers
│   ├── mcp/                        # MCP JSON-RPC server (tools/list, tools/call, ...)
│   ├── k8s/                        # typed clientset + dynamic client wiring
│   └── config/                     # env-driven config
├── deploy/k8s/eob-mcp.yaml         # SA + ClusterRole + Deployment + Service
├── deploy/README.md                # first-deploy walkthrough
├── Dockerfile                      # multi-stage, distroless final
└── Makefile                        # build, test, lint, proto, image
```

Adding a new tool:

1. Add the RPC + request/response messages to `proto/eob/v1/service.proto`.
2. `make proto` — regenerates the gRPC server interface.
3. Implement the method on `*service.Server` in a new `internal/service/<name>.go`.
4. Add a thin wrapper in `internal/tools/<name>.go` that parses MCP JSON args
   into the proto request, calls the service, marshals the proto response
   back to JSON. Register it in `cmd/eob-mcp/main.go`.
5. `make build && make test`.

## Roadmap

| Phase | Scope | Status |
|---|---|---|
| 0 | Skeleton, `cluster_identity`, `eob_health`, distroless image | shipped |
| 1a–c | Generic CRD CRUD (`resource_list/get/apply/delete/schema`), in-cluster deploy | shipped |
| 1d | Proto-first dual mode (MCP + gRPC over one service); TLS hooks (off by default, mTLS-capable) | shipped |
| 1e | Durable MCP connection (Ingress or autossh tunnel) | in progress |
| 1f | CI gates: govulncheck, golangci-lint, gosec, build + image scan | not started |
| 3 | Data plane: `StreamList`, `StreamStats`, `StreamRead` (raw envelopes + jq filter) | not started |
| 5 | MCP Resources (`eob://directives`, `eob://streams`, `eob://docs/runbook`), polish | not started |

**Explicit non-goals** (live in other services, not here):

| Out of scope | Lives where |
|---|---|
| Packet dissection | `tshark` / `sharkd`, on the consumer or in a sidecar |
| Stream aggregation / SQL / group-by / top-talkers | DuckDB / Pandas / consumer's choice |
| Directive templating, `render_template` | The LLM hand-rolls YAML against `ResourceSchema`, or a separate template service |
| Cross-stream correlation, `flow_view`, `process_view` | Consumer composes via `StreamRead` + `jq` / DuckDB |
| Decoded-stream production for data-sovereignty cases | Separate in-cluster `eob-decoder` sidecar (publishes decoded JetStream streams that `eob-mcp` reads like any other) |

## License

Apache-2.0.

## Multi-cluster fleet topology

`eob-mcp` is the per-cluster building block. For deployments where many
EoB clusters share a single operations team and want one UI across all of
them — with payload bytes staying inside each cluster — see
[`FLEET.md`](FLEET.md). That document describes the console + inference +
web-UI stack that consumes `eob-mcp` over MCP, the data-sovereignty
story, and the two recommended deployment topologies.

## Related

- [`mimetrix/XC-eBPF`](https://github.com/mimetrix/XC-eBPF) — EoB install
  bundle and operational runbook for F5 XC CE sites.
- [Mantis EoB / Tawon](https://mantisnet.com) — the EoB platform itself.
- [Model Context Protocol](https://modelcontextprotocol.io) — protocol
  spec.
