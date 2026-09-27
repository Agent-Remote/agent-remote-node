# 02 Architecture

The private Helper reclamation coordinator now holds the cancellable lifecycle mutex and proves
original Native ready spec/launch authority, terminal capture/cleanup, absent transient root/network,
stable original stopped unit and empty cgroup. Previous-boot inputs require absent current unit/cgroup;
legacy inputs without managed authority remain retained. Runtime/work/frozen-object mount aliases are
checked before marking and at deletion phase boundaries. Per-entry guards check cancellation, boot,
root absence and cgroup while the executor retains inode/mount-ID/content/reader-lock checks. External
command count does not grow with file count. Pending intent resumes without renewed authorization.
The separate authenticated `reclaim_skill_finalization` operation now starts its Helper monotonic
60-second budget before sending a fresh challenge and saved acknowledgement. Worker echoes that
challenge through authenticated Server HTTP and returns only strict authorization JSON. IPC and HTTP
delay cannot extend the Helper budget. The 15-minute socket lifetime owns disconnect cancellation,
reader shutdown and cancellable lifecycle waits. `resume_skill_reclamation` accepts Node/session only
and can consume only existing durable intent without renewed authority or a Worker transfer ledger.
Worker schedules first reclamation after transfer returns and closes its read hold; pending inventory
resumes directly and completed content is skipped. No generic result cache or Node credential enters
Helper authority. See `docs/skill-node-reclamation.md`.

Finalization readers protect complete upload/export lifetimes with a shared kernel flock on the private
immutable `finalization/manifest.json` inode. No file creation is required for export. The authenticated descriptor operation adds kind `hold`, bound
to the original capture. SCM_RIGHTS transfers the held read-only descriptor; Helper restart does not
release a surviving client's lock. First marking and deletion take a nonblocking exclusive lock.
No in-memory lease, persisted deadline or generic path is authority. Individual frozen-object
read descriptors also retain shared inode locks; deletion refuses outstanding readers. The locked manifest
is retained with audit records. Active reads block reclamation rather than being silently interrupted.

Local marking refuses a fresh observation whose current publication remains conflicted; remote byte
availability does not override unresolved-conflict retention. A later resolved publication can retain
the same incoming checkpoint. Equal publication ID/attempt must preserve its original terminal status.

The Linux reclamation executor may remove only the marked original `work` and `finalization/objects`
roots. It requires caller-owned lifecycle/reference exclusion, validates durable intent, then checks
all remaining entries against the frozen manifest before any removal. Descriptor-relative no-follow
traversal and kernel mount IDs reject substituted or nested mounts. Every file/link is rechecked
before unlink; unknown/changed bytes are preserved. Interrupted deletion resumes only the same intent,
and metadata/completion publication remains separate from filesystem removal. This executor alone
does not expose a Helper operation or start Worker scheduling.

Local reclamation journal primitives retain an immutable original-input intent and a separate
completion receipt under `finalization/`. Marking requires the exact terminal acknowledgement,
runtime-cleanup receipt, a fresh process-local authorization budget and a complete unchanged captured
work tree. Excluded system paths must be absent or empty so uncaptured bytes remain protected.
It records original work/object directory identities; replay never refreshes remote authority.
Pending/completed reclamation remains readable audit metadata but cannot be exported, recaptured
or presented as ordinary transferable content. Completion requires both original content roots
absent and parent fsync. These primitives remain separate from deletion. The serialized Helper operation independently
proves absent writers, mounts/aliases and local references before marking or resuming deletion.

Current Server availability for redundant finalization reclamation is a separate authenticated
read, never inferred from a historical upload/publication acknowledgement. `internal/api` verifies
the complete original snapshot/capture/checkpoint binding and strict 60-second authorization
shape, deriving a conservative monotonic deadline from request start. It requests no paths and
performs no automatic retry. The neutral authorization schema belongs to `internal/skillmanager`.
This transport alone does not authorize Helper deletion, remove references or enable reclamation
scheduling. Local marking primitives do not replace independent stopped-runtime/reference proof.

Native skill runtime dependencies are discovered by the Helper, never supplied by tasks. The
initial Python adapter probes only `/usr/bin/python3`, `/usr/local/bin/python3` and their same-directory
versioned interpreter targets. Root-owned, non-writable directory/file chains and an ELF executable
are required. A bounded isolated probe runs as the session UID with an empty inherited environment;
dependency identity binds CPython major/minor, ABI flags, byte order, Linux architecture and exact
target path. Preparation seals the mapping for capture without copying interpreter bytes. Retry
uses the original mapping; new materialization and first mount verify dependencies again. Historical
capture/export never require today's interpreter. Unknown paths remain portability errors; this
does not certify third-party binary extension compatibility or Docker Sandbox support.

Stopped-work export is a separate read-only recovery stream, `stream_stopped_skill_export`.
Only verified absence of the finalization directory permits it; an existing incomplete, linked,
legacy or corrupt capture never falls back to work. The Helper holds its cancellable mutation lock
through scanning, streaming and final checks. Lock acquisition and initial/final scans each have
a 15-minute bound; progressing authorized transfer has no separate fixed ceiling. It requires the exact sealed
Native snapshot, retained ready spec/launch authority, and repeated
passive whole-cgroup quiescence; previous-boot reads additionally require absent runtime resources.
It does not stop, reconcile, freeze, fsync content, allocate another content copy, acknowledge or
clean up. Source metadata and the full tree digest are checked again before completion. Legacy v1 export
retains its 100000-entry bound; negotiated recovery uses the bounded format described below. Both
retain signed 64-bit expanded-size validation, while
streaming the complete observed byte count independently of runtime admission quotas. A fixed
10 GiB byte ceiling must not prevent recovery of over-quota work. Metadata/frame limits, bounded
buffers, live authorization and transfer deadlines still apply; legacy v1 entry overflow remains a failure. Gateway reauthorization runs during initial
inspection/scanning as well as transfer. Per-object checks use only this connection's bounded
1–10-second validity, measured from HTTP request start. Renewal starts halfway through validity;
independent expiry cancels blocked renewal/output. Header and footer require fresh HTTP proof,
and failed/late renewal is terminal. Original identity and learned facts remain immutable; only an exact live successor may advance expiry. Recovery produces no local-durable finalization receipt.
Each verification POST closes its HTTP/1 connection after the response, preventing the next periodic
renewal from racing that connection's server-side idle expiry. The shared transport and other API
requests retain their ordinary pooling. An uncertain EOF is terminal and is never replayed; the
five-second request deadline and short authorization window remain unchanged. The gateway uses
`/renew` to continue the same original token/binding; fixed-expiry `/verify` remains available.
An absent termination record permits only an unclean observation and additionally requires an
original retained canonical invocation. Corrupt or linked termination evidence never counts as
absence. No termination record is written by export.

Frozen snapshot export uses `internal/skillexport` behind the existing SSH forced-command gateway.
It reauthorizes a short-lived Server grant bound to the original user token, device, SSH key and
snapshot, then reads Helper-frozen manifest/object descriptors or the separate guarded stopped-work stream. Helper single-session inspection
is read-only and cannot reconcile, freeze or stop a runtime. Export does not upload data, acknowledge
persistence, invoke cleanup or enable backend capability. No grant enters process arguments or logs.

Independent deployment draining uses the authenticated `drain_skill_deployment` Helper operation.
It serializes with preparation and permanently fences the exact original attempt before returning
a durable receipt. It cannot stop a session, alter an account, delete retained input or acknowledge
Server failure/cancellation. Late preparation requests for that attempt are rejected after restart.

Initial account takeover transport lives in `internal/api`. Every operation binds the exact
Server reservation and task; file transfer also binds the current upload attempt and retained
manifest entry. Responses are bounded and validated, redirects are refused, and content bytes
stream through readers rather than JSON/base64. Transport availability alone does not enable
Helper operations, worker dispatch or managed runtime capability advertisement.

Takeover lease maintenance uses the authenticated poll envelope's `lease_attempt` and exact record
UUID. Renewal remains in `internal/api`; worker lease supervision derives a conservative local
deadline from Server duration and request start time, accounting for network delay and clock skew.
Any renewal failure or exhausted budget cancels dependent work, closes its renewal loop and retains
the Helper capture. It cannot revive an expired attempt or substitute for the final authority receipt.
The internal retained-capture transfer coordinator refreshes Server authorization before any Helper
read, owns lease supervision through upload completion, and streams each distinct digest once. A
verified committed receipt wins a concurrent terminal renewal rejection. Every failure preserves
retained files. Dedicated Native takeover dispatch now extends one lease over capture initiation
and retained transfer. Server reservation and Helper capture own durable recovery; generic ledger
failures cannot consume the task. See `docs/skill-account-capture.md`.

## Layout And Privilege Boundary

```text
cmd/agent-remote-node/     Unprivileged worker entrypoint
cmd/agent-remote-attach/   Forced-command SSH gateway
cmd/agent-remote-runtime/  Privileged helper entrypoint
internal/api/              Control-plane client and payloads
internal/config/           Configuration loading and defaults
internal/ledger/           Local task idempotency ledger
internal/worker/           Polling, dispatch, and reconciliation
internal/runtimehelper/    Root-owned Unix socket protocol and engine
internal/runtime/          Capability and resource snapshots
internal/browser/          Browser runtime specifications
internal/devicecontrol/    Opaque device-control activation and relay bridge
internal/egobrowser/       Node-owned ego-browser broker and relay protocol
internal/egobrowserartifact/ Immutable wrapper and Skill verification
internal/portforward/      Authorized session loopback tunnels
internal/toolaccounts/     Docker Sandbox account-binding runtime
internal/toolsessions/     Docker Sandbox tool-session runtime
internal/workspace/        Managed workspace operations
```

The worker must remain unprivileged and must not join the Docker group. Privileged actions pass through the root-owned Unix socket, peer credential checks, request validation, and serialized engine execution. Packages must not bypass this boundary with direct host mutations.
The worker persists successful and failed task outcomes in its local idempotency ledger. When the
control plane reissues an expired lease, the worker reports that stored terminal outcome instead
of invoking the privileged runtime operation again.
The worker, privileged helper, and every transient managed runtime explicitly disable core dumps;
device screenshots, input, credentials, and session state must not enter host crash artifacts.

## User Skill Manager

Managed admission recovery lives in the worker's process-owned registry and serialized lifecycle
gate. Startup records only its initial successful nonce/UID grant; historical readiness never
restores one. The independent finalization reader checks the exact original grant for running
observations, then requests `drain_unadmitted_skill_session` when it is lost. The Helper accepts only
Node/session identity and independently proves the retained same-boot launch and current invocation.
It drains only browser-enabled original runtimes, using common graceful stop, cgroup proof and
finalization. Disabled sessions stay running. Same-boot starting records persist an independently verified
observed invocation without certifying readiness, then use the same admission and exit paths;
previous-boot bundles use passive recovery in `docs/skill-runtime-recovery.md`. Frozen inventory and
frozen/not-started observations retire matching grants without
unregistering later nonces.

Finalization HTTP transport lives in `internal/api`, separately from leased startup and takeover.
It binds the original snapshot/session, immutable tree digest, idempotency key and clean/unclean flag.
Begin/status validate the original receipt and monotonic upload attempt; completion requires the
exact attempted upload and retained incoming checkpoint. File streams verify length, digest and
classification before accepting their bounded receipt. Separate publication distinguishes published,
conflicted, detached and superseded outcomes. Transport never retries an uncertain write, reads Helper
paths, acknowledges local cleanup or advertises capability. The Helper now exposes frozen manifests
and objects through exact-binding read-only descriptors. An independent worker loop now scans retained
Native finalizations, transfers frozen bytes and saves exact persistence/publication receipts in a
separate worker ledger. Neutral receipt schemas live in `internal/skillmanager`; `internal/api`
aliases them and retains all HTTP/authentication. The worker passes those exact receipts through
`acknowledge_skill_finalization`, then requests separate stopped-runtime cleanup after terminal
publication. The independent loop now invokes separate per-session reconciliation for unfinished
bundles. It discovers the original ready draft and sealed invocation through retained identity,
leaves same-boot prepared/unknown launches untouched, observes stable starting invocations without
readiness, and captures same-boot natural exits only after whole
cgroup quiescence. An exited original service may be released; a live or replacement unit is never
drained by this operation. Before the first upload, the worker separately confirms the exact frozen
termination with Server under the original snapshot/task/generations. This request has no startup
lease and cannot report other sessions missing. A distinct valid kernel boot and repeated passive
unit/cgroup/network/mount absence now permit recovery of prepared/starting/started bundles as unclean,
unless an earlier immutable clean termination exists. Previous-boot cleanup requires the same proof
plus acknowledged terminal retention. Real kernel-reboot and complete runtime acceptance remain required.

`list_skill_finalizations` reads the independent store without creating it, touching work or requiring
a transient spec. It returns at most 16 sorted session candidates and a continuation cursor. Broken
bundles produce bounded per-session diagnostics, so later captures can progress. Original 64-bit
generations remain integers across the Helper JSON boundary. Single-session frozen inspection uses
the same exact integer decoding and disconnect cancellation as inventory, including lifecycle-lock
waits; cancelled export authority cannot leave an inspection waiting for the generic deadline.
Worker failures retain the same input.
Only the dedicated acknowledgement operation changes the privileged journal. Separate cleanup verifies
the retained publication and original stopped runtime; it retains work, frozen objects and all SkillStateRoot
metadata. No HTTP client or Node credentials enter the Helper.

`internal/api` now has exact managed startup confirmation transport. It sends only original
snapshot/task/session/account/backend/attempt identity and neutral unit/terminal metadata to the
dedicated managed-result route. A committed matching acknowledgement proves Server acceptance;
HTTP success alone, changed receipt fields or a legacy completion response do not. Uncertain writes
are not automatically retried, and explicit retries must retain the original complete outcome.
Dedicated Native queue dispatch now retains its lease through local proposal persistence and exact
Server confirmation. Generic legacy result caching cannot consume managed task errors.

A separate worker loop inspects durable managed-start proposals independently of task redelivery.
It records only an exact already-committed Server receipt and never republishes readiness or touches
Helper/runtime authority. Unaccepted proposals stay pending for a future leased original-runtime
recovery unless exact inspection proves the task cancelled. That observation retires the original
proposal durably without accepting readiness or changing the independent finalization flow. Per-record compare-and-swap prevents stale background observations from replacing a newer
proposal. This acknowledgement loop cannot launch or stop runtime processes.

Managed preparation downloads use the exact Server snapshot and task-record UUID in `internal/api`.
The client verifies the full Node/user/account/session/backend binding, complete manifest digest,
epochs and member identities before returning a preparation input. File downloads remain scoped to
that same snapshot/task, refuse redirects and transformed HTTP content, and verify exact lengths,
digests and text classification while streaming into caller-owned private staging. Successful
download is not a Helper preparation receipt or runtime readiness; partial staging cannot be mounted.

The internal managed preparation coordinator now renews the exact snapshot task's current poll
attempt, retrieves its authenticated original input, creates the bound Native spec and streams the
manifest files into Helper preparation. Its returned receipt proves local preparation only. It does
not complete a create task or launch a runtime; the separate startup coordinator owns those steps.
Snapshot and takeover share the same owned lease loop, but retain distinct identities and endpoints.

The separate `start_managed_session` Helper operation now starts a fully prepared Native session at
most once. It binds the original logical request, complete creation input and ready spec to a private
launch intent before systemd execution. Readiness seals the original transient invocation ID. Exact
completed replay returns historical readiness even after transient cleanup; it is not a liveness
probe. Incomplete same-boot recovery verifies the original active unit, cgroup, UID and unchanged
writable private nosuid/nodev mount without mounting or repairing it. Absent or stopped units are
drained through the shared finalizer and never relaunched. Uncertain recovery retains pending state.
Disconnect cancellation uses independent bounded writer cleanup. Native queue dispatch uses these
operations; complete cross-boot task reconciliation remains unfinished.

The internal worker startup coordinator now owns one task lease through original snapshot fetch,
launch recovery, spec/content preparation, peer admission and startup. `recover_managed_session`
runs before preparation and requires current original runtime evidence; a historical ready receipt
alone cannot certify liveness. Completed launches must still match the sealed invocation. An absent
launch intent returns only `not_started`; corrupt or linked records remain pending. Recovery cannot
submit a fresh launch. Peer admission must succeed before first launch, and the runtime UID must
remain the same across preparation/start. Lease loss, lost responses and admission failures use
`cancel_managed_session` with a fresh bounded context, including when launch already returned success.
Cancellation binds the exact original spec/request and active invocation before common stop/finalization. Retained data
is never removed. The coordinator requires caller-owned broker registration and classifies uncertain
outcomes as nonterminal. Dedicated Native queue execution also publishes the exact ready/stopped
outcome under that lease, with durable pending confirmation and independent receipt inspection.

Managed broker admission now separates initial authorization from recovery. The worker discards
task-supplied broker material and builds session context from its own configuration/registration.
First launch may grant the trusted runtime UID access. Recovery only verifies an already admitted
session/nonce/UID in the current broker process; it cannot grant ACLs or restore authorization.
Re-registration, revocation and broker restart cannot adopt the nonce still held by a live process.
Failed verification enters exact cancellation and retains skill state as pending. Nonces remain
process-local; recovery does not persist or resurrect them. Queue integration must retain these
separate admission paths and complete stopped-session finalization/reporting.

The dedicated managed queue precedes legacy decoding. Legacy session decoding and Helper start
operations reject any `skill_manager` marker with `SKILL_MANAGER_UNSUPPORTED`, including null and
case aliases. The Helper checks before replaying its legacy result cache; no old receipt can certify
a managed snapshot. Requests cannot silently drop the pointer and launch against account files.

`prepare_skill_snapshot` is a separate Helper stream operation for a preexisting matching trusted
Native session spec. Shared preparation schemas live in `internal/skillmanager`; `internal/api`
retains HTTP/authentication. The worker sends a bounded immutable snapshot and answers only digest
requests from that manifest. Each raw file ends with a completion byte sent only after authenticated
download verification. The Helper independently verifies bytes, selects paths/UID/GID/policy from
its spec and configuration, and atomically publishes the bundle. Node credentials and caller-chosen
host paths never cross this protocol. Socket EOF cancels preparation through a bounded pipe; every
handler owns its reader goroutine. Snapshot and deployment preparation share a three-hour
absolute stream budget for default-sized sequential transfers; earlier caller deadlines and
ongoing exact-attempt lease renewal still apply. Preparation is serialized with other Helper mutations.

`prepare_managed_session_spec` is a separate non-launching Native operation. It accepts the
original snapshot identity/digest, selected system pins and declarative session input. Before new
session state, it requires a closed account fence, existing no-follow account directories, an absent
systemd unit and no existing session directory or retained work. It records immutable private intent,
then a nonce-free complete spec draft before publishing runtime files. Ready replay verifies the
original receipt and files without rebuilding. Interrupted publication can reuse the same complete
draft on the original boot; uncertain or changed state remains an error. Shared account directories
and skill content are not rewritten. Workspace and developer-profile preparation use existing Helper
primitives. The worker preparation coordinator leases this operation; runtime startup is still separate.

The retained binding includes the exact task-record UUID and a digest of all original snapshot
metadata, including system references and member resolutions. Identical retries preserve work;
changed fixed inputs conflict. This operation does not create a spec, mount, launch, mark Server
readiness or enable a backend capability. Those remain
the managed startup coordinator's responsibilities.

Preparation now strictly parses the original system release selection and checks it against the
Helper's embedded release before requesting content. Enabled ego-browser runtimes also verify their
wrapper/Skill source manifests and current bytes. Selected device skills require the Helper binary's
compiled Node release and exact runtime protocol, not the worker's configurable version label.
The sealed binding retains these pins, and Native mount rechecks them. Missing historical pins or
unavailable old artifacts block new launch while finalization and cleanup remain available.

Native overlays include only selected system skills: ego-browser is mandatory; the device skill
requires its selected device protocol. Before first mount and on mount replay, the actual selected
system copies must match every embedded file, directory, byte, readonly mode and root ownership,
with no extra entries, symlinks or hard links. Verification never repairs an already mounted tree.

The reviewed root skill-manager design authorizes `internal/skillmanager` for versioned manifest
validation and durable private session copies. Only the privileged runtime helper materializes and
captures copies beneath the configured SkillStateRoot; it must remain independent of SessionRoot
cleanup. The worker transfers authenticated content through `internal/api`, not task-result bodies.
Native and Docker require separately verified capabilities. Account and user configuration only
affect newly reserved session snapshots. System-managed skill artifacts retain their existing pins.

## Local Device Control

The node may advertise a managed device-control proxy capability only after independently
confirming the configured proxy path is a regular executable file. The capability is
protocol-versioned, exposes `platform=macos`, and lists each healthy enabled runtime backend:
Native requires its network namespace probe, while Docker Sandbox requires its full runtime probe.
Project files, Claude output, and task payloads cannot
override the proxy path or relay endpoint. The node does not parse or persist GUI payloads.

Linux release packages must include the matching `agent-remote-device-proxy` artifact. The
installer verifies its SHA-256 digest, installs immutable version directories under
`/opt/agent-remote/device/releases`, and atomically switches the `current` symlink. Reinstalling
the same version with different bytes is rejected. A missing, non-executable, or replaced proxy
keeps device-control capability disabled.

Eligible Native and Docker Sandbox Claude sessions receive a root-generated inline
`--strict-mcp-config --mcp-config` value and inline `Stop`, `StopFailure`, and `SessionEnd` hooks. They retain the
account's complete user settings source while rejecting CLI arguments that could replace settings,
disable managed hooks, or override MCP configuration. The runtime helper also installs and updates
the Agent Remote-owned `agent-remote-device` skill before account binding and session startup; it
does not modify other account skills or configuration. Both hooks invoke the verified
proxy binary in exec form over a fixed session-private lifecycle socket. For Native, the root helper
read-only binds the verified proxy binary and per-session bridge directory at fixed Bubblewrap paths.
For Docker Sandbox, it records those paths in the root-owned trusted spec, mounts them through the
Docker Sandbox lifecycle, and restores numeric traversal ACLs after persisting the spec. Project files,
account configuration, and user-provided CLI arguments cannot replace these paths.
Node CI must compare the managed skill against the exact reviewed Device commit recorded in
`managed-skill-source.json`, and a Node release must compare it against the matching Device
version tag. Updating the embedded skill and its source commit is one atomic change. Tests must
also verify that the runtime writes the complete embedded skill tree into the Claude account
without content drift.

The unprivileged worker owns the per-generation Unix bridge listener, authenticates its fixed
binding against the activation manifest, exchanges proxy relay material with node credentials,
and opens only the server-returned fixed relay path. It forwards opaque nested-TLS bytes in both
directions and never decodes action or screenshot JSON. The Rust proxy performs mutual TLS 1.3,
exact peer pinning, and exporter confirmation before sending an application frame.

Generation values use the control plane's positive signed 64-bit range. Activation and live
context payloads are capped at `9223372036854775806`; deactivation may use the reserved terminal
value `9223372036854775807`. Values outside those bounds are rejected before local state changes.

## Ego Browser Broker

The unprivileged Node daemon owns a separate per-tool-session ego-browser broker. Eligible Linux
Claude runtimes receive only an owner-only Unix socket path, a process-local capability nonce bound
to their exact `tool_session_id`, and non-secret workflow defaults. They never receive or submit a
binding ID, relay URL, ticket, device credential, long-term key, user identity, or local browser
path. The broker resolves the current binding from the nonce after each control-plane refresh, so a
session may be created before the user claims it without becoming ambiguous when other users have
bindings on the same Node. Session stop removes the capability, and broker restart invalidates all
previously issued nonces. The broker derives `agent-remote:<tool_session_id>` from that nonce,
returns the dedicated Task Space in every one-use permit, and rejects a different Task Space lock
scope. The wrapper therefore cannot redirect the normal Skill workflow with an environment
override.

The broker is the only remote endpoint that redeems a wrapper-role ticket and opens the outbound
`ego_browser_bridge` relay. It allocates persistent monotonic sequences, issues one-use bounded
permits, wraps per-request session keys to the registered local Bridge key, serializes WebSocket
writes, and dispatches out-of-order responses by the complete binding, generation, request, and
sequence identity. Cancellation keeps bounded tombstones so a late response cannot corrupt a
later request. Reload, revoke, generation or policy drift, relay failure, and shutdown clear all
pending material without replaying heredoc scripts.

Official `ego-browser` Skill content and the Linux remote wrapper are immutable, release-pinned
managed artifacts. Runtime PATH selects the wrapper; no remote browser is installed or launched.
The broker is independent of all local GUI device-control packages, identities, roots, and routes.

The wrapper is available to eligible Claude tool sessions on both runtime backends. Native uses its
dedicated per-user identity; Docker Sandbox uses the configured Node service identity as a fixed
non-root UID/GID and executes the sandbox process with `-u UID:GID`. The privileged helper returns
the selected numeric UID over the root-owned helper socket. The
worker removes that internal field from the task result, starts the broker socket as `0600` below a
`0700` directory, and grants only directory traverse plus socket read/write through numeric POSIX
ACL entries. The broker then requires the session nonce and the exact kernel `SO_PEERCRED` UID on
every wrapper connection. UID 0 is rejected. Docker startup validates and mounts the pinned wrapper,
Skill source, and broker socket directory, refreshes the canonical embedded Skill tree into the
account, prepends the wrapper directory to the process `PATH`, and passes the nonce through the
process environment rather than command arguments. Its root-owned spec never stores the nonce.

`tests/linux_ego_browser_uid_acl_test.sh` cross-compiles the real broker test and runs it in a
rootful Linux container with `acl` installed. It checks the exact directory and socket ACLs, proves
the authorized non-root UID connects, proves an unlisted UID is blocked by the filesystem, then
temporarily grants that UID filesystem access and proves `SO_PEERCRED` still rejects it.

The skill materializer accepts an already-authorized complete manifest and a digest-only content
opener from the helper. It prepares an ordinary writable copy in a private staging directory,
verifies streamed bytes, records original and actual permission baselines, fsyncs files and
directories, and publishes the complete bundle with one rename. Caller-supplied runtime dependency
mappings must come from the helper adapter, never directly from a task. It does not launch a
process or advertise backend capability. Runtime mount wiring and finalization must consume this
bundle before a backend can claim support; staging alone is not backend acceptance evidence.

`skillmanager.CaptureWorkTree` is called only after a backend proves every managed writer exited.
It hashes and fsyncs the complete discovery directory without following links, restores unchanged
permission normalization, and leaves original files intact on quota/portability failures. Tool-format
validation is separate: malformed SKILL.md bytes remain capturable. A helper-sealed snapshot binds
user/account/node/session/snapshot IDs, generation, directory epoch, initial tree and capture policy.
`FinalizeWorkTree` atomically publishes private file objects, a durable manifest and journal, then replays that receipt on
retry without needing the transient runtime spec. Authenticated Server acknowledgements advance
local_durable -> upload_pending -> persisted -> published/conflicted/detached; unclean recovery can
only become persisted_unclean then detached. These APIs are internal primitives until both runtime
lifecycles and authenticated Server finalization references are wired; they do not enable a backend.

Native stop/cleanup must confirm writer quiescence before deleting transient directories or
capturing skills. A failed systemctl stop, malformed/unknown unit state, populated cgroup (including
nested children), or inability to inspect cgroup state preserves the session spec and files. The
helper reads the unit's ControlGroup before stopping, checks the terminal unit state afterwards,
and uses cgroup.events populated=0 (or confirmed removal) as the process-group check. Successful
unit exit and unclean termination remain separate facts for the finalization journal.

Failed or cancelled systemd-run/startup-readiness checks use the same writer-quiescence check
before removing network namespaces or unmounting temporary storage. This recovery uses a fresh
bounded context so cancellation of the original launch cannot stand in for proof of exit. Missing
systemd units after a reboot remain unclean, even when their old cgroup is conclusively absent.

The Helper's internal Native preparation entry point seals a session-addressed bundle atomically,
including the helper-selected backend/resource, boot and UID/GID identity. A session spec records
its snapshot ID before preparation. The dedicated stream operation now delivers validated input to
that entry point for an existing matching spec; separate spec creation and leased internal startup
are connected to Native queue dispatch. Finalization transport and complete runtime recovery/acceptance
remain required, and no managed capability is advertised. Stop and cleanup
can locate a retained bundle after its transient spec disappears. A locally finalized stop reports
stopped plus state_pending; cleanup refuses it until the complete Server-retained terminal state.
Corrupt records, different node/account bindings and missing expected snapshots preserve the files.

For a sealed Native spec, launch binds only the work directory onto a protected per-session mount
point. Bubblewrap overlays both `/home/runtime/.claude/skills` and `/account/.claude/skills` with this
same directory. Root-owned embedded system skills are readonly at both aliases, and an enabled ego
browser's verified release artifact overrides both ego-browser aliases. No runtime can traverse the
private store parent. Mount identity is checked by inode/device and statx mount ID. Cleanup uses
normal unmount, never lazy detach, before deleting any session directory; busy or uncertain mounts
preserve state. Finalized bundles cannot be mounted for a new launch. A failed launch that reached
execution retains an unclean journal before any transient cleanup.

`tests/linux_skill_mount_test.sh` runs the real mount helper and generated Bubblewrap command as a
non-root UID in an isolated Linux container. It verifies writable/executable ordinary files, both
aliases, readonly system entries and persistence after unmount plus session deletion. A separate
busy-mount case proves cleanup refusal. These are actual mount tests but do not exercise systemd
service startup, real Claude exit status propagation, Server transfer or Docker Sandbox acceptance.

Managed Native supervisors retain the original tmux pane until its exit status can be inspected.
Only an observed zero tool/pane exit lets the supervisor exit successfully; missing panes, killed
servers, invalid status or nonzero exits fail closed. Managed systemd units use RemainAfterExit so
natural success remains inspectable until stop. Reconciliation treats an exited unit with an empty
cgroup as inactive. Stop retains clean pre-stop evidence only when the unit already exited and the
complete cgroup was empty; clearing/collecting the unit cannot turn an unknown or killed exit clean.
A helper-private termination receipt is fsynced before capture so a quota or upload failure cannot
change a previously proven termination classification on retry. Such a receipt blocks relaunch.

Managed Native stop first signals only the supervisor to request Claude's terminal exit workflow:
one Ctrl-C, then a second after one second if the pane remains alive. The supervisor only reports
success if the original pane returns a normal zero status. The Helper waits up to ten seconds for
that outcome and for the entire cgroup to empty before stopping the retained unit. A unit that does
not cooperate uses KillMode=mixed and TimeoutStopSec=10s: systemd ultimately kills the full cgroup,
and the Helper retains its content as unclean. Keyboard interruption in canonical terminal mode may
kill the pane rather than yield a normal status; that case must also remain unclean. These timings
are bounded runtime behavior, not claims that arbitrary third-party applications flush in ten seconds.

`tests/linux_skill_systemd_test.sh` builds the actual runtime binary and exercises Engine.launch,
network namespaces/firewalls, tmpfs, the supervisor, tmux, Bubblewrap, unit/cgroup inspection, capture
and final cleanup under an isolated systemd PID 1 with a private writable cgroup namespace. It mounts
no host filesystem or host cgroup tree. Synthetic terminal programs exercise zero/nonzero exit,
SIGKILL, cooperative input-driven exit and forced timeout. Server retention acknowledgements remain
explicit test inputs; this is not Claude or Server end-to-end acceptance and not Docker Sandbox.

Configuration import execution gets a typed, exact-task authorization through `internal/api`, then
validates Node/user/account identity in the worker. `internal/toolaccounts/import_config.go` preflights
the entire bounded batch and current mode before any existing account configuration is written.
Ownership denial from task start is saved in the task ledger and reported as a stable failure;
completed task replay never reruns import or rechecks a later directory as a new write.

Configuration import execution runs behind the runtime helper as specified in
`docs/skill-account-fence.md`. The worker owns control-plane authorization only; the helper owns
paths, execution identity, persistent account fences and exact-input import receipts. Config bytes
are transient request data and never enter receipts or logs. Takeover and import mutations share
the helper's serialization boundary; a persistent fence outlives both task leases and processes.

The account fence also gates legacy Native/Docker startup, binding and backend migration before
filesystem mutation. Helper-selected account paths bind the fence to the actual target, including
Docker callers. Native launch verifies the fence again; only an exact retained managed snapshot
can use the existing protected mount path. Delayed tasks cannot self-declare managed mode. This is
writer admission, not proof of legacy process quiescence, and must not stop existing sessions.

First account capture follows `docs/skill-account-capture.md`. Reconciliation summaries cannot
prove takeover quiescence. The Helper must retain an immutable binding and ordinary private copies
before upload; published retries read those copies, never a potentially changed original directory.

`open_account_capture_file` is a read-only Helper operation for a previously sealed capture. It
accepts exact binding plus manifest/object selection, never a host path. Object selection additionally
matches Helper receipt and tree digest. The serialized opener checks the durable fence, capture and
manifest membership before transferring one read-only regular-file descriptor via SCM_RIGHTS.
Small response metadata and a bounded ACK remain on the socket; complete manifests and file bytes
never enter Helper JSON frames, logs or task results. The client rejects mismatched metadata, writable
or nonregular descriptors, truncated ancillary data and extra descriptors; cancellation closes the
socket and all unadopted handles. This does not initiate capture or enable incomplete backend proof.

Backend migrations persist an immutable whole-migration intent before their supervised backup copy.
Target and rollback ACL commands use separate Helper-selected systemd services. Launcher exit and
complete cgroup quiescence are independent facts; an unknown target writer forbids rollback. The
Helper leaves the original intent pending if any rollback ownership or ACL operation fails, even
when its service has exited. A new terminal failure requires all rollback operations to succeed;
historical failed receipts still prove writer termination only, not restored source permissions.
Helper flushes account/backup filesystems before same-boot terminal completion. Exact completed
receipts replay without mutation even after fencing; unresolved work blocks replacement tasks.
Unresolved local migration/copy history also blocks new legacy binding, session launch and config
import writes, including after Helper replacement. Completed config-import and runtime task replay
remain read-only; the history gate never stops an existing session. Missing skill stores retain
legacy behavior without creating state. A skill takeover fence has priority over migration history.
Native takeover accepts backend-writer evidence only with matching terminal migration and completed
copy records. Local history is checked even if Server inventory omits it. This does not prove Docker
or orphan-process quiescence and does not enable public takeover or capability advertisement.

Docker Sandbox compatibility requires successful, bounded command-specific help for create, exec and
rm. A removed plugin can exit zero while printing a removal notice; this must not advertise backend
support. New binding/session starts recheck before mutable account/runtime preparation. Stop and
cleanup recheck before killing tmux or removing trusted specs, retaining evidence when lifecycle
commands disappear. This is a compatibility gate, not sandbox-wide writer exit proof. A separate sbx
binary or ordinary Docker daemon cannot be silently substituted for the configured sandbox adapter.

Managed Native stop dispatch uses a distinct `skill_finalization` pointer, not the startup marker.
Clean managed termination requires a successful normal exit and an empty whole cgroup observed
before `systemctl stop`. A successful unit result after forced cleanup cannot prove that its other
writers exited normally; preserve such input as unclean even if the supervisor returned zero.
Malformed markers and transient stop failures bypass permanent generic failure caching. Stop and
full original frozen-capture observation precede the bounded 10-second transfer attempt. Runtime
admission ownership is released before the shared transfer gate is acquired; background inventory
may take the admission gate while holding the transfer gate, so reversing that order is forbidden.

Deployment transport has a separate immutable input type in `internal/skillmanager` and authenticated
manifest/file/lease calls in `internal/api`. It validates original owner/account/operation/attempt/task,
plan and complete tree identities without inventing a session. Transport does not publish readiness,
write account heads, invoke the Helper or advertise deployment capability.

Independent Helper deployment preparation follows `docs/skill-deployment-preparation.md`. It stores
a complete private directory under the original attempt without session/spec/process authority.
Only the authenticated worker socket can delegate the validated original Server input. A locally
prepared receipt does not publish Server readiness or advertise execution capability.

Deployment dispatch composes exact Server lease, original content, independent Helper preparation
and dedicated result confirmation. Its metadata-only pending/confirmed ledger and read-only recovery
loop follow `docs/skill-deployment-preparation.md`; generic task terminal APIs remain unavailable.

Dedicated deployment termination transport in `internal/api` distinguishes Server revocation from
Helper drain and final acknowledgement. It preserves the exact original intent/result across reads
and writes, rejects mismatched authority, and never retries an uncertain POST automatically. Transport
alone does not invoke Helper, change the worker journal or enable ordinary deployment scheduling.

Independent deployment failure orchestration lives in `internal/worker`, using dedicated API and
Helper interfaces and the existing atomic task ledger. It checks Server revocation before leased
preparation, persists each authority transition, and recovers termination without a preparation lease.
Requested revocation alone never calls Helper drain. An exact committed intent is required first;
final terminal confirmation additionally requires the saved permanent drain. Background recovery
may complete this path but cannot prepare, launch, mutate sessions, or delete retained content.

Migration writer supervision separates private phase journals in `skillmanager` from systemd execution
and observation in `runtimehelper`; see `docs/skill-backend-writer-recovery.md`. Per-phase authority
must survive Helper replacement without widening Worker privileges or reusing predictable unit names
as launch identity. Server recovery authorization remains a separate boundary.

Backend migration completion recovery is metadata-only and reuses exact original authority. New
writer-version-2 ownership intents precede direct target/rollback ownership writes; ACL receipts alone
cannot distinguish a rollback interrupted before its first command. Same-boot complete phase sets can
converge through original Helper dispatch after repeated passive quiescence checks. Missing/old evidence
or incomplete ownership remains pending, and a failed outcome cannot settle Server recovery gates.

## Explicit passive migration recovery contract

Explicit passive backend migration recovery uses a separate administrator-selected request UUID
and exact original logical task ID. Server pins the original task-record UUID itself, preserves
that task/result, and creates recover_tool_account_runtime:<account UUID>:<request UUID>. The
payload is a version-1 binding of recovery task/record, original task/record, Node/user/account,
tool and source/target. Only a terminal original migration still owning the recovery-required
profile can admit a new recovery. Same-key replay is read-only; another key waits for the previous
recovery to end. User content locks serialize acceptance, fresh Node authorization and results.
The current active owner, affinity, legacy directory, no active sessions, source and profile must
still match. A live recovery task lease and exact poll attempt are required before Helper access
and first result acceptance. Success echoes the exact authorization and recovered=true; failure is
fixed content-free metadata. Original failure remains immutable; recovery success advances only
that original profile and preserves account disable. Failed recovery keeps admission closed.
The Helper operation recover_account_migration bypasses generic caches and uses existing
version-2 receipts. Same-boot recovery may settle completed metadata but cannot begin/copy/chown/ACL/
stop anything. Previous-boot recovery requires an already durable whole-migration succeeded receipt,
complete original copy/target phase and ownership evidence, no rollback, and repeated absence of
all recorded current units and cgroups. It rechecks the original account, backup, unchanged receipts
and current boot without rewriting old-boot metadata. A started old-boot migration cannot be promoted
from phase receipts. Missing/old-version evidence, failed/incomplete rollback, foreign/live services,
invalid account, managed fence or absent backup remain recovery-required. Completed target evidence
is rechecked even if the local whole result already says succeeded. This does not authorize
interrupted ownership repair, rollback attestation or backend advertisement.

A failed managed capture can produce a separate capture_pending observation only after fresh
original writer quiescence and a valid private termination record. It contains the exact original
snapshot binding, retained unclean classification and a fixed content-free failure code, never an
invented finalization record or digest. Worker reports that observation independently of upload and
retries through the ordinary inventory. Managed stop completion may retain a null incoming digest;
this confirms process exit only and never authorizes cleanup or local content reclamation.

Default-limit Native finalization uses a dedicated 15-minute reconciliation/admission-drain budget
on both client and Helper, including mutation-lock waiting and capture. Earlier caller deadlines and
disconnect cancellation still win; ordinary Helper calls keep their existing short timeout. Actual
100,000-entry capture exceeded the former 30-second limit. Sequential materialization, capture and
frozen-object copying reuse a per-operation buffer; hashing, fsync, ownership checks and private
publication semantics are unchanged. See `docs/skill-capacity-acceptance.md` for capacity evidence
and remaining transport/full-runtime limits.

Scoped frozen-object reads use `read_skill_finalization_objects` for one exact Native capture.
The Helper validates the original private session and complete manifest once, then retains a bounded
manifest index and anchored object directory for up to 100,000 sequential requests within 15 minutes.
The initial read-only manifest descriptor transfers its shared kernel lock to the client. Each object
is an original manifest member with a separate read-only, single-link descriptor and shared lock;
anchor metadata is rechecked on every request. Worker upload and frozen SSH export use this reader
while retaining their independent content checks and authorization. EOF/cancellation owns socket
shutdown and lifecycle-lock waiting; a client-held manifest still protects consumption after Helper
exit. No persistent cache, path authority or new journal state is added. See
`docs/skill-finalization-readers.md` for the protocol, tests and actual 100,000-object evidence.


Explicit `recovery_version:1` export negotiation now enables the separate bounded-metadata
stopped-work recovery format above 100,000 entries. Manifest v1 remains capped. Source retains
original sealed authority and complete scan comparisons; files stream before their verified hash
trailer to avoid silent prehash stalls. Gateway retains exact live authorization and complete EOF
verification; CLI uses private hash-addressed disk metadata, validates whole topology/content and
requires footer/EOF/SSH success before publication. Old gateways reject unsupported negotiation.
See the negotiated recovery wire contract in the Node `docs/skill-node-export.md`; actual complete
recovery acceptance remains tracked separately, with no production capability advertisement.


Runtime capability probes are independent read-only observations and do not take the lifecycle
mutation mutex. A separate cancellable probe mutex permits only one active observation per Helper;
waiting for another probe consumes the same three-second deadline and never waits behind copies
or reclamation. The operation retains
peer authentication and request validation, has a three-second total deadline, and bounds command
output and process-group cancellation. No probe starts/stops a session, repairs state, caches an
availability result or enables Skill capability advertisement. Current filesystem/disk and dependency
checks still determine availability. Mutating operations retain their existing serialization.


A dead tmux pane can precede collection of its process exit status. The managed supervisor waits
up to one second for that exact pane's initially missing status, without sending further terminal
interrupts after observing closure. Within this window, at most once per 200 ms, it asks the same
tmux server to run the fixed `/bin/true` child, waking child reaping on versions observed to retain
a zombie without a final pane status. This child does not access skill content and supplies no
termination authority; only the original pane's independently observed status is accepted. Only a subsequently observed canonical zero exit can succeed;
a missing/nonzero/malformed status, a revived pane or transport failure stays unclean. This wait
never replaces the independent pre-cleanup whole-cgroup and original-invocation checks.


Passive backend-migration completion now requires bounded, repeated full account/backup content
and topology equality plus stable private permission/inode evidence; see the bounded inventory
contract in `docs/skill-backend-writer-recovery.md`. Data xattrs participate in content equality;
ACL/capability changes remain separate permissions. Descriptor-relative traversal refuses special
files, nested mounts, external hardlinks and substitutions. No account bytes or fingerprints leave
the Helper. The v1 socket has a 15-minute total budget, owns disconnect cancellation and cancels
lifecycle-lock waits. Worker maintains the exact original Server lease through inspection and result
acceptance, subtracts round-trip delay and cancels on uncertain renewal. Oversized/slow/unverifiable
trees stay pending. This is passive verification,
not interrupted ownership repair, historical backup attestation or exact source-permission rollback.


New whole-migration receipts use version 2 (distinct from copy/writer version 2) and require the
private historical baseline/attestation contract in `docs/skill-backend-writer-recovery.md`.
The baseline precedes any copy, binds original tree permissions/content and Helper-selected parent
identities/ACLs, and cannot be synthesized after interruption. Before target writes, both trees and
parents must match it. Terminal failure additionally requires exact source tree and parent
permissions; successful source-backend ACL commands alone cannot reopen local admission.
Immutable attestation follows repeated synced inventory and writer/quiescence checks. History gates
protect missing/corrupt/orphaned evidence; old whole version-1 records are never silently upgraded.
Default external recovery remains version 1 and passive. Explicit source verification uses version 2
as specified in `docs/skill-backend-writer-recovery.md`. Interrupted repair and incomplete previous-boot
recovery remain separate work and may not reuse original writer identities or overwrite outcomes.


During first original version-2 migration execution only, successfully drained source rollback
phases now lead to descriptor-anchored restoration of the original backup's owners, full modes,
ACLs and capabilities plus the original parent traversal ACLs. The existing immutable rollback
intent precedes those synchronous Helper writes. Repeated full inventory, inode/hardlink identity,
mount boundaries, context cancellation and the final baseline attestation remain mandatory.
No new subprocess is launched, backup/data xattrs/contents are not rewritten, and a partial restore
stays pending. Passive replay/recovery never enters this executor. Explicit interrupted repair remains
separate work; version-2 source verification reopens only an already attested original Server profile.


Interrupted permission repair must preflight shared traversal parents before any account or parent
write. Only unchanged original metadata and the exact access-ACL/mode transformations of the original
target/rollback traversal commands are eligible; an interrupted original chmod-before-ACL restore
is also recognized. Default ACLs, owners, identities, special bits and all unrelated xattrs must
remain original. Unknown named-user/group entries or permissions are preserved and block repair.
Recheck the complete observed parent immediately before restoration. Numeric runtime identities must
come from Helper-owned lookup/evidence, never recovery payloads. This preflight is not independent
repair authority: the future explicit repair action still needs its own durable intent/completion
record and live Server lease; passive verification cannot enter a permission writer.


Independent repair journal primitives use one immutable `migration-repair-intent-...` record per
original task, immutable `migration-repair-attempt-...` records per recovery task/record, poll attempt
and kernel boot, and one immutable `migration-repair-complete-...` record. Intent binds the original
whole-version-2 started record, original Server task-record UUID, source/target, historical baseline
and complete original metadata digest. Original copy must already be durably complete. Each attempt
pins the intent digest and a distinct recovery task identity; completion pins one saved attempt.
All files remain private and metadata-only. No original phase/whole receipt is rewritten. Beginning
repair permanently fences original receipt-writing entrypoints. Pending, corrupt or orphaned repair
records keep admission closed; only a valid completion over unchanged original metadata may settle
that original local history. The Helper must prove quiescence, exact restored content/permissions,
parent metadata and filesystem durability before publishing completion. These journal primitives
alone do not expose a mutable Helper operation or issue Server success.


## Explicit interrupted source repair

Version 3 adds the sole mutating recovery action `repair_source`; its binding has exactly twelve
fields. Default version 1 and read-only `verify_source` version 2 retain their exact contracts.
CLI flags are mutually exclusive, and task key, Server grant, poll renewal and result pin the action.
The Helper requires a whole-v2 started original, durable completed copy and original baseline,
plus repeated passive quiescence of every present phase. No original phase is refreshed. A previous
boot requires absent recorded units and cgroups in the current kernel. Missing ownership phases
can be interrupted work, but missing/corrupt copy or content evidence cannot be reconstructed.

Before writing, the Helper verifies full source/backup content, baseline backup permissions,
original account inode and every shared parent. Parent ACLs must match original traversal effects;
chmod-before-ACL partial restoration requires completed original rollback or existing independent
repair intent. It rechecks observed parent metadata immediately before each write. Independent
immutable intent/attempt records precede synchronous descriptor-anchored permission restoration.
The socket lifecycle lock excludes other Helper work, and Worker lease loss cancels the operation.
No new process or runtime identity is created; content, backup and original metadata stay unchanged.

Completion follows repeated exact source/backup/parent checks, syncfs on every participating
filesystem, quiescence, stable kernel boot and unchanged original evidence. A completed retry only
revalidates; it never starts another attempt or rewrites permissions. A fresh matching Server result
keeps the source backend, marks the original profile `rolled_back`, preserves original failure and
account disable, and releases only that original admission gate. Version-3 failure explicitly says
source restoration is unconfirmed, because a cancelled repair may have made partial permission
writes. Original version-1/2 failure wording is retained. This does not advertise runtime capability.
