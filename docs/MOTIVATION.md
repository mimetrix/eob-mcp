# Why `eob-mcp` exists

This document is the customer-anchored motivation for the `eob-mcp`
service. It's the companion to [`ARCHITECTURE.md`](ARCHITECTURE.md):
that one says **what** `eob-mcp` is and what it deliberately is not;
this one says **why** the investment exists and which customer
problems it makes tractable.

If you're reading this as an internal F5 / Mantis stakeholder
deciding whether to fund continued work on `eob-mcp`, the two named
accounts below are the load-bearing answer.

---

## TL;DR

Two named customers drive the investment, with complementary
requirements that map cleanly onto the **two front doors** of the
same canonical service:

- **Verizon (VZW)** is running an **internal AI effort** that drives
  EoB through LLM agents — provisioning directives, investigating
  anomalies, reasoning over the data plane. They need an
  **LLM-native** interface, which is exactly what MCP is. This is
  the load-bearing reason `eob-mcp` is an MCP server in the first
  place, not just a gRPC API.
- **AT&T (ATT)** wants to **operate many EoB instances across many
  clusters from one console**. Each EoB site is independent and
  cluster-isolated by design; ATT needs a **federation surface**
  that lets one aggregator UI talk to all of them and merge results
  by site of origin. That's a service-to-service workload, which is
  exactly what gRPC is for.

These two asks line up exactly with the two halves of the
proto-first dual-mode design: **MCP for VZW's AI agents, gRPC for
ATT's federation aggregator, one `service.Server` underneath
both**. Neither customer pays the cost of the other's protocol; both
get the same canonical contract.

Without `eob-mcp`, both efforts would have to be built against
`kubectl`, raw CRDs, and per-site NATS JetStream connections —
viable for one site, painful for ten, unworkable for fifty.
`eob-mcp` makes both customer paths into a single supported
integration.

---

## Driving customer #1 — Verizon (AI-driven ops)

### What VZW is doing

Verizon is running an **internal AI effort** that uses LLM agents to
investigate, configure, and reason about their EoB-equipped sites.
The goal isn't "build a controller against an API"; it's "let our AI
agents *use* EoB the same way an engineer would, with the same
fluency, at agent speed."

That means:

- The **consumer is an LLM agent** — Claude, an internal Verizon
  LLM stack, or both. The agent invokes tools, reads results,
  reasons over data, decides what to do next.
- Workflows are **conversational and exploratory**, not
  pre-programmed. "Why is DNS slow at site X?" or "give me a
  capture of HTTP traffic from pods labeled `app=billing` for the
  next ten minutes" are the actual shapes — not scripted CD
  pipelines.
- The agent provisions / inspects / pauses / restarts
  ClusterDirectives the same way a human operator would — only it
  does it in milliseconds, can fan out across hundreds of pods,
  and can do it inside the loop of an ongoing investigation.
- Operations data (stream contents, directive status, k8s events)
  flows back into the agent's context for further reasoning — it
  is **not** for a controller to consume and store.

### Why MCP specifically (not "any API")

MCP is the right protocol for this consumer class for reasons
that don't apply to a controller-driven design:

- **Tool-use is a first-class protocol primitive.** MCP standardizes
  how an LLM discovers tools, learns their schemas, calls them with
  validated arguments, and consumes structured results. Building
  that against an arbitrary gRPC or REST API would force Verizon to
  build (and maintain) their own LLM-to-API adapter for every tool.
- **The major LLM clients already speak it.** Claude Code, Claude
  Desktop, Cursor, the Anthropic API's MCP support, and a growing
  ecosystem of agent frameworks all consume MCP servers directly.
  Verizon's internal effort plugs into the same surface without
  bespoke glue.
- **Schemas are LLM-shaped, not service-shaped.** MCP tool
  descriptions are human-language strings written for an LLM to
  reason about — "List Kubernetes resources of a given Kind" is the
  description, not just a method signature. The schema *is* the
  prompt for the LLM, and we own it.
- **Server-side gating.** When a backend isn't reachable (no kube
  client, no NATS), we register only the tools that work — the LLM
  never sees a tool that will only return errors. Argument
  validation lives at the tool boundary, not in the prompt.
- **Same canonical contract as the gRPC surface.** Every MCP tool
  delegates to the same `service.Server` method ATT's aggregator
  hits over gRPC. Verizon's AI agents and ATT's console see the
  same source of truth — they just translate it for different
  consumers.

### What this needs from `eob-mcp`

- **An LLM-native tool surface** with the seven Tawon CRDs reachable
  via generic CRUD, structured stream tools, and human-readable
  descriptions. → `cluster_identity` / `eob_health` / `resource_*`
  / `stream_*` MCP tools.
- **Output shapes an LLM can reason about** — structured JSON, slim
  summaries on list, full unstructured objects on get, jq-filtered
  envelopes on stream read. No protobuf to parse, no base64 the
  LLM has to babysit, no opinionated aggregations. → tools
  wrappers in `internal/tools/`.
- **Tool-level conditional registration** so the LLM only sees what
  this server can actually do today. → `serviceBackends`
  registration in `cmd/eob-mcp/main.go`.
- **Federation envelope on every response** so an agent operating
  across sites can route its reasoning correctly. → `ClusterRef`.
- **Same lifecycle primitives operators use** (apply / get / list /
  delete / pause / restart via the `startAt`/`stopAt` recipe) so an
  agent's "investigate then act" loop doesn't need any new
  primitives the human operator wouldn't have. → existing
  `resource_*` tools.

### What VZW gets back from this design

A single MCP endpoint per EoB site that:

1. Plugs into Claude / their internal LLM stack / any MCP-aware
   agent framework with **zero custom adapter code**.
2. Exposes the same canonical contract their human operators
   already use — agent + human reach for the same primitives.
3. Surfaces tool failures as structured errors the LLM can reason
   about and recover from (e.g. "this kind doesn't exist" → ask the
   user to clarify), not as opaque protocol-level errors.
4. Survives Tawon chart changes — the MCP tool surface is owned
   by us; the underlying CRD lookups are dynamic so a CRD field
   rename in Tawon does not break the agent contract.

The AI effort is the load-bearing reason `eob-mcp` is an MCP server
in the first place. A controller-driven Verizon would have asked
for "gRPC, please" and been served by the same architecture; an
AI-agent-driven Verizon asked for "MCP, please" and got it on the
same code path.

---

## Driving customer #2 — AT&T (federate/operate)

### What ATT is doing

AT&T runs (or will run) EoB across **many** clusters — different
sites, possibly different regions, definitely different blast
radii. From an operational standpoint they need a **single console**
that sees all of them. From a security standpoint they need each
cluster to remain independent (compromise of one site does not
compromise the others).

That's a classic federation problem: many independent backends,
one aggregator UI. The model is the same shape F5 itself uses for
its XC platform.

### What this needs from `eob-mcp`

- **A federation envelope on every response.** Site identity must
  travel with the data, so the aggregator can key results by site
  without out-of-band correlation. → `ClusterRef` on every RPC
  response, with `site_id` / `tenant` / `region`.
- **Cheap liveness so the aggregator can fan out across N sites
  efficiently.** Polling every site with full `EoBHealth` at 30s
  doesn't scale; the aggregator needs a single cheap call to filter
  "which sites need attention." → `Heartbeat`.
- **Live data plane without polling.** ATT operators looking at a
  capture stream from site X want the new envelopes as they land,
  not on a 5s poll. → `TailStream` (live JetStream tail).
- **Live state plane without polling.** Aggregator's "view of
  what's running" should self-update when CRs change at any
  site. → `WatchResources`.
- **Unified event feed.** Aggregator's "ops timeline" merges k8s
  Events + the audit trail of who-did-what across every site. →
  `EventStream` (k8s + audit, both sources unified).
- **Per-site authentication** so the aggregator's connection to
  each site can be independently authorized and revoked. → mTLS
  hooks already in place; cert provisioning is open work
  (`Phase 1h`).
- **Version-skew tolerance.** Sites won't all upgrade in lockstep;
  the aggregator needs to know which features each site supports.
  → Capability discovery via `cluster_identity` + `Heartbeat`
  build identity, with future structured `GetCapabilities` if the
  shape gets richer.

### What ATT gets back from this design

A single gRPC client implementation, repeated across N sites, that:

1. Identifies which site each response came from at the wire level,
   so an aggregator just merges streams by `cluster.site_id`.
2. Doesn't poll for state — `WatchResources` + `EventStream` +
   `TailStream` push, the aggregator subscribes.
3. Doesn't poll for liveness — one cheap `Heartbeat` per site at
   30s tells the aggregator which sites are healthy.
4. Doesn't need a custom protocol — gRPC, reflection on, generated
   clients work everywhere.

---

## What was on the table before `eob-mcp`

Without this service, both customers above would have to build
against the surfaces that ship today with the Tawon stack:

- **`kubectl` + raw CRDs**: works for one site. For VZW's AI effort
  it means giving the LLM `kubectl exec` privileges — operationally
  unacceptable and not the right cognitive surface for an agent.
  For ATT it doesn't help federate across sites.
- **The Mantis dashboard**: human-only, single-site, not
  programmable. Neither an LLM agent nor a federation aggregator
  can drive it.
- **Direct NATS JetStream connections**: works for the data plane
  but requires every consumer to know the per-site streamstore
  Service name, hostAliases workarounds, cluster.local quirks (see
  [`UPSTREAM-FIXES.md`](../../eob-xc-install/UPSTREAM-FIXES.md)
  for the laundry list). Effectively makes every consumer — agent
  or aggregator — a platform integrator.
- **A bespoke "LLM-to-CRD adapter" Verizon could build themselves**:
  technically possible, structurally wrong. Every customer who
  wants to AI-augment their EoB ops would have to build and
  maintain their own version. MCP exists to remove exactly this
  kind of per-customer adapter from the equation.
- **Helm**: install-time only. Not a runtime control plane.

None of those compose at fleet scale. None give you a federation
envelope. None give you typed introspection of what's installed.
None give you a single audit trail per site, let alone across
sites. None survive a Tawon chart field rename without consumer
breakage.

`eob-mcp` collapses all of those into one supported integration
surface.

---

## What `eob-mcp` adds, summarized

For the curious reader who hasn't seen
[`ARCHITECTURE.md`](ARCHITECTURE.md) yet, the four load-bearing
design decisions are:

1. **Proto-first dual-mode.** One `service.proto` is the canonical
   contract. Two front doors translate it for two consumer classes:
   **MCP for LLM-agent consumers (VZW's AI effort, the Inspector,
   any agent framework)** and **gRPC for service-to-service
   consumers (ATT's aggregator, future federation clients)**. Both
   delegate to the same in-process `service.Server`. New RPCs are
   added to the proto first; the front doors stay thin and uniform.
2. **Federation envelope on every response.** `ClusterRef` ships
   `site_id` / `tenant` / `region` on every unary response and every
   streaming message. An aggregator never has to ask "which site
   did this come from?" — the answer is on the wire.
3. **Streaming surface.** `WatchResources` (state plane),
   `TailStream` (data plane), and `EventStream` (audit + k8s
   events) replace polling for all three of the things an ATT
   aggregator cares about.
4. **Generic CRUD across every Tawon CRD.** All seven CRDs are
   reachable through `Resource*` with dynamic client + RESTMapper —
   no codegen per CRD, no breaking on field renames, no
   maintenance debt as Tawon evolves.

---

## How each customer gets served, concretely

### VZW recipe

A Verizon AI agent looks roughly like this:

```
[VZW AI agent]
   │  (Claude / internal LLM stack / agent framework)
   │
   │  MCP tool calls over HTTP/JSON-RPC
   ▼
eob-mcp:8443 (per EoB site)
   │
   ├── cluster_identity              ("which site am I on?")
   ├── eob_health                    ("is the stack healthy?")
   ├── resource_list / resource_get  ("what directives exist?")
   ├── resource_apply / _delete      (provision / pause / restart)
   ├── resource_schema               (discover CRD shape on the fly)
   └── stream_list / _stats / _read  (read the data plane, with jq filters)
```

No bespoke LLM-to-API adapter. No proto codegen. The agent picks up
the tool catalog from the MCP server itself — descriptions,
argument schemas, output shapes — and reasons about what to do
next. Same `service.Server` under the hood that ATT's gRPC
aggregator hits.

### ATT recipe

An ATT aggregator console looks roughly like this:

```
            ┌──────────────[ATT console]──────────────┐
            │           federation aggregator          │
            └────┬───────────┬───────────┬─────────────┘
       gRPC     │   gRPC    │   gRPC   │   gRPC
                ▼            ▼          ▼          ▼
        eob-mcp@site-1  eob-mcp@site-2  ...    eob-mcp@site-N
              │              │                       │
              ▼              ▼                       ▼
        Heartbeat       Heartbeat            Heartbeat   (poll, 30s)
        WatchResources  WatchResources       WatchResources
        TailStream      TailStream           TailStream
        EventStream     EventStream          EventStream
```

The aggregator multiplexes streams across all sites, keys results
by `cluster.site_id`, falls back gracefully when a site is
unreachable (its Heartbeat stops). The console doesn't know or care
about Tawon CRD internals — it speaks `eob.v1.EoBService` and that's
the only contract it has.

---

## What `eob-mcp` deliberately does not do

Per `ARCHITECTURE.md`'s "do one thing" doctrine, neither customer
gets these from `eob-mcp` itself:

- **Cross-site aggregation / correlation.** That's the aggregator's
  job (ATT's console, in the example above). `eob-mcp` returns
  single-site results, federation-envelope-tagged, and stops.
- **DNS / HTTP / packet decoding.** Tawon has decoder tasks for that
  (`payload` → `dns` → `publish` chains); `eob-mcp` is a read
  surface, not a decoder.
- **Templating "common workflows."** No `directive_pause` or
  `directive_restart` RPCs. The pattern lives consumer-side as a
  `resource_apply` recipe (see
  [`project_tawon_directive_pause_restart`](#) memory).
- **Prose synthesis or LLM-side reasoning.** The MCP front door
  exposes data; the LLM client reasons over it. Server doesn't.

The constraint is deliberate — it's what keeps the service
small, replaceable, and trustworthy as the protocol primitives
around it (MCP, gRPC, eventually whatever comes next) evolve.

---

## Roadmap by customer need

Open work tracked in [`TODO.md`](../TODO.md), ordered by which
customer ask it unblocks:

| Item | VZW (AI agents) | ATT (federation) | Notes |
|---|---|---|---|
| **Phase 1h — cert provisioning** | needed | needed | mTLS hooks done; cert pipeline is the blocker. Becomes structural once ATT runs 10+ sites and when VZW's agents reach beyond a single site. |
| **Per-call authz / actor extraction** | needed | needed | Audit events have an `actor` field; today empty. mTLS subject → actor is the obvious next step. For VZW it answers "which AI agent did what." |
| **Phase 1e — durable MCP connection** | **needed** | low | Tunnel / Ingress for the MCP endpoint matters most for the AI-agent consumer. ATT's aggregator goes gRPC. |
| **Phase 1f — claude-code `/mcp` bug** | medium | nil | Affects the LLM-side dev loop directly. VZW's agents may hit the same. |
| **Resource templates + `resources/list` MCP surface** | medium | nil | Makes CRs and streams browseable as URIs without a tool call — good for LLM discoverability. MCP-only. |
| **MCP subscriptions / live-tail tool** | medium | nil | Live data on the MCP side; mirrors the gRPC `TailStream` for the agent consumer. |
| **Pagination cursors** | low | needed | ATT's aggregator paging across many sites needs resumable reads. |
| **Capability negotiation RPC** | low | needed | Graceful degradation across version skew in a multi-site fleet. |
| **Logging-handler → ErrorCount24h** | low | medium | Heartbeat's `error_count_24h` is currently always 0. Wire to the slog handler. |

---

## Related documents

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — the canonical scope statement
  (what `eob-mcp` is and is not)
- [`../README.md`](../README.md) — installation and operational
  walkthrough
- [`../FLEET.md`](../FLEET.md) — how the gRPC surface composes into a
  fleet console
- [`../TODO.md`](../TODO.md) — what's open and why
- [`../../eob-xc-install/INFRASTRUCTURE-POSITIONING.md`](../../eob-xc-install/INFRASTRUCTURE-POSITIONING.md)
  — the broader EoB-as-site-infrastructure framing (parent context
  for both customer asks above)
- [`../../eob-xc-install/UPSTREAM-FIXES.md`](../../eob-xc-install/UPSTREAM-FIXES.md)
  — Mantis-side fixes that retire local glue, indirectly unblocking
  some of the integration cost VZW and ATT see today
