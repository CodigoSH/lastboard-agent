# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
