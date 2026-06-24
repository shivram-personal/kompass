# Kompass core image — Python/FastAPI auth gateway that serves the rebranded
# UI and proxies /api/engine/* to the engine over pod loopback.
#
# Build from the repo root:  docker build -f build/core.Dockerfile -t kompass-core .
#
# core is the only container fronted by the Service; it listens on 0.0.0.0:8080.

# ---------------------------------------------------------------------------
# Stage 1: build the rebranded React UI (apiBase is /api/engine).
# ---------------------------------------------------------------------------
FROM node:20-alpine AS ui-builder
WORKDIR /app
COPY package*.json ./
COPY web/package*.json ./web/
COPY packages/k8s-ui/package*.json ./packages/k8s-ui/
RUN npm ci --prefer-offline --no-audit
COPY web/ ./web/
COPY packages/k8s-ui/ ./packages/k8s-ui/
RUN npm run build --workspace=web

# ---------------------------------------------------------------------------
# Stage 2: Python runtime.
# ---------------------------------------------------------------------------
FROM python:3.12-slim-bookworm AS runtime
LABEL org.opencontainers.image.title="Kompass"
LABEL org.opencontainers.image.description="Kompass — AI-augmented multi-cluster Kubernetes operations console"
LABEL org.opencontainers.image.vendor="Kompass"

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    KOMPASS_STATIC_DIR=/app/web

WORKDIR /app

COPY kompass_core/requirements.txt ./kompass_core/requirements.txt
COPY kompass_core/requirements-gcp.txt ./kompass_core/requirements-gcp.txt
# Runtime deps + the GCP Cloud KMS client (production envelope encryption).
RUN pip install --no-cache-dir -r kompass_core/requirements.txt \
    && pip install --no-cache-dir -r kompass_core/requirements-gcp.txt

COPY kompass_core/ ./kompass_core/
COPY --from=ui-builder /app/web/dist /app/web

# Non-root, owns nothing it shouldn't. Phase 8 layers read-only rootfs +
# dropped caps via the pod securityContext.
RUN useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin kompass \
    && mkdir -p /app/data && chown 10001:10001 /app/data
# SQLite app DB lives here (PVC-mounted in GKE). Owned by the runtime user.
ENV KOMPASS_DB_URL=sqlite:////app/data/kompass.db
USER 10001:10001
VOLUME ["/app/data"]

EXPOSE 8080
ENTRYPOINT ["python", "-m", "kompass_core"]
