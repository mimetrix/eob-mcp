# `eob-mcp` architecture and scope

This document is the canonical statement of what `eob-mcp` is — and,
more importantly, what it is not. Read it before adding an RPC, a
package, or a dependency. If a proposed change does not match the
scope below, it belongs in a different service.

---

## The product, in one sentence

`eob-mcp` is **the typed, federated API to one EoB site**, exposed
over MCP and gRPC from a single in-process service.

That's the whole product. Everything else in this document elaborates
on what that does and does not mean.

---

## The "one thing" principle

The scope is fixed and small on purpose. A narrowly-scoped service:

- can be reasoned about, tested, and replaced without coupling to a
  specific LLM, a specific decoder, or a specific aggregation engine
- composes cleanly with whatever the consumer already runs
  (Wireshark / DuckDB / jq / the LLM of the week)
- fails in isolation — a tshark hang or a DuckDB OOM doesn't take down
  the EoB API
- doesn't need to be re-built every time the analysis ecosystem moves

The complexity of an alternate "smart orchestrator" design scales with
the sophistication of the consumer driving it. That's a coupling we
won't make.

---

## What `eob-mcp` owns

Two halves, no more:

### Control plane

| RPC / tool | Purpose |
|---|---|
| `ClusterIdentity` | site_id, tenant, region, k8s_version, eob_version, mcp_version |
| `EoBHealth` | snapshot of operator/dashboard/streamstore/webhook/agent components |
| `ResourceList` | list Kubernetes resources by Kind |
| `ResourceGet` | fetch one resource |
| `ResourceApply` | Server-Side Apply (with dry-run and force) |
| `ResourceDelete` | idempotent delete |
| `ResourceSchema` | OpenAPI v3 schema for a CRD |

### Data plane (Phase 3)

| RPC / tool | Purpose |
|---|---|
| `StreamList` | catalog of NATS JetStream streams for this site |
| `StreamStats` | counts, bytes, rates for one stream over a time window |
| `StreamRead` | raw Tawon JSON envelopes with optional server-side **jq** filter |

Every response carries a `cluster` envelope (`ClusterRef`: site_id,
tenant, region) so a federating aggregator can disambiguate results
across sites without out-of-band metadata.

---

## What `eob-mcp` does NOT own

These are explicit non-goals. They live elsewhere; the consumer or a
separate service composes them with what `eob-mcp` returns.

| Capability | Lives where | Notes |
|---|---|---|
| **Packet dissection** | `tshark` / `sharkd` / `wireshark-common` | Adopt; do not re-implement. |
| **Stream aggregation** (`group_by`, `top_talkers`, `aggregate`, …) | DuckDB / SQL / Pandas | Consumer's choice. |
| **Filter / query language** | `jq` syntax via `itchyny/gojq` | We adopted it; we did not invent it. |
| **Cross-stream correlation** (`flow_view`, `process_view`) | Consumer joins via `StreamRead` + `jq` / DuckDB | |
| **Directive templating** (`list_templates`, `render_template`) | LLM hand-rolls YAML against `ResourceSchema`, or a separate template service | The schema we expose is sufficient input for any LLM. |
| **Workflow orchestration** | The LLM / aggregator / human / shell pipeline | We're not a pipeline runner. |
| **LLM-friendly prose synthesis** | The LLM, every time | We return structured proto/JSON; the consumer narrates. |
| **Decoded-stream production for data-sovereignty cases** | Separate in-cluster `eob-decoder` sidecar | Reads raw streams → dissects → publishes decoded streams back to JetStream. `eob-mcp` reads the decoded streams like any other. |
| **Content policy (redaction, sampling)** | The decoder pipeline or the console | Not our responsibility. |
| **Generic packet-analysis tool** | Wireshark, again | A pcap with no EoB origin is not our concern. |

**The test**: if a different consumer — or a shell script, or a curl
loop — would still want the same call shape, it belongs in `eob-mcp`.
If the value depends on the consumer being smart, it doesn't.

---

## Layering

```
┌────────────────────────────────────────────────────────────────────────┐
│ Any LLM / agent framework / aggregator / shell pipeline / dashboard    │
└──────────────────────────────┬─────────────────────────────────────────┘
                               │
   ┌───────────────┬───────────┼──────────────────────────┐
   ▼               ▼           ▼                          ▼
  MCP          gRPC native    OpenAI-style          future X-protocol
  (today)      (today)        function-calling      (sibling package
   │               │           (sibling package)     under internal/)
   ▼               ▼           ▼                          ▼
 internal/    eobv1.        internal/openai/         internal/<x>/
 tools/       Register-     (future)                 (future)
 (MCP         EoBService-
  wrappers)   Server
   │               │           │                          │
   └───────────────┴───────────┼──────────────────────────┘
                               ▼
                ┌──────────────────────────────────────┐
                │   internal/service.Server            │
                │   (the only thing that holds         │
                │    business logic; proto-typed)      │
                └──────────────┬───────────────────────┘
                               ▼
        ┌──────────────────────┴────────────────────────┐
        ▼                      ▼                        ▼
  internal/k8s/         internal/streams/         internal/filter/
  (today)               (Phase 3, wraps           (Phase 3, wraps
                         nats.go)                  itchyny/gojq)
```

Four layers, each with one responsibility:

1. **`proto/eob/v1/service.proto`** — the contract. Every consumer
   (MCP, gRPC, anything else) talks to this surface. Changes here
   are versioned (`eob.v2`, never modify `eob.v1`).
2. **Front-door adapters** (`internal/tools/`, future siblings) —
   project the proto onto a specific on-wire protocol. **No business
   logic.** Parse args → call service → marshal response.
3. **The service** (`internal/service/`) — the only place business
   logic lives. Each method composes backend interfaces; if a method
   passes ~50 LOC, that's a scope-leak signal.
4. **Backend interfaces and adapters** (`internal/k8s/`,
   `internal/streams/`, `internal/filter/`, …) — narrow interfaces
   (StreamReader, Filter, …) with concrete impls that wrap external
   libraries. Swappable; mockable.

---

## Rules for adding work

### Adding a new RPC

1. Add the request/response messages + RPC entry in
   `proto/eob/v1/service.proto`.
2. `make proto` to regenerate stubs.
3. Implement on `*service.Server` in a new
   `internal/service/<name>.go`. **One file per RPC.** Method body
   composes existing backend interfaces; if you need new backend
   capability, add it to the interface, not inline.
4. Add a thin wrapper in `internal/tools/<name>.go` (one file per
   tool or one file per related group). Parse args → call service →
   marshal proto response. **No business logic.**
5. Register the wrapper in `cmd/eob-mcp/main.go`.
6. `make build && make test`.

### Adding a new backend

1. Define the interface in a new package under `internal/`.
   Narrow — the smallest surface that satisfies our use case.
   *Do not* re-expose the underlying library's full API.
2. Write a concrete impl in the same package. Keep it thin —
   adaptation only, no logic on top.
3. The service holds the interface, not the concrete type.
4. Provide an in-memory fake in the same package for tests.

### Adding a new front-door protocol

1. New sibling package under `internal/`, e.g. `internal/openai/`.
2. Same shape as `internal/tools/`: per-RPC adapter functions.
3. Wire it up in `cmd/eob-mcp/main.go` next to the existing
   listeners.
4. The proto contract does not change. The service does not
   change. Only the projection.

---

## Lines we will not cross

These would each indicate the scope principle has been violated.
Catch them in review.

- ❌ A new RPC whose value depends on the consumer being an LLM.
- ❌ A method on `*service.Server` that exceeds ~50 LOC.
- ❌ Importing a packet-dissection library
  (`gopacket`, `miekg/dns` for L7 parsing, …) into `internal/service/`
  or anywhere in this repo.
- ❌ Adding a SQL parser, query engine, expression language, or
  aggregation framework to this repo.
- ❌ Returning pre-formatted prose / Markdown / chat strings from
  the service. Service returns proto; front doors do not add
  semantic formatting either.
- ❌ A backend package that re-exports the underlying library's
  types instead of presenting a narrow interface of our own.
- ❌ Coupling any test to a specific LLM's behavior.

---

## The drag-and-drop pcap, again

If a user drops a pcap into a chat and asks for analysis, **`eob-mcp`
has no role** when the pcap has no Tawon origin. `tshark` alone is
sufficient. This is a feature, not a gap: `eob-mcp` is not a generic
packet-analysis tool, and never will be.

When the pcap *did* come from EoB:

1. `StreamList` → find the stream that captured it.
2. `StreamRead` (optionally with a `jq` filter) → fetch the raw
   envelopes including the originating directive metadata, node,
   pod, process, flow ID.
3. The consumer pipes those records to `tshark` (locally or via an
   in-cluster sidecar) for dissection.
4. The consumer pipes dissected records to DuckDB / `jq` / its own
   logic for aggregation and reasoning.

The federation envelope (`cluster: { site_id, tenant, region }`) lets
the consumer keep results disambiguated across sites without out-of-band
metadata. That is `eob-mcp`'s value-add — not the decoding, not the
aggregation, not the prose.
