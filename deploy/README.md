# Deploying eob-mcp

This is the first-deploy walkthrough for the F5 XC cluster (master-0
node). It covers build, push to the in-cluster registry, apply, and
smoke-test. Everything assumes you have `ssh xcuser@<master-0>` access
and that the EoB stack (operator, dashboard, streamstore, webhook,
agent DaemonSet) is already running in the `tawon-operator` namespace.

## Layout

- `k8s/eob-mcp.yaml` — single combined manifest: ServiceAccount,
  ClusterRole + ClusterRoleBinding, Deployment (1 replica, hostNetwork,
  distroless nonroot), Service (ClusterIP with two ports: `mcp` :8443
  and `grpc` :9443).
- Image is referenced as `quay.io/mantisnet/eob-mcp:dev` in the
  manifest. Every node mirrors `quay.io/mantisnet/*` to the in-cluster
  registry at `172.31.44.247:5000`, so pushing to that registry is
  sufficient — no manifest edit needed on each iteration.

The Pod binds `:18443` (MCP) and `:19443` (gRPC) on the node interface;
the Service publishes those as `:8443` and `:9443` respectively. Lower
ports are taken on the host by other XC processes.

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

The Service is ClusterIP only; expose both ports locally with
port-forward rather than adding an Ingress for now.

```bash
# In one terminal — forwards both MCP (:8443) and gRPC (:9443):
kubectl -n tawon-operator port-forward svc/eob-mcp 8443:8443 9443:9443
```

### HTTP/MCP

```bash
curl -s http://localhost:8443/healthz
# -> ok

curl -s http://localhost:8443/readyz
# -> ready

curl -s http://localhost:8443/version | jq
# -> {"version":"...","commit":"...","date":"..."}
```

### gRPC

The gRPC listener has reflection enabled, so `grpcurl` introspects the
service without needing the `.proto` files locally.

```bash
# List the registered services. Expect eob.v1.EoBService (plus the
# standard grpc.reflection / grpc.health services).
grpcurl -plaintext localhost:9443 list

# Call ClusterIdentity. Expect site_id / tenant / region populated from
# the env vars set on the Pod, plus k8s_version + eob_version from the
# live cluster.
grpcurl -plaintext localhost:9443 eob.v1.EoBService/ClusterIdentity

# Same data, end-to-end through the MCP wrapper:
curl -s -X POST http://localhost:8443/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"cluster_identity","arguments":{}}}' | jq
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
# eob_version pulled from the operator Deployment's
# `app.kubernetes.io/version` label (e.g. "v2.39.36-rc1"), with the
# operator's `manager` container image tag as fallback.
curl -s -X POST http://localhost:8443/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"cluster_identity","arguments":{}}}' | jq

# eob_health — expect operator/dashboard/streamstore to report ok on a
# healthy cluster. `webhook` reports kind=MutatingWebhookConfiguration
# (the cluster-scoped `eob-mutate` config installed by the chart);
# `agent` is the aggregate across per-directive DaemonSets matching the
# DirectiveLabelSelector (default `app.kubernetes.io/name=tawon-directive`)
# — status will be "absent" when no ClusterDirectives are deployed. The
# `directives` field is a per-DS breakdown; `agents_per_node` reports
# `{ready, total}` counts per node.
curl -s -X POST http://localhost:8443/mcp \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"eob_health","arguments":{}}}' | jq
```

## Enabling TLS (optional)

Both listeners are plaintext by default. To turn TLS on without a
manifest rewrite:

1. Create a TLS Secret in the `tawon-operator` namespace. cert-manager's
   `Certificate` resource produces this layout natively; alternatively
   apply directly:

   ```bash
   kubectl -n tawon-operator create secret generic eob-mcp-tls \
     --from-file=tls.crt=server.crt \
     --from-file=tls.key=server.key \
     --from-file=ca.crt=client-ca.crt    # only needed for mTLS
   ```

2. Edit `k8s/eob-mcp.yaml` and uncomment four blocks: the three
   `-tls-*` args, the `volumeMounts` block on the container, the
   `volumes` block on the Pod. Both listeners pick up the same cert.

3. `kubectl apply` and `rollout restart`. Verify with:

   ```bash
   # MCP over TLS
   curl -k https://localhost:8443/version

   # gRPC over TLS — drop -plaintext, optionally -insecure for self-signed
   grpcurl -insecure localhost:9443 list
   ```

mTLS path: once a client CA exists (e.g. signed by the aggregator's CA),
include `ca.crt` in the Secret. Step 2's `-tls-client-ca` flag flips
both listeners to `RequireAndVerifyClientCert`. Clients without a cert
signed by that CA fail at the TLS handshake.

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
