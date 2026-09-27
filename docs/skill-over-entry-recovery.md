# Recovery above the manifest entry limit

Complete manifest v1 remains limited to 100,000 entries. Neither raising that limit nor splitting
one directory into independently published manifests is recovery authority. Over-entry recovery
needs a separate complete stream with bounded metadata and a verified destination. It must not
create a second Node content copy or depend on free Node staging space.

## Implemented scanner primitive

`skillmanager.WalkRecoveryTree` is a Linux read-only primitive, not a Helper operation or wire
protocol. It observes the complete tree through the original directory descriptor. It keeps the
existing bounded preparation baseline, at most 256 names at each of 129 traversal levels, a file
hashing buffer and two incremental digests. It does not retain a map or manifest proportional to
the observed tree and does not create temporary metadata or content files. Expanded bytes and
entry counters are checked against signed 64-bit arithmetic, independently of capture quotas.

Each provisional callback carries the ordinary entry fields, with preparation-added permissions
normalized by the exact original baseline. System exclusions and sealed runtime dependencies retain
their existing meaning. Internal symlinks use the same link-resolution rules as manifest validation,
but resolve metadata through anchored descriptors; no runtime dependency or external link is opened
for content. Special files, unsafe paths, missing targets, link cycles and excessive directory depth
are rejected. Cancellation and callback failures terminate the pass.

The recovery digest has its own domain, `agent-remote-skill-recovery-tree-v1`, followed by the same
NUL-delimited entry fields as v1 in **filesystem enumeration order**. It is deliberately not a
canonical manifest digest. An unchanged filesystem must produce the same traversal order across
verification passes; otherwise export must fail. A separate private source digest includes the
root and entry inode/device/type/ownership/link-count/size/mtime/ctime observations, excluding atime.
Replacing a file with identical bytes therefore changes source evidence even if its content digest
does not change. Neither digest is a finalization, publication, checkpoint or deletion receipt.

A successful individual scan is provisional. Metadata is rechecked around file reads, callbacks
and directory traversal, but an earlier sibling can change after its local check. The
coordinator must compare **all** fields of complete initial, transfer and final observations while
holding the existing lifecycle exclusion and independently rechecking original writer quiescence.
Any mismatch withholds success. The callback must never be treated as permission to publish partial
content. Recovery scanning alone does not prove application-level database consistency.

`OpenStoppedWorkRecovery` now binds these scans to the same retained source opener as ordinary
stopped export. It requires the exact sealed session, private baseline, original owned work directory
and absent finalization directory. It preserves an existing clean/unclean termination; absent evidence
is unclean and never written back. Each transfer/final comparison rechecks the retained records and
original work identity before and after a complete scan. `Walk` exposes only the current entry's
callback-scoped read-only file and requires the visitor to consume its exact complete verified content.
Skipped bytes, callback mutation, changed retained authority or any observation mismatch reject the
pass. No source descriptor is handed to a worker and no second content copy or journal is created.
The caller must still supply lifecycle exclusion and independent original-writer quiescence; this
source component is now invoked by the separately negotiated Helper operation.

## Negotiated integration and acceptance

The separate recovery format, explicit stdin negotiation, Helper writer/lifecycle coordination,
gateway verification and CLI private disk-index staging are now implemented. Legacy callers retain
the original v1 stream; new clients advertise `recovery_version:1`. Matching gateways keep frozen
exports unchanged and choose the recovery format only for independently proven stopped work with
absent finalization. Old gateways reject the new field rather than silently accepting unsupported
recovery. No Server content API, published manifest bound or capability advertisement changes.

`Stream` now emits provisional file extent metadata before hashing/transmitting each file in one
pass. Trailing hash/classification follows the bytes. This avoids a large silent prehash between
objects exceeding the thirty-second progress budget. Initial and final complete scans remain
independent bounded phases. `RecoveryHasher` counts and hashes complete ordered entries on source
and relay; `RecoveryFileVerifier` validates late content claims without retaining the file. The
source compares complete content and private inode observations; gateway rechecks live exact
authority and withholds its footer until Helper EOF. See `skill-node-export.md` for framing.

CLI writes only private fixed or hash-derived names, validates explicit parents and path uniqueness
using a disk index, and checks every internal link against that index before publishing. It compares
the full journal digest again, fsyncs content/metadata and retains mandatory footer/EOF/SSH-success
gates. No remote path is extracted. The recovery bundle identifies its separate format and cannot
masquerade as an importable manifest v1 checkpoint. Actual small negotiated SSH and its unchanged-source checks passed. Actual CLI disk-index acceptance
passed 100,001 entries. Actual 100,001-entry SSH passed complete bundle verification, revocation and unchanged source
inventory (88590, 1051.80 seconds overall). New-format paced SSH continuation also passed (49223): the last renewal occurred 930.951 seconds
after original verification, followed by rejected revocation and unchanged source inventory.
Actual destination-exhaustion SSH also passed (71250): an 8 MiB owned HFS image rejected the
>32 MiB stream, staging was removed, a fresh ordinary-destination retry verified complete content,
and the source inventory remained unchanged. Above-default-byte recovery also passed with this format (6924): the complete 10 GiB + 1 MiB
source, revoked retry and unchanged inventory were independently verified. These are concrete
acceptance workloads, not an arbitrary entry count, throughput or backend guarantee.

Actual scanner acceptance uses `AGENT_REMOTE_RUN_SKILL_RECOVERY_CAPACITY=1` and
`TestRecoveryScanExceedsManifestLimitInOneDirectory`: 100,001 real ordinary files in one directory,
two complete matching passes and an independent assertion that normal capture still rejects them.
This is Linux component evidence, not actual SSH, daemon lifecycle, nonempty byte capacity or Docker
Sandbox acceptance. Focused cases cover metadata parity with capture, unsafe links/special files,
same-byte inode replacement, earlier-sibling mutation, cancellation, byte-quota independence and
the fixed depth bound.

`TestStoppedRecoveryWalksOverEntrySourceWithoutManifestOrCopy` additionally prepares an original
sealed session, creates 100,001 nonempty ordinary files (100,008 expanded bytes), streams and verifies
all of them, and performs a final complete source comparison. Its small preparation fixture uses
zero disk reserve inside disposable tmpfs solely to set up sealed metadata; this is not default
capture reserve or disk-capacity evidence. Other source tests reject changed snapshot/baseline/
termination, linked work, an existing capture, cancellation, callback mutation and skipped reads.
