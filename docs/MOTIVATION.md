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
requirements that both map cleanly onto the same architecture:

- **Verizon (VZW)** wants to **build their own internal control
  plane** for EoB — provisioning, configuring, and managing the
  observation stack from their own tooling, not from `kubectl` or
  the Mantis chart. They need a **typed, programmatic API** that
  can be driven from a Verizon-owned controller.
- **AT&T (ATT)** wants to **operate many EoB instances across many
  clusters from one console**. Each EoB site is independent and
  cluster-isolated by design; ATT needs a **federation surface**
  that lets one aggregator UI talk to all of them and merge results
  by site of origin.

Without `eob-mcp`, both of these efforts would have to be built
against `kubectl`, raw CRDs, and per-site NATS JetStream connections
— viable for one site, painful for ten, unworkable for fifty.
`eob-mcp` makes both customer paths into a single supported
integration: one proto, two front doors, federation-native from the
first response.

---

## Driving customer #1 — Verizon (build/integrate)

### What VZW is doing

Verizon is staffing an internal effort to make EoB a first-class
piece of their network-observability infrastructure. The goal is
not "use the Mantis dashboard"; the goal is **"drive EoB from a
Verizon-built control plane that integrates with their existing
operational systems."**

That means:

- Verizon's tooling becomes the **client**, not the user.
- The control plane provisions ClusterDirectives / Directives /
  Streams across one or more EoB-equipped sites.
- Operational state (directive readiness, agent health, stream
  catalog) is consumed programmatically — not screen-scraped from
  the dashboard.
- Lifecycle operations (pause, restart, scheduled rollouts, dry-run
  validation, rollback) are scripted against an API contract that
  doesn't churn under them.

### What this needs from `eob-mcp`

- **A typed contract**, not a YAML/kubectl interface. Verizon's
  controller needs a proto/schema it can codegen clients against,
  with documented field semantics that don't shift between Tawon
  chart revisions. → `proto/eob/v1/service.proto`
- **Programmatic CRUD on every Tawon CRD**, with status surfacing
  and dry-run support. → `ResourceList` / `ResourceGet` /
  `ResourceApply` (with `dry_run`/`force`) / `ResourceDelete` /
  `ResourceSchema`.
- **Batched operations** so a Verizon rollout doesn't pay N × RTT
  per site for N directives. → `BatchApply` with per-item
  independent results.
- **Live state without polling.** Once provisioned, the controller
  needs to know when a directive transitions Ready→Stopped, or when
  an agent pod crashes, or when a Stream condition flips, **as it
  happens**. → `WatchResources` + `EventStream`.
- **Audit trail.** Every apply / delete the Verizon controller
  issued is observable on the same EventStream the controller is
  already consuming. → audit events published from
  `ResourceApply` / `ResourceDelete` / `BatchApply` into the
  in-process audit broker.

### What VZW gets back from this design

A single gRPC endpoint per EoB site that:

1. Speaks Verizon's preferred IPC (gRPC with proto).
2. Has predictable identity on every response (`ClusterRef`).
3. Survives Tawon chart changes — the proto is owned by us, not by
   the upstream chart, and the underlying CRD lookups are dynamic
   so a CRD field rename in Tawon does not break the API contract.
4. Doesn't make Verizon learn or operate the MCP protocol — the
   gRPC surface stands on its own.

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

- **`kubectl` + raw CRDs**: works for one site, makes a Verizon
  controller into a kubectl-shaped wrapper, doesn't help ATT
  federate across sites.
- **The Mantis dashboard**: human-only, single-site, not
  programmable.
- **Direct NATS JetStream connections**: works for the data plane
  but requires every consumer to know the per-site streamstore
  Service name, hostAliases workarounds, cluster.local quirks (see
  [`UPSTREAM-FIXES.md`](../../eob-xc-install/UPSTREAM-FIXES.md)
  for the laundry list). Effectively makes every consumer a
  platform integrator.
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
   contract. Two front doors translate: MCP (for LLM agents and the
   Inspector) and gRPC (for the customer-built controllers and
   aggregators above). Both delegate to the same in-process
   `service.Server`. New RPCs are added to the proto first; the
   front doors are thin and uniform.
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

A Verizon-internal controller looks roughly like this:

```
[VZW controller] ──gRPC──► eob-mcp:9443 (per EoB site)
       │
       ├── ResourceApply / BatchApply   (provision directives)
       ├── ResourceGet / ResourceList   (inspect state)
       ├── WatchResources               (react to status changes)
       ├── EventStream                  (audit + k8s events)
       └── ResourceSchema               (catalog of available CRDs)
```

No MCP involved. No Tawon-chart-version knowledge required. The
controller speaks our proto; we handle the rest.

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

| Item | VZW | ATT | Notes |
|---|---|---|---|
| **Phase 1h — cert provisioning** | needed | needed | mTLS hooks done; cert pipeline is the blocker. Becomes structural once ATT runs 10+ sites. |
| **Per-call authz / actor extraction** | needed | needed | Audit events have an `actor` field; today empty. mTLS subject → actor is the obvious next step. |
| **Pagination cursors** | low | needed | ATT's aggregator paging across many sites needs resumable reads. |
| **Capability negotiation RPC** | low | needed | Graceful degradation across version skew in a multi-site fleet. |
| **Logging-handler → ErrorCount24h** | low | medium | Heartbeat's `error_count_24h` is currently always 0. Wire to the slog handler. |
| **Phase 1f — claude-code `/mcp` bug** | nil | nil | Affects LLM-side dev loop only. Already filed-ready. |

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
