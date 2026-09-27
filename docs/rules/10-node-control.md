# 10 Node Control

`docs/skill-lifecycle-acceptance.md` records the production daemon acceptance boundary. The opt-in
runner uses the shipped systemd units, a separate nonroot Worker and real Linux Claude. The
credential-free `--version` case now proves Native start/natural-exit/finalization/publication/local
reclamation across real HTTP and socket boundaries. Real inference/inheritance is a separate opt-in;
neither this smoke case nor its isolated Server capability fixture enables production advertisement.

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

`GET /node/skill-finalizations/{id}/reclamation-authorization` returns current complete Server
content evidence, bound to the original Node/user/account/session/snapshot, finalization/checkpoint,
tree and termination classification. The Node requires a separately saved terminal publication,
strictly parses all fields and rejects redirects, missing/ambiguous fields, changed input and an
exhausted monotonic budget. Server timestamps define a duration, not permission to trust host clock
agreement. HTTP success alone cannot delete Helper data or prove local writer/reference absence.

Native Python discovery is a Helper-local bounded probe of fixed trusted system interpreter paths,
with isolated Python flags, sanitized environment and non-root session credentials. A task or
manifest cannot choose the executable to probe. Preparation and mount reject unverified required
runtime links; optional missing interpreters do not prevent Python-free skills from preparing.

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
inspection/scanning as well as transfer. Per-object checks use only this connection's bounded
1–10-second validity, measured from HTTP request start. Renewal starts halfway through validity;
independent expiry cancels blocked renewal/output. Export verification POSTs close their HTTP/1
connection after each response so periodic renewal does not retain an idle connection across the
server's keep-alive expiry. This per-request policy does not alter other API connection pooling,
retry a failed verification or extend its five-second HTTP deadline. Explicit `/renew` continuation
replaces only a still-live exact predecessor under the original user token.
Header and footer require fresh HTTP proof,
and failed/late renewal is terminal. Original identity and learned facts remain immutable; only an exact live successor may advance expiry. Recovery produces no local-durable finalization receipt.
An absent termination record permits only an unclean observation and additionally requires an
original retained canonical invocation. Corrupt or linked termination evidence never counts as
absence. No termination record is written by export.

`agent-remote-skill-export --snapshot <UUID> --protocol 1` is a dedicated SSH forced command. Device
and SSH-key identities come only from installed forced-command arguments. The short-lived grant
arrives on stdin and is revalidated online during streaming. No caller-selected Node path, command,
digest or Helper binding grants file access. Only a matching existing frozen capture can be sent;
missing data, live work or expired authorization cannot fall back to an empty or partial export.

Deployment drain accepts only the complete original Native deployment binding through the private
authenticated Helper socket. The configured Node and original account fence must match. The same
cancel-aware mutation lock used by preparation waits for any original copy to end before sealing
the attempt. A durable drain receipt proves that local preparation cannot resume; Server terminal
confirmation and worker failure/cancellation integration are separate protocol steps.

Finalization transport is independent of the old startup lease. Only the Helper's frozen original
capture supplies the snapshot/session, manifest and immutable termination classification. HTTP begin
may replace an expired upload with a strictly newer attempt under the same finalization identity;
status never renews. File writes and completion bind the exact upload ID. A file receipt is not full
persistence, and a retained incoming checkpoint is not account publication. Unclean input can only
publish as detached. Client receipt validation and Server authentication do not establish local
writer quiescence or authorize deletion. The worker transfers exact receipts to the dedicated Helper
acknowledgement operation, and requests transient cleanup separately after full terminal publication.

The independent worker finalization loop runs for configured Native nodes with Server credentials,
separately from task polling and its lease. Each iteration scans one sorted page, processes every
candidate despite per-record failures and advances the cursor; the next pass revisits failures.
It records immutable original input before begin, uses status to recover lost acknowledgements,
streams each distinct frozen file digest once per attempt, then records complete persistence before
requesting publication. `superseded` remains retained/pending and cannot create a new publication
attempt automatically. Shutdown cancels the loop and Helper reads. Public logs use a fixed bounded
pending error; private paths and response bodies are not logged. This loop cannot freeze live work. Its separate reclamation request requires fresh
remote content evidence and independent Helper runtime/reference proof before content removal. After each durable receipt, it acknowledges the corresponding Helper phase;
after terminal publication it requests guarded transient cleanup. Lost Helper acknowledgements and
cleanup replies replay saved receipts without republishing Server data. Rebuilding a lost worker ledger
must obtain the full publication before acknowledging an already-terminal Helper journal.

`acknowledge_skill_finalization` accepts exactly version 1, the full original capture, the typed Server
finalization receipt and explicit nullable publication. It requires the configured Node and canonical
retained Native resource, independently of transient specs. It cannot create a finalization, upgrade
legacy frozen bytes, recapture work, select conflict sides, stop a runtime or delete resources.
The Helper stores the complete acknowledgement before advancing local state. A published status in
the finalization receipt alone still requires the separate publication receipt. Superseded maps only
to persisted retention and is never a terminal cleanup grant.

`cleanup_skill_finalization` accepts only the exact terminal capture. It requires the separately
retained matching publication acknowledgement, verifies original runtime identity and passively checks
systemd plus whole-cgroup quiescence. It never issues stop/kill or admits a replacement runtime.
Same-boot cleanup checks existing trusted specs; missing
specs use only the retained canonical binding and Helper-derived resource names. Skill-work mounts
must match original work and unmount normally; tmp mounts must be actual tmpfs and also unmount normally.
Busy, linked or uncertain mounts remain pending. Network deletion is preceded and followed by bounded
namespace inventory; command failure is not treated as absence. Runtime-root removal is parent-synced
before the independent durable cleanup marker. Completed replay checks that resources have not
reappeared and never removes replacements. Both operations have owned 30-second cancellation and
cancellable lock waits, require exact integer metadata and return only the original typed record.

Previous-boot first cleanup instead follows `docs/skill-runtime-recovery.md`: require original private
spec authority and twice prove valid distinct boot plus absent unit, whole cgroup, namespace and all
runtime/work mounts and aliases. It never stops units or removes mounts/namespaces from the new boot.
Missing specs and interrupted directory removal can resume after acknowledgement. Parent sync still
precedes the completion marker; completed replay refuses reappearing resources.

`list_skill_finalizations` requires exactly the configured `node_id` and a canonical session-ID
`cursor` (empty for a new pass). Its response contains `items`, `next_cursor`, `invalid_names`, with
at most 16 ascending items containing exactly `session_id`, nullable `record`, and `code`. A record
retains the original full binding. Otherwise code distinguishes `not_finalized`,
`invalid_retained_state` or `unsupported_backend`. Existing corrupt/linked/incomplete bundles cannot
be treated as absent finalizations. Malformed session names produce an aggregate diagnostic without
returning their paths. Only verified no-follow ENOENT can produce an empty absent-store inventory.
The scan bounds memory, reads directory entries in batches and checks cancellation. Its 30-second
handler owns disconnect cancellation and a cancellable mutation-lock wait. Legacy journals are listed
without upgrading them; only format 1 frozen objects can transfer. The inventory is not a transactional
snapshot; newly inserted earlier IDs appear on the next pass.

`open_skill_finalization_file` is a separate read-only Helper socket operation. It requires the full
original snapshot/owner/epoch binding, a complete frozen journal and the retained Native runtime
identity. Object requests additionally bind the immutable tree digest, clean/unclean classification
and declared object digest. The Helper opens only its private objects, never caller paths or the
work tree. It passes one root-owned 0600 ordinary O_RDONLY descriptor through SCM_RIGHTS; the client
requires CLOEXEC, exact size and metadata, then verifies the manifest or streamed content. Malformed
frames close all received descriptors. The 30-second handler owns disconnect cancellation and a
bounded ACK wait; waiting for the mutation lock is cancellable. Reading cannot initiate capture,
upgrade historical journals, advance local acknowledgement state, stop a runtime or delete content.

Managed Native startup results use the dedicated authenticated
`/node-api/tasks/{logical task_id}/managed-start-result` confirmation. The typed client binds the
original snapshot/task-record/session/account/backend and exact poll attempt, accepts only ready or
Helper-proven stopped outcomes, and excludes paths, UID, arguments and broker material. Receipts
must be committed and echo the exact original outcome. Extra, missing, null, aliased or duplicate
fields fail closed. An unacknowledged result remains uncertain; it is not a terminal ledger entry or
permission to relaunch. The dedicated queue persists the original proposal before publication and
recovers acceptance through exact inspection. This client does not itself establish readiness or quiescence.

Pending confirmation recovery inspects the exact original proposed outcome through the dedicated
read-only managed-result inspection route. A committed exact receipt may be consumed after task
polling has ended. A strictly newer poll attempt fences an absent older receipt before leased
recovery may propose a new outcome; same-attempt absence cannot rule out in-flight publication.
An exact unaccepted cancelled observation at the original or a later attempt separately retires the
proposal, since Server cancellation excludes later acceptance. Retired records stop background
inspection and reject startup replay without Helper calls or confirmation. Expired/pending/live
observations remain pending. Retirement grants no runtime or cleanup authority; finalization runs
independently and still requires its own capture, transfer and acknowledgement.

The internal startup-and-confirmation coordinator keeps the existing startup lease through durable
proposal storage and Server confirmation. A verified committed response wins a concurrent renewal
failure, including when persisting the local acknowledgement fails. Unknown network outcomes retain
the exact proposal and drain the original runtime. Helper-proven stopped recovery may publish only
the bounded stopped result; generic errors cannot become terminal failures. A saved confirmed task
is resolved as historical acceptance through inspection, never as current runtime readiness.

Legacy `create_tool_session` decoding rejects a present `skill_manager` marker instead of discarding
it. The worker routes any such marker, or existing managed-start journal, before generic terminal
replay. Dedicated Native decoding requires the exact four-field pointer and original poll record,
Node, logical idempotency key and positive attempt, plus canonical creation fields. Null/case-aliased
markers, changed task types and malformed pointers remain nonterminal errors with no legacy fallback.
The Helper's legacy path still returns `SKILL_MANAGER_UNSUPPORTED`. No managed capability is advertised.

- Task polling's additive `task_record_id` is the Server database UUID for exact skill-content
  authorization. `task_id` remains the logical StartTask/result/ledger identity. Managed consumers
  must validate the record UUID and must not infer it from a logical task name or mutable payload.

- Snapshot preparation reads bind the exact poll record UUID and immutable snapshot identity on every
  manifest/file request. Downloads must verify the response identities, canonical tree, file length,
  digest and content classification before making staging usable. A Server success envelope alone
  cannot authorize launch or certify complete local materialization.

- `prepare_skill_snapshot` uses a small ordinary Helper request followed by a separate input of at
  most 64 MiB. The Helper requests each file by digest, and the worker sends a completion byte only
  after authenticated download verification. Neither side follows caller-supplied host paths.
  Snapshot and deployment streams own a three-hour absolute upper bound for default-size
  sequential transfers; the earlier caller deadline always wins. Each HTTP download remains
  bounded separately, and exact-attempt lease renewal must continue throughout. Cancellation or
  socket loss also cancels Helper preparation between reads. An uncertain response must be retried using
  the same complete input, since atomic publication may already have completed. This operation
  neither launches nor changes the ordinary descriptor-transfer ACK protocols.

- The preparation stream requires canonical fixed ego-browser version/commit/tree fields and, when
  selected, device Node-release/protocol fields. Unknown system names, incomplete metadata, aliases,
  nulls, duplicates and noninteger protocols fail before file reads. Selection must match the trusted
  runtime spec. Mounting rechecks retained pins and actual selected system files; missing historical
  pins cannot silently acquire today's release. Stop and retained data recovery do not require an
  obsolete artifact to be installed again.

- Heartbeats and reconciliation contain bounded capability and resource summaries, never secret state.
- Poll only with node credentials and lease only tasks assigned by the authenticated control plane.
- Validate task type, identifiers, backend, paths, resource limits, locale, and timing before execution.
- Task execution and terminal result reporting are idempotent by `task_id`; the ledger replays both
  successful and failed terminal results after a lease is reissued, without repeating runtime work.
- Cancellation and lease expiry must stop work when the operation can be interrupted safely.
- Reconciliation reports facts and may identify missing sessions; it never replays prior commands.
- Before first upload, managed frozen input is reported to the exact snapshot's `/termination`
  route through `internal/api`. Its committed stopped envelope must echo every original identity,
  digest, integer generation and unclean flag. HTTP success alone is insufficient. Failure preserves
  the same durable input and cannot begin an upload. No automatic transport retry or partial runtime
  inventory is sent; content persistence and Helper cleanup retain their separate receipt protocols.
- `reconcile_skill_session` is separate from read-only inventory. Its exact Node/session request
  returns not_started, running or finalized with an explicitly nullable original capture. Both
  started and invocation-observed same-boot launches use natural-exit capture. It checks loaded unit invocation, transient
  identity, user and canonical cgroup together. Running observations additionally require original
  spec files and work mount; they do not grant broker admission or report Server readiness. Exited
  observations require a stable repeated unit reading and empty whole cgroup before releasing the
  exited service, then another stopped-unit/cgroup proof before capture. Missing units are unclean.
  Starting records with a loaded unit first require the original private draft, canonical nonzero
  invocation, transient user/cgroup identity and a stable repeated unit observation. Running units
  also require the original published spec and unchanged mount. Exited units accept an absent
  transient spec but reject changed bytes. Persist observed invocation before returning running
  or releasing an exited service; this is not a readiness receipt. Absent starting units require
  repeated absence and whole-cgroup quiescence before unclean capture. Prepared, changed or unknown
  state never permits replacement launch or writer termination. The handler owns its 30-second cancellation, disconnect reader and cancellable lock.
  The background worker reconciles not_finalized candidates and transfers newly frozen records;
  it never sends an inventory page as a complete runtime_sessions snapshot to Server.
- A valid distinct current kernel boot allows passive recovery of original prepared/starting/started
  bundles through retained ready spec authority. Twice require absent unit, cgroup and namespace,
  unchanged-or-missing transient spec, unaliased paths, and no mount or bind alias exposing runtime
  root/work. Newly captured input is unclean unless original termination was already retained.
  Manual stop/cleanup_resources use this path before loading transient artifacts. Common managed
  stop refuses old/unavailable boot identity, preventing delayed operations from stopping new units.
- `drain_unadmitted_skill_session` shares reconciliation's exact request, response and cancellation
  boundary, but may drain a started or invocation-observed original same-boot browser-enabled runtime after the trusted
  worker asserts lost admission. Helper independently checks original launch/draft/preparation and
  loaded invocation, transient identity, user and cgroup; graceful and final stop recheck them.
  Replacements/unknown state/populated cgroups cannot become frozen captures. Browser-disabled
  runtimes remain running under normal spec/mount checks. No replacement nonce or launch is issued.
  Worker startup and background inspection hold the same cancellable process-owned gate; current
  verification must use the initially granted nonce/UID, never a later registration or saved result.
- Device-control activation and deactivation tasks validate every binding member and generation,
  reject unknown fields, and cannot supply commands, executable paths, relay endpoints, or secrets.
  Generation validation matches the Server `BIGINT` range and reserves its maximum for terminal
  deactivation only.
- Full-trust runtime contexts advertise and preserve the complete canonical capability set:
  `observation_mode_v2`, `ax_state_v2`, `adaptive_settle_v2`, `clipboard_payload_v2`,
  `session_full_trust_v1`, `application_launch_v1`, and `global_clipboard_v1`. Partial sets are
  rejected and same-generation reconciliation cannot widen or narrow the set. Legacy
  `per_application_approval` contexts accept only v1, the exact three-name v2 observation base, or
  that base plus `clipboard_payload_v2`; they reject full-trust launch, clipboard, and authorization
  capabilities before writing managed state.
- Ego-browser binding snapshots require `remote_platform=linux`, `local_platform=macos`, the
  independent browser relay kind and channel, a compatible wrapper protocol, positive generation,
  active healthy lease, and a capability set whose allowlist and Site Learning names exactly match
  their verified digests. The wrapper cannot widen this snapshot.
- Ego-browser context is injected into eligible `native` and `docker_sandbox` Claude tool sessions.
  The runtime helper must return the selected non-root runtime UID; the worker strips that internal value, grants the numeric UID a
  narrow POSIX ACL on the broker directory/socket, and the broker matches it with Linux
  `SO_PEERCRED`. UID 0 is ineligible. Docker uses a root-owned trusted spec, executes as the fixed
  UID/GID, mounts the verified wrapper/Skill/broker paths, and never persists or places the nonce in argv.
- The broker enforces the lower of Node and binding parallelism limits, strict wire-order sequence
  submission, one-time permit consumption, admission-time lease renewal, and immediate conflict
  errors. Disconnect or unknown results are never retried automatically.

Privileged helper requests use a versioned local protocol, peer credential authorization, strict operation allowlists, managed-root path checks, and serialized mutation. SSH attach and sync forced commands re-authorize each connection with the server and continue to deny arbitrary commands and forwarding not explicitly approved.

Before executing a configuration import, obtain fresh authorization from the exact task's
`config-import-authorization` endpoint through `internal/api`. Verify task/Node/user/account identity,
directory mode and epoch; unavailable or old Servers fail closed. Never trust directory mode from
the queued payload. Preflight the complete file batch before the first write, rejecting account-level
skills when mode is migrating or managed_v1. This check does not replace takeover's required drain
and local exclusion of already executing legacy writers.

The serialized helper executes imports from validated typed input, with helper-selected runtime
UID/GID and paths. Poll responses and import helper frames allow 16 MiB encoded data while unrelated
messages retain 1 MiB. Import file lists are bounded to 12 MiB encoded plus the existing raw quotas.

After a persistent account fence is closed, delayed legacy launch/binding/backend-migration tasks
return `MIGRATION_PENDING` before side effects. The worker preserves this bounded Helper code.
Completed-task replay remains a saved result, not permission to launch again. Existing stop and
inspection operations remain available so legacy writers can exit normally before takeover.

`prepare_managed_session_spec` bypasses the legacy result cache and returns only a bounded
`spec_ready` receipt with session/snapshot/task-record IDs, backend and runtime UID. Its socket handler
uses a 30-second connection context, cancels on disconnect or unexpected trailing input, and can leave the
mutation-lock wait on cancellation. The typed client closes its socket on context cancellation and
checks every receipt identity. Public failures use `SKILL_SPEC_UNAVAILABLE`; unrestricted filesystem
or launch-input errors do not cross this operation. Existing descriptor-transfer ACKs are unchanged.
Cancellation is checked between existing filesystem/ACL preparation steps and before sealing ready;
a cancelled operation may leave an intent or draft for exact recovery. No process is launched.
New specs carry `managed_skills`; both content preparation and launch require their ready receipt.
Content preparation also checks the original task-record UUID and complete snapshot input digest.

Snapshot preparation renewal uses `/node/skill-snapshots/{id}/lease`, the task-record UUID and poll
attempt. The typed API client validates the complete Node/user/account/session/backend/snapshot/task
identity, exact attempt and bounded server-relative duration. Worker budgets subtract request latency
and avoid comparing host wall clocks. Any uncertain renewal cancels dependent work, including HTTP
and Helper streams. Completion joins the renewal loop and checks that the last granted deadline has
not passed. Lease loss is not a terminal task failure and must not release retained preparation.

The internal coordinator refreshes authorization before reading a snapshot or contacting Helper,
then binds spec and content preparation to that same complete input. It returns only a preparation
receipt, never runtime readiness or a create-task success. Native queue dispatch uses the separate
startup-and-confirmation coordinator with original broker ownership and nonterminal error handling;
no backend capability is advertised.

The separate internal startup coordinator now extends that same owned lease through launch recovery,
preparation, runtime peer admission and startup. It calls `recover_managed_session` first. A five-field
`not_started` response (status/session/snapshot/task-record/backend) allows exact preparation; an
existing intent must recover through its original private authority and current runtime evidence.
An original completed invocation must still match. Absent/non-running units enter retained stop/
finalization and return `SKILL_START_STOPPED`; historical success cannot return a current running
result. Recovery never starts a new unit. Corrupt or linked intents do not authorize preparation.

After any uncertain Helper attempt, `cancel_managed_session` receives the same full request and
logical ID under a fresh 30-second context. No intent returns `not_started`; an existing exact intent
must pass private binding checks, original active invocation checks and common writer stop/finalization before returning the same five
fields with `stopped`. This remains necessary if a successful launch response races lease loss.
Client receipt checks reject extra/missing/foreign fields. Cancellation does not remove retained work
or change historical readiness. Worker errors remain nonterminal until finalization/reconciliation;
dedicated Native dispatch must not cache these errors as generic failed tasks.

`start_managed_session` takes the exact `ManagedSessionSpecRequest` and logical request ID used for
spec preparation. It bypasses generic result caching and shares the managed socket's 30-second
connection/cancellation boundary. The typed response verifies all identities, unit, UID, tmux name
and canonical owner/workspace/account path suffixes. A completed launch returns the historical
`running` result, not current liveness. Active incomplete recovery checks the exact transient service
before and after readiness and never remounts work; absent/non-running units enter common stop and
finalization. `SKILL_START_STOPPED` requires retained-state finalization; `SKILL_START_PENDING` keeps
the original attempt unresolved. Neither authorizes replacement launch or terminal failure caching.
Cancellation after launch uses a fresh bounded quiescence/cleanup context before releasing mutation
ownership. Native worker dispatch now confirms ready/stopped outcomes. Finalization transport and
complete cross-boot task convergence and real kernel-reboot acceptance remain required; the local operation itself does not renew the
Server task lease.

Managed startup's broker peer adapter clears untrusted task context and separates `authorize` from
`recover`. Initial admission may call `AuthorizeToolSessionPeer`; recovery must use read-only
`VerifyToolSessionPeer` for the existing session/nonce/UID. Repeated registration does not prove an
original grant. A restarted or revoked broker peer remains unavailable; the startup coordinator then
drains the original runtime under its exact cancellation request and retains nonterminal state.
Recovery never issues a replacement nonce/UID grant. Factory rollback removes only a newly created
matching registration, so a stale rollback cannot revoke a later registration.

Managed stop tasks carry original snapshot/preparation/user/account identity in `skill_finalization`.
Only Native is accepted. A successful generic Helper stop map is insufficient: exact reconciliation
must return a frozen object-backed capture matching the pointer. Process stop and local freezing
finish before the 10-second Server saving attempt. Failure preserves the independent recovery work;
completion sends an immutable stopped result and the original snapshot UUID as operation ID. Server
requires the separately committed termination evidence before accepting that result. Replay resends
only that exact receipt and cannot claim current data persistence or publication.

The optional two-kernel VM proof (`tests/linux_skill_kernel_reboot_test.sh`) runs without privileged
Docker or host devices. Its seed stage keeps real managed writers alive until the VM owner cuts power;
the second kernel must recover unclean data without tool replay. Synthetic receipt acknowledgement
can test retention/cleanup but cannot stand in for the complete Server/worker/Claude acceptance.

Native takeover queue execution follows `docs/skill-account-capture.md`. Refresh exact Server
authorization before Helper access and hold one current-attempt lease through capture and transfer.
Use the Server reservation and immutable Helper capture for restart recovery. Never cache transient
takeover failure or use generic result-cache replay as authority. Committed replay uses the exact
original Server receipt; neither task completion nor upload permits local source/capture deletion.

Deployment requests use `/node/skill-deployments/{attempt_id}` with the exact task-record UUID and
current poll attempt. Manifest responses are bounded to 64 MiB; file responses require the original
manifest entry's exact size, digest and classification with no redirect or content transformation.
Every identity and the original plan digest is revalidated. Lease receipts bind that same input and
current poll attempt and carry only a short server-relative time budget. No automatic HTTP replay,
filesystem publication or execution success follows from content transport alone.

`prepare_skill_deployment` uses the authenticated private Helper socket and the independent
attempt/input binding in `docs/skill-deployment-preparation.md`. All bytes remain bounded and
verified; disconnect cancels unpublished preparation. Local success grants neither process launch
nor generic task completion. The worker submits durable original receipts through dedicated Server
confirmation/inspection under the current poll lease. Lost responses use read-only inspection;
cancelled or expired observations alone cannot authorize local cleanup. Server scheduling requires
explicit policy and a compatible report; Node capability advertisement remains gated.

Deployment dispatch first recovers any committed Server revocation before preparation. Dedicated
termination recovery runs independently of task polls and backend admission: it recovers original
intent, permanently drains the same Helper attempt, and confirms only the durably saved receipt.
Unknown responses stay pending. Failed preparation uses bounded classifications, never raw errors;
uncertain success confirmation stays in its success journal unless Server has committed revocation.
Confirmed success cannot be downgraded by a stale termination writer. Server polling schedules only
explicitly compatible pending targets; Node capability advertisement remains gated. First-use proof
boundaries are specified in `docs/skill-first-use-acceptance.md`.

Backend copy and ACL supervision follows `docs/skill-backend-writer-recovery.md`: explicit launch UUID
in a root transient unit description, durable invocation binding, no relaunch of saved intents, and
complete cgroup checks before recording terminal evidence. Cancellation retains the service; only a
revalidated already exited original unit may be cleaned up. Observation is internal, not a new
Worker recovery grant or user-facing recovery command.

Original migration replay may passively finish complete same-boot phase metadata as described in
`docs/skill-backend-writer-recovery.md`. It must not launch missing phases, restart copy, change
ownership/ACLs, stop a live writer or override any original terminal outcome. New recovery commands
and authorization of interrupted work remain a separate unfinished control-plane boundary.

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
Long finalization uploads rotate the exact-capture object reader between files after ten minutes,
before its independent fifteen-minute connection deadline. Acquire the replacement read hold before
closing the previous reader; keep the outer upload hold and Server attempt unchanged. This is reader
lifetime management, never upload renewal, recapture or permission to skip byte verification.

The additive `RenewNodeExport` control-plane transport calls the separate `/renew` endpoint with
the same exact Node/snapshot/forced-command device/key and current memory-only grant. It validates
the strict successor envelope, exact predecessor SHA-256, bounded credential shape, and current
permission identity without retrying uncertain requests. HTTP/1 verification/renewal connections are
not reused and keep the five-second request budget. The gateway consumes this transport under
the renewable lifetime rules below; existing `/verify` stays fixed-expiry.

## Renewable export transport lifetime

Gateway continuation exchanges the current memory-only grant through `/renew`, retaining exact
predecessor linkage, original binding and all learned capture facts. A successor is accepted only
while the old connection-local window is still live; a late response cannot revive it. Original
user-token expiry/revocation still ends export. `/verify` remains the initial check and the legacy
authority fallback; fallback permissions cannot advance expiry. No uncertain POST is replayed.

A complete transfer no longer has an independent fifteen-minute ceiling. Initial metadata/scanning
and stopped-work final verification remain bounded phases of fifteen minutes each, and earlier
caller cancellation/deadlines win. Gateway and Helper bound each output write block (at most 32 KiB)
to thirty seconds and close transport on failure. CLI bounds gaps between incoming bytes to thirty
seconds and retains separate fifteen-minute initial/final scan waits. Each scan wait ends when
its first prefix byte arrives; the remaining prefix bytes use the ordinary progress bound. Frozen descriptor readers
rotate between objects after ten minutes; a replacement is acquired before the old reader closes,
and the outer immutable manifest hold remains throughout. Complete hashes, exact source recheck,
private staging and required footer/EOF/process success are unchanged. No capabilities are enabled.


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


## Explicit source restoration verification

`recover-runtime --verify-source` submits `action: "verify_source"`. Its immutable binding is
version 2 with exactly twelve fields, including that action. Default recovery retains version 1
and its original eleven fields; an absent action is never serialized as null. Request keys cannot
change actions. Authorization, renewal and successful results echo the exact versioned binding.
The Helper only verifies an already terminal whole-version-2 failed original with its pre-copy
baseline, immutable failure attestation, copied backup, target/source ownership intents and all
three successful source rollback phases. Repeated complete content/permission/parent checks and
writer quiescence are mandatory; previous-boot records additionally require current unit/cgroup
absence. Verification never repairs permissions or settles incomplete receipts.
Only a fresh matching successful verification marks the original Server profile `rolled_back`,
keeps the source backend and releases that profile's admission gate. Original task/result and
account disable remain unchanged. Failure retains the gate; terminal replay cannot change a newer
profile. This does not implement interrupted repair or enable any runtime capability.


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
