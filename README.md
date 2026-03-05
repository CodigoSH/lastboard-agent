<div align="center">
  <br />
  <h1>Lastboard Agent</h1>
  <p><strong>Secure Docker bridge for Lastboard — monitor and control remote containers without exposing the Docker daemon.</strong></p>

  <p>
    <img src="https://img.shields.io/badge/License-Apache_2.0-green?style=for-the-badge" />
    &nbsp;
    <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white" />
    &nbsp;
    <img src="https://img.shields.io/badge/Docker-ghcr.io-2496ED?style=for-the-badge&logo=docker&logoColor=white" />
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
- Port `2377` reachable from the Lastboard host (or any custom port you configure)

---

## Quick Start

### Docker Compose (recommended)

Copy the following `docker-compose.yml` to your remote host:

```yaml
services:
  lastboard-agent:
    image: ghcr.io/codigosh/lastboard-agent:latest
    container_name: lastboard-agent
    ports:
      - "2377:2377"
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
- **`ports`** — change `2377:2377` if you need a different port. The right side must match `PORT` if you override it.
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
  -p 2377:2377 \
  --restart unless-stopped \
  ghcr.io/codigosh/lastboard-agent:latest
```

---

## Connecting to Lastboard

1. Open your Lastboard dashboard
2. Add a **Docker** widget, or open the settings of an existing one
3. Click **Add Host**
4. Select type: **Lastboard Agent**
5. Enter the host address: `your-server-ip:2377`
6. Enter the token you chose
7. Click **Add** — your containers will appear immediately

---

## Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `TOKEN` | *(required)* | Bearer token for authentication. Use a strong random value. |
| `PORT` | `2377` | Port the agent listens on. |
| `SOCKET_PATH` | `/var/run/docker.sock` | Path to the Docker Unix socket. |

---

## Security

- The agent never exposes Docker directly — every request requires a valid Bearer token
- Use a strong random token (minimum 32 characters recommended: `openssl rand -hex 32`)
- Only the operations Lastboard needs are proxied: list containers, read stats, start, stop, restart
- For extra protection, restrict port `2377` in your firewall to allow connections only from your Lastboard host IP

---

## Health Check

The `/health` endpoint requires no authentication and can be used to verify the agent is running:

```bash
curl http://your-server:2377/health
```

```json
{"status":"ok"}
```

---

## Building from Source

```bash
git clone https://github.com/CodigoSH/lastboard-agent.git
cd lastboard-agent
go build -o lastboard-agent .
TOKEN=my-secret ./lastboard-agent
```

Requires Go 1.26+. No external dependencies.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).

---

<div align="center">
  <sub>Built with ❤️ by the CodigoSH Team</sub>
</div>
