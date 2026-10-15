# Security policy

## Reporting a vulnerability

Email **admin@codigosh.com**. Please do not open a public issue for a vulnerability.

Include what you did, what happened and what you expected. If you have a proof of
concept, keep it to a test machine of your own.

## What the agent assumes

Lastboard Agent runs next to Docker on a host you own and answers one dashboard,
Lastboard, over HTTP. The Docker API is the keys to the host, so the design is a
small allowlist rather than a proxy:

- **Five calls and nothing else.** List containers, read a container's CPU and
  memory, and start, stop or restart one. Anything else is refused even with a
  valid token: no creating, deleting or `exec`-ing into containers, no images,
  volumes or networks.
- **The query string is allowlisted too.** Only `all` reaches the list, stats are
  always a single reading, and start, stop and restart take none, so a leaked
  token cannot hold a stats stream open, make the daemon size every container on
  disk, or stop one with a timeout of days.
- **A token on every request**, compared in constant time. On every route but
  `/health` a request without the right one gets `401` whatever it asks for, so a
  caller who has none cannot tell which routes exist. The token is removed before
  the request is forwarded: Docker never sees it.
- **A container ID that is not a plausible ID or name** stops at the agent with
  `400`, before anything reaches the socket.

What a leaked token gives away is the lifecycle of containers that already exist.
It cannot create, delete or enter one, and it cannot compromise the host.

## What it does not claim

- **A read-only mount is not the protection.** `:ro` on a Unix socket stops nothing
  from being written through it; it is in the examples as good practice. The limit
  is the allowlist above.
- **It runs as root in the container**, because it has to read the Docker socket.
  To run it unprivileged, set a user and give it the group that owns the socket, with
  `user` and `group_add` together: `group_add` alone leaves it root (see
  `docker-compose.yml`).
- **The port is yours to restrict.** Limit it in your firewall to the Lastboard host
  and use a strong random token (`openssl rand -hex 32`). The agent warns at startup
  when the token is shorter than 32 characters or is one of the examples from the
  documentation; it only warns.

## What it does not do

- It stores nothing and sends nothing anywhere: no database, no telemetry, no
  third party. It keeps no record of requests; its log carries its startup lines,
  the token warning when there is one, and a line when it cannot reach Docker.
- It uses only the Go standard library, and a test fails the day a module is
  required, so it is named in `NOTICE` before it ships.

## Updating

The agent has no updater of its own: it is distributed as a container image, and
updating is pulling the new image and recreating the container.

## Supported versions

Only the latest release receives fixes.
