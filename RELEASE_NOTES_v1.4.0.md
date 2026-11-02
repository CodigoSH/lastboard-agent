### Added
- **The size of the host**, `GET /info`: how many processors it has — and, where the kernel lists them, how many of those are real cores, not threads — and how much memory. Nothing else of what Docker says about its host leaves: not its name, its kernel or its registries.
- **How much disk Docker uses**, `GET /system/df`: the size of its images, containers, volumes and build cache, and how much of it nothing uses. Names, tags and paths are dropped. It can take several seconds on a host with a lot of data.

Lastboard 1.10 reads both for its Docker page: CPU and memory against the real size of each host, the cores and threads, and what Docker holds on disk. An agent from before these calls keeps working; Lastboard says it is too old for them.

### Changed
- The documentation says that an agent is added to Lastboard in its settings, and the Podman section links to Lastboard's own Podman guide.
