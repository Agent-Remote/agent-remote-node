# Native first-use acceptance proof

## Release acceptance scheduling

For the 2026-09-27 release, the user deferred real Linux and genuine Docker Sandbox acceptance
until after publication with their assistance. The Linux Skill copy, configuration-import, mount
and systemd runners are therefore in the manually triggered `skill-runtime-acceptance.yml`
workflow. Ordinary CI and release checks continue to run. A skipped/manual workflow is not passing
acceptance evidence. The observed GitHub runner rejected non-root Bubblewrap UID mapping; that
environment prerequisite must be resolved before claiming successful mount acceptance. This
workflow does not substitute for genuine Docker Sandbox or actual model acceptance.


The separate [daemon lifecycle harness](skill-lifecycle-acceptance.md) builds the formal Worker and
Helper binaries and preserves the shipped systemd privilege settings. Its runtime-only and real
Claude learning opt-ins have separate evidence requirements; they do not change this older proof's
scope or enable production capability advertisement.

The opt-in `TestFirstUseWorkerLiveServer` uses a disposable authenticated Server fixture containing
only URL, Node token, Node ID, accepted operation ID and the expected response-loss mode. The Server
must have accepted a pending library installation for a legacy Native account. No takeover, task,
poll lease, managed directory, Helper fence, capture or deployment input may be fixture-created.

`tests/linux_skill_first_use_test.sh` runs the worker test inside an owned systemd container with a
private cgroup namespace. Only the disposable read-only credential fixture is mounted. The Helper
uses the unchanged default storage policy on the container disk; systemd's small tmpfs is not a
substitute for the persistent state filesystem. No space check is disabled for the proof. The Helper
and Worker use ordinary authenticated polling, capture/file transfer and result confirmation.
A real transient service leaves a child writing the old skills directory after its parent exits.
The first capture must wait without killing the child; after natural exit, the capture must retain
its late write. The Helper's first fence and immutable capture are created by production code.

The same original task recovers normally or after a deliberately lost initial-publication response.
Committed takeover replay runs with the Helper stopped and changed old source bytes. The next poll
must produce the resolved deployment with both installed and manual sources and the original late
write. A restarted Helper prepares that input and the dedicated result becomes durable. No session
or effective-use observation is invented by deployment readiness.

This proves the initial Native capture-to-deployment path with real systemd writer inspection, not
Claude startup, Docker/sbx support or runtime capability advertisement. The test capability report
remains an explicit fixture until complete backend acceptance passes.

## Reproduce from the repositories

Keep the Server and Node checkouts beside each other, with Docker, Go and `uv` available. From
`agent-remote-server`, run:

```sh
AGENT_REMOTE_RUN_SKILL_FIRST_USE_TEST=1 uv run pytest -q tests/test_skill_first_use_live.py
```

Use `AGENT_REMOTE_TEST_NODE_REPO=/absolute/path/to/agent-remote-node` for a different checkout layout.
The tests are skipped by default. The opt-in creates an owned privileged systemd container with a
private cgroup namespace; it does not use host process/cgroup mounts. Docker must support this mode.
The harness maps `host.docker.internal` to the Docker host gateway, including on Linux.

The Server test uses a fresh fixture database/content store, real user and Node authentication and
production HTTP routes. Only request database connections are replaced; the application background
loops are disabled. No real deployment credentials are read. The fixture creates a legacy account,
a compatible test capability report and an uploaded library package. Installation is accepted over
HTTP before any task, takeover or managed directory exists. The test checks original-operation
readiness/replay, immutable accepted digest, sealed discovery, exact task/result counts and absence
of session/snapshot/effective-use records after the Linux proof.

The suite covers both normal completion and a response dropped after the actual takeover commit.
Private plaintext connection fixtures, owned container/image and local test binary are cleaned up;
the usual pytest temporary database/content retention applies. The Server listens on a temporary
host port so its isolated container can reach it. This does not run the complete Node daemon or its
heartbeat loop. Worker and Helper run in a root test process in this proof; independent nonroot
protocol tests supply separate privilege-boundary evidence.
