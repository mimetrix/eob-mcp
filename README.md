# eob-mcp

A Model Context Protocol (MCP) server that turns Claude Code into a
conversational front-end for [Mantis EoB / Tawon](https://mantisnet.com).
Generate capture and payload directives from natural language; analyze the
resulting NATS JetStream data without leaving the chat.

**Status:** early. Designed but not yet implemented.

---

## What it is

The Mantis EoB stack on a Kubernetes cluster (today: F5 XC Customer Edge
sites — see [`eob-xc-install`](https://github.com/mimetrix/XC-eBPF) for
that installer) gives you a CRD-driven capture/observation platform. The
existing UI is a per-cluster dashboard; the existing CLI is `kubectl` + the
JetStream API.

`eob-mcp` adds a third surface: an MCP server that exposes the EoB
primitives as **tools and resources** consumable by Claude Code (or any
MCP-aware client). A user describes intent in English; Claude orchestrates
the right combination of directive generation, application, stream reads,
decode, and aggregation; results come back as structured text + tables.

It is not a replacement for the dashboard or `kubectl` — both still work.
It is the conversational layer on top.

## What it lets you do

A typical session, four turns:

1. *"Capture DNS traffic from coredns across the cluster for 5 minutes."*
   — Claude renders a `ClusterDirective` from a template, validates it
   against the live CRD schema, shows you the YAML, and applies it after
   you confirm.

2. *"What did we catch?"* — Claude reads the resulting JetStream stream,
   decodes the payloads, and summarizes: query counts, top destinations,
   protocol breakdown, anomalies.

3. *"Why didn't master-2's agent come up?"* — Claude pulls operator logs,
   the agent pod's logs, and the admission webhook journal for that node;
   correlates them; explains the root cause.

4. *"Stop the directive and write me a summary."* — Claude deletes the
   directive and produces a markdown report citing the captured data.

The MCP server's job is to provide **primitives**. Claude orchestrates.

## Architecture

```
                        Claude Code
                             |
                       MCP (HTTP+SSE)
                             |
                  +----------v---------+
                  |     eob-mcp        |
                  |  (Go, ~20 MB img)  |
                  +---------+----------+
                            |
        +-------------------+--------------------+
        |                   |                    |
        v                   v                    v
    kube-apiserver     NATS JetStream    pod logs / metrics
  (Tawon CRDs +        (capture +        (kube-rbac-proxy
   ClusterDirective    payload           on :18443,
   operations)         streams)          journald via k8s)
```

Two clean layers inside the server:

- **Acquisition layer** — schema-driven generation of `ClusterDirective`
  resources from natural-language intent; validate via server-side dry-run;
  apply; observe readiness; stop. Pre-vetted templates for the common
  shapes (port capture, process capture, DNS watch, payload extract).

- **Analysis layer** — list, inspect, read, decode, search, and aggregate
  NATS JetStream data produced by capture/payload directives. Filtering
  uses dot-path expressions over the JSON envelope; aggregation runs
  server-side so Claude doesn't have to ingest millions of messages.

## Stream formats it understands

Tawon writes structured JSON envelopes to JetStream, one stream per
directive. The MCP server speaks both natively:

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

The inner `payload` is a standard L2 Ethernet frame and parses cleanly via
`gopacket` (IP → TCP/UDP → app layer).

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
nanosecond timestamps, and the raw L7 payload. The `where`/`group_by`
expressions in the analysis tools target these fields directly.

## Tool surface (planned)

### Acquisition

- `get_directive_schema()` — pulls the live `ClusterDirective` OpenAPI v3
  schema from the cluster
- `list_templates()` — pre-vetted directive shapes for common asks
- `render_template(name, params)` — skeleton + params → YAML
- `validate_directive(yaml)` — server-side dry-run validation
- `apply_directive(yaml)` — applies after explicit user confirmation
- `stop_directive(name)` — graceful stop

### Analysis

- `list_streams()` — catalog of capture/payload streams + sizes
- `stream_stats(stream, since)` — counts, bytes, rates
- `read_messages(stream, since, until, limit, where)` — server-side filter
- `sample_messages(stream, n, strategy)` — for streams too large to read
  linearly
- `decode_capture(messages[])` — Ethernet/IP/L4 summary via `gopacket`
- `decode_payload(messages[], proto)` — DNS/HTTP/TLS/raw decoders
- `aggregate(stream, group_by[], measures[], where, since, until)`
- `top_talkers(stream, by, n, since, until)`
- `flow_view(stream, flow_id)` — reconstruct a single conversation
- `process_view(stream, pod_or_process, since, until)`
- `search(stream, pattern, mode, target, since, until)`

### Resources

Read-only browsable via MCP `resources/list`:

- `eob://directives` — live list of `ClusterDirective` resources
- `eob://directives/{name}` — one directive's spec + status
- `eob://templates` — directive template catalog
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
| Image base | `gcr.io/distroless/static-debian12:nonroot` |
| Image size (target) | ~15–20 MB |
| Replicas | 2 (HA across masters) |
| Network | ClusterIP service in `tawon-operator` namespace |
| MCP transport | HTTP + SSE (Streamable HTTP, MCP spec) |
| TLS | inline self-signed in v0; cert-manager in v1 |
| Auth | Bearer token (k8s ServiceAccount projected, or external OIDC) |
| RBAC | scoped to Tawon CRDs in `tawon-operator` + `operators` |
| NATS access | `tawon-streamstore-*.tawon-operator.svc:4222` (in-cluster) |
| Resources | requests 100m/128Mi, limits 500m/512Mi |

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

## Repository layout (planned)

```
eob-mcp/
├── README.md                 # this file
├── LICENSE                   # Apache-2.0
├── go.mod
├── cmd/eob-mcp/              # entry point
├── internal/
│   ├── mcp/                  # MCP protocol layer
│   ├── nats/                 # JetStream client
│   ├── k8s/                  # CRD client, directive ops
│   ├── decode/               # gopacket / DNS / HTTP decoders
│   ├── aggregate/            # group_by, top_talkers, search
│   └── templates/            # embedded directive YAML templates
├── deploy/
│   ├── helm/                 # Helm chart
│   └── manifests/            # plain manifests
├── Dockerfile                # multi-stage, distroless
├── Makefile                  # build, test, lint, image, push
└── .github/workflows/        # CI: build + lint + test + scan + push
```

## Roadmap

| Phase | Scope | Status |
|---|---|---|
| 0 | Skeleton, `eob_health`, `list_directives`, CI gates, distroless image | not started |
| 1 | Acquisition layer (schema, validate, apply, stop, templates) | not started |
| 2 | Analysis core (list/stats/read/sample, decoders) | not started |
| 3 | Analysis higher-level (aggregate, search, flow/process views) | not started |
| 4 | Resources (docs, schema, templates as MCP resources), redaction, polish | not started |

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
