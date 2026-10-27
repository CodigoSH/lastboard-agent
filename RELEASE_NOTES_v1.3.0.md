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
