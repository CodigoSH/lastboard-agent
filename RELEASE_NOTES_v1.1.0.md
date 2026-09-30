**Only what Lastboard asks for reaches Docker**

### Added
- The agent says which version it is, on `/health` and when it starts
- The image reports its own health, so `docker ps` shows the agent as healthy once it is serving
- A warning at startup when the token is too short, or is the example from the documentation
- Instructions for the release binaries: download, check against `checksums.txt`, and run without a container

### Changed
- Only the parameters Lastboard sends reach Docker, so a leaked token can no longer hold a stats stream open, make the daemon measure every container on disk, or stop one with a timeout of days
- The token stops at the agent and is no longer passed on to Docker
- Built with Go 1.27.1 on Alpine 3.24
