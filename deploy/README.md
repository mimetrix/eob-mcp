# Deploying eob-mcp

This is the first-deploy walkthrough for the F5 XC cluster (master-0
node). It covers build, push to the in-cluster registry, apply, and
smoke-test. Everything assumes you have `ssh xcuser@<master-0>` access
and that the EoB stack (operator, dashboard, streamstore, webhook,
agent DaemonSet) is already running in the `tawon-operator` namespace.

## Layout

- `k8s/eob-mcp.yaml` — single combined manifest: ServiceAccount,
  ClusterRole + ClusterRoleBinding, Deployment (1 replica, distroless
  nonroot), Service (ClusterIP :8443).
- Image is referenced as `quay.io/mantisnet/eob-mcp:dev` in the
  manifest. Every node mirrors `quay.io/mantisnet/*` to the in-cluster
  registry at `172.31.44.247:5000`, so pushing to that registry is
  sufficient — no manifest edit needed on each iteration.

## Edit before first apply

Open `k8s/eob-mcp.yaml` and update the three identity env vars to the
real XC site / tenant / region you want the fleet console to display:

```yaml
- name: EOB_SITE_ID
  value: "<your XC site name>"
- name: EOB_TENANT
  value: "<your XC tenant id>"
- name: EOB_REGION
  value: "<your XC region>"
```

These flow through to `cluster_identity` output verbatim.

## Build and push (on master-0)

```bash
# Sync the repo onto master-0 — adjust the local path to wherever
# you have eob-mcp checked out.
rsync -a --exclude=.git ./eob-mcp/ xcuser@master-0:~/eob-mcp/

ssh xcuser@master-0
cd ~/eob-mcp

# Build directly with the in-cluster registry tag so we only have one
# tag to manage. Distroless final image is ~15-20 MB.
podman build \
  --build-arg VERSION="$(git -C . describe --tags --always 2>/dev/null || echo dev)" \
  --build-arg COMMIT="$(git -C . rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t 172.31.44.247:5000/mantisnet/eob-mcp:dev \
  .

# Push to the in-cluster registry. The mirror config on all three
# nodes will resolve quay.io/mantisnet/eob-mcp:dev to this location
# at pull time.
podman push --tls-verify=false 172.31.44.247:5000/mantisnet/eob-mcp:dev
```

## Apply

```bash
# On master-0 (or wherever your KUBECONFIG points at the XC cluster):
kubectl apply -f ~/eob-mcp/deploy/k8s/eob-mcp.yaml

# Watch it come up.
kubectl -n tawon-operator get pods -l app.kubernetes.io/name=eob-mcp -w
```

The Deployment requests modest resources (50m CPU, 128 Mi memory) and
caps at 256 Mi — readonly root FS, drop-all capabilities, nonroot UID
65532. If the pod fails to start, the most likely culprit is a pull
failure: confirm the push succeeded and that the node's
`registries.conf.d` still rewrites `quay.io/mantisnet/*` to
`172.31.44.247:5000`.

## Smoke test (port-forward)

The Service is ClusterIP only; expose it locally with port-forward
rather than adding an Ingress for now.

```bash
kubectl -n tawon-operator port-forward svc/eob-mcp 8443:8443

# In another terminal:
curl -s http://localhost:8443/healthz
# -> ok

curl -s http://localhost:8443/readyz
# -> ready

curl -s http://localhost:8443/version | jq
# -> {"version":"...","commit":"...","date":"..."}
```

## MCP tool checks

The server speaks MCP over HTTP at `/mcp`. Two tools are registered:
`cluster_identity` and `eob_health`. The transport accepts JSON-RPC
2.0 requests; the snippets below post directly with `curl`.

```bash
# List tools.
curl -s -X POST http://localhost:8443/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq

# cluster_identity — expect non-empty k8s_version (e.g. v1.31.x) and
# eob_version derived from the tawon-operator image tag (e.g. "rc6").
curl -s -X POST http://localhost:8443/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"cluster_identity","arguments":{}}}' | jq

# eob_health — expect operator/dashboard/streamstore/webhook/agent
# to all report status:"ok" on a healthy cluster, plus an
# agents_per_node map with one entry per node where the DS landed.
curl -s -X POST http://localhost:8443/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"eob_health","arguments":{}}}' | jq
```

## Iteration loop

```bash
# After a code change:
podman build -t 172.31.44.247:5000/mantisnet/eob-mcp:dev .
podman push --tls-verify=false 172.31.44.247:5000/mantisnet/eob-mcp:dev
kubectl -n tawon-operator rollout restart deploy/eob-mcp
kubectl -n tawon-operator rollout status deploy/eob-mcp
```

Tag `:dev` is intentional during this phase; once we have CI publishing
versioned tags, the Deployment image will move to a pinned version
(e.g. `:0.1.0`) and rollouts will be driven by image tag bumps rather
than `rollout restart`.

## Teardown

```bash
kubectl delete -f ~/eob-mcp/deploy/k8s/eob-mcp.yaml
```

This removes the Deployment, Service, ServiceAccount, ClusterRole, and
ClusterRoleBinding. It leaves the `tawon-operator` namespace alone.
