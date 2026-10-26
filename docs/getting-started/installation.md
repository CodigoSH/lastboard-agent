# Installation

The agent runs as a container next to Docker on the host whose containers you want to see in Lastboard. You need Docker on that host, and the agent's port (`2375` by default) reachable from the machine Lastboard runs on.

## With Docker Compose

Copy this `docker-compose.yml` to the remote host:

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

Three things to know before you start it:

- **`TOKEN`** is the shared secret Lastboard will use to authenticate. Replace the placeholder with a random one:

  ```bash
  openssl rand -hex 32
  ```

  The placeholder above is one of the values the agent recognises: it starts, but its log warns that anyone who has read this page can control your containers.
- **`ports`**: change the left side of `2375:2375` to publish a different port. The right side has to match `PORT` if you override it.
- **`volumes`**: `/var/run/docker.sock` is how the agent reaches Docker, and it is the only thing it touches. The `:ro` is good practice but **does not limit what can be done through the socket** — what limits it is the agent's own allowlist (see [API reference](../reference/api.md)).

Then start it:

```bash
docker compose up -d
```

## With `docker run`

The same thing in one command:

```bash
docker run -d --name lastboard-agent --restart unless-stopped -p 2375:2375 \
  -e TOKEN=your-secret-token \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  git.codigosh.com/codigosh/lastboard-agent:latest
```

## Image tags

The image is published for `linux/amd64` and `linux/arm64` at `git.codigosh.com/codigosh/lastboard-agent`:

| Tag | What it is |
| :--- | :--- |
| `latest` | The newest stable release. |
| `X.Y` | The newest release of one minor series, so it follows patch releases but never jumps to the next minor. |
| `X.Y.Z` or `vX.Y.Z` | One exact release. |

`latest` is what the examples use, and it is the right choice if you pull updates deliberately. Pin `X.Y.Z` if you want a host to stay exactly as it is until you change it.

## Settings

| Variable | Default | Description |
| :--- | :--- | :--- |
| `TOKEN` | *(required)* | The bearer token Lastboard sends on every request. The agent exits at start-up without it. |
| `PORT` | `2375` | The port the agent listens on inside the container. |
| `SOCKET_PATH` | `/var/run/docker.sock` | Where the Docker socket is, inside the container. |

## Check that it is running

The image has a Docker health check, so `docker ps` shows the container as `healthy` once the agent is serving. You can ask it yourself from the machine that runs Lastboard — `/health` needs no token:

```bash
curl http://your-server:2375/health
```

```json
{"status":"ok","version":"<the release you are running>"}
```

`/health` answers for the agent, not for Docker: it stays `ok` even if the agent cannot reach the socket. To see that, ask for the container list with the token (see [Troubleshooting](../help/troubleshooting.md)).

## Add it to Lastboard

In Lastboard, enter edit mode, add a **Docker** widget (or open an existing one), choose **Add New Host**, pick **Lastboard Agent** as its type, and enter the host and port, `your-server-ip:2375`, and the token. The [Docker widget guide](/docs/lastboard/widgets/docker/) walks through the screen and what each row shows.

## With Podman

Podman answers the same API through a socket of its own. With rootless Podman, turn the socket on once, as the user that runs your containers, and mount it where Docker's would be:

```bash
systemctl --user enable --now podman.socket

podman run -d --name lastboard-agent -p 2375:2375 \
  -e TOKEN=your_secure_random_token \
  -v $XDG_RUNTIME_DIR/podman/podman.sock:/var/run/docker.sock \
  --security-opt label=disable \
  git.codigosh.com/codigosh/lastboard-agent:latest
```

Without `--security-opt label=disable`, on a host where SELinux is enforcing, the agent cannot open the socket and every call that needs Docker answers `502` with `docker unreachable`. Leave the user as it is: in rootless Podman the container's root is your own user, the one that owns the socket, so it gives nothing away. The [Docker widget guide](/docs/lastboard/widgets/docker/#podman) has a Quadlet file to run it as a service.

## Behind a reverse proxy

The agent speaks plain HTTP, so on its own the token crosses the network in the clear. If the host is not on a network you trust, put the agent behind a reverse proxy that terminates TLS, and make sure the proxy forwards the `Authorization` header untouched: without it every request is unauthenticated and the agent answers `401`. In Lastboard, type the address with `https://` so the widget reaches it over HTTPS — the widget guide explains how.

## Running without root

The image does not define a user, so **the agent runs as root** inside its container. That is what lets it read the Docker socket, which is normally owned by `root` and the `docker` group.

To run it unprivileged, set a user **and** give that user the group that owns the socket. Find the group on the host:

```bash
stat -c %g /var/run/docker.sock
```

and add both to the compose file (`999` stands for the number you got):

```yaml
    user: "65534:65534"
    group_add:
      - "999"
```

Both are needed. `group_add` alone changes nothing, because the container is still root. And a non-root user without the group cannot read the socket: the agent starts and stays `healthy`, but every call that needs Docker answers `502`.

On a host where SELinux is enforcing, the container can be denied access to the socket even as root: [With Podman](#with-podman) has the option that allows it.

## Updating

The agent has no updater of its own. Pull the new image and recreate the container:

```bash
docker compose pull && docker compose up -d
```

It keeps nothing, so recreating it loses nothing. What changed in each version is on the [releases page](https://git.codigosh.com/CodigoSH/lastboard-agent/releases).

## Removing it

```bash
docker compose down
```

There is no data to clean up: the agent stores nothing.
