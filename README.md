# agent-remote-node

<p align="center"><img src="assets/agent-remote-icon.svg" alt="Agent Remote icon" width="80" height="80"></p>

<p align="center">
  <a href="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://codecov.io/gh/Agent-Remote/agent-remote-node"><img alt="Codecov" src="https://codecov.io/gh/Agent-Remote/agent-remote-node/graph/badge.svg"></a>
  <a href="https://github.com/Agent-Remote/agent-remote-node/stargazers"><img alt="GitHub Stars" src="https://img.shields.io/github/stars/Agent-Remote/agent-remote-node?style=flat&logo=github"></a>
  <img alt="Go 1.26.6" src="https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white">
  <a href="LICENSE"><img alt="License: GPL-3.0" src="https://img.shields.io/github/license/Agent-Remote/agent-remote-node"></a>
</p>

English | [中文](README.zh-CN.md)

Node-side runtime for agent-remote.

The node runs on a VPS and talks to `agent-remote-server` by polling the control plane. It does not expose public HTTP ports.

## Commands

```sh
go test ./...
```

```sh
go run ./cmd/agent-remote-node --help
```

Run managed enrollment from a logged-in control workstation:

```sh
agent-remote node install --node <node-id-or-prefix>
```

The CLI authenticates the pinned Node archive with its checksum and Sigstore bundle, transfers and
installs it over a dedicated SSH stdin, and only then issues a short-lived join code over a separate
SSH invocation. New Nodes keep ego-browser disabled unless the administrator explicitly adds
`--enable-ego-browser`; reinstalling an existing Node preserves its current setting.

Direct `register --registration-token` is an advanced legacy compatibility path, not the normal
enrollment flow:

```sh
go run ./cmd/agent-remote-node register \
  --config ./config.json \
  --server-url http://localhost:8000 \
  --node-id <node-id> \
  --registration-token <registration-token>
```

```sh
go run ./cmd/agent-remote-node heartbeat --config ./config.json
```

```sh
go run ./cmd/agent-remote-node poll-once --config ./config.json
```

```sh
go run ./cmd/agent-remote-node run --config ./config.json
```

```sh
go run ./cmd/agent-remote-node install-ssh --config ./config.json
```

```sh
go run ./cmd/agent-remote-attach --config ./config.json --session <session-id> --device <device-id> --dry-run
go run ./cmd/agent-remote-attach --config ./config.json --binding <tool-account-id> --device <device-id> --dry-run
```

`install-ssh` prepares the managed `authorized_keys` file. Runtime SSH keys are written atomically by the `sync_ssh_keys` node task with forced-command restrictions. Each task replaces only the named device's managed keys, preserving other devices while removing stale keys after rotation.

`prepare_workspace` tasks install the device's stable SSH gateway key and ask the privileged runtime helper to create the workspace as the control-plane user's Linux UID. Mutagen commands are re-authorized by device and node, then run without network access in a Bubblewrap view containing only that user's data.

`create_binding_session` and `create_tool_session` use the backend pinned by the control plane. Native sessions run the managed Claude binary under a per-user UID with systemd cgroup limits, Bubblewrap filesystem isolation, a dedicated network namespace, nftables egress filtering, a quota-limited temporary filesystem, and a per-session tmux socket. Docker Sandbox sessions run as the Node service's fixed non-root UID/GID, with owned account/workspace paths and numeric ACL access. Docker lifecycle state is recorded in a root-owned trusted spec, and Docker operations remain behind the privileged helper; the Node worker is not a member of the Docker group.

Developer credential profiles provide persistent Git and GitHub CLI configuration directories for both runtime backends. SSH private keys remain on the client: only an authorized `ssh -A` attach is bridged through a session-local Unix socket, and only while that connection is active. Native and Docker Sandbox attach both resolve the tmux target and SSH mode from root-owned runtime state instead of control-plane resource names. The gateway continues to deny TCP forwarding, X11 forwarding, and user SSH rc execution.

Session port forwarding uses a separate no-PTY forced command and does not enable OpenSSH TCP forwarding. After redeeming a device- and SSH-key-bound one-time token, one HTTP/2 tunnel carries CONNECT streams for exactly one authorized runtime loopback port. The privileged Runtime Helper resolves either the Native network namespace or the root-owned Docker Sandbox spec and returns only an already-connected socket FD over `SCM_RIGHTS`; clients cannot provide a host, IP, PID, namespace path, sandbox name, or container ID. Heartbeats advertise this capability for every healthy enabled backend.

Managed macOS device control advertises the required `observation_mode_v2`,
`ax_state_v2`, and `adaptive_settle_v2` base plus the optional
`clipboard_payload_v2` extension only when at least one enabled runtime backend and the verified device
proxy are available. The advertised backend list is exact: Native additionally requires its network
namespace probe, while Docker Sandbox requires its full runtime probe. The Server selects the complete required base, includes
supported extensions, or uses an empty v1 fallback; partial sets and
same-generation capability changes are rejected. New generations use the
supported v2 set by default, while the Server emergency switch forces the empty
v1 set. Runtime Helper writes
the negotiated set into the owner-only managed context, starts the proxy with the
four-tool compact MCP surface, and fixes zero-content optimization metrics at
`/tmp/agent-remote-device-optimization.jsonl` inside the isolated session.

Account binding on either runtime backend requires a registered device token and an active SSH key. Binding attach uses the same forced-command gateway as normal sessions, is re-authorized by the control plane on every connection, and reaches Docker tmux only through the trusted helper.

`create_browser_session` node tasks start a temporary Kasm Chrome container by default. The browser runtime receives timezone, locale, launch URL, incognito Chrome arguments, and a temporary VNC password. It does not mount workspace or tool-account directories. `stop_browser_session` removes the container and the temporary profile directory under `browser_root`.

## Config

The immutable wrapper/Skill source contract is documented in
`docs/ego-browser-artifacts.md`; runtime UID/ACL diagnostics, metrics, and
recovery are in `docs/ego-browser-operations.md`. Eligible Claude tool sessions on both
Native and Docker Sandbox receive a runtime-scoped broker capability. Docker startup verifies and
mounts the release-pinned artifacts, refreshes the exact managed Skill tree in the account, prepends
the wrapper directory to `PATH`, and passes the nonce only through the process environment.

The advanced compatibility `register` command writes the node token to the configured JSON file:

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "",
  "supported_tool_types": ["claude"],
  "heartbeat_interval_seconds": 30,
  "poll_interval_seconds": 5,
  "ledger_path": "./agent-remote-node-ledger.json",
  "ssh_authorized_keys_path": "./authorized_keys.agent-remote",
  "attach_binary_path": "agent-remote-attach",
  "workspace_root": "/var/lib/agent-remote/users",
  "account_root": "/var/lib/agent-remote/users",
  "skill_state_root": "/var/lib/agent-remote-skill-state",
  "skill_state_policy": {
    "checkpoint_bytes": 1073741824,
    "directory_bytes": 10737418240,
    "entries": 100000,
    "minimum_free_bytes": 2147483648,
    "reserve_percent": 5
  },
  "docker_binary_path": "docker",
  "tmux_binary_path": "tmux",
  "mutagen_binary_path": "mutagen",
  "browser_root": "/var/lib/agent-remote/browser-sessions",
  "browser_image": "kasmweb/chrome:1.18.0",
  "browser_public_base_url": "",
  "browser_docker_network": "",
  "allowed_runtime_backends": ["docker_sandbox", "native"],
  "runtime_socket_path": "/run/agent-remote/runtime.sock",
  "runtime_binary_path": "/usr/local/bin/agent-remote-runtime",
  "claude_runtime_path": "/opt/agent-remote/runtimes/claude/current/bin/claude",
  "device_proxy_path": "/opt/agent-remote/device/current/bin/agent-remote-device-proxy"
}
```

The config file contains node credentials and must be stored with deployment-level file permissions.

`skill_state_root` stores durable skill copies, journals, account fences and import receipts. The Linux root helper requires
an independent root-owned `0700` directory with safe root-owned ancestors, outside runtime, account,
workspace, browser and broker roots. Do not put it beneath the installer’s worker-owned data directory.
Omitted configuration uses the values above; an explicit policy must supply every field. Byte limits
use bytes, `entries` counts manifest entries, and startup reserve is the greater of
`minimum_free_bytes` and `reserve_percent` of filesystem capacity. These settings and local storage
primitives do not enable managed sessions: backend mounts, authenticated transfer and Server
finalization must be integrated and verified before a backend advertises skill-manager support.


On Linux, the installer automatically runs `configure-ego-browser` after installing the managed
runtime. It updates the wrapper and Skill paths, versions, and digest from the immutable `current`
release while preserving the existing `ego_browser_enabled` value. Installing or upgrading the
artifact does not implicitly enable the bridge. To synchronize an independently installed runtime,
run:

```sh
sudo agent-remote-node configure-ego-browser \
  --config /etc/agent-remote-node/config.json \
  --runtime-root /opt/agent-remote/ego-browser
```

Only enable the bridge after the Server evidence and artifact-bound canary have passed:

```sh
sudo agent-remote-node configure-ego-browser \
  --config /etc/agent-remote-node/config.json \
  --runtime-root /opt/agent-remote/ego-browser \
  --enable
sudo systemctl restart agent-remote-runtime.service agent-remote-node.service
```

Native session specifications now carry a root-generated, non-sensitive runtime configuration
snapshot. The supervisor and Claude child therefore do not need to read the protected node config,
so dynamic session users can start normally even when `/etc/agent-remote-node/config.json` is
owner-only. Upgrade the Runtime Helper and Node binaries together; existing root-owned session
specifications remain usable across later node configuration changes.

No public listener, Docker port publish, NAT rule, or dynamic WireGuard ACL is created for session forwards. The existing restricted SSH port is the only data-plane entry. Runtime Helper and node services must be upgraded before enabling the control-plane policy.

`browser_public_base_url` is optional. When it is empty, the node reports the local Docker port mapping for KasmVNC. In deployed environments, set it to the node-side HTTPS reverse-proxy URL that reaches the browser container stream endpoint.

For a control plane and node running on the same Docker host, set `browser_docker_network` to the control-plane Compose network (for example `agent-remote_default`). Browser containers then join that private network and the control plane reaches KasmVNC by container DNS without exposing its port on the host.

## Install

For the managed path, create the Node and its SSH transport in the admin console, then run this from
the logged-in control workstation:

```sh
agent-remote node install --node <node-id-or-prefix>
```

This path authenticates and installs the release before asking the control plane for a one-time join
code. The release archive and code use separate SSH stdin invocations. A persisted exchange ID lets
the same command recover an interrupted enrollment without putting the code or resulting Node token
in argv, environment variables, URLs, logs, or terminal output. The default leaves a new Node's
ego-browser capability disabled and preserves an existing Node's setting; `--enable-ego-browser` is
an explicit administrator intent and still requires local release verification.

The direct registration-token installer remains available only for advanced legacy provisioning on
a clean Debian 12+ or Ubuntu 22.04+ VPS. If `cosign` is missing, the installer downloads the
checksum-pinned official verifier into a private temporary directory for this run. Direct downloads
still fail closed unless the release checksum and Sigstore workflow identity both verify:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token>
```

This compatibility path installs the dependencies required by the selected backends without upgrading packages that are already installed, configures the restricted SSH gateway, installs the managed device proxy, registers the node, starts both systemd services, and verifies the runtime probe and control-plane heartbeat. Its registration token is a short-lived secret carried in argv, so restrict this path to isolated manual maintenance and keep it out of shell history and logs. With the default `native` backend it also enables IPv4 forwarding and user namespaces, downloads Claude Code `latest` through Anthropic's official installer, and installs the latest verified Node.js 22 release with `npm` and `npx` into the same read-only managed runtime. The default does not require KVM or Docker. Run it as root, or as a user that has `sudo` access; the installer elevates only the system operations.

The default native dependency set also provides a consistent AI development baseline on minimal VPS images: standard shell/text/file utilities, `rg`, `jq`, Git/Git LFS/GitHub CLI, archive tools, `rsync`, Python 3 with pip and venv, SQLite, a C/C++ build toolchain, and common process/network/DNS diagnostics. The installer verifies the commands after package installation and repairs a broken `awk` alternatives link by reinstalling `gawk`. These host tools are exposed read-only inside Native sessions and do not grant additional privileges.

The command is idempotent. Re-running it upgrades the node binaries, Claude, and the selected Node.js release line, refreshes the system layout, and reuses the existing node token. Add `--force-register` only when intentionally replacing the node registration.

On a fresh installation the installer chooses an unused UDP port from the dynamic range `49152-65535` instead of using the well-known WireGuard port `51820`. The selected port is written consistently to the WireGuard interface, Runtime Helper unit, node configuration, and advertised public endpoint. Later upgrades preserve that port. An existing legacy `51820` installation is migrated automatically; pass `--wireguard-listen-port 51820` only when retaining it is intentional. To select a new random port after a route-level block, rerun the installer with `--rotate-wireguard-listen-port`, allow the printed UDP port in any host or cloud firewall, then refresh each client with `agent-remote wireguard config` before restarting its tunnel. Local availability does not prove an upstream network path, so verify a real handshake from the affected client after rotation.

Install a specific node release or pin the official Claude version:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --version <node-version> \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token> \
  --claude-version <claude-version>
```

For a supply-chain-pinned Claude artifact, add all three options:

```sh
--claude-version <version> --claude-source <artifact-or-url> --claude-sha256 <sha256>
```

Node.js defaults to the latest verified release in the 22.x line. Pin an official version with `--nodejs-version`, or pin a supplied archive with all three options:

```sh
--nodejs-version <version> --nodejs-source <archive-or-url> --nodejs-sha256 <sha256>
```

The installer fails before enabling the worker when a selected backend does not satisfy its probe.
Native requires Linux 5.15+, systemd 249+, cgroup v2, Bubblewrap user namespaces, and the configured
locale. Docker Sandbox requires Linux, a root helper, tmux, Git, POSIX ACL tools, a valid non-root
runtime identity, and an installed Docker CLI whose daemon and `docker sandbox create`, `exec` and
`rm` commands are available. The installer and Helper verify command-specific usage: Docker releases
that replace the plugin with a successful removal notice are unsupported. New starts fail before
account/workspace changes; stop and cleanup preserve trusted runtime records when lifecycle commands
are unavailable. This compatibility check does not establish sandbox-wide writer quiescence or
managed-skill acceptance. Use `--runtime-backends native,docker_sandbox` for both or
`--runtime-backends docker_sandbox` for Docker only. To install files without registration or startup,
omit the three control-plane options and add `--no-start`.

Install from an extracted release archive with the same one-command options:

```sh
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## Release Packaging

Node and Device use independent release versions. `release-dependencies.json` pins the exact
Device proxy tag, commit, and artifact-signing workflow embedded by a Node release. Updating that dependency is an explicit,
reviewable source change; preparing a new Node version does not rewrite it.

Linux packages require an architecture- and libc-matched managed device proxy at:

```text
$DEVICE_PROXY_DIR/linux-amd64-glibc/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-arm64-glibc/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-amd64-musl/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-arm64-musl/agent-remote-device-proxy
$DEVICE_PROXY_DIR/<target>/VERSION
```

The installer verifies its digest, installs it under
`/opt/agent-remote/device/releases/<version>/`, and atomically switches `current`. Reusing a
version with different bytes is rejected, and capability remains disabled if the proxy is absent
or not executable.

```sh
DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
```

The release flow builds six archives: `darwin-amd64`, `darwin-arm64`, `linux-amd64-glibc`, `linux-arm64-glibc`, `linux-amd64-musl`, and `linux-arm64-musl`. The Go binaries are built with `CGO_ENABLED=0`; the glibc and musl labels exist so installers and users can select packages by deployment environment.

Each archive includes node binaries, installer, systemd unit, sample config, license, and notices.
Linux archives also include the managed device proxy.

GitHub Actions runs this packaging flow for `v*` tags and uploads the archives to the GitHub Release.

## License

agent-remote-node is licensed under GPL-3.0-only. See `LICENSE`.

Third-party dependency notices are listed in `THIRD_PARTY_NOTICES.md`.

### Configuration import ownership checks

Configuration imports require a Server that supports the task-bound
`/api/v1/node-api/tasks/{task_id}/config-import-authorization` endpoint. Upgrade Server before Node.
Node rechecks the task's current account directory mode before writing and rejects an incomplete,
expired or unavailable authorization. A queued task cannot grant itself legacy ownership. If an
account is migrating or managed, a batch containing `~/.claude/skills` fails with
`SKILL_MANAGER_OWNS_PATH` before any files are written; use CLI
`account import-config --exclude-skills` for the remaining configuration. Plugin skills and project
history keep their separate selection rules. Successful task retries report the saved result.

On Linux, the worker sends authorized imports to the privileged Helper; upgrade both Node binaries
together. The Helper selects the account path and non-root runtime owner. Its private
`skill_state_root` retains immutable account fences and exact-input task receipts across restart;
back up this root with the account data and do not delete it to clear a failed import. Once the Helper
observes migrating/managed mode, an older legacy authorization cannot reopen skill imports.
The same fence blocks new legacy sessions, binding processes and backend migrations with
`MIGRATION_PENDING`; existing sessions are not force-stopped and explicit stop remains available.
Server also rejects new binding/migration planning for nonlegacy accounts. Managed binding and
backend migration need dedicated snapshot-aware adapters before those operations can reopen.
`CONFIG_IMPORT_PENDING` requires reconciliation of an interrupted write; `CONFIG_IMPORT_FAILED`
replays the saved failure. Neither permits automatic reimport. Non-Linux Helper imports are unsupported.

Import limits are 1 MiB per file, 8 MiB raw total and 12 MiB encoded file-list JSON. Only task polling
and Helper import frames use a 16 MiB transport ceiling; ordinary calls retain 1 MiB. Run
`bash tests/linux_config_import_test.sh` for actual Linux ownership, restart and full-quota socket tests.
See [account fences and receipts](docs/skill-account-fence.md) for recovery boundaries.

These checks do not enable directory takeover or advertise skill-manager support. Takeover still
requires draining existing imports and excluding legacy writers during the stable initial capture.

The internal snapshot download client verifies the original snapshot/task-record UUID and complete
Node/user/account/session/backend binding, strict manifest metadata, digest and member resolution.
Files stream with exact length, hash, text classification and HTTP validator checks; redirects and
transformed content are rejected. Cancellation closes blocked reads. Downloaded bytes still require
Helper-owned atomic preparation and pinned system artifact verification before any runtime launch.
The worker routes managed Native tasks through exact pointer validation, leased recovery/startup and
durable Server confirmation. Malformed markers cannot enter legacy startup. No managed backend
capability is advertised while finalization transport and complete runtime acceptance remain pending.

The Helper's separate `prepare_skill_snapshot` stream can now consume the original snapshot for an
existing trusted Native spec. It requests only manifest digests, independently verifies each object,
and atomically retains the work tree plus complete input identity. Exact retries preserve learned
content; changed fixed inputs fail. Socket disconnect cancels preparation, and interrupted transfers
discard unpublished staging. Creating the trusted spec and coordinating
mount/start/lease recovery remain outside this preparation operation.

The internal worker preparation coordinator now holds an exact snapshot/task/poll-attempt lease
while downloading, creating the trusted Native spec and streaming its files to Helper. It cancels on
uncertain renewal and returns only a local preparation receipt. The internal startup coordinator
extends one lease through original-launch recovery, preparation, peer admission and launch. It drains
the exact original runtime on ambiguous failure or lease loss, including loss after a successful
Helper response. Recovery requires the original live invocation and an existing broker peer grant;
broker restart or revocation causes retained cancellation instead of replacement authorization.
The dedicated Native queue uses this coordinator and retains nonterminal failures for recovery;
preparation success alone is not session readiness.

The managed-start confirmation client binds a ready/stopped result to the original snapshot, task
record and lease attempt. It accepts only a committed exact echo; uncertain writes require an explicit
retry of the same outcome. A separate worker loop inspects pending receipts even after task polling
ends. A newer poll attempt must fence an unaccepted old proposal before it can be replaced.
An exact unaccepted cancelled observation instead retires the original proposal with its evidence.
Retirement stops background inspection and rejects delayed startup replay without granting runtime
or cleanup authority; independent finalization still saves the retained work.
The task ledger now syncs a private
temporary file before atomic replacement and syncs its parent before accepting the write. An uncertain
publication blocks further task execution until reopen, and corrupt empty/null ledgers fail closed.

The finalization API client now streams a frozen complete input through separate begin, upload,
complete, status and publication routes. It binds the original snapshot, termination classification,
digest and upload attempt; unclean input remains detached. Real Server-to-Go tests verify persistence,
publication and exact replay. The Helper now atomically freezes private file objects with its journal
and passes exact-bound read-only descriptors to the worker. Runtime work and transient files can be
removed without losing those original upload bytes. Historical journals upgrade only under the
finalizer's writer-exit proof and exact content verification. An independent background loop now
pages through retained Native captures, uploads the original bytes and persists separate Server
persistence/publication receipts in `<ledger_path>.skill-finalizations`. Restart and lost responses
resume the same input; conflicts and superseded decisions are retained. Exact acknowledgements now
advance the privileged Helper journal before a separate operation verifies stopped writers and cleans
transient resources. Cleanup has its own durable receipt and preserves all frozen objects and work.
The loop also reconciles unfinished Native bundles against their original sealed launch, freezes
same-boot natural exits after whole-cgroup exit proof, and transfers the result. Missing transient
specs do not prevent this recovery. Same-boot prepared or unprovable launches remain pending;
running observations cannot restore broker admission. First upload now separately confirms exact
termination with Server, independently of a full runtime inventory or preparation lease. Lost replies
reuse the original capture, and the stopped receipt never substitutes for content persistence.
For sealed same-boot launches, lost broker admission now drains the original enabled runtime through
a separate Helper operation and retains its content. Startup and background inspection serialize;
new registrations cannot restore original grants. Browser-disabled sessions remain running.
Interrupted same-boot starting records now retain a verified `observed` invocation before running
inspection, admission-loss draining or natural-exit capture. This phase does not certify readiness;
leased recovery must still verify the original runtime. Common managed stops use the same retained
invocation even when transient specs are missing.
After a distinct valid kernel boot, retained prepared/starting/observed/started bundles now recover through
repeated passive unit/cgroup/network/mount absence checks. New captures are unclean unless original
termination was already durably classified. Acknowledged previous-boot cleanup can resume without
stopping, unmounting or deleting newly present resources. See `docs/skill-runtime-recovery.md`.
The isolated kernel-reboot acceptance and its verified scope are described below.

The separate `prepare_managed_session_spec` operation now creates a trusted Native spec without
launching. A private intent and immutable nonce-free draft make exact retries and interrupted
publication recoverable on the original boot. Completed retries verify original files and runtime
identity; missing or changed state is retained as an error. Account skills are not rewritten.
The operation requires the closed account fence and existing account directories. The dedicated
Native queue owns preparation, startup and confirmation under one lease.

`start_managed_session` now launches the prepared Native session at most once. A durable private
intent precedes systemd-run; readiness seals the original invocation ID. Same-boot recovery checks
the active original unit and mounts without repairing them; a missing/stopped unit is finalized and
never relaunched. Completed retries replay historical readiness even after transient cleanup.
Disconnect cancellation stops writers and retains unclean work. Real isolated systemd tests cover
these paths with synthetic tools. `recover_managed_session` separately checks current liveness;
stopped historical starts require finalization. Already-confirmed runtimes after broker restart and
previous-boot retained bundles now have separate recovery paths. Normal-stop saving and status are
connected as described below; complete real Server/worker/Claude acceptance remains pending.

Preparation and Native mounting verify the snapshot's fixed system releases. Ego-browser pins must
match embedded provenance and bytes; enabled wrapper artifacts are verified separately. The device
skill requires the Helper build's exact Node release and selected protocol, and is hidden when not
selected. Mount replay verifies actual system copies without repairing them. Old snapshots lacking
pins remain recoverable but cannot launch against today's artifacts.

Native takeover capture and queue recovery are documented in
[`docs/skill-account-capture.md`](docs/skill-account-capture.md). It closes a durable account fence,
checks historical and local Native writers without stopping them, and retains a separate private
copy plus manifest for retries. Pending import intents and uncertain backend evidence block it.
Dedicated Native tasks now renew their exact lease through Helper capture and authenticated transfer;
Server reservation and immutable capture recover retries without reimporting later source edits.
Server session admission now initiates original Native reservations. Docker sandbox/orphan inventory, interrupted backend-copy recovery,
verified rollback and full runtime acceptance remain required. Original directories are retained;
no capability is advertised by this implementation.

Backend account migrations retain a durable whole-migration intent and supervise backup copies plus
target/rollback ACL commands with systemd. Unknown writer exit prevents rollback and replacement
tasks (`STATE_MIGRATION_PENDING`). Exact terminal receipts replay the saved success/failure without
re-copying or changing permissions; failed results use `STATE_MIGRATION_FAILED`. The Helper flushes
account and backup filesystems before terminal persistence. Complete migration/copy receipts can
satisfy Native takeover's backend-writer check; old copy-only evidence cannot. Interrupted started
work and mixed-backend writer proof still require further work. Ordinary Native session admission
now initiates takeover, as described above.

Managed Native stop freezes the original session before attempting Server saving for up to 10 seconds.
The immediate attempt and independent background recovery share one finalization ledger instance;
network failure preserves frozen input and does not keep writers alive. Stop tasks report only immutable
process identity; current saving status is queried by the original snapshot UUID on the Server.

A disposable two-kernel reboot acceptance is available as `tests/linux_skill_kernel_reboot_test.sh`.
It boots a real managed Native runtime in QEMU TCG, abruptly powers off the VM and recovers its
persistent Skill data under a new kernel. ARM64 and amd64 acceptance passed; select the guest with
`AGENT_REMOTE_KERNEL_TEST_ARCH=arm64` or `amd64` (default: Docker host architecture). See
[recovery contract](docs/skill-runtime-recovery.md) for the verified scope and requirements.

The deployment transport client now reads the complete original account-directory input through
separate task/attempt-bound Node endpoints, verifies the Server's canonical plan and tree digests,
and streams exact manifest file bytes under the current poll lease. Dedicated worker execution now
keeps one renewable lease across download, Helper preparation and exact Server confirmation.

The dedicated `prepare_skill_deployment` Helper operation now atomically retains the complete
original directory under its attempt identity. The private receipt pins the full input; exact replay
verifies retained bytes without modifying account or session state. The worker journals the receipt
before confirmation; independent read-only inspection recovers a lost acknowledgement without
invoking the Helper. Dedicated failure/cancellation now uses the durable recovery path below;
Server ordinary polling now schedules compatible pending targets; Node capability activation remains pending. See [independent preparation](docs/skill-deployment-preparation.md).

The separate `drain_skill_deployment` Helper operation now permanently fences an original attempt
under the same lock as preparation. Its durable receipt survives lost responses and restart while
preserving retained content. The worker requires committed Server revocation before invoking drain.

The dedicated HTTP client now preserves Server revocation intent and exact Helper drain through
terminal confirmation and read-only result inspection. It rejects altered bindings/classification
and uncertain writes are not automatically retried. The worker saves the request, committed intent,
Helper drain and accepted terminal observation in distinct durable phases. Independent recovery
handles lost responses without a preparation lease and preserves any original success proposal.
Confirmed success cannot be downgraded. Server ordinary scheduling is implemented; full runtime
acceptance and Node capability advertisement remain pending.

The [first-use proof](docs/skill-first-use-acceptance.md) covers ordinary acceptance and polling,
real systemd descendant writers, the initial Helper fence/capture, takeover response-loss recovery,
and resolved deployment. It does not establish Claude startup or Docker/sbx acceptance.

Frozen Native data can now be exported through the existing SSH forced command with the matching
CLI's `skill state export --snapshot UUID --scope account-directory --account-id UUID --output PATH`.
It reads only an existing immutable Helper capture, with live original-user/device/key checks,
independently of Server upload quota. It never stops a session, acknowledges upload or reclaims data.
See [frozen snapshot export](docs/skill-node-export.md) for protocol and verification boundaries.
