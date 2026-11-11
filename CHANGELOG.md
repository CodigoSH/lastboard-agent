# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.5.1] - 2026-11-11

### Security
- Built with Go 1.27.4, which fixes a crash of the HTTP/1 server caused by a race in the request body size limit (GO-2026-6859). The container image is built with it.

## [1.5.0] - 2026-11-02

### Added
- **Readings of CPU and memory without the wait.** A stats request may carry `one-shot=true`, and the agent now passes it on to Docker: the daemon then answers at once, instead of holding the reply for its next sample of a second. Only that exact value goes on — any other is dropped, and `stream=false` is still set whatever the caller sent, so a request can never open a stream.

Lastboard 1.10 asks for its readings this way: a host with fifty containers shows CPU and memory in about a second and not in ten. An agent from before this release keeps working; its readings come as they always did, a second for every eight containers. A daemon older than Docker 20.10 (API 1.41) does not know the parameter and answers as it always did.

## [1.4.0] - 2026-11-02

### Added
- **The size of the host**, `GET /info`: how many processors it has — and, where the kernel lists them, how many of those are real cores, not threads — and how much memory. Nothing else of what Docker says about its host leaves: not its name, its kernel or its registries.
- **How much disk Docker uses**, `GET /system/df`: the size of its images, containers, volumes and build cache, and how much of it nothing uses. Names, tags and paths are dropped. It can take several seconds on a host with a lot of data.

Lastboard 1.10 reads both for its Docker page: CPU and memory against the real size of each host, the cores and threads, and what Docker holds on disk. An agent from before these calls keeps working; Lastboard says it is too old for them.

### Changed
- The documentation says that an agent is added to Lastboard in its settings, and the Podman section links to Lastboard's own Podman guide.

## [1.3.0] - 2026-10-27

### Added
- **A container's details**, `GET /containers/{id}/json`: its image, state, health, ports, mounts, networks and restart policy. The environment leaves the host as **names only** — the values, often passwords, are taken out before the answer is sent, and an answer that cannot be read that way is not passed on.
- **A container's log**, `GET /containers/{id}/logs`, always as a batch: never a stream, 100 lines by default and 1000 at most, `since` only as a Unix time, and the answer cut at 4 MiB. Lastboard 1.10 uses both for its Docker page.
- How to run the agent with **rootless Podman**, in the installation guide.

### Changed
- Built with Go 1.27.3.
- The image's `latest` tag now goes only to the newest release.

### Fixed
- **`latest` pointed at v1.0.6 from 24 October 2026.** An old release was rebuilt by mistake and took the tag. If you pulled `git.codigosh.com/codigosh/lastboard-agent:latest` since then, pull it again: this release puts `latest` back where it belongs.
- Running the agent without root needs a user as well as the group, and the guide now says so.

## [1.2.0] - 2026-10-01

### Changed
- The README's installation is Docker alone: the compose file, and the same thing as a single `docker run`
- A page for the agent on codigosh.com, and a mark and a showcase image of its own

### Removed
- The release binaries, `checksums.txt` and its Ed25519 signature. None of the releases carries them any more, this one or the earlier ones: the agent is the image at `git.codigosh.com/codigosh/lastboard-agent`, and anyone running a binary can switch to the image or build from source with Go

## [1.1.1] - 2026-10-01

### Added
- `checksums.txt` is signed with Ed25519, and the README shows how to check the signature with OpenSSL before running a downloaded binary

## [1.1.0] - 2026-09-30

### Added
- The agent says which version it is, on `/health` and when it starts
- The image reports its own health, so `docker ps` shows the agent as healthy once it is serving
- A warning at startup when the token is too short, or is the example from the documentation
- Instructions for the release binaries: download, check against `checksums.txt`, and run without a container

### Changed
- Only the parameters Lastboard sends reach Docker, so a leaked token can no longer hold a stats stream open, make the daemon measure every container on disk, or stop one with a timeout of days
- The token stops at the agent and is no longer passed on to Docker
- Built with Go 1.27.1 on Alpine 3.24

## [1.0.6] - 2026-06-10

### Added
- Initial release of Lastboard Agent — Docker API proxy for the Lastboard dashboard

### Fixed
- Correct catch-all routing in Go ServeMux
- Make Docker API version transparent: forward client version to daemon
- Fix proxy memory leak for streaming endpoints

### Changed
- Change default port from 2377 to 2375
- Harden agent binary: strip symbols, trim paths, no CGO
- Migrate CI to self-hosted Forgejo Actions
