# syntax=docker/dockerfile:1

# ==========================================
# Stage 1: Build (Go, cross-compiled — no QEMU)
# ==========================================
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod .
COPY main.go .

# Cross-compile for the target platform from the native builder arch.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /app/lastboard-agent .

# ==========================================
# Stage 2: Runtime (Alpine, no RUN = no QEMU)
# ==========================================
FROM alpine:3.21

COPY --from=builder /app/lastboard-agent /usr/local/bin/lastboard-agent

EXPOSE 2375

# Runs as root by default so it can read the Docker socket (root:docker 0660),
# matching the rest of the CodigoSH stack. The socket is the only thing the
# agent touches, it is mounted read-only, and the API is reduced to an
# allowlist — so this is the controlled surface, not a privileged free-for-all.
# To run unprivileged instead, add the host's docker GID via `group_add`
# (see docker-compose.yml).
ENTRYPOINT ["lastboard-agent"]
