# 09 State And Persistence

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

Reclamation authorization transport adds no new Node journal phase. Its Server response echoes a
fresh per-request UUID and describes current remote content; the monotonic deadline exists only in
the requesting process. Never persist that deadline or reinterpret a saved response as a fresh grant
after restart. Durable local intent and explicit reclaimed-content inventory use the separate primitives
described above and the separate Helper operation. Existing acknowledgement and runtime-cleanup
records do not themselves delete retained bytes.

Native Python dependency mappings live in the existing snapshot capture policy. New preparation
discovers only fixed adapter-owned paths and seals their version/ABI/architecture identities. Exact
preparation retries reuse the sealed map, including old empty maps, rather than adopting newly
installed interpreters. First mount revalidates the sealed dependencies before exposing work; a
missing or changed interpreter blocks launch but never prevents stopped capture or export. Existing
generic test-only `python3` identifiers are not silently upgraded into verified production identities.

Stopped-work export is a separate read-only recovery stream, `stream_stopped_skill_export`.
Only verified absence of the finalization directory permits it; an existing incomplete, linked,
legacy or corrupt capture never falls back to work. The Helper holds its cancellable mutation lock
through scanning, streaming and final checks. Lock acquisition and initial/final scans each have
a 15-minute bound; progressing authorized transfer has no separate fixed ceiling. It requires the exact sealed
Native snapshot, retained ready spec/launch authority, and repeated
passive whole-cgroup quiescence; previous-boot reads additionally require absent runtime resources.
It does not stop, reconcile, freeze, fsync content, allocate another content copy, acknowledge or
clean up. Source metadata and the full tree digest are checked again before completion. Export
retains the protocol's 100000-entry bound and signed 64-bit expanded-size validation, while
streaming the complete observed byte count independently of runtime admission quotas. A fixed
10 GiB byte ceiling must not prevent recovery of over-quota work. Metadata/frame limits, bounded
buffers, live authorization and transfer deadlines still apply; entry overflow remains a failure. Gateway reauthorization runs during initial
inspection/scanning as well as transfer. Recovery produces no local-durable finalization receipt.
An absent termination record permits only an unclean observation and additionally requires an
original retained canonical invocation. Corrupt or linked termination evidence never counts as
absence. No termination record is written by export.

An independent deployment drain writes one immutable private
`deployment-drain-<attempt UUID>.json` under SkillStateRoot. Its schema-1 receipt binds the complete
original deployment identity and a Helper-generated UUID. Atomic no-replace publication and parent
fsync precede acknowledgement; replay revalidates ownership, permissions, identity and fsync.
The record permanently denies preparation of that attempt. It preserves absent or complete bundles
and corrupt content bytes without claiming availability or authorizing reclamation. An existing
bundle must retain its safe matching preparation identity before the first drain. It requires the
existing original account fence and never repairs or advances it.

Managed runtime admission is process-only, keyed by original session and retaining only nonce/UID.
Only initial successful peer authorization may populate it; broker registration, historical Server
readiness and runtime recovery cannot recreate it. Worker copies share its cancellable gate and map.
Startup owns that gate until its original admission is recorded, so background inspection cannot
drain a just-launched runtime based on an earlier missing grant. Verified frozen records and
not-started observations remove the matching entry. Never write this map or broker nonces to the
startup/finalization ledgers.

- Configuration contains node credentials and requires deployment-controlled permissions.
- Registration writes credentials atomically and must not disclose them in output.
- The ledger stores only bounded local task execution metadata needed for idempotency.
- Finalization transfer metadata uses a separate `<ledger_path>.skill-finalizations` ledger, keyed
  by the original snapshot UUID. Schema 1 retains the full Helper capture plus nullable exact Server
  finalization and publication receipts. Persist original input before begin, current upload before
  files, and incoming checkpoint before publication. Use the existing atomic/fsync/CAS ledger
  operations and preserve integer generations. No manifests, content, credentials or per-file bytes
  enter this ledger. The idempotency key is always `skill-finalization:<snapshot UUID>`.
  Original capture is durable before the first exact Server termination observation. An uncertain
  stopped receipt is retried with the same capture; it does not advance content retention or the
  Helper journal. No extra local receipt phase is necessary: a known upload already proves Server
  accepted terminal authorization, and a terminal saved publication never repeats termination.
  Incoming and merged-result checkpoints are separate fields; a newer upload requires a greater
  attempt, and a retained incoming checkpoint cannot change. Lost responses resume through original
  status or an exact idempotent request. A saved publication decision is immutable here, including
  conflicts/detached/superseded. The worker transfers validated receipts through the dedicated Helper
  acknowledgement operation; the worker ledger is never itself a privileged cleanup record.
- Ledger updates publish a private temporary file after file sync, then rename and sync the parent.
  Readers receive detached JSON values. Failed writes never expose an uncommitted in-memory entry;
  uncertainty after rename blocks further reads and writes until reopen. An existing empty or null
  ledger is corruption, not proof that no task ran. The existing JSON map remains the disk format.
- Managed startup uses distinct `managed_start_pending` and `managed_start_confirmed` entries.
  Their schema-1 payload contains only the complete snapshot binding and exact bounded outcome;
  logical task identity stays in the ledger key. Persist the pending record before sending its
  confirmation. Conditional updates compare the entire original record so background inspection
  cannot overwrite a newer attempt. Replacing an older proposal requires an exact unaccepted
  Server observation from the new, strictly greater poll attempt. Same-attempt proposals are immutable.
  Confirmed records cannot be downgraded. A distinct `managed_start_retired` entry retains the
  original schema-1 payload plus an exact `retirement` observation: same outcome, accepted=false,
  task_status=cancelled and current attempt at least the proposed attempt. Only pending entries can
  retire, using full-record CAS; stale cancellation cannot replace a newer or confirmed proposal.
  Pending/confirmed formats remain readable unchanged. Retirement never confirms readiness, deletes
  content, authorizes Helper cleanup or replaces finalization. All three states bypass legacy replay.
- Tool login state, browser profiles, account archives, and session resources stay under configured managed roots.
- Runtime identities and paths derive from validated server IDs and configured roots, never unchecked task paths.
- Device-control activation manifests contain binding and generation metadata only. They are
  atomically stored as owner-only files beneath `device_control_root`; connection tickets,
  certificate pins, exporter data, screenshots, and action payloads are never persisted there.
- Relay tickets, peer certificate pins, and exporter contexts exist only in the live bridge call
  stack and are discarded when the generation listener closes.
- Ego-browser state persists only the binding/generation-scoped next sequence needed for replay
  prevention. Relay tickets, request permits, session keys, key wraps, ciphertext, heredoc text,
  browser output, artifacts, and device credentials remain in bounded process memory and are
  cleared on broker restart or generation change.
- Browser-request cancellation stores only its exact three-field completion result or a fixed
  content-free failure in the task ledger; unrestricted broker error text is never persisted.
- Ego-browser broker lifecycle logs use finite error codes; socket, filesystem, protocol, and
  relay error text is never rendered into operational logs.
- Managed official Skill and wrapper artifacts are installed into immutable version directories
  from a release manifest that pins source provenance and SHA-256 digests. Before either backend
  starts Claude, the helper verifies those sources and refreshes the account's managed Skill copy;
  runtime projects cannot replace the verified host artifacts or injected wrapper-first PATH entry.
- Each Docker Sandbox resource has a root-owned, owner-only trusted spec containing its kind,
  runtime UID/GID, tmux and sandbox identities, and enabled feature paths. Nonces remain process-only.
  Binding specs are excluded from tool-session reconciliation and cannot carry tool-session features.
- Writes that affect authorization, keys, services, or configuration must be atomic where possible and preserve recoverable failure behavior.

State format changes require compatibility tests or a documented migration. Install and upgrade scripts must remain idempotent and must not overwrite valid operator configuration unexpectedly.

A prepared skill bundle contains a root-protected baseline outside its runtime-owned `work`
subdirectory. The baseline records both source and materialized manifests; owner-write/search
normalization is not an account edit. A failure before atomic bundle publication leaves no usable
partial copy. The eventual bundle parent must be under independent SkillStateRoot, with only the
work subtree mounted into the session; SessionRoot cleanup must never own this data. Preparation
requires enough expanded disk space plus the configured reserve (default max(2 GiB, 5%)).

Prepared skill bundles seal `snapshot.json` as helper-owned 0600 metadata before launch. Runtime
capture fsyncs ordinary files and directories; it never follows arbitrary external links or removes
original input after a failure. New finalizations copy each distinct regular-file digest to private
helper-owned 0600 `finalization/objects/` content. Objects, `manifest.json` and `record.json` publish
together through fsync and no-replace directory rename; `objects_version=1` records this guarantee.
Helper finalization admission includes the configured filesystem reserve. The frozen input survives
work-directory or transient-spec removal. Journal reads revalidate the binding, owner/mode and complete
tree digest. Retries cannot change clean/unclean classification, recapture a corrupt journal, or
repurpose a snapshot for another owner/epoch. A missing runtime spec is not evidence of finalization.
Published/conflicted/detached Server-retained states permit session-view deletion; this does not
itself authorize local content cleanup. The dedicated Helper acknowledgement and background transient
cleanup are integrated; complete lifecycle/Server deletion reconciliation still needs verification.
Metadata uses bounded streaming writes: manifests share the Server's 64 MiB transport ceiling,
baselines allow both manifests plus envelope overhead, and small journal records are capped at 1 MiB.

Historical manifest-only records remain readable with `objects_version=0`. Only the writer-quiescent
finalizer may add objects, copying exactly the original manifest bytes without recapturing or changing
termination/state. It atomically publishes objects before the version marker. Recovery validates the
entire original object set before finishing that marker; missing/corrupt existing objects are not
repaired. Missing or changed original work keeps the old receipt pending. Read-only transfer never
upgrades a journal. Neither format grants deletion of local work or frozen content.

`finalization/acknowledgement.json` retains schema version 1, the full frozen capture, exact typed
Server finalization receipt and nullable publication receipt. The authorized Node worker supplies
these after HTTP validation and worker-ledger fsync; this is delegated worker authority, not a
cryptographic Server signature. The Helper revalidates the original Node/session/runtime binding.
It fsyncs and atomically publishes the acknowledgement before advancing `record.json` through
supported phases. Replay resumes intermediate states; duplicate acknowledgement re-syncs the directory
without rewriting its bytes. Changed inputs, older uploads, replaced incoming checkpoints and changed
existing publication decisions are rejected. Superseded retains persisted state and cannot authorize
transient cleanup. Low-level state transition functions remain unavailable as socket operations.

`finalization/runtime-cleanup.json` records successful transient cleanup with the exact terminal
capture and Helper-selected original session root. It requires the retained publication acknowledgement,
and is private, no-follow, singly linked and immutable. Runtime-root deletion is followed by parent
directory fsync before this marker is published. Replay refuses reappearing runtime roots, writers or
network namespaces rather than deleting replacement resources. A configuration change cannot repurpose
the marker for another root. Neither acknowledgement nor cleanup removes work, objects or snapshot
references. Interrupted first cleanup from an earlier boot uses repeated passive absence proof in
`docs/skill-runtime-recovery.md`, preserving original authority and refusing current mounts/resources.
No new reboot marker changes the original runtime boot or frozen termination classification.

SkillStateRoot defaults to `/var/lib/agent-remote-skill-state`, deliberately outside the installer's
worker-owned data directory. The privileged opener rejects overlap and symlink aliases with runtime,
account, workspace, browser and broker roots, and validates every ancestor through directory
handles without following links. Existing unsafe roots are rejected rather than silently repaired.
The store is root-owned 0700; root-owned sticky ancestors such as /tmp are allowed. Policy defaults
are 1 GiB per checkpoint, 10 GiB per complete directory, 100,000 entries and max(2 GiB, 5%) free
space. Configuration alone does not authorize capture or advertise a backend capability.

Session-indexed bundles use `session-<session UUID>/` beneath SkillStateRoot. Atomic preparation
publishes work, baseline.json, snapshot.json and runtime.json together. runtime.json contains only
helper-selected backend/resource/boot/UID/GID values. An identical retry validates the sealed
receipt without re-reading source objects or modifying learned state. An existing incomplete bundle
is an error, never treated as a missing session or overwritten. The old low-level bundle primitives
remain available for tests; production preparation must use the atomic session preparation API.

Transferred preparations additionally seal the original task-record UUID and complete snapshot input
digest. Both fields are absent in older internal bundles and must be present together in new streamed
preparations. Old records remain readable for finalization/recovery, but cannot be silently upgraded
or replayed as proof of a different input. A changed system pin, member resolution, generation or
starting checkpoint conflicts even when the ordinary file tree happens to be identical.

New prepared bindings also retain typed `system_releases` pins. An absent field in older internal
records remains readable for stop/finalization/recovery, but cannot authorize a mount using current
artifacts. Partially populated malformed pins make the record invalid. A binary upgrade cannot substitute its
new embedded system skill for a different reserved release. Device Node release identity comes from
the Helper build's `config.DefaultVersion`, which the existing release build embeds through ldflags.

`skill_snapshot_id` is optional in the existing session spec for legacy compatibility. Managed
specs require a valid UUID and tool-session kind. Old binaries that reject this new field must not
be used for managed sessions. Root configuration changes that hide retained state fail closed;
operators must restore access to the original store before cleanup or recovery.

`termination.json` records the helper-proven clean/unclean result and snapshot binding before capture.
It is helper-owned 0600, fsynced and atomically published. Capture failure preserves it, and retries
cannot reclassify the same stopped work using an unloaded unit's later status. A termination receipt
blocks mounting the bundle again even when no finalization manifest exists yet. A complete older
finalization journal remains authoritative without requiring this additional pre-capture receipt.

Configuration import writes anchor the configured account root and walk user-controlled descendants
through directory handles with no-follow opens. Preflight every destination before creating account
directories or writing files; a symlink in any existing ancestor or destination rejects the batch.
Each write reopens the path through the anchor, writes and fsyncs a private temporary file, atomically
renames it and fsyncs its parent. Configured-root aliases may be resolved once; account paths never
supply alternate root authority. This prevents symlink substitution from redirecting a non-skill
import into skills or outside the account; it does not replace takeover's writer exclusion.

SkillStateRoot account fences and import receipts follow `docs/skill-account-fence.md`. Once present,
a fence cannot be removed by a legacy grant. Import receipts preserve started/succeeded/failed state;
incomplete started records require recovery rather than silently repeating the write. All records
use private no-follow reads and fsync-before-rename publication, bound to Node/user/account/task.

Private JSON records are flat names opened with descriptor-relative `openat(O_NOFOLLOW)` against
the verified private directory. Do not rely on `os.Root.OpenFile` flags to reject leaf links: its
resolution may turn a dangling link into a misleading not-found outcome. Both dangling links and
links to valid private records must fail closed, including at legacy runtime admission.

Account takeover bundles follow `docs/skill-account-capture.md`: helper-private `record.json`,
complete `manifest.json` and digest-addressed ordinary objects publish atomically beneath independent
SkillStateRoot. Incomplete/corrupt existing bundles are retained errors, never capture retries.
Original paths and their permissions remain unchanged; helper capture receipts contain identities,
digests and source existence only. No capture or upload success authorizes original-source deletion.

`account-copy-<logical task SHA-256>.json` records only immutable copy identity, an input digest,
boot/unit identity and started/copied/failed phase. It uses the existing private no-follow JSON,
fsync-before-rename and no-replace intent primitives. No account bytes or credentials enter this
record. Completing copy evidence requires same-boot whole-cgroup exit proof; terminal records cannot
be changed. Copy evidence does not authorize backup deletion, rollback or whole-migration replay.

`migration-account-copy-<logical task SHA-256>.json` binds the original copy intent and complete Helper
execution configuration. Its started intent precedes backup creation; terminal succeeded/failed state
requires the original completed same-boot copy, all ownership/ACL writers drained and account/backup
filesystem sync. Terminal outcomes are immutable. Exact replay revalidates both records, preserves
the original result and does not mutate account state. A failed outcome does not certify successful
rollback. Started records require recovery and block replacement tasks; neither migration nor copy
metadata authorizes backup deletion. Old copy-only records remain insufficient writer evidence.
New execution must retain started state on every rollback error, including a known nonzero ACL
exit or identity/ownership failure. This tighter completion gate does not reinterpret historical
failed receipts as verified rollback evidence or add automatic recovery authority.

Managed Native spec creation retains `spec-intent-<session UUID>.json` and
`spec-draft-<session UUID>.json` under independent SkillStateRoot. Intent binds the logical request,
exact task-record/snapshot/owner identity, complete snapshot input, launch parameters and Helper
configuration through a digest. The draft stores the complete spec with its broker nonce omitted;
it is private 0600 data, not a log or task result. A ready receipt requires the exact draft digest.
Drafts and initial intent use no-replace publication; the terminal ready receipt is immutable.
All private JSON reads reject hard links as well as symlinks, unsafe ownership and permissions.

Runtime `spec.json`, `timezone` and `resolv.conf` publish with no-follow checks, fsync and no-replace
rename. Existing bytes must match. Ready replay verifies files, private draft, current numeric runtime
identity and original boot; it cannot recreate missing state. Pending recovery reuses the retained
draft and refuses an existing unit or work bundle. A draft from another boot remains retained for
explicit recovery. These records certify spec preparation only, not Server authority, current lease,
process absence after launch, content preparation or runtime readiness.

`session-launch-<session UUID>.json` binds the original ready spec, boot and unit. Its no-replace
`starting` intent must be durable before systemd-run. Same-boot recovery may advance it to
`observed`, pinning a nonzero canonical invocation after repeated unit/spec checks without certifying
readiness. `started` seals readiness only for that same invocation. Existing schema-1 starting/started
records remain readable; older binaries must not process the additive observed phase. Every phase
requires the original private spec intent/draft. Once observed or started, invocation
identity is immutable. A completed record certifies only historical readiness; replay does not
recreate transient files or restart the process. An incomplete record never permits another launch,
even when no unit remains. Stop/finalization receipts retain failed or cancelled work independently
of this launch record. These records carry neither broker nonces nor Server lease authority.

Session-addressed launch discovery binds the stored launch to the verified retained snapshot's
Node/user/account/session/snapshot/task/backend and runtime boot/unit. It revalidates the original
ready intent and draft. Only absence of the launch filename means not_started; missing authority
inside an existing launch is corruption. The Helper additionally compares draft preparation digest,
UID/GID and managed request identity. A missing transient spec cannot replace this durable authority.

Previous-boot prepared bundles may discover their original ready spec before a launch intent exists.
Discovery binds every original owner/task/snapshot and verifies the private draft digest. Absent,
starting, observed and started launches require distinct valid current boot plus complete passive resource
absence before capture. Existing corrupt launch records remain errors. New reboot captures are
unclean; existing termination and frozen receipts retain their exact classification and input.

Immediate stop saving and background inventory share one pointer-owned finalization coordinator.
Its cancellable gate owns lazy opening and every transfer using the single ledger instance. Never
open another mutable ledger on the same path. Stop task receipts contain only original session,
snapshot operation, digest and clean/unclean identity; saving phases belong to the independent
ledger and Server status, and must not be frozen into task replay. The Helper capture remains the
recovery source even when acquiring the transfer gate or opening the worker ledger times out.

Native takeover queue execution follows `docs/skill-account-capture.md`. Refresh exact Server
authorization before Helper access and hold one current-attempt lease through capture and transfer.
Use the Server reservation and immutable Helper capture for restart recovery. Never cache transient
takeover failure or use generic result-cache replay as authority. Committed replay uses the exact
original Server receipt; neither task completion nor upload permits local source/capture deletion.

Independent deployments use `deployment-<attempt UUID>/` with a private schema-1 `deployment.json`,
complete work directory and baseline. See `docs/skill-deployment-preparation.md`. Replay verifies
the full original input digest and retained bytes, never repairing corruption or overwriting live
session data. This durable local preparation alone does not authorize a terminal task result.

Worker deployment confirmations use `deployment_prepared_pending` and `deployment_prepared_confirmed`
entries in the single existing task ledger. Schema 1 contains the exact bounded result only. Preserve
integer values with UseNumber; persist pending before HTTP confirmation, then confirm by whole-record
CAS. A new poll proposal requires an exact unaccepted original Server inspection and preserves the
same Helper preparation receipt. The independent recovery loop only inspects pending results.

Independent deployment termination uses the original task ledger key and four schema-1 phases:
`deployment_termination_requested`, `deployment_termination_revoked`,
`deployment_termination_drained`, and `deployment_termination_confirmed`. Save the bounded original
request before revocation HTTP, the committed Server intent before Helper drain, and the exact drain
before terminal HTTP. Preserve any pending preparation proposal in every phase; confirmed success
cannot enter termination. Whole-record CAS fences concurrent queue/background writers. Requested
recovery first looks up Server intent; a new poll may replace only an uncommitted request's poll.
Saved intent, drain, and final accepted poll are immutable. Neither journal authorizes content GC.

New backend migration copies carry `writer_version: 2`, ownership intents and private phase receipts
as specified in `docs/skill-backend-writer-recovery.md`. Starting intent precedes launch, observation
pins one exact invocation, and terminal evidence requires whole-cgroup quiescence. History admission
must inspect orphaned/pending writer records as well as parent migration/copy records. Old receipts
remain readable without being upgraded into invocation or rollback authority.

Exact same-task backend-migration replay can finish saved writer-version-2 metadata only after the
original same-boot phase set is complete and passively quiescent. Existing copy success may complete
its missing copy receipt, but no phase is created or re-executed. Whole outcome publication follows
filesystem synchronization and a second exact phase/quiescence verification. Old-boot or incomplete
work stays pending; a recovered failed result is not source permission or Server reopening authority.

Writer version 2 adds a separate target/rollback ownership intent before identity lookup and direct
Lchown. Its presence, including without any ACL phase receipt, fixes the attempted direction. Phase
journals and terminal outcomes require it; inventory protects orphaned intents. Old started version-1
migrations remain pending because their direct-ownership crash windows were not journaled.

Explicit backend recovery uses the independent recover_tool_account_runtime task identity and
bypasses the generic Worker ledger and Helper result cache. Each delivery freshly authorizes the
exact poll attempt and passively rechecks existing original receipts. It never creates missing
state or begins account work. Only original writer-version-2 same-boot metadata may converge;
already-successful local receipts also require fresh phase/quiescence/account/backup checks.
A previous-boot whole-migration succeeded receipt may be revalidated read-only after repeated
current unit/cgroup absence. Its original boot and every receipt remain unchanged; started
previous-boot metadata cannot converge from historical phase success alone.

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
