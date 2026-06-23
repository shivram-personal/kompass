# Diagnose — network path diagnostics

The **Diagnose** tab in the resource detail view (and the matching MCP `diagnose` tool) answers one question for a network entry kind:

> *If traffic is sent toward this resource, does it reach a healthy process — and if not, which hop is the first to break?*

The trace has two layers:

1. **Static** — is the path wired correctly in config + current pod state? Pure functions over the in-memory informer cache, no per-call API requests. Always on.
2. **Active reachability test** (optional, one-shot) — send DNS / TCP / TLS / HTTP probes along the declared path and report what came back. Runs only when the operator clicks **Run test**.

The active layer can escalate the static verdict when probes give clear evidence of a real failure on a hop (every non-skipped probe failed → that hop counts toward broken; over half failed → counts toward degraded). It never softens a static verdict: a critical static finding outranks probe state, and an unverifiable path stays unverifiable.

Probes run from the operator's current vantage (laptop or in-cluster). A failure caused by the vantage itself — NetworkPolicy blocking Radar's path, a service-mesh mTLS handshake without a client cert — surfaces as a probe failure and can escalate the verdict the same way a real failure would. See "What it deliberately does NOT do" below for the boundaries of what probes can and cannot tell you.

## Mental model

```
   ┌─────────────────────────┐
   │       Upstreams         │   ← parallel hops INTO the subject
   │  Ingress · HTTPRoute    │     (each judged independently)
   └────────────┬────────────┘
                │
   ┌────────────▼────────────┐
   │         Subject          │   ← the resource you opened
   └────────────┬────────────┘
                │
   ┌────────────▼────────────┐
   │       Downstream         │   ← the chain FROM the subject
   │   Service → Pods         │     (the broken hop indexes here)
   └─────────────────────────┘
```

**Upstreams are parallel.** A Service that's reached by both `ingress-a` and `ingress-b` does not become broken when only one of them fails — the other still delivers traffic. The verdict only degrades to *broken* if every upstream is broken, *degraded* if some are.

**Downstream is a chain.** The first critical finding along Downstream is the broken hop, named `brokenAt`. Findings on later hops are still shown but the diagnosis starts at the first failure.

## Supported entry kinds

| Kind | Downstream chain | Upstreams |
|------|------------------|-----------|
| **Service** | Service → selected pods | every Ingress / HTTPRoute / GRPCRoute referencing this Service |
| **Ingress** | Ingress → backend Services → pods of the first backend | none (external entry) |
| **HTTPRoute / GRPCRoute** | Route → backend Services → pods of the first backend | every parent `Gateway` from `parentRefs` |
| **Gateway** | Gateway → attached routes (capped at 20) | none |

Other network surfaces in the topology (Traefik IngressRoute, Istio VirtualService, Knative) are deliberately out of scope today — they would each need their own resolution logic and aren't reached by the same `parentRefs` / `backendRefs` shape.

## What each hop carries

Every hop has:

- A **resource reference** (kind, namespace, name) and an **edge label** (e.g. `HTTPRoute->Service`) so the path reads top-to-bottom in the traffic direction.
- **Findings** — the detections that already exist in the issues pipeline, attached to the hop where the failure is observable. The Phase 0 additions to this pipeline:
  - **`gwroute:backend-port-mismatch`** — an HTTPRoute / GRPCRoute references a Service port that doesn't exist; the message names the Service's actual ports for actionability.
  - **Gateway-API route parent conditions** (`Accepted=False`, `ResolvedRefs=False`, `Programmed=False`) — read directly from `status.parents[]` so the controller's own verdict is the source of truth.
  - **`svc:probe-port-mismatch`** — a selected pod's readiness probe targets a port the Service does not route to, which explains why a Service may show no ready endpoints even when processes are listening.
- **Meta** — pod counts (`selected` / `ready`), `endpointSource: pod-readiness`, `headless`, and `selectorless` flags so the UI can render the right shape without re-deriving them.

For each finding the trace populates a **kubectl reproducer command** — a one-liner the operator can paste to see the raw state behind the finding. Examples:

```bash
kubectl describe service api -n prod
kubectl get pods -n prod -l app=api
kubectl get httproute api-route -n prod -o jsonpath='{.status.parents}'
```

## Reachability test (active probes)

The **Run test** button under the verdict fires one round of probes against the declared path:

| Hop | What runs |
|-----|-----------|
| Ingress / Gateway hostname | DNS → TCP → TLS (if HTTPS) → HTTP |
| Service | Direct TCP to ClusterIP:port (in-cluster), or HTTP via the K8s API server's `/services/{name}:{port}/proxy/` subresource (from a laptop) |
| Pods | Direct TCP to PodIP:port (in-cluster) or HTTP via `/pods/{name}/proxy/` (from a laptop) for up to 3 sampled ready pods; the remaining pods get a "sampled N of M" skip row |
| HTTPRoute / GRPCRoute | Skipped — routes have no own routable address; reachability is the upstream Gateway + downstream Service |

Each row reports outcome (`ok` / `fail` / `skipped`), latency, the path it traversed (`pod-to-pod path` or `via Kubernetes API`), and an HTTP status detail when available. The total budget is 3 seconds; per-hop runs in parallel within that envelope. Probes are an action, not a polling state; the button fires once, results land, the next static refetch replaces them.

**Vantage** is detected automatically (`KUBERNETES_SERVICE_HOST` set ⇒ in-cluster). The UI does not surface vantage as a primary concept — the same button works from both, and the per-row footnote names the primitive used so power users can interpret asymmetric results.

**RBAC for active probes (from a laptop):** the user identity reading the trace must hold `get services/proxy` and `get pods/proxy` in the target namespace. In-cluster Radar uses the data path directly and doesn't need these. To disable the active layer entirely, deny those permissions to the role Radar uses.

## What it deliberately does NOT do

- **No EndpointSlice reads.** The endpoint signal is pod-readiness; the trace marks this with `endpointSource: pod-readiness` so the approximation is honest.
- **No NetworkPolicy rule evaluation.** When a NetworkPolicy selects the subject's pods, the trace says "N NetworkPolicies select these pods; traffic may be restricted (rules not evaluated)" — anything more would force us to model CNI behavior and we'd be wrong some of the time.
- **No external-path probing** for Service type LoadBalancer / NodePort / ExternalName — we cover the in-cluster path; the external path requires modelling cloud LB state honestly and is intentionally out of scope today.
- **No new CRDs.** Everything reads from the same informer cache the rest of Radar uses.

These are not gaps to fix soon — they are the line that keeps the feature trustworthy in clusters Radar cannot fully see.

## Verdict semantics

| Verdict | When |
|---------|------|
| **healthy** | No findings on any hop |
| **degraded** | Warnings somewhere, or some-but-not-all upstreams broken |
| **broken** | A critical finding on any Downstream hop, OR every upstream is broken |
| **unknown** | The subject resource isn't in the cache, RBAC denies the namespace, the relevant API isn't installed (Gateway API on a cluster without it), or the informer cache hasn't completed initial sync |

The UI shows the verdict at the top of the panel with a one-sentence reason for `degraded` / `broken` / `unknown`. Operators should treat *unknown* as a pause-and-investigate signal — it means the trace can't honestly answer the question, not that everything is fine.

## MCP

The `diagnose` MCP tool returns a `trace` field for these kinds instead of the pod-log fan-out it does for workloads. An agent that calls `diagnose(kind=service, ...)` gets the path-shaped answer in one call, along with `relatedIssues` for raw-issue follow-up. Pass `probe: true` to add the active reachability test to the response.

## Performance

**Static trace:**

- No per-call API requests; pure functions over the in-memory informer cache
- Linear in path length + selector match counts, which are bounded by the cache contents
- Target <100ms typical, <300ms on a 200-pod namespace
- Five-second polling from the UI while the Diagnose tab is open; data is cached client-side so retabbing within the drawer is instant

**Active reachability test:**

- Budgeted at 3 seconds total; per-hop probes run in parallel within that envelope
- Each layer respects a strict per-call timeout (DNS 250ms, TCP 700ms, TLS 1s, HTTP 1s) so a single dead hop can't starve the rest
- Triggered by an explicit operator click; no polling, no sticky on-state
