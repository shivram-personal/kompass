# Kompass engine image — the near-upstream Go cluster-visibility engine.
#
# Build from the repo root:  docker build -f build/engine.Dockerfile -t kompass-engine .
#
# The engine binds to pod loopback by default (--host 127.0.0.1) and is never
# exposed outside the pod; kompass-core is the only reachable surface.

# ---------------------------------------------------------------------------
# Stage 1: build the frontend (embedded into the Go binary's static FS).
# ---------------------------------------------------------------------------
FROM node:20-alpine AS frontend-builder
WORKDIR /app
COPY package*.json ./
COPY web/package*.json ./web/
COPY packages/k8s-ui/package*.json ./packages/k8s-ui/
RUN npm ci --prefer-offline --no-audit
COPY web/ ./web/
COPY packages/k8s-ui/ ./packages/k8s-ui/
RUN npm run build --workspace=web

# ---------------------------------------------------------------------------
# Stage 2: build the Go engine.
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS backend-builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
COPY pkg/ pkg/
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=frontend-builder /app/web/dist internal/static/dist/

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-s -w -X main.version=${VERSION}" \
    -o /kompass-engine ./cmd/explorer

# ---------------------------------------------------------------------------
# Stage 3: minimal runtime.
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
LABEL org.opencontainers.image.title="Kompass Engine"
LABEL org.opencontainers.image.description="Kompass cluster-visibility engine (internal; reachable only via kompass-core)"
LABEL org.opencontainers.image.vendor="Kompass"

COPY --from=backend-builder /kompass-engine /kompass-engine

# Loopback bind is the binary default; pod-local only. No EXPOSE — the engine
# is intentionally not published as a container port.
USER nonroot:nonroot
ENTRYPOINT ["/kompass-engine"]
CMD ["--no-browser", "--host", "127.0.0.1", "--port", "9280"]
