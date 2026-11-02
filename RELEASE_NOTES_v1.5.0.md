### Added
- **Readings of CPU and memory without the wait.** A stats request may carry `one-shot=true`, and the agent now passes it on to Docker: the daemon then answers at once, instead of holding the reply for its next sample of a second. Only that exact value goes on — any other is dropped, and `stream=false` is still set whatever the caller sent, so a request can never open a stream.

Lastboard 1.10 asks for its readings this way: a host with fifty containers shows CPU and memory in about a second and not in ten. An agent from before this release keeps working; its readings come as they always did, a second for every eight containers. A daemon older than Docker 20.10 (API 1.41) does not know the parameter and answers as it always did.
