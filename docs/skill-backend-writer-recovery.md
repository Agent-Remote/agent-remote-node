# Backend migration writer authority

Explicit recovery must identify the original privileged copy and ACL services before inspecting,
waiting for, or stopping them. A predictable unit name plus an empty cgroup does not identify a
particular launch. The per-migration receipt continues to block admission while this work is pending.

New account copy inputs set `writer_version: 2`. Each copy or target/rollback ACL phase first saves
one private `migration-writer-<task-and-phase SHA256>.json` intent. Its immutable identity includes
the original copy binding and boot, a fixed phase, command/configuration digest, and a Helper-generated
launch UUID. The UUID appears in the transient unit description; the unit must run as root with
Restart=no and RemainAfterExit=yes. The Helper records a nonzero systemd invocation before publishing
terminal writer evidence. Every subsequent observation requires the same description, invocation,
canonical unit/cgroup, boot and execution properties. Launch response alone is never completion.

The source account and backup are unchanged by inspection. Cancellation leaves the original service
and retained intent for explicit recovery. A loaded matching service can be observed after Helper
replacement, but no read or replay launches it again. Missing, foreign, malformed or previous-boot
runtime evidence remains unresolved. Terminal writer evidence requires the complete cgroup empty;
finished success/failure and invocation are immutable. A terminal receipt precedes best-effort removal
of the already exited transient service. Cleanup revalidates original identity and cannot stop a
live or replaced service. Logs/receipts contain no account bytes or credentials.

New terminal whole-migration evidence requires the original successful copy writer and all successful
target phases, or all successful rollback phases after failure. Every existing phase must be terminal.
Historical writer_version=0 receipts retain their previous meaning, not new recovery authority. New
incomplete or orphaned writer records also block account writer admission independently of the parent
receipt. This supervision supports the explicit passive recovery protocol below. It does not
authorize new permission changes, certify source rollback or permit automatic migration replay.

`migrate_account` bypasses the generic Helper result cache. Exact replay revalidates the retained
migration and copy/phase authority before returning the historical outcome. A generic cached result
without that original authority remains recovery-required and cannot start another copy or ownership
pass. Old schema-0 writer receipts are accepted only with matching schema-0 copy evidence. The cache
is preserved for audit; deleting it is not a supported recovery action.

Validation includes Linux private-journal and admission tests, real systemd copy/ACL lifecycle tests,
and an actual surviving service observed by a replacement Engine from retained metadata. The service
runs once, keeps its invocation, and converges after natural exit. A separate same-name service with
a different invocation survives historical cleanup. This is a Helper component acceptance test,
not a full daemon replacement, account failover or user-command recovery acceptance.

Same-task replay may now converge a same-boot writer-version-2 migration whose original operations
have all finished. It passively refreshes existing phase receipts, verifies each original service
is exited/absent with an empty canonical cgroup, and completes a copied-but-unrecorded copy receipt.
It never creates a phase, launches a command, changes account ownership/ACLs or recaptures a backup.
Complete successful target phases with no rollback require fresh read-only account verification;
complete rollback phases produce only the original failed outcome. Source permissions are not
attested and the Server recovery gate is not reopened by that failure. Missing phases, unknown
writers, changed invocation, old boot, legacy evidence, or any incomplete/failed rollback remain
pending. Both filesystems are synchronized and all phase/quiescence checks are repeated before
publishing the whole-migration receipt. This closes completed-work/result-recording gaps, not the
remaining recovery of genuinely interrupted ownership work. Terminal Server failure is handled
only through the separate authorized recovery task below.

Writer version 2 also retains one private `migration-ownership-<task-and-phase SHA256>.json` intent
before target/rollback identity lookup or any direct Lchown. The intent fixes original copy, phase
and backend. A rollback intent without any ACL record is already rollback-in-progress and blocks
target-success inference. ACL phases and final outcomes require their matching ownership intents;
orphaned ownership intents block admission. Started writer-version-0/1 migrations cannot use this
new metadata-completion recovery because absence of an old rollback ACL does not prove that direct
rollback ownership writes never started. Historical terminal records remain immutable and readable.

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

## Cross-component passive recovery acceptance

The opt-in recovery acceptance must use the real administrator CLI over HTTP, the shipped
unprivileged Node worker unit and root Helper unit in a disposable systemd/cgroup container.
Inject an original Helper reply loss only after actual copy/ownership completion; the ordinary
Worker must record/report its failure. Then restart both daemons and route recovery directly
through the authenticated production Helper socket. No migration receipt/result may be seeded
as proof. Check original task immutability, disable preservation, no additional writer launches,
and unchanged complete account/backup bytes and metadata. A missing-backup case must remain
blocked; lost recovery acceptance/completion responses must be read back under the original key.
This test uses synthetic account content and no model credentials/inference. It proves backend
recovery transport/lifecycle, separately from actual skill learning, SSH and Docker Sandbox.

The Server runner is `AGENT_REMOTE_RUN_RUNTIME_RECOVERY_TEST=1 uv run pytest -q
tests/test_runtime_migration_recovery_live.py`; it supplies a private disposable fixture to
`tests/linux_runtime_recovery_test.sh`. That script builds current Node binaries and uses the
shipped systemd units. Both lost-replies and missing-backup cases passed on 2026-09-25: Worker
UID 22000 with zero effective capabilities and NoNewPrivileges, distinct replacement daemon PIDs,
and exactly four original writer launches with none added by recovery. Inventory comparison retains
file hashes, links, modes, ownership, sizes, mtime, ctime, inode and device for account and backup.
The fixture never enters the image build context; the runner removes its own container and image.

## Completed migration after kernel change

`migration_previous_boot_linux.go` revalidates only a writer-version-2 migration whose whole succeeded
receipt was already durable before the previous kernel ended. It never finishes a started receipt
or rewrites its boot. Original copy/ownership/phase checks, no rollback, current account/backup
verification and repeated absence of current units/cgroups are required. Empty or linked current
cgroups are also rejected. All original bytes, ownership, modes, inode and modification/change
times remain unchanged. The Server still independently authorizes the exact recovery task/lease.

Focused Linux tests passed twelve previous-boot cases and the existing explicit/completion recovery
contracts. They include a unit appearing during reinspection, malformed/missing evidence, current
cgroups, rollback intent and cancellation. Those fixtures simulate the old boot identity.
The full Linux Helper package and Node host quality gate also passed after the implementation.

The separate actual two-kernel case passed on Linux amd64 (2026-09-26):

```sh
AGENT_REMOTE_KERNEL_TEST_CASE=migration AGENT_REMOTE_KERNEL_TEST_ARCH=amd64 \
  AGENT_REMOTE_KERNEL_TEST_DISK_ON_HOST=1 tests/linux_skill_kernel_reboot_test.sh
```

Its first kernel executes actual copy and ownership services for two independent fixtures: one
completes the whole migration, the other deliberately stops before whole completion. After the
external power cut, only the completed original may recover. The second kernel compares complete
account/backup/receipt inventories and checks that exactly four original writer launches remain
for each fixture. This is a Helper/VM component test with synthetic accounts, not Server/CLI
acceptance, real account enrollment or Docker Sandbox support.

The optional host-disk flag places only the owned raw VM disk in the runner's private host temp
directory. The guest still uses the same ext4 filesystem, fsync, external power cut and two actual
kernels. The script removes that file with its container/image on exit. `COPY --chmod` sets fixture
permissions without running a foreign-architecture shell after installing guest packages. The
passing run reused the existing amd64 guest-package cache and ran QEMU natively on arm64.


## Bounded full-tree backup verification

Passive completion recovery now inventories the complete account and original backup twice around
filesystem synchronization. Success requires equal content/topology and unchanged observations of
both trees, followed by the original phase/quiescence and journal checks. Content includes empty
directories, streamed file hashes, opaque symlink targets, within-tree hardlink topology and data
xattrs. UID/GID, full mode and all xattrs have a separate private permission digest; POSIX ACL and
file-capability xattrs may differ between source and target permissions. No digest, path, xattr or
account byte enters public results or logs. This comparison verifies current retained copies;
it is not a historical pre-copy fingerprint or exact source-permission rollback attestation.

Traversal uses descriptor-relative openat2/statx, refuses linked root/ancestor paths, special files,
nested mounts (including same-device bind mounts), an account/backup root inode alias, external
hardlinks and substitutions. File and
directory identities/metadata are rechecked after reads; symlinks are never followed. Bounds are
1,000,000 entries, 128 levels, 4096 relative-path bytes, 64 MiB of accumulated path names, 64 KiB
per xattr name-list/value, 1 MiB of xattrs per entry and 64 MiB per scan. File bytes stream through
a reused 128 KiB buffer with cancellation between reads; signed-64-bit lengths remain supported.
Unsupported filesystems or unverifiable trees remain recovery-required without altering content.

The v1 operation keeps its passive semantics and now has a 15-minute total budget, including
lifecycle-lock waiting. The authenticated socket owns disconnect cancellation; timeout cannot leave
an orphaned scan. The Worker renews the exact original Server authorization before and during the
inspection and terminal result request. Renewal refuses expired or changed authority; the Worker
subtracts the full request round trip from each Server duration and cancels Helper work on any
uncertain renewal. A committed result wins a concurrent terminal renewal refusal. Ordinary Helper
calls retain their short budget. Original same-task completion and previous-boot inspection also
bound scan lifetime. A tree exceeding these bounds remains unresolved. Explicit interrupted repair,
incomplete previous-boot recovery and verified source rollback remain unfinished; these checks do
not enable them or advertise a backend.

The current inventory/lease implementation passed full Linux Helper tests and all actual systemd
migration cases. Actual two-kernel migration acceptance passed again on 2026-09-26 with current
binaries, preserving original account/backup inventories and writer counts while rejecting the
incomplete original. Current CLI/production-daemon recovery also passed both reply-loss and missing-
backup cases in 24.93 s. The latter reused the existing Native package image and mounted freshly
built Node/Helper/test binaries and current service files; no old binary supplied proof. Logs:
`/tmp/skill-migration-inventory-kernel.log`, `/tmp/skill-migration-lease-linux-helper.log`,
`/tmp/skill-current-recovery-daemon-cached.log`. These remain passive synthetic-account proofs.


## Historical permission baseline for new migrations

New whole-migration records use `version: 2`; copy/writer records retain `writer_version: 2`.
These are separate schema versions. Older Helpers reject the new whole record. Historical whole
version 1 remains readable with its original proof requirements and is never upgraded by replay.
The external recovery binding stays version 1 and passive, with exactly its existing fields.

Before starting the backup copy, the Helper scans the original account twice and persists an
immutable private `migration-baseline-account-copy-*.json`. It binds the complete original
migration/configuration/boot, account root device/inode, content/topology/data-xattr digest,
UID/GID/full-mode/all-xattr digest and the exact parent chain modified by traversal ACL commands.
Parent evidence includes directory identity, owner/group/mode, original access/default ACL values
and an all-xattr digest. Parent paths are Helper-derived; none is accepted from recovery requests.
Parent metadata is bounded to 128 directories and 256 KiB of paths/ACL values in a record below
1 MiB. The ordinary full-tree scan bounds still apply. These fingerprints and ACLs remain private;
no account file bytes enter the baseline, task results or logs.

Baseline creation is forbidden once any original copy receipt exists. Fresh backup creation uses
anchored no-follow directory operations and exclusive mkdir: existing backups (including empty
directories) are retained errors. StateRoot remains root-owned and not writable by other users,
with its required Worker traversal; the migrations child must be root-owned mode 0700. The
existing state/backup permissions are never repaired as part of this check. A version-2 ownership intent
requires the original baseline. After copy completion and before target writes, the Helper verifies
that both account and independent backup still match the historical content and permissions, and
that every parent retains its original permissions. A mutually modified account/backup cannot
replace historical evidence merely by matching each other.

Whole completion repeats full baseline checks around filesystem sync and rechecks the exact
original writer phases and quiescence. Success permits target account permissions and traversal
ACL changes while requiring original content, unchanged backup permissions and retained account/
parent directory identities. Failure additionally requires exact original account permissions and
all original parent permissions. Merely reapplying source backend ACLs is insufficient. The live original execution now restores
exact original permissions as described below; any remaining permission difference leaves the
migration pending.

Only then does the Helper seal `migration-attestation-account-copy-*.json`, binding the exact
baseline digest and outcome, before publishing the whole terminal record. Lost attestation replies
can replay the same immutable record. Version-2 terminal replay and history admission require this
attestation and baseline; missing, corrupt, linked and orphaned evidence remains closed. Passive
same-boot and completed previous-boot recovery also verify the retained historical baseline.

### Original execution restores exact source permissions

After all original source rollback ACL writers have drained successfully, the same live serialized
Helper execution restores the historical account permissions. It requires the exact pending whole
version-2 record/current boot, original target and source ownership intents, complete original copy,
complete rollback phases and a verified historical baseline. Passive recovery never invokes this
writer; existing started tasks still take the passive path instead of repeating mutations.

The restorer verifies both full inventories again, retains their bounded inode/hardlink indexes,
and opens source/backup roots through no-follow descriptors. Each child stays under its original
mount. Regular files and symlinks must retain an inode from the complete inspected link set, the
same link count and corresponding canonical hardlink path; substituted or externally linked inputs
are rejected. Symbolic links use descriptor-relative no-follow chown. Files/directories restore
UID/GID, full mode, access/default ACLs and file capabilities (capabilities last because chown clears
them). Data xattrs and content bytes are never rewritten. Parent traversal directories additionally
must retain their original identities and ownership; only their saved modes/access/default ACLs
are restored. Original absent ACLs are removed, including residual target named-user permissions.

The original immutable rollback intent precedes every direct syscall. No subprocess or detached
writer is created. Cancellation is checked during scans, traversal and attribute writes; errors
retain the backup and leave the whole migration pending. Full source/backup/parent verification,
syncfs, repeated quiescence and immutable attestation are still required before terminal failure.
The executor does not declare a partially restored tree complete, rewrite historical outcomes,
automatically retry after Helper replacement, or expose a new mutable recovery operation.

Actual systemd cases now restore both custom original permissions and residual target ACLs. Linux
checks additionally cover set-ID bits, file capabilities, opaque links/hardlink ownership, original
parent ACL absence, mutually changed/aliased inputs and cancellation after an actual mode write.

Supervised interrupted repair under a new explicit recovery task, incomplete previous-boot recovery
and treatment of legacy migrations without historical baselines remain unfinished. Source-profile
reopening uses the separate verification contract below. Default v1 recovery remains passive.


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


Source verification acceptance now includes the full Linux Helper suite, real systemd rollback,
production CLI/Server/unprivileged Worker/root Helper with daemon replacement and lost responses,
and an actual ARM64 two-kernel power cut. Completed target and attested exact source rollback verify
in the second kernel; target phase success and exact source restoration without terminal attestation
remain blocked. Original inventories and four/five writer-launch counts remain unchanged. These
synthetic-account proofs do not certify tool inference or Docker Sandbox execution. Detailed current
results are recorded in the root repository's `docs/skill-manager-implementation-status.md`.


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
