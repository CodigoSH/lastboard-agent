**Podman as well as Docker: the disk answer read in both shapes, and the engine told apart**

### Added
- A tenth call, `GET /version`. It says which engine runs behind the agent (`docker` or `podman`) and its release, so the dashboard can name it. Nothing else of the daemon's answer leaves.

### Fixed
- The account of the engine's disk use is asked for with its records, which API 1.52 and later leave out by default.

### Changed
- The disk answer is read in both shapes engines use, and leaves as the same sizes and counts, without a single name, tag or path.
- The README and the reference say ten calls, and the Docker / Podman screen is called by its name.
