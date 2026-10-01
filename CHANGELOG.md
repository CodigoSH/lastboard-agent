# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.2.0] - 2026-10-01

### Changed
- The README's installation is Docker alone: the compose file, and the same thing as a single `docker run`
- A page for the agent on codigosh.com, and a mark and a showcase image of its own

### Removed
- The release binaries, `checksums.txt` and its Ed25519 signature. A release now carries its notes and nothing else; the agent is the image at `git.codigosh.com/codigosh/lastboard-agent`. Releases up to v1.1.1 keep their files, and anyone running a binary can switch to the image or build from source with Go

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
