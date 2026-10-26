# Troubleshooting

Each entry is something you can see, what causes it, and what to do. The agent's own log is the first place to look: `docker logs lastboard-agent`.

## Lastboard says "Token rejected", or `curl` gets `401`

The token the agent was started with is not the one being sent, or it is being sent with a scheme other than `Bearer`. Check it from the machine that runs Lastboard:

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOKEN" http://your-server:2375/containers/json
```

`401` means the token does not match. Any other answer means it does: `200` is the list, and `502` is a token that works with an agent that cannot reach Docker (see below). If you changed `TOKEN`, recreate the container: the agent reads it only when it starts.

If the request goes through a reverse proxy, a proxy that does not forward the `Authorization` header sends the agent a request with no token at all, and the agent answers `401`. Compare a call through the proxy with one straight to the agent.

## Lastboard says the host is not responding

Lastboard cannot reach the agent at all. From the machine that runs Lastboard:

```bash
curl http://your-server:2375/health
```

If that does not answer, the problem is between the two machines and not in the agent: the container is not running (`docker ps`), the port is not published (the left side of `ports`), the port in Lastboard is not the one you published, or a firewall is in the way. If you changed `PORT`, the right side of `ports` has to be the same number.

## `/health` answers, but the containers do not load

The agent is serving but cannot talk to Docker, so every call that needs Docker answers `502` with `{"error":"docker unreachable"}`. `/health` never asks Docker, so it stays `ok` and the container stays `healthy`. The reason is in the log, on a line that starts with `docker proxy error`:

| The line ends with | It means |
| :--- | :--- |
| `connect: no such file or directory` | There is no socket at `SOCKET_PATH`. The volume is not mounted, or the path is not where your Docker keeps its socket. |
| `connect: permission denied` | The socket is there, but the user the agent runs as cannot read it. This is what a non-root user without the socket's group looks like: see [Running without root](../getting-started/installation.md). |

## The container stops as soon as it starts

The log says `TOKEN environment variable is required but not set`. The agent will not run without a token: set `TOKEN` and recreate the container.

## The log shows a WARNING about the token

There are two, and both only warn:

- `TOKEN is the example value from the documentation` — the token is one of the placeholders from the examples, so anyone who has read them can control your containers.
- `TOKEN is N characters long; use at least 32` — a short token is easier to guess.

Generate a real one with `openssl rand -hex 32`, set it in the container and in Lastboard, and recreate the container.

## A call returns `404` or `405`

The agent only answers the [seven calls](../reference/api.md) and `/health`. Docker's other calls — creating or deleting a container, running a command in one, images, volumes, networks — do not exist in the agent, on purpose, so they answer `404`. A call that exists with the wrong method answers `405`, and its `Allow` header lists the right ones. A path with a version in front, like `/v1.41/containers/json`, is a `404` too: leave the version out.

A `404` whose body is Docker's own message comes from Docker: the container you named does not exist.

## Starting or stopping answers `304`

That is not a failure. Docker answers `304` when the container is already in the state you asked for: starting one that is running, or stopping one that is stopped.

## The container is `unhealthy`

The health check asks the agent for `/health` on the port it listens on, and it follows `PORT`. Check `docker logs lastboard-agent`: if the agent exited or never started, the log says why.

## Still stuck

The [support page](https://support.codigosh.com/codigosh/lastboard-agent) is the place for questions and bug reports. Say which version you run (`/health` tells you) and what the agent's log says.
