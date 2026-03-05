# Stage 1 — builder
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod .
COPY main.go .

RUN go build -o lastboard-agent -ldflags="-s -w" .

# Stage 2 — final
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/lastboard-agent /usr/local/bin/lastboard-agent

EXPOSE 2377

USER nobody

ENTRYPOINT ["lastboard-agent"]
