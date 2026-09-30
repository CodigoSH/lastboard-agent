# syntax=docker/dockerfile:1

# ==========================================
# Stage 1: Build (Go, cross-compiled — no QEMU)
# ==========================================
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder

WORKDIR /app

COPY go.mod .
COPY main.go .

# Cross-compile for the target platform from the native builder arch.
ARG TARGETOS
ARG TARGETARCH
# The release tag, passed by the image workflow. A local build says "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /app/lastboard-agent .

# ==========================================
# Stage 2: Runtime (Alpine, no RUN = no QEMU)
# ==========================================
FROM alpine:3.24

COPY --from=builder /app/lastboard-agent /usr/local/bin/lastboard-agent

EXPOSE 2375

# /health needs no token and never touches the Docker socket, so this says
# whether the agent is serving — not whether Docker is. busybox wget ships
# with Alpine; PORT is read at run time, so an overridden port is followed.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-2375}/health" || exit 1

# Runs as root by default so it can read the Docker socket (root:docker 0660),
# matching the rest of the CodigoSH stack. The socket is the only thing the
# agent touches, it is mounted read-only, and the API is reduced to an
# allowlist — so this is the controlled surface, not a privileged free-for-all.
# To run unprivileged instead, add the host's docker GID via `group_add`
# (see docker-compose.yml).
ENTRYPOINT ["lastboard-agent"]
