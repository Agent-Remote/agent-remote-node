# Native daemon and Claude lifecycle acceptance

`tests/linux_skill_lifecycle_test.sh` builds the production Node and Helper executables and runs
the shipped systemd units inside an owned disposable systemd container. The test changes only
executable locations, fixture user/group and the pinned Claude path/port placeholder. It preserves
the units' filesystem restrictions, runtime directory ownership and privilege settings. The Worker
runs as UID 22000, the Helper as root and the Native account as a different UID 12345. The harness
checks actual daemon process UIDs. No host process, cgroup or account directory is mounted.

The paired Server test is `tests/test_skill_lifecycle_live.py`. It creates a fresh database/content
store, original identities, a legacy account and a pre-synchronized workspace. An installation is
accepted through authenticated HTTP before any task, managed directory or snapshot exists. Only
production polling creates takeover/deployment/start tasks and retained runtime authority. The
Server application uses its actual routes/authentication and request database sessions; application
background services remain disabled.

Production skill capability advertisement remains disabled. This harness adds only its test skill
capability to the original heartbeat before production authentication and transaction handling.
Independent HTTP requests remain concurrent. Actual backend availability is preserved, and the
fixture checks that the Node itself did not advertise skills. This is isolated lifecycle evidence.

Two separate opt-ins prevent a no-inference smoke test from being mistaken for learning acceptance:

- `AGENT_REMOTE_RUN_SKILL_DAEMON_TEST=1` runs one real Claude `--version` session. The actual local
  attach executable connects a pty, the process exits naturally and the independent Worker loops must
  confirm termination, transfer/publish the frozen content, obtain fresh reclamation authorization
  and durably remove redundant work/objects. It requires no Claude credentials or model inference.
- `AGENT_REMOTE_RUN_SKILL_LIFECYCLE_TEST=1` additionally runs two real Claude Haiku inference sessions.
  Each prompt selects the learning skill by name without supplying its path or instructions.
  The exact Claude session transcript must contain a matching successful `Skill` invocation;
  a direct file read or failed tool invocation cannot stand in for discovery. Transcripts are read
  privately with bounded parsing and never printed.
  The first writes an unpredictable fact into `learning/memory.txt`; the test requires clean
  publication and local reclamation. It stops and restarts both daemons, creates a new session and
  checks the fact in the new materialization before attaching. The second Claude must read that fact
  and copy it to a workspace witness file. The second prompt does not supply the fact. Both original
  Server snapshots/finalizations/publications must remain distinct, clean and retained.

From the sibling Server checkout, supply a real Linux executable for the Docker server architecture
and its SHA-256 from the official release manifest:

```sh
AGENT_REMOTE_RUN_SKILL_DAEMON_TEST=1 \
AGENT_REMOTE_TEST_CLAUDE_BINARY=/absolute/path/to/linux/claude \
AGENT_REMOTE_TEST_CLAUDE_SHA256=<verified-sha256> \
uv run pytest -q tests/test_skill_lifecycle_live.py -k runtime
```

For actual learning, replace the first switch with `AGENT_REMOTE_RUN_SKILL_LIFECYCLE_TEST=1`, provide
`AGENT_REMOTE_TEST_CLAUDE_CREDENTIALS=/absolute/private/path/to/.credentials.json`, and omit `-k runtime`.
Use a disposable Linux Claude credential file; the harness does not read macOS Keychain or export a
host login. `AGENT_REMOTE_TEST_NODE_REPO` selects an alternate Node checkout. Defaults skip both tests.

The runner verifies the artifact checksum, keeps credentials outside the image build context, mounts
only its private fixture directory read-only, copies login state with mode 0600 to the runtime account
and never prints tool output, daemon logs or configuration. Daemon restarts retain the same persistent
data. Owned containers/images, compiled binaries and private fixture copies are removed on exit.
Normal pytest temporary database/content retention still applies.

The fixture provisions an already synchronized workspace and an already authenticated account.
Session admission uses user HTTP and attach uses the production local runtime executable. It does
not certify CLI commands, forced-command SSH authorization, sync setup, account enrollment, Docker
Sandbox, kernel reboot or coordinated restore. Passing the runtime-only case is insufficient to
claim actual learning or next-session inheritance. An unexecuted opt-in is not acceptance evidence.

## Recorded runtime evidence

On 2026-09-25 the runtime-only case passed with real Claude Code 2.1.220, Linux ARM64,
Debian 12/systemd 252 and a private Docker cgroup namespace. The official artifact SHA-256 was
`159e4a51d796f3bf14677577100f7efb845611b1ceaf0c30cbd8d4650d942185`. The pty transcript contained the
actual Claude version, and the final assertions required a clean published capture, retained Server
snapshot, stopped session, durable reclamation receipt and absent redundant content/transient root.
The run took 20.98 seconds including build/container setup. The local environment required a temporary
apt HTTPS bootstrap using the host's public CA bundle, with TLS verification enabled; the repository
runner and runtime test bodies otherwise supplied the proof. No inference or login state was used.

The daemon test exposed and drove a Server fix: generic runtime reconciliation could interrupt a
managed session before its leased launch appeared. Server reconciliation now excludes sessions with
persisted skill snapshots and retains the dedicated original-snapshot-bound startup/termination
protocol. The new HTTP regression tests failed before the fix and passed afterward. The learning
opt-in is implemented but has not yet run with real credentials, so inheritance remains unverified.

The Server full run recorded 1722 passes, 74 skips and one failure in the pre-fix retention-clock
expectation, with 83.26% coverage. That run had loaded the old expectation before it was edited.
After correcting the expectation to preserve managed history, the final focused reconciliation,
retention and lifecycle-fixture run passed 23 tests with two PostgreSQL-only skips. No second full
Server run is claimed. Ruff, Mypy, docstring checks, Linux ARM64 Go compilation/vet and shell syntax
checks passed. The runtime acceptance itself passed separately with the final fixture ownership
and locale settings.

The separate CLI lifecycle runner uses the current host agent-remote/fclaude, real Mutagen,
registered isolated SSH credentials, production forced-command attach/sync, and shipped daemon
units. A deterministic synthetic tool exercises session writes and inheritance without model
credentials. Its contract includes local skill installation through CLI, actual workspace sync,
explicit stop and published status, a new independent session reading retained state, and deletion
without losing the saving operation. It does not establish real model inference or advertise support.

On 2026-09-25 this CLI case passed in 30.47 seconds with current host binaries, official Mutagen
0.18.1 and disposable Linux ARM64 daemons. The writer handles the supervisor's terminal shutdown
request and exits zero before capture; a signal-killed writer correctly produced detached unclean
state during fixture development. Checks include binary bytes, mode 0750, deleted source content,
an empty directory, a relative symlink, modified instructions, an account-local skill and both
managed directory aliases after restart. Account-effective selection remains model_loaded=false.
The Node verifier observes reclamation for both original snapshots before the host deletes the first
session and queries its retained publication operation. Capability setup is explicit test-only SQL.

Run from the Server checkout with `AGENT_REMOTE_RUN_SKILL_CLI_LIFECYCLE_TEST=1 uv run pytest -q
tests/test_skill_cli_lifecycle_live.py`. The optional `AGENT_REMOTE_TEST_MUTAGEN_ARCHIVE` points to
a cached official archive, still checked against the pinned platform digest. Per-run SSH keys,
device/user credentials, Mutagen daemon/data and containers are removed on exit.


On 2026-09-26, the learning acceptance was strengthened with exact-session successful Skill-tool
transcript evidence. Twenty-two parser cases and eight Linux transcript-selection cases pass,
but dedicated Linux credentials are still required to execute actual inference. The runner now
uses HTTPS APT with a public CA BuildKit secret and an immutable script snapshot. Network TLS
failures currently prevent fresh dependency downloads; cached Native dependency images were used
for the separately recorded runtime-only checks, with current binaries and the pinned real Claude
artifact. They do not establish inference or account enrollment. The full Server gate passed
1,817 tests with 97 skips and 83.04% coverage, including all formatting/type/docstring checks.
