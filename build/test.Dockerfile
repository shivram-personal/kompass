# Kompass containerized test gate (SPEC §11).
#
# Bundles every toolchain the phase gates need: Go 1.26+, Node 20+,
# Python 3.12+, kubectl, kind, Trivy, govulncheck, pip-audit, and the docker
# CLI (to drive the host daemon over a mounted socket for image builds + kind).
#
# Built and run by `make test-container`; it executes build/run-tests.sh.

FROM python:3.12-bookworm

ENV DEBIAN_FRONTEND=noninteractive \
    GOPATH=/root/go \
    PATH=/usr/local/go/bin:/root/go/bin:/usr/local/bin:$PATH

# Go 1.26 — copied wholesale from the official image (no clobber: separate dir).
COPY --from=golang:1.26-bookworm /usr/local/go /usr/local/go

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg git make bash jq \
    && rm -rf /var/lib/apt/lists/*

# Node 20 (NodeSource).
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Docker CLI (talks to the host daemon via the mounted /var/run/docker.sock).
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
        > /etc/apt/sources.list.d/docker.list \
    && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

# kubectl.
RUN curl -fsSLo /usr/local/bin/kubectl \
        "https://dl.k8s.io/release/v1.31.0/bin/linux/$(dpkg --print-architecture)/kubectl" \
    && chmod +x /usr/local/bin/kubectl

# kind.
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSLo /usr/local/bin/kind \
        "https://kind.sigs.k8s.io/dl/v0.24.0/kind-linux-${ARCH}" \
    && chmod +x /usr/local/bin/kind

# Trivy (official apt repo — robust across arches, no release-asset guessing).
RUN curl -fsSL https://aquasecurity.github.io/trivy-repo/deb/public.key \
        | gpg --dearmor -o /usr/share/keyrings/trivy.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/trivy.gpg] https://aquasecurity.github.io/trivy-repo/deb generic main" \
        > /etc/apt/sources.list.d/trivy.list \
    && apt-get update && apt-get install -y --no-install-recommends trivy \
    && rm -rf /var/lib/apt/lists/* \
    && trivy --version

# govulncheck + pip-audit.
RUN go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
RUN pip install --no-cache-dir pip-audit==2.7.3

WORKDIR /workspace
ENTRYPOINT ["bash", "build/run-tests.sh"]
