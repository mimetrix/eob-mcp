# Multi-cluster EoB fleet architecture

How `eob-mcp` fits into a fleet of EoB-equipped Kubernetes clusters with a
single shared user interface — without sending captured payloads off-site
and without locking the deployment to any one model vendor.

This document is **forward-looking design**. The repo currently only ships
`eob-mcp` (one component of the picture below); the console, inference
service, and web UI are separate projects (likely separate repos) that
consume `eob-mcp` over MCP. They're described here so the per-cluster
server design stays compatible with the fleet target from day one.

---

## Why a fleet view

A typical EoB deployment is **many clusters, one operations team**:

- A managed-services provider runs EoB on every F5 XC CE site they
  operate; one SOC analyzes traffic across all of them.
- An enterprise runs EoB on regional clusters (us-east, eu-west, ap-south);
  one platform team manages directives and reads results.
- A research/SecOps team uses EoB in temporary lab/red-team clusters that
  spin up and down; one console outlives the clusters.

In all three cases the user-facing question is the same: *"give me one
place to look, regardless of which cluster the data is on."*

`eob-mcp` is the per-cluster building block. This doc describes the
fleet-level system that consumes it.

---

## Architecture

```
                       ┌─────────────────────┐
                       │  Browser (per user) │
                       └──────────┬──────────┘
                                  │
                          ┌───────▼────────┐
                          │  Web UI        │   chat + dashboards +
                          │  (forked OSS   │   cluster picker
                          │   chat client) │
                          └───────┬────────┘
                                  │
              ┌───────────────────▼────────────────────┐
              │  Console backend                       │
              │   - MCP multiplexer (N clusters)       │
              │   - chat history / saved queries       │
              │   - SSO / OIDC / RBAC                  │
              │   - calls inference service            │
              └─────┬───────────────────────────┬──────┘
                    │                           │
            ┌───────▼─────────┐         ┌───────▼──────────────────────────┐
            │ Inference svc   │         │ eob-mcp servers (one per         │
            │  (vLLM / Ollama │         │ cluster), each exposing:         │
            │   + OSS model)  │         │   - MCP over HTTP /mcp           │
            └─────────────────┘         │   - gRPC over TLS (federation)   │
                                        │   site A | site B | site C ...  │
                                        └──────────────────────────────────┘
```

Each `eob-mcp` exposes the same surface twice: MCP for chat-style
consumers (LLM agents) and gRPC for programmatic federation (an
aggregator fanning out to N sites). Both front doors hit the same
in-process service struct, so behavior cannot drift between them.

### The four components, and what they own

| Component | Role | Build vs. buy |
|---|---|---|
| **`eob-mcp`** (per cluster) | The tool surface — schema-driven directive generation, JetStream analysis, decoders, RBAC scoped to that cluster | **Build** — this repo |
| **Inference service** | Hosts the LLM. Tool-calling-capable OSS model (e.g. Llama 3.3 70B Instruct, Qwen 2.5 72B Instruct, DeepSeek V3). Single endpoint per org. | **Off-the-shelf** — vLLM or Ollama, deploy as-is |
| **Console backend** | Holds MCP client connections to every cluster's `eob-mcp`, persists chat history, handles SSO/RBAC, mediates between the UI and the model | **Build small, or fork** — see below |
| **Web UI** | Chat panel, cluster selector, dashboard widgets, history browser | **Fork an OSS chat client** — see below |

---

## The pragmatic UI path: fork, don't build

Several OSS chat clients already implement MCP multiplexing, history
persistence, and OIDC. Forking one of these is much cheaper than building
a custom UI from scratch:

| Project | Native MCP | Multi-MCP | OSS model support | Self-host |
|---|---|---|---|---|
| LibreChat | yes | yes | any OpenAI-API-compatible | yes |
| Open WebUI | yes | yes | Ollama-first | yes |
| AnythingLLM | yes | yes | yes | yes |

The recommended starting path:

1. Fork the chat client (start with the one whose architecture is the
   cleanest fit — LibreChat is currently the most mature MCP integration).
2. Add an EoB-specific dashboard panel: live directive count, stream
   health, agent pod readiness, all driven by `eob_health` /
   `list_directives` calls against each connected `eob-mcp`.
3. Add a "cluster selector" so a user can scope a question to one site,
   to a tagged subset, or to the whole fleet.
4. Configure each `eob-mcp` endpoint + bearer token in the chat client's
   MCP server registry.

Estimated effort: ~2 weeks of focused frontend work to a working demo,
on top of the OSS chat client's existing infrastructure.

---

## Data sovereignty story

The most important property of this architecture: **captured payload bytes
stay near their home cluster**, governed by where the consumer chooses to
do the heavy lifting.

`eob-mcp` itself is deliberately narrow: it returns Tawon's raw JSON
envelopes from JetStream with an optional server-side `jq` filter, plus
the federation `cluster` envelope. **It does not decode bytes and does
not aggregate.** Those responsibilities sit in:

- **Co-located in-cluster services** — e.g. an `eob-decoder` sidecar
  that reads raw streams, dissects via `tshark`, and publishes decoded
  streams back to JetStream. `eob-mcp` then serves the decoded streams
  like any other. Payload bytes never leave the cluster.
- **The consumer** — if data-sovereignty isn't the binding constraint
  for a given workload, the consumer can pull raw envelopes via
  `StreamRead` and run `tshark` / DuckDB / `jq` locally.

The console then synthesizes a natural-language response via the
inference service over whichever structured results came back.

What crosses the wire:

| Direction | Content | Sensitive? |
|---|---|---|
| Browser → console | natural-language prompts | sometimes |
| Console → `eob-mcp` | MCP tool calls (JSON-RPC) | low (no payload data) |
| `eob-mcp` → console | structured results (counts, decoded fields, *small* sampled payloads) | depends — server can redact |
| Console → inference svc | prompts + structured tool results | same |
| Inference svc → console | model output | same |

What does **not** cross the wire under normal operation, when the
recommended in-cluster decoder pipeline is in place:

- Raw packet captures (stay in the cluster's NATS JetStream)
- Full payload byte streams (stay in JetStream; `eob-decoder` produces
  decoded summary streams that the console reads instead)
- Pod / container / process metadata for non-sampled traffic
- Any data from clusters the user isn't authorized to query

If a consumer asks for raw envelopes directly via `StreamRead` without
a decoder pipeline in front, those bytes *do* cross the wire (gated by
the consumer's authentication + the per-tool RBAC). The data-sovereignty
guarantee is architectural — it's earned by deploying the decoder
in-cluster, not by clever `eob-mcp` server-side logic.

`eob-mcp` is the authentication + authorization + audit boundary;
content policy (redaction, sampling) lives in the decoder pipeline or
the console.

For air-gapped sites: the inference service can run inside the same
cluster as the console (or any sovereignty zone). No traffic ever needs
to reach the public internet.

---

## Two deployment topologies

### Topology 1 — Central ops cluster (recommended for ≥ 3 sites)

```
                              ┌─────────────────┐
                              │  Ops cluster    │
                              │   ┌───────────┐ │
                              │   │  Console  │ │
                              │   │  + Web UI │ │
                              │   └─────┬─────┘ │
                              │   ┌─────▼─────┐ │
                              │   │ Inference │ │
                              │   │ (GPU node)│ │
                              │   └───────────┘ │
                              └────────┬────────┘
                                       │ MCP over TLS
                ┌──────────────────────┼──────────────────────┐
                │                      │                      │
        ┌───────▼──────┐      ┌────────▼─────┐      ┌─────────▼────┐
        │ EoB site A   │      │ EoB site B   │      │ EoB site C   │
        │  + eob-mcp   │      │  + eob-mcp   │      │  + eob-mcp   │
        └──────────────┘      └──────────────┘      └──────────────┘
```

**When to use:** more than 2 or 3 sites, a single ops team, a dedicated
GPU budget you want to share, or any time the inference cost would be
duplicated across sites.

**Properties:**
- One GPU cluster serves the whole fleet; cheaper at scale than a GPU per site
- Single SSO integration point
- Console becomes the audit/RBAC choke point
- Console's blast radius is the whole fleet — needs HA

### Topology 2 — Embedded console (smaller, single-tenant)

```
        ┌──────────────────────────────────────────┐
        │  EoB site A (designated "home" site)     │
        │   ┌──────────┐  ┌──────────┐  ┌────────┐ │
        │   │ Console  │  │Inference │  │eob-mcp │ │
        │   │ + Web UI │  │          │  │ (this) │ │
        │   └──────────┘  └──────────┘  └────────┘ │
        └────────────────┬─────────────────────────┘
                         │ MCP over TLS
                ┌────────┴────────┐
                │                 │
        ┌───────▼────────┐ ┌──────▼────────┐
        │ Site B         │ │ Site C        │
        │  + eob-mcp     │ │  + eob-mcp    │
        └────────────────┘ └───────────────┘
```

**When to use:** 1–3 sites total; one of them is the "home" site that
gets the console; no dedicated ops infrastructure.

**Properties:**
- No extra cluster to provision
- The home site's GPU (if any) does double duty
- Simpler ops, but the home site is a SPOF for the console
- Migrates cleanly to Topology 1 when you outgrow it

---

## Per-cluster `eob-mcp` requirements for fleet operation

For a fleet-aware design that doesn't require retrofits, every `eob-mcp`
instance exposes three things uniformly:

### 1. Cluster identity RPC

```
ClusterIdentity() → {
  cluster: {
    site_id: "srikan-tf-test-0",
    tenant:  "platform-svc-nbryikfr",
    region:  "us-east-2"
  },
  k8s_version: "v1.34.2-ves",
  eob_version: "v3.0.0-rc4",
  mcp_version: "0.1.0"
}
```

(A `capabilities` field will arrive once we have a versioned feature
flag bucket worth advertising.)

Lets the console enumerate connected clusters without external metadata
and reveal which one a result came from.

### 2. Origin-stamped results — the `cluster` envelope

Every RPC's response carries a `cluster` block (the same shape used in
`ClusterIdentity` above):

```json
{
  "cluster": {
    "site_id": "srikan-tf-test-0",
    "tenant":  "platform-svc-nbryikfr",
    "region":  "us-east-2"
  },
  ...response body...
}
```

So when the aggregator merges results from N clusters, every payload
already knows where it came from — no out-of-band site tagging needed.
This is encoded in `proto/eob/v1/service.proto` as the `ClusterRef`
message, embedded as field 1 of every response message.

### 3. Standardized RPC names + signatures

Tools / RPCs have **exactly** the same names and signatures across
every `eob-mcp`. No site-specific extensions in v1; new behavior comes
through proto-versioned additions (`eob.v2`). The aggregator can fan
out generically:

```
for site_conn in fleet:
    site_conn.EoBHealth(EoBHealthRequest{})
```

Without a name registry to consult.

These three properties are easy to ship in v1 and expensive to retrofit
after an installed base exists. They are encoded in the `.proto` file,
so both the MCP wrappers and the gRPC front door inherit them for free.

---

## RBAC model

Three layers, each enforced at its own boundary:

| Layer | Enforced where | Granularity |
|---|---|---|
| **User → console** | Console backend (OIDC + groups) | role: viewer / operator / admin |
| **Console → `eob-mcp`** | `eob-mcp` (bearer token on the MCP path; **mTLS on the gRPC path** when the aggregator is the caller) | per-cluster, per-role; tool/resource allowlist |
| **`eob-mcp` → cluster** | Kubernetes RBAC on `eob-mcp`'s ServiceAccount | namespace + verb level |

The dual-front-door design maps naturally to this split: MCP is the
"agent talking" path (bearer-auth, lower trust) while gRPC is the
"infrastructure-to-infrastructure" path (mTLS, mutual identity). The
underlying authorization decisions on the service side are identical;
only the authentication channel differs.

This keeps the trust boundary tight: a compromised console doesn't get
arbitrary cluster access — it can only call the MCP tools the per-cluster
bearer token authorizes. A compromised `eob-mcp` can only do what its
ServiceAccount permits in that one cluster's k8s API.

Redaction is configured per `eob-mcp`. Default: redact on. Operators can
opt out per-tool-call for forensic work, but the opt-out is logged.

---

## What's in scope for *this* repo

| Component | This repo | Separate repo |
|---|---|---|
| `eob-mcp` per-cluster server | yes | — |
| Helm chart for `eob-mcp` | yes (`deploy/helm/`) | — |
| Reference Dockerfile for `eob-mcp` | yes | — |
| Console backend | — | future `eob-console` (TBD) |
| Web UI | — | fork of an OSS chat client |
| Inference service | — | off-the-shelf (vLLM/Ollama) |
| Per-cluster credentials/secrets | — | belongs in ops tooling, not in code |

This repo stays focused on shipping a great per-cluster MCP server. The
console and UI are downstream concerns that consume it; building them is
a separate effort that should be evaluated on its own merits once
`eob-mcp` is stable.

---

## Open design questions

1. **Console transport between web UI and backend.** WebSocket vs. plain
   HTTP+SSE. Both work for streaming tool-call results to the UI;
   WebSocket is more bidirectional but requires more frontend plumbing.
2. **~~Console authentication to per-cluster `eob-mcp`.~~** *Partially
   resolved.* The gRPC front door supports mTLS today (flag-gated, off
   by default — see `-tls-client-ca` in `README.md`). The MCP front
   door still defers to bearer tokens. Remaining open piece is the
   cert provisioning story (cert-manager vs. SPIFFE vs. external CA);
   the wire-level protocol choice is settled.
3. **Inference deployment shape.** Single shared inference cluster vs.
   per-site (sovereignty argument) vs. per-tenant (cost-isolation
   argument). May depend on customer.
4. **Fleet-level cache layer.** Cluster catalog, recent health summaries,
   stream catalogs — these are small and re-read constantly. Worth a
   cache layer in the console backend with sub-minute TTL.
5. **Cross-cluster joins.** "Show me directives running template X across
   the fleet" requires either Claude/the model doing a fan-out aggregation
   in-context (works), or a server-side `fleet_*` toolset in the console
   backend (cleaner). Probably both, eventually.

---

## Related

- `README.md` — the per-cluster `eob-mcp` design (this repo's primary
  artifact)
- [`mimetrix/XC-eBPF`](https://github.com/mimetrix/XC-eBPF) — EoB install
  bundle for F5 XC CE sites; one site equals one `eob-mcp` deployment
- [Model Context Protocol](https://modelcontextprotocol.io) — protocol
  spec consumed by the console
- [LibreChat](https://github.com/danny-avila/LibreChat),
  [Open WebUI](https://github.com/open-webui/open-webui),
  [AnythingLLM](https://github.com/Mintplex-Labs/anything-llm) — OSS chat
  clients that could host the web UI layer
