**The agent comes as a container image**

### Changed
- The README's installation is Docker alone: the compose file, and the same thing as a single `docker run`
- A page for the agent on codigosh.com, and a mark and a showcase image of its own

### Removed
- The release binaries, `checksums.txt` and its Ed25519 signature. A release now carries its notes and nothing else; the agent is the image at `git.codigosh.com/codigosh/lastboard-agent`. Releases up to v1.1.1 keep their files, and anyone running a binary can switch to the image or build from source with Go
