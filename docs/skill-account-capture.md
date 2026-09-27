# Retained first-takeover capture

The account takeover in the root design requires an unchanged legacy source, actual writer
quiescence, and a durable capture that retries cannot replace with a later directory. Server task
terminal states and the existing reconciliation list are insufficient: reconciliation skips damaged
specs, and a missing Docker tmux process does not prove sandbox filesystem quiescence.

The helper owns the capture binding (Node/user/account/takeover/task/backend/directory epoch and
Server inventory digest), its permanent account fence, filesystem selection and capture policy.
The private `takeover-<account UUID>` bundle contains a small `record.json`, a bounded complete
`manifest.json`, and `objects/<sha256>` ordinary files. The immutable record fixes a random Helper
receipt UUID, tree digest and source existence. Files are copied and verified into helper-owned
0600 objects beneath 0700 directories, never hardlinked to the mutable source. Manifest modes stay
original. Reserved system paths are excluded; all other files, binary state, empty directories and
portable cross-entry links remain represented. No tool-format interpretation occurs here.

Before first publication the caller must close the matching account fence, hold Helper mutation
serialization, and prove every legacy writer exited without issuing stop/kill. Native proof must
cover whole cgroups plus historical bindings and locally discovered resources, not only session
rows or tmux panes. Missing/corrupt or unattributable evidence is a denial. Pending private import
intents also deny capture, even if their Server task is terminal. Docker needs its own sandbox-wide
proof; the Native check must not claim Docker acceptance.

Capture hashes the complete quiescent source, copies each object using descriptor-relative
no-follow reads and exact digest/length/classification validation, then checks the complete source
again before publishing. The complete private bundle is fsynced and renamed with no-replace.
Failures before publication remove only this call's private staging. The source is never renamed,
removed or chmodded. Missing skill roots can produce an explicit empty capture without creating
an account directory. A published bundle with missing/corrupt metadata fails closed, never recaptures.

On retry, the retained binding/manifest/fence are verified before the source is opened; the original
capture survives subsequent source changes, removal, Helper restart and Server upload-lease expiry.
Digest-authorized object reads use the retained manifest only, reject unsafe links/ownership/modes,
and stream verified private copies. Transfer still must verify bytes against the manifest. This
format is not permission to mount captured objects writable or to delete original data.

No generic fence removal, rollback or cleanup operation is introduced. Verified source rollback,
Server acknowledgement and retention must be wired explicitly. The Native task workflow now consumes these primitives through exact authorization. They do not advertise
managed runtime capability or constitute real Claude/Docker Sandbox acceptance.

## Current Native integration boundary

The internal `captureNativeAccountTakeover` method validates the Server inventory digest, closes the
exact epoch fence and replays an existing private capture before inspecting any old source. Its
Native proof reads every local spec through root-owned descriptor-relative no-follow handles;
malformed specs are not skipped. It inspects historical session/binding units plus locally listed
managed units and leftover system.slice cgroups, including units absent from Server inventory.
No stop/kill command is issued. A terminal main process still needs an empty whole cgroup.

Unknown/Docker resource backends, backend-copy tasks without complete migration receipts and
same-user/unknown-user Docker specs return `STATE_WRITERS_UNKNOWN`. The separate sandbox-wide and
orphan-Docker inventory remains required. The Native socket operation and dedicated worker dispatch
now revalidate Server ownership, maintain the exact task lease and stream only retained capture.
Successful local capture does not certify mixed-backend takeover or runtime capability. Original
source and receipt remain retained until explicit verified acknowledgement/rollback rules exist.

## Verification

The Linux fence/import suite now includes the capture, digest inventory and read-only Native
inspection cases. It checks ordinary independent copies, original modes, malformed skill data,
binary files, empty directories, cross-entry links, deduplication, source removal/retry, empty
captures, scope/identity drift, missing/corrupt/aliased metadata, unsafe objects, cancellation,
quotas, source aliases, pending import intents, root-owned spec validation, unknown backends,
unlisted units, leftover populated groups, unavailable cgroup roots and malformed unit listings.

The isolated systemd suite additionally starts a real synthetic managed unit whose main process
exits successfully while a child remains in its cgroup. Native takeover rejects it, never stops it,
then captures its late write after natural child exit. The source file remains present. This runs
alongside the six prior synthetic Native lifecycle cases. It is direct systemd/cgroup evidence,
not real Claude acceptance or Docker Sandbox acceptance.

## Authenticated transfer client

`internal/api` now provides `GetSkillTakeover`, `BeginSkillTakeover`, `PutSkillTakeoverFile` and
`CompleteSkillTakeover`. Each call binds the original `AccountTakeoverBinding` and exact task UUID;
completion additionally requires the retained Helper capture identity and digest. Status validates
all binding fields, inventory digest and phase combinations before the caller can use the response.
The largest inventory is 10,000 records; this dedicated response budget is 4 MiB and unrelated APIs
keep their smaller limits. Capture declarations are capped at 64 MiB.

Files are raw streaming request bodies. `PutSkillTakeoverFile` consumes and closes its reader,
checks exact length, SHA-256 and incremental UTF-8/binary classification, and requires verified EOF
before accepting the Server acknowledgement. Its response must name the exact upload and digest.
Redirects are refused, responses are bounded, and transfer calls honor caller cancellation with a
10-minute outer request timeout. The client never automatically retries an uncertain request or
logs private response bodies. Stable skill error codes survive without Server message contents.
The transport timeout does not extend the Server task lease, and StartTask does not renew it.
`RenewSkillTakeoverLease` renews only the live exact task and its current poll `lease_attempt`. The
internal transfer coordinator owns renewal while consuming an already sealed capture. It derives
a conservative monotonic deadline from the Server duration and request start, cancels work after
any uncertain renewal, and joins the renewal loop before returning. Committed receipt replay skips
Helper reads; a verified commit wins a concurrent renewal rejection. None of these paths deletes
retained content. The dedicated initiation coordinator extends this lease over capture itself;
transient outcomes never become cached terminal failures.

The worker cannot open SkillStateRoot. The read-only Helper descriptor operation below supplies
retained content after Native capture initiation. Complete Docker writer proof, library deployment scheduling, verified rollback and source retention acknowledgement remain integration work.
Server completion is not authorization to delete the Helper capture or the original discovery root.

Task polling now exposes `TaskEnvelope.TaskRecordID` (`task_record_id`), the database UUID used in
`AccountTakeoverBinding.TaskID`. `TaskEnvelope.TaskID` remains the existing logical name for task
start/result reporting and ledger replay. A managed task consumer must validate the record UUID;
it cannot derive it from the logical name or trust a substitute in the task payload.


## Read-only Helper descriptor transport

`Client.ReadAccountCapture` obtains and validates an existing complete manifest;
`Client.OpenAccountCaptureObject` obtains one object reader from that exact capture. Both use the
`open_account_capture_file` socket operation, existing peer-UID authorization and exact immutable
binding. The request selects `kind=manifest` or `kind=object`; object requests also fix the Helper
receipt UUID, tree digest and file digest. No host path, directory handle, UID or new capture request
is accepted. The worker must obtain fresh Server authorization before using these local primitives.
The Helper's retained identity check does not replace the Server's current owner/task/lease checks.

The Helper verifies the existing root/fence/receipt/manifest under its serialization lock, opens one
ordinary root-owned 0600 single-link file with no-follow flags and releases the lock before network
waiting. Reading cannot create a missing store or its ancestors. Only the read-only file descriptor
is transferred via SCM_RIGHTS. Manifest bodies remain bounded at 64 MiB and never enter Helper JSON
frames. Response metadata is bounded at 32 KiB, including worst-case JSON escaping of a legal 4096-byte
file path; ordinary Helper frame limits remain unchanged. The ACK bounds descriptor handoff only and
does not modify capture state, release a fence or authorize cleanup.

The Linux client receives descriptors with atomic `MSG_CMSG_CLOEXEC`, checks regular-file type,
root ownership, mode, single-link status, exact length and read-only access, and seeks to the start.
It tolerates fragmented socket metadata but rejects extra/truncated descriptors, malformed or
oversized frames, duplicates and binding drift. Numeric fields decode directly to their declared
integer types; epochs above 2^53 cannot be rounded into a different authorization. Cancellation
interrupts socket reads and closes unadopted descriptors. Complete manifest bytes are parsed and
compared with the retained tree digest; object callers must still verify streamed bytes, as the
HTTP upload client does, and own closure of the returned reader. Non-Linux helpers reject this API.

The Linux fence/import test script includes descriptor tests with actual non-root subprocesses:
the admitted UID can read the supplied descriptor but cannot open the store or write the file;
another UID cannot use the socket. Tests cover source removal, changed identities, unsafe links,
missing stores, forged paths, fragmented/truncated/multiple descriptors, cancellation and descriptor
leaks, a manifest larger than the ordinary 1 MiB frame, escaped long paths and full-width integer
identities. A combined Helper-descriptor/HTTP-client case rejects same-length object corruption
before accepting remote acknowledgement. Docker-hosted Go race tests exercise these Linux cases;
this is local transfer evidence, not Docker Sandbox or real Claude acceptance.

## Backend backup copy evidence

New backend migrations persist a private `account-copy-<task digest>.json` intent before starting
`/bin/cp`. The exact Node/user/account/logical task, source/target/path digest, boot ID and derived
systemd unit are immutable. A dedicated `agent-remote-copy-<digest>.service` runs as a bounded
10-minute service with no restart, whole-cgroup kill policy, disabled core dumps and discarded
stdout/stderr. The Helper observes both launcher completion and terminal unit/empty cgroup evidence
before recording `copied` or `failed`; ambiguous launch, cancellation, changed boot or unknown cgroup
state leaves `started`. Cancellation never treats the killed systemd client as proof of service exit.

The copy record alone proves only the copy phase. Historical copy-only records cannot authorize
re-copying changed source data, ownership replay or takeover. Original data and partial backups
remain retained. Copy failures preserve the bounded `STATE_COPY_PENDING`/`STATE_COPY_FAILED` codes.

New migrations additionally persist `migration-account-copy-<task digest>.json` before any account
mutation. It binds the exact copy intent and Helper execution configuration. Target and rollback
ownership walks run synchronously under Helper serialization; each access/default/traversal ACL
command runs in its own bounded systemd service with the same independent unit/cgroup proof as copy.
Unknown target writers prevent rollback; any rollback error prevents new terminal completion. A
new task cannot bypass an unresolved local migration by choosing another logical task ID.

After all writers exit, the Helper flushes the account and backup filesystems before recording the
same-boot `succeeded` or `failed` outcome. Failure means the migration failed with no remaining
migration writer; historical failed receipts do not assert that source permissions were successfully
restored. New execution additionally requires every source ownership/ACL operation to succeed
before saving failed state. A nonzero rollback command or source identity/ownership error keeps the
original started intent pending, retains the backup and prevents a replacement migration. This
does not add interrupted-migration recovery or upgrade historical receipts to rollback proof. Exact
terminal replay validates both original records and returns the retained result without repeating
copy or ownership, including after an account fence closes. Changed input is rejected. Started
migration evidence reports `STATE_MIGRATION_PENDING`; a retained failed result reports
`STATE_MIGRATION_FAILED`. Interrupted started work still requires explicit recovery rather than
automatic continuation. Neither receipt authorizes backup deletion.

Native takeover checks both Server backend inventory and local migration/copy history, including
records omitted from Server inventory. Only complete terminal migration evidence with its original
completed copy satisfies that writer check. Missing, copy-only, corrupt, aliased, mismatched and
oversized records remain denial conditions. This is migration-writer proof, not Docker sandbox or
orphan-process quiescence. Server session admission can initiate Native reservations; capability advertisement remains disabled.
The isolated systemd suite covers success, target ACL failure with successful rollback and a real
source-UID read of retained learning, failures in each rollback ACL phase, invalid source identity,
and an ACL service surviving
client cancellation. Test account data uses `/var/tmp` because the container's `/tmp` mount lacks ACL
support. Linux receipt tests separately cover replay, fence priority and incomplete evidence denial.

New legacy binding/session starts and config-import writes now also check local migration/copy
history before mutation. An unresolved intent, copy-only record or failed copy returns
`STATE_MIGRATION_PENDING`; replacing the Helper cannot reopen admission. A skill takeover fence has
priority, and completed import/task replay stays read-only. Worker preserves bounded migration
denials from Helper or Server. This closes the concurrent-writer path while recovery is pending;
it does not implement interrupted migration recovery. Server-side admission and original result
binding are documented in `agent-remote-server/docs/skill-backend-migration-recovery.md`.

## Docker runtime compatibility prerequisite

The installed Docker CLI examined during integration now prints a removed-plugin notice for
`docker sandbox` and exits zero. Its ordinary Docker daemon remains available, which cannot prove
sandbox lifecycle or quiescence. Helper and installer now require command-specific create/exec/rm
usage from bounded probes. Binding/session startup rejects an incompatible adapter before account
mutation; stop/cleanup preserve trusted specs instead of treating the stub's zero exit as removal.
Mixed-backend capture remains denied. The newer standalone sbx API needs its own explicit adapter and
real backend verification; it is not silently treated as the previous Docker Sandbox interface.

## Native capture initiation and task recovery

`capture_account_takeover` accepts only the original binding and authenticated writer inventory.
The existing peer-UID check and cancellable serialized mutation gate guard all operations. Its
4 MiB request limit accommodates the bounded 10,000-entry inventory; no host paths or caller proof
flags are accepted. A five-minute operation bound covers copying, and EOF, extra input or worker
context cancellation cancels the Helper call and joins its reader. Errors are content-free
`MIGRATION_PENDING`; no generic result cache can bypass the account capture journal.

The `takeover_tool_account_skills` queue branch validates the exact logical/idempotency identity,
record UUID, Node, poll attempt, Native backend, protocol versions and eight canonical payload
fields before HTTP or Helper access. A fresh Server GET precedes one lease supervisor spanning
capture and upload. Neither a transient failure nor a historical generic ledger failure consumes
the task. The pending Server task is reissued after lease expiry; Helper restart reopens the same
immutable capture. An uncertain commit is recovered by GET of the original reservation.

Committed replay requires no lease renewal, Helper or source access. The worker then reports exactly
`status`, `takeover_id`, `task_record_id`, `tool_account_id`, `checkpoint_id`, `capture_digest`.
Server completion validates these against its original committed receipt and revalidates the active
owner and task payload. Confirmation never republishes the head. Server reservation plus Helper
capture provide durable recovery; no second worker ledger or content cleanup authority is added.
