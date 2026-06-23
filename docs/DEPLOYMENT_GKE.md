# Deployment Guide — Shipping Kompass to GKE

**Companion to:** `SPEC.md`
**Goal:** Deploy the two-container Kompass app into your existing GKE cluster, in its own namespace, securely and repeatably.

> Do not deploy until Phase 8 (hardening) is green in-container. This guide assumes the images already passed `make test-container`.

Kompass runs as **one pod with two containers**: `kompass-core` (Python/FastAPI — the only container exposed to users) and `kompass-engine` (the Go cluster engine, bound to pod loopback, never exposed). All ingress terminates at `kompass-core`.

---

## 0. Prerequisites

- Existing GKE cluster (Standard or Autopilot) with `kubectl` access.
- `gcloud`, `kubectl`, `helm`, `docker` installed and authenticated.
- Permissions to push to Artifact Registry, create KMS keys, bind IAM, deploy workloads + NetworkPolicies.
- Workload Identity enabled (strongly recommended):
  ```bash
  gcloud container clusters describe experience-gke-qa --zone us-east4-a \
    --format="value(workloadIdentityConfig.workloadPool)"
  # If empty (schedule a maintenance window — disruptive on running clusters):
  gcloud container clusters update experience-gke-qa --zone us-east4-a \
    --workload-pool=experience-qa.svc.id.goog
  ```

Shared variables:
```bash
export PROJECT_ID="experience-qa"
export REGION="us-east4"
export CLUSTER_NAME="experience-gke-qa"
export ZONE="us-east4-a"
export NS="kompass"
export AR_REPO="experience-qa"
export CORE_IMAGE="$REGION-docker.pkg.dev/$PROJECT_ID/$AR_REPO/kompass-core"
export ENGINE_IMAGE="$REGION-docker.pkg.dev/$PROJECT_ID/$AR_REPO/kompass-engine"
export TAG="v0.1.0"
```

---

## 1. Build & push both images

```bash
gcloud artifacts repositories create "$AR_REPO" \
  --repository-format=docker --location="$REGION" \
  --description="Kompass images"
gcloud auth configure-docker "$REGION-docker.pkg.dev"

# Engine (Go, rebranded, near-upstream)
docker build -t "$ENGINE_IMAGE:$TAG" -f build/engine.Dockerfile .
# Core (Python/FastAPI + built React UI)
docker build -t "$CORE_IMAGE:$TAG"   -f build/core.Dockerfile .

# Scan before push (gate)
trivy image --severity HIGH,CRITICAL --exit-code 1 "$ENGINE_IMAGE:$TAG"
trivy image --severity HIGH,CRITICAL --exit-code 1 "$CORE_IMAGE:$TAG"

docker push "$ENGINE_IMAGE:$TAG"
docker push "$CORE_IMAGE:$TAG"
```

---

## 2. KMS key for envelope encryption

Encrypts kubeconfigs, provider API keys, and the OIDC client secret.

```bash
gcloud kms keyrings create kompass --location="$REGION"
gcloud kms keys create kompass-app-secrets \
  --location="$REGION" --keyring=kompass --purpose=encryption
```
Key resource name (used in app config):
```
projects/$PROJECT_ID/locations/$REGION/keyRings/kompass/cryptoKeys/kompass-app-secrets
```

---

## 3. Workload Identity (no static keys)

```bash
gcloud iam service-accounts create kompass-app --display-name="Kompass app"
export GSA="kompass-app@$PROJECT_ID.iam.gserviceaccount.com"

# KMS encrypt/decrypt on the app key only
gcloud kms keys add-iam-policy-binding kompass-app-secrets \
  --location="$REGION" --keyring=kompass \
  --member="serviceAccount:$GSA" \
  --role="roles/cloudkms.cryptoKeyEncrypterDecrypter"

# (If using Google Managed Prometheus) read access
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$GSA" --role="roles/monitoring.viewer"

kubectl create namespace "$NS"
kubectl create serviceaccount kompass-ksa -n "$NS"

gcloud iam service-accounts add-iam-policy-binding "$GSA" \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:$PROJECT_ID.svc.id.goog[$NS/kompass-ksa]"

kubectl annotate serviceaccount kompass-ksa -n "$NS" \
  iam.gke.io/gcp-service-account="$GSA"
```

---

## 4. In-cluster RBAC (least privilege)

The engine reads the **local** cluster; remote clusters are accessed via uploaded kubeconfigs (encrypted, decrypted in memory by core). Default the local binding to read-only.

```bash
kubectl create clusterrole kompass-readonly \
  --verb=get,list,watch \
  --resource=pods,nodes,services,deployments,replicasets,statefulsets,daemonsets,jobs,cronjobs,events,configmaps,namespaces,persistentvolumeclaims,persistentvolumes,horizontalpodautoscalers,ingresses

kubectl create clusterrolebinding kompass-readonly-binding \
  --clusterrole=kompass-readonly \
  --serviceaccount="$NS:kompass-ksa"
```
> Add a separate minimal write Role only if editors will apply actions to the **local** cluster (e.g. patch deployments for scale/restart). Remote-cluster writes are governed by the uploaded kubeconfig's own RBAC.

---

## 5. Persistence for the app DB

`kompass-core` owns a SQLite DB (users, audit, config) that must survive restarts.

```yaml
# pvc.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: kompass-data, namespace: kompass }
spec:
  accessModes: ["ReadWriteOnce"]
  resources: { requests: { storage: 5Gi } }
```
```bash
kubectl apply -f pvc.yaml
```
Schedule PVC snapshots (or a CronJob export) and document restore.

---

## 6. Deploy (Helm, two containers in one pod)

Representative `values.yaml`:
```yaml
core:
  image: { repository: $REGION-docker.pkg.dev/$PROJECT_ID/$AR_REPO/kompass-core, tag: v0.1.0 }
  resources: { requests: { cpu: "250m", memory: "256Mi" }, limits: { cpu: "1", memory: "1Gi" } }
engine:
  image: { repository: $REGION-docker.pkg.dev/$PROJECT_ID/$AR_REPO/kompass-engine, tag: v0.1.0 }
  bindAddress: "127.0.0.1"   # loopback only — never exposed
  resources: { requests: { cpu: "250m", memory: "512Mi" }, limits: { cpu: "1", memory: "1Gi" } }

serviceAccount: { create: false, name: kompass-ksa }
authMode: local              # local + optional Google SSO configured in-app
kms:
  keyName: projects/experience-qa/locations/us-east4/keyRings/kompass/cryptoKeys/kompass-app-secrets
persistence: { enabled: true, existingClaim: kompass-data }

securityContext:             # applied to BOTH containers
  runAsNonRoot: true
  runAsUser: 10001
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  seccompProfile: { type: RuntimeDefault }
  capabilities: { drop: ["ALL"] }

probes:
  liveness:  { path: /healthz, port: core }
  readiness: { path: /readyz,  port: core }
```
Only the **core** container is fronted by the Service; the engine listens on loopback inside the pod.

```bash
helm upgrade --install kompass ./deploy/helm/kompass -n "$NS" -f values.yaml
```

One-time initial admin password (printed once by core):
```bash
kubectl logs -n "$NS" deploy/kompass -c kompass-core | grep -A2 "INITIAL ADMIN CREDENTIALS"
```
Log in and change it immediately (the app forces this).

---

## 7. Ingress + TLS

Expose **core only**, internally, behind a stable internal IP with TLS.

### 7.1 Reserve a static internal IP (so the DNS name never breaks)

If you let GKE allocate the load-balancer IP, it can change when the LB is recreated and your DNS record breaks. Reserve a regional **internal** static IP first:

```bash
# Pick the subnet your users' network can route to (VPN/interconnect/same-VPC)
export KOMPASS_IP_NAME="kompass-internal-ip"
export KOMPASS_SUBNET="cluster-subnet-experience-vpc-qa"

gcloud compute addresses create "$KOMPASS_IP_NAME" \
  --region="$REGION" --subnet="$KOMPASS_SUBNET" --purpose=GCE_ENDPOINT

# Read back the assigned IP — you'll point DNS at this
gcloud compute addresses describe "$KOMPASS_IP_NAME" \
  --region="$REGION" --format="value(address)"
```

Then create **one A record** in your **private** DNS zone: `kompass.internal.example.com` → the IP above. (Private DNS, not public — this tool must not be internet-reachable.)

### 7.2 TLS for an internal hostname

The Google-managed-certificate annotation issues **public** certs and will **not** validate a private hostname. For an internal host, load your own / internal-CA certificate as a Kubernetes TLS secret instead:

```bash
kubectl create secret tls kompass-tls \
  --cert=path/to/kompass.crt --key=path/to/kompass.key -n "$NS"
```
(Or use cert-manager with an internal issuer and let it populate the secret.)

### 7.3 The Ingress (references the reserved IP + the TLS secret)

For GKE internal Ingress, the reserved address is referenced by **name** via the static-IP annotation. (If you front the Service with an internal LoadBalancer instead of Ingress, pin the same reserved IP with `networking.gke.io/internal-load-balancer-allow-global-access` / `loadBalancerIP` on the Service — pick one path, not both.)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kompass
  namespace: kompass
  annotations:
    kubernetes.io/ingress.class: "gce-internal"
    # Reference the reserved internal static IP BY NAME (value of $KOMPASS_IP_NAME):
    kubernetes.io/ingress.regional-static-ip-name: "kompass-internal-ip"
spec:
  tls:
    - hosts: ["kompass.qa.exp.com"]
      secretName: kompass-tls          # from §7.2
  rules:
    - host: kompass.qa.exp.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend: { service: { name: kompass, port: { number: 80 } } }
```

Replace `kompass-internal-ip` with your `$KOMPASS_IP_NAME` and `kompass.internal.example.com` with your real internal hostname. Prefer an internal LB. If you front it with IAP later, Google SSO inside the app still gives you per-user identity in the audit log.

### 7.4 Google SSO (OIDC) — configured in-app, not here
After deploy, an admin opens Settings → SSO and enters the Google OAuth client ID/secret (secret is KMS-encrypted), allowed hosted domain(s), and default role. Set the OAuth **redirect URI** to `https://kompass.qa.exp.com/api/auth/oidc/callback`. Local auth keeps working alongside SSO for gradual migration.

---

## 8. NetworkPolicy (lock down)

Allow core egress only to DNS, the K8s API, Prometheus/GMP, and approved provider endpoints; allow **no ingress to the engine** from outside the pod.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: { name: kompass-egress, namespace: kompass }
spec:
  podSelector: { matchLabels: { app: kompass } }
  policyTypes: ["Egress"]
  egress:
    - {}   # TEMP during bring-up — REPLACE before prod with explicit allow-list:
           # DNS; API server; Prometheus/GMP; resolved provider endpoint ranges.
```
Intra-pod loopback (core↔engine) needs no policy. Document the final egress allow-list before prod.

---

## 9. Post-deploy validation (run against real GKE)

1. Pod `Running`; both containers non-root, read-only FS; engine has no Service/Ingress.
2. Log in as admin; rotate password; create a viewer and an editor scoped to one cluster.
3. Verify the 403 matrix for one endpoint per role.
4. Add a second cluster via kubeconfig upload; confirm ciphertext-only at rest and switchable.
5. Nodes view: cross-check pods-per-node and summed requests vs `kubectl describe node`.
6. Configure a provider + key + model picker; confirm key masked and absent from logs.
7. Ask the AI a question; confirm streamed answer + model badge.
8. As editor, apply a `scale_deployment` proposal on the assigned cluster; confirm diff/confirm, then verify the audit row exists and predates the change.
9. Export audit as CSV and JSON.
10. Token/cost dashboard reconciles; set a low daily budget and confirm enforcement.
11. Enable Google SSO; log in with a Google account; confirm a Kompass user is created/mapped with the default role and appears in audit.
12. Confirm no user-facing screen references the upstream engine.

Promote to prod only after all pass on **non-prod** GKE.

---

## 10. Day-2 operations
- **Upgrades:** rebuild → scan → push new tags → `helm upgrade`. Periodically rebase the engine on upstream; re-run `make test-container` before deploy.
- **Backups:** verify PVC snapshots / scheduled exports; test restore quarterly.
- **Key rotation:** rotate the KMS key version; run the provided re-encrypt maintenance command.
- **Monitoring:** scrape core `/metrics`; alert on auth lockouts, provider errors, token-budget breaches.
- **Cost control:** review the dashboard; tune default and per-user daily budgets.