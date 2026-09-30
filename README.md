<div align="center">
  <br />
  <h1>Lastboard Agent</h1>
  <p><strong>Secure Docker bridge for Lastboard — monitor and control remote containers without exposing the Docker daemon.</strong></p>

  <p>
    <img src="https://img.shields.io/badge/dynamic/json?url=https://git.codigosh.com/api/v1/repos/CodigoSH/lastboard-agent/releases/latest&query=%24.tag_name&label=Latest&style=for-the-badge&color=blue" />
    &nbsp;
    <img src="https://img.shields.io/badge/License-Apache_2.0-green?style=for-the-badge" />
    &nbsp;
    <img src="https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white" />
    &nbsp;
    <img src="https://img.shields.io/badge/Docker-git.codigosh.com-2496ED?style=for-the-badge&logo=docker&logoColor=white" />
  </p>

  <br />
</div>

---

## What is this?

The Lastboard Agent runs on any remote Docker host as a lightweight container. It mounts the local Docker Unix socket and exposes a secure, token-authenticated HTTP API that Lastboard uses to monitor and control containers on that machine.

Instead of exposing the Docker daemon over TCP — which is inherently dangerous — Lastboard communicates only with the agent. The agent acts as a controlled, authenticated proxy: it forwards only the specific operations Lastboard needs and nothing more.

No database. No config files. Restart or replace it at any time — it's fully stateless.

---

## Requirements

- Docker installed on the remote host
- Port `2375` reachable from the Lastboard host (or any custom port you configure)

---

## Quick Start

### Docker Compose (recommended)

Copy the following `docker-compose.yml` to your remote host:

```yaml
services:
  lastboard-agent:
    image: git.codigosh.com/codigosh/lastboard-agent:latest
    container_name: lastboard-agent
    ports:
      - "2375:2375"
    environment:
      - TOKEN=change-this-to-a-secret-token
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    restart: unless-stopped
```

Three things to know before running:

- **`TOKEN`** — the shared secret Lastboard will use to authenticate. Generate a strong one:
  ```bash
  openssl rand -hex 32
  ```
- **`ports`** — change `2375:2375` if you need a different port. The right side must match `PORT` if you override it.
- **`volumes`** — `/var/run/docker.sock` must not be changed. This gives the agent access to the local Docker daemon.

Then start it:

```bash
docker compose up -d
```

### Docker Run

```bash
docker run -d \
  --name lastboard-agent \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -e TOKEN=your-secret-token \
  -p 2375:2375 \
  --restart unless-stopped \
  git.codigosh.com/codigosh/lastboard-agent:latest
```

### Binary (without a container)

Every release also publishes a static binary for `linux-amd64` and `linux-arm64`, for hosts where you would rather not run the agent in a container. Pick the version from the [releases page](https://git.codigosh.com/CodigoSH/lastboard-agent/releases), download it with its checksums, and verify before running:

```bash
VERSION=v1.0.7   # the release you want
ARCH=amd64       # or arm64
BASE=https://git.codigosh.com/CodigoSH/lastboard-agent/releases/download/$VERSION

curl -fsSLO "$BASE/lastboard-agent-linux-$ARCH.tar.gz"
curl -fsSLO "$BASE/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "lastboard-agent-linux-$ARCH.tar.gz"
```

Run it as a user that can read the Docker socket — root, or a member of the host's `docker` group — with the same environment variables as the container:

```bash
TOKEN=your-secret-token ./lastboard-agent
```

Keeping it running across reboots (a systemd unit, for example) is up to you; the container above does that for you with `restart: unless-stopped`.

---

## Connecting to Lastboard

1. Open your Lastboard dashboard
2. Add a **Docker** widget, or open the settings of an existing one
3. Click **Add Host**
4. Select type: **Lastboard Agent**
5. Enter the host address: `your-server-ip:2375`
6. Enter the token you chose
7. Click **Add** — your containers will appear immediately

The widget side of this is covered in the [Docker widget documentation](https://codigosh.com/docs/lastboard/widgets/docker/).

---

## Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `TOKEN` | *(required)* | Bearer token for authentication. Use a strong random value. |
| `PORT` | `2375` | Port the agent listens on. |
| `SOCKET_PATH` | `/var/run/docker.sock` | Path to the Docker Unix socket. |

---

## Security

- **Allowlisted by design** — the agent forwards only the exact Docker API calls Lastboard needs and rejects everything else with `404`, even with a valid token:

  | Method | Path | Operation |
  | :--- | :--- | :--- |
  | `GET` | `/containers/json` | List containers |
  | `GET` | `/containers/{id}/stats` | Read CPU / memory stats (one reading; streaming is not forwarded) |
  | `POST` | `/containers/{id}/start` | Start a container |
  | `POST` | `/containers/{id}/stop` | Stop a container |
  | `POST` | `/containers/{id}/restart` | Restart a container |

  Creating, deleting, `exec`-ing into containers, or any other Docker operation is **not reachable** through the agent. A leaked token can only list and control the lifecycle of existing containers — never compromise the host.

  Query parameters are allowlisted too: only `all` reaches the list, stats are always a single reading, and start, stop and restart take none. Anything else — `size=true`, a stop timeout, a signal — is dropped before the request is forwarded.
- **Bearer token on every request** — validated with constant-time comparison to prevent timing attacks. Unauthenticated requests get `401` without revealing which routes exist. The token is checked by the agent and removed before the request is forwarded; Docker never sees it.
- Use a strong random token (minimum 32 characters recommended: `openssl rand -hex 32`). The agent logs a warning at startup if the token is shorter than that or is one of the example values shown in the documentation.
- The Docker socket is mounted **read-only** and the container runs with no other access.
- For extra protection, restrict port `2375` in your firewall to allow connections only from your Lastboard host IP.
- The agent runs as `root` inside the container by default so it can read the Docker socket. To run unprivileged, grant only the host's docker group via `group_add` (see [docker-compose.yml](docker-compose.yml)).

---

## Health Check

The `/health` endpoint requires no authentication and can be used to verify the agent is running, and which version it is:

```bash
curl http://your-server:2375/health
```

```json
{"status":"ok","version":"v1.0.7"}
```

The image uses the same endpoint as its Docker `HEALTHCHECK`, so `docker ps` shows the agent as `healthy` once it is serving. It checks the agent, not the Docker daemon behind it.

---

## Building from Source

```bash
git clone https://git.codigosh.com/CodigoSH/lastboard-agent.git
cd lastboard-agent
go build -o lastboard-agent .
TOKEN=my-secret ./lastboard-agent
```

A build from source reports its version as `dev`. The release builds stamp the tag with `-ldflags "-X main.version=vX.Y.Z"`.

Requires Go 1.27.1+. No external dependencies.

---

## Support

We are an open-source initiative managed through [Open Collective Europe](https://opencollective.com/codigosh). If our tools are useful to you, an unconditional donation helps keep the infrastructure running.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).

---

A project by [CodigoSH](https://git.codigosh.com/CodigoSH) — built to last.
