# Node-local finalization reclamation

The Server's separate fresh full-content authorization, Node local journal, Linux deletion executor,
kernel-backed read lifetime protection, authenticated Helper coordinator and Worker scheduling are
implemented for original managed Native finalizations. Complete deployed lifecycle acceptance and
Server-retired history policy remain pending.
Existing acknowledgement and transient-runtime cleanup never remove retained work or frozen objects.

## Remote observation

`Client.AuthorizeSkillReclamation` performs an authenticated GET of
`/api/v1/node/skill-finalizations/{id}/reclamation-authorization?request_id=<fresh UUID>`.
The response must bind the fresh random challenge and the original saved terminal acknowledgement:
Node, owner, account, session, snapshot, finalization, incoming checkpoint, complete tree and unclean
flag. A later terminal publication may retain the same original input; attempts cannot regress.
Superseded or pending publication cannot grant reclamation. The same publication ID/attempt cannot
change its terminal decision. A `conflicted` response proves remote availability only: local marking
refuses unresolved conflicts under design §5.2 until a later terminal publication resolves them.

Server holds the user storage lock while validating unretired original metadata and every physical
object's complete length/hash/classification through the no-follow private store. Missing, linked,
corrupt, retired or deleting content refuses authorization despite a readable historical receipt.
The response is `no-store`, bounded to 16 KiB, with no redirects or automatic HTTP retry. Node uses
request start plus the exact 60-second Server duration as a conservative monotonic deadline. All
request time, including full Server scanning, is charged. No wall-clock synchronization is required.
The deadline must never be serialized or reconstructed; the returned authorization is an observation,
not a Server content lease or a cryptographic signature.

The Worker uses `AuthorizeSkillReclamationChallenge` to echo the live Helper-selected challenge on
that same endpoint. It returns only the observation; no Worker deadline crosses the socket. The
Helper's earlier process-local timer, described below, also charges both IPC directions.

## Local journal

`MarkFinalizationReclamation` is a Linux skillmanager primitive behind the separate Helper operation.
The Helper holds lifecycle exclusion and independently proves writer, mount/alias and local-reference
absence. Worker never accesses the privileged filesystem directly. The primitive requires:

- Exact terminal object-backed capture, matching retained acknowledgement and original runtime
  cleanup receipt, including its Helper-selected session root.
- Matching fresh authorization with a live monotonic deadline of at most 60 seconds. The budget
  is rechecked after scanning, immediately before publishing intent.
- Original work and object directories opened without following leaf symlinks, with distinct
  device/inode identities recorded in the immutable intent.
- Complete work rehash against the frozen tree using original permission baseline and dependency
  policy. Changes or additional captured entries preserve all local content. Excluded system paths
  must be absent or empty directories; unrecorded files, links or nested content remain protected.

It atomically writes private `finalization/reclamation.json` with version 1, exact terminal capture,
Server authorization, original session root and both directory identities. File sync, no-replace
rename and parent sync precede success. Exact replay re-syncs the saved intent without refreshing its
remote authority, rereading partially removed content or manufacturing a new deadline. A changed
input, authorization or original root is rejected.

`ReadFinalizationReclamation` independently checks the saved intent against retained capture,
manifest, acknowledgement and cleanup records. It checks any remaining root against its original
identity and refuses replacement directories, symlinks and malformed private metadata. Missing roots
are permitted only with this validated intent. This is necessary for interruption between deletions;
it does not refresh Server authority or prove current runtime/reference absence.

`RetainFinalizationReclaimed` never deletes files. It requires the exact saved intent and both roots
absent, syncs their parents, then atomically retains an identical `finalization/reclaimed.json`.
Replay preserves its bytes. Any reappearing root invalidates completion instead of being removed.
Snapshot, baseline, runtime/launch/termination authority, frozen manifest, capture, acknowledgements
and cleanup metadata remain audit records. No deadline is recovered from either persisted record.

## Readers and recovery

Audit `ReadFinalization` remains readable after partial or complete removal only with valid journal
evidence. Frozen manifest/object openers reject pending/completed reclamation explicitly. Finalizing
again cannot recapture, and work cannot be reopened for mounting. `OpenSessionWork` uses kernel
`openat(O_NOFOLLOW)` because `os.Root.OpenFile` can resolve a same-root leaf symlink first.

The private inventory reports a nil record with `reclamation_pending` or `content_reclaimed`.
Corrupt intent/completion stays `invalid_retained_state`, never `not_finalized`. Worker transfer
recovery resumes pending deletion through its separate Helper operation and skips completed content
without uploading or reconciling another capture. Both validated states retire the original admission grant;
a later unrelated broker nonce is preserved. Export cannot turn either state into an empty complete
bundle or fall back to stopped-work recapture.

## Active readers

`HoldFinalizationContent` acquires a nonblocking shared kernel flock on the immutable private
`finalization/manifest.json` inode. That inode survives content reclamation and is never replaced by
acknowledgement/state updates. Reusing it creates no lock file, journal entry or content allocation,
so a frozen export remains read-only even when the filesystem cannot allocate new metadata.

The existing authenticated `open_skill_finalization_file` operation adds kind `hold`, with the same
exact binding and empty object-specific fields as a manifest request. It returns one read-only,
close-on-exec descriptor, full original capture, kind `hold`, positive bounded manifest size and null
entry. Client validation rejects changed identities, malformed metadata and descriptor counts.
SCM_RIGHTS preserves flock's open-file-description ownership. Closing/restarting the sending Helper
does not release another process's surviving descriptor; closing the final descriptor does.

Worker upload and frozen gateway export acquire this hold before manifest/object transfer and keep
it through operation completion. Every success/error/cancellation path owns descriptor closure.
Export reauthorization and blocked-output cancellation remain active while the hold is owned.
Acknowledgement-only replay needs no content read hold. No user grant or credential enters the lock.

Initial marking and the deletion executor acquire a nonblocking exclusive lock on the same manifest.
Active readers therefore preserve content without a process-local lease or reconstructed deadline.
New read holds are refused once reclamation intent exists. Raw manifest descriptors also take shared
locks, and individual frozen-object descriptors hold shared inode locks; the executor's complete
preflight checks an exclusive object lock before deleting any siblings. Private single-link checks
prevent replacing lock authority with an aliased metadata/object file. Active readers may briefly
include the sender's descriptor until its existing transfer acknowledgement handling completes.

Linux tests use actual flock and SCM_RIGHTS. They close original and replacement Helper servers while
client descriptors survive, then prove reclamation exclusion ends only when all readers close. Real
UID 65534 receives read-only holds without traversing the private store; UID 65533 is denied. These
are protocol/kernel lifetime tests, not a deployed daemon restart acceptance claim.

## Linux deletion executor

`ReclaimFinalizationContent` accepts only an exact durable intent and a mandatory caller-supplied
runtime/reference verification function. It deletes the two fixed original roots; callers cannot
select another path. Complete intent replay revalidates proof and syncs the existing completion.

Before the first unlink it scans **both** remaining trees against the full frozen manifest. Missing
entries are allowed for restart after partial deletion; changed contents, types, permissions, link
targets, unexpected names and unsafe private objects fail without deleting siblings in that call.
Original permission normalization remains valid. Excluded system directories must remain empty.
Each distinct frozen-object digest has one exact file declaration, independent of repeated work uses.

Traversal uses descriptor-relative `openat`, `fstatat`, `readlinkat` and `unlinkat`, without following
symlinks. Kernel `statx` mount IDs must match the protected parent mount: even a bind mount of the
same inode/device is rejected. A kernel without mount-ID support preserves content with an error;
this requirement does not change session admission. External aliases in other locations still need
the caller's mount/reference proof.

Deletion rehashes every remaining ordinary file's complete bytes and classification, checks its
metadata before/after streaming, and compares the entry again after the independent proof and before
unlink. Directory/root identity is rechecked, known links are unlinked without traversing targets,
and unknown/special entries remain protected. Mutation directories sync on success, cancellation and
error; each removed root's parent syncs before the separate immutable completion receipt is saved.
Cancellation or an uncertain write leaves the original intent available for restart. No audit record,
snapshot binding, takeover source or deployment bundle is removed.

`tests/linux_skill_reclamation_test.sh` runs actual deletion and restart tests in a disposable Linux
container, with real same-inode bind mounts under a private mount namespace. The ordinary Linux
component suite skips only this opted-in mount case. Tests also cover changed/new work, corrupt or
hardlinked objects, root replacement between verification and unlink, nested links/empty directories,
normalized permissions and unresolved conflict retention. Their proof callback is a controlled test
input; the Helper-specific checks have separate coverage below.

## Helper runtime proof

The private `Server.reclaimFinalization` coordinator holds the cancellable lifecycle mutex throughout
inspection, fresh authorization, marking and deletion. The original complete Native capture must match
the retained snapshot and canonical runtime identity. It requires the original ready spec/draft and
launch authority, matching terminal acknowledgement and original runtime-cleanup receipt. Legacy
bundles lacking managed preparation identity remain retained. No stop, kill, mount repair, invocation
observation, readiness update or network deletion is part of this proof.

The original transient runtime root must be absent. Same-boot units must be inactive, failed or absent;
a remaining unit must match the recorded invocation, transient user and canonical cgroup. The whole
cgroup must be empty. A same-boot unobserved starting record can qualify only when its unit is absent;
missing launch authority does not qualify. Previous-boot preparation may lack a launch record, but
requires a stable distinct kernel boot and completely absent current unit/cgroup. Corrupt or linked
launch records never count as missing. Both paths require an absent network namespace, stable repeated
unit observations and no current mounts or filesystem aliases of the runtime root, work or frozen
objects. This includes aliases of individual frozen files and aliases of the enclosing bundle.

`ReclaimFinalizationContentWithChecks` separates complete external resource inspection at phase
boundaries from the per-entry guard. The Helper guard checks cancellation, boot, runtime-root absence
and whole cgroup on every destructive entry. Its held lifecycle mutex excludes concurrent Helper
launch/mount/network mutations; executor inode, content, mount-ID and kernel reader locks remain
mandatory. Full spec/unit/network/mount inspection runs before deletion, before/after each content
root and before completion. Thus external command count is bounded independently of the file count;
the original single-callback executor still applies that callback at both levels.

First marking requires a callback returning a live **Helper-process** monotonic deadline. This private
callback is not a socket frame and never accepts a serialized Worker deadline. The socket exchange
below owns its timing. Marking also receives a context capped by that original deadline so scanning
cancels on exhaustion. Once intent exists, restart ignores the authorization callback and rechecks
original runtime authority before resuming the same intent. Completed replay refuses reappearing resources.

Linux tests call the private serialized coordinator with controlled authorization and systemctl/ip
command fixtures, exercising actual journal writes and deletion. They cover same/prior boot phases,
missing/changed authority, live/replacement/exited units, cgroups, network, reader exclusion, cancelled
lock waits and actual partial deletion followed by a new coordinator. Real bind mounts exercise work,
object, file and bundle aliases plus same-inode object-root mounts; the mount runner is
`tests/linux_skill_mount_test.sh`. These fixtures do not prove a live systemd/Server/Worker handoff.

## Authenticated socket exchange

`reclaim_skill_finalization` accepts exactly `{capture:<full terminal capture>}` under the existing
private Unix socket's peer credentials. It bypasses the generic task-result cache. The entire call
has a 15-minute bound including lifecycle-lock wait, proof and deletion. A single owned reader accepts
at most one bounded authorization line; EOF, oversized input or trailing input cancels the operation.
Every return closes the connection and joins the reader. Errors expose only a fixed `pending` frame.

For unmarked content, the Helper generates a random UUID and starts a 60-second monotonic deadline
**before** writing `{version:1,kind:"authorize",challenge:<UUID>,acknowledgement:<saved receipt>}`.
Worker validates that receipt against its expected complete capture and fetches fresh authenticated
Server evidence using exactly that challenge. It returns only the strict `ReclamationAuthorization`
JSON object on the same connection. Helper rejects changed challenges, identities, publication,
fields or lifetime; its original timer is never refreshed by HTTP timestamps, a Worker duration or
another response. A stalled challenge write and both IPC directions consume the same budget.

Worker monitors Helper output while HTTP is in flight. Helper timeout/shutdown cancels that HTTP
context; Worker cancellation closes the socket and cancels Helper scanning/lock waits. Initial marking
must complete within the challenge budget. Once intent is durable, deletion uses the remaining outer
call context; interruption is recoverable without reviving an expired authorization.

`resume_skill_reclamation` accepts exactly `{node_id,session_id}`. It can only find an already-valid
durable reclamation intent and independently prove its original resources again. It never requests
authorization, creates intent, changes the input or needs the Worker transfer ledger. Both operations
return `{version:1,kind:"reclaimed",record:<exact original terminal capture>}` only after durable
completion, or `{version:1,kind:"pending"}`. Changed/ambiguous/oversized frames and an extra challenge
fail closed. A lost completion response replays through the same immutable intent after Helper restart.

The existing background finalization page coordinator now attempts reclamation only after
`transferFinalization` has returned, which closes its upload read hold and completes exact acknowledgement
and transient cleanup. It checks the saved original terminal transfer receipts before requesting the
Helper exchange. Reclamation failure retains pending status and does not prevent later page items from
progressing. A `reclamation_pending` inventory entry goes directly to resume without content transfer
or another HTTP observation; `content_reclaimed` is skipped. Conflicted observations still preserve
local content until fresh evidence shows a later resolved terminal publication. No reclamation phase
is added to the Worker ledger; Helper intent/completion remains deletion authority.

Tests compose actual Unix sockets with authenticated HTTP fixtures, actual filesystem removal and
new Helper instances after unread completion replies. Separate real UID 65534/65533 children prove
authorized unprivileged reclamation/resume and denied peers without private-store traversal. Virtual
clock tests charge IPC and HTTP delay against the original deadline, including exact expiry and stale
challenge rejection. Worker tests prove reader closure before reclamation, durable cleanup ordering,
pending recovery without HTTP, conflict preservation and changed-completion rejection. These are
component/integration fixtures, not a complete deployed systemd/Claude acceptance claim.

## Required completion work

Complete the deployed CLI/Server/unprivileged Worker/Helper/systemd/Claude acceptance, including
original upload, reclamation, restart and next-session inheritance. Takeover sources and deployment
bundles remain outside this authority. Server-retired history needs a separate explicit policy; the
current fresh endpoint deliberately refuses it. Coordinated restore remains separately unverified.

Linux tests exercise the real filesystem executor and reopen after interrupted removal, in addition
to journal/reader and socket contracts. They do not establish coordinated restore or a complete
CLI/Server/unprivileged Worker/systemd/Claude lifecycle.
