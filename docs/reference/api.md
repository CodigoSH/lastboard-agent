# API reference

The agent answers five calls and a health check. Everything it forwards, it forwards to Docker's own API and returns Docker's answer unchanged; everything else it refuses itself.

## The calls

| Method | Path | What it does | Query it keeps |
| :--- | :--- | :--- | :--- |
| `GET` | `/containers/json` | Lists containers, running and stopped. | `all` |
| `GET` | `/containers/{id}/stats` | Reads one container's CPU and memory, once. | none |
| `POST` | `/containers/{id}/start` | Starts a container. | none |
| `POST` | `/containers/{id}/stop` | Stops a container. | none |
| `POST` | `/containers/{id}/restart` | Restarts a container. | none |
| `GET` | `/health` | Says the agent is serving, and which version it is. | none |

`{id}` is a container ID or a container name.

Paths with a version in front, such as `/v1.41/containers/json`, are refused: call the paths exactly as written above.

## Authentication

Every request except `/health` carries the token:

```
Authorization: Bearer <your token>
```

The scheme has to be `Bearer`; its letter case does not matter. The token is compared in constant time. A missing or wrong token gets `401` on **every** route, the five calls and any other path alike, so a caller who has no token cannot tell which routes exist. Once a request passes, the token is removed before it is forwarded: Docker never sees it.

## The query string

Only the parameters Lastboard sends reach Docker; every other one is dropped before the request is forwarded.

- **The list** keeps `all` (include stopped containers). Anything else, for example `size=true`, is dropped.
- **Stats** are always a single reading: the agent sets `stream=false` whatever the caller sent, so a request for a stream still answers once and closes.
- **Start, stop and restart** keep nothing, so a timeout (`t=`) or a signal on a stop has no effect.

## A container ID or name

`{id}` has to look like a container ID or name: letters and digits first, then letters, digits, `_`, `.` or `-`, up to 128 characters. Anything else is answered `400` by the agent, before it reaches the socket.

## Status codes

| Code | Who answers | When |
| :--- | :--- | :--- |
| `200` | Docker | The list or a stats reading. |
| `204` | Docker | A start, stop or restart was done. |
| `304` | Docker | Nothing to do: starting a container that is running, or stopping one that is stopped. It is not an error. |
| `400` | the agent | The ID is not a plausible container ID or name. Body: `{"error":"invalid container id"}`. |
| `401` | the agent | No token, a wrong one, or a scheme other than `Bearer`. Body: `{"error":"unauthorized"}`. |
| `404` | the agent | A path that is not one of the calls above, with any method. The body is the plain text `404 page not found`. |
| `404` | Docker | The container does not exist; the body is Docker's own message. |
| `405` | the agent | One of the calls above, with the wrong method. The `Allow` header lists the right ones. |
| `502` | the agent | The agent cannot reach Docker. Body: `{"error":"docker unreachable"}`, and the reason is in the agent's log. |

`/health` is a different case: it answers `200` with `{"status":"ok","version":"…"}` without touching Docker, so it still answers when Docker is down.

## Trying it by hand

From the machine that runs Lastboard, with the token in `TOKEN`:

```bash
# The containers on that host
curl -H "Authorization: Bearer $TOKEN" "http://your-server:2375/containers/json?all=true"

# One reading of a container's CPU and memory
curl -H "Authorization: Bearer $TOKEN" http://your-server:2375/containers/my-app/stats

# Restart it
curl -X POST -H "Authorization: Bearer $TOKEN" http://your-server:2375/containers/my-app/restart
```

## What a leaked token can and cannot do

The agent is built so that losing the token costs as little as possible.

**With the token, someone can:**

- list the containers on that host. The answer is Docker's own, so it includes everything Docker returns for each container, labels among it;
- read the CPU and memory of any of them;
- start, stop or restart any of them.

**With the token, someone cannot:**

- create, delete or run a command in a container, or touch images, volumes or networks: those calls do not exist in the agent;
- hold a stats stream open, make the daemon measure every container on disk, or stop a container with a timeout of days: the query is cut down as described above;
- reach anything on the host outside Docker.

**What the agent does not do for you:**

- **Encrypt.** The token crosses the network as written. Keep the port on a network you trust, or behind a reverse proxy that terminates TLS.
- **Limit who can connect.** Restrict the port in your firewall to the machine that runs Lastboard.
- **Rely on `:ro`.** Mounting the Docker socket read-only does not limit what can be done through it, so it is not part of this protection. The allowlist is.
- **Pick a strong token.** Use `openssl rand -hex 32`. At start-up the agent warns, and only warns, when the token is shorter than 32 characters or is one of the examples from the documentation.

To report a vulnerability, follow the [security policy](https://git.codigosh.com/CodigoSH/lastboard-agent/src/branch/main/SECURITY.md).
