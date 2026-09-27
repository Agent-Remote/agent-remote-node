# Frozen snapshot export

The SSH gateway recognizes only `agent-remote-skill-export --snapshot UUID --protocol 1`.
The installed forced-command device and key arguments supply the connection identity. Malformed
export commands fail without attach/sync fallback. No public HTTP listener is added to Node.

`internal/skillexport` owns the bounded stream and periodic authorization. `internal/api` verifies
its stdin-only original user grant through the authenticated Server endpoint. The Helper's
`inspect_skill_finalization` reads one retained record; `read_skill_finalization` and existing
read-only object descriptors supply content. Inspection cannot create the state store, freeze live
work, stop writers, change a record, upload, acknowledge or reclaim anything. The gateway also
requires ObjectsVersion 1 and agreement with every known Server termination/finalization fact.

Wire framing is specified by the root `docs/skill-manager-wire-v1.md`; Server authorization is
specified by its `docs/skill-node-export.md`. Each individual grant lasts at most 15 minutes; authorized continuation uses `/renew`.
Initial verification creates a connection-local validity window of the returned 1–10 seconds,
measured from HTTP request start and capped by the current grant expiry. Before/after each object,
the gateway checks that window, cancellation and immutable observed facts locally. Renewal starts
at half the current window (checked every 100 ms); each response may shorten the next interval.
A separate expiry watcher closes blocked HTTP/output even if renewal has not returned. A failed
or late renewal is terminal. Fresh HTTP verification is mandatory immediately before the public
header and successful footer, including stopped-work recovery. No authority is shared between
connections or persisted. Original identity and previously learned Server facts cannot change.
A valid successor can advance expiry only within the original user token lifetime.
An expired/revoked original token, user, device or key closes the stream within this bounded window.
The caller must close stdin after its bounded `{version:1,grant:...}` handshake. Grants and private
Helper paths never enter output. The public failure is `STATE_EXPORT_UNAVAILABLE`.

Verification:

- `go test -race ./internal/skillexport ./internal/api ./cmd/agent-remote-attach`
- `bash tests/linux_skill_copy_test.sh` covers missing/foreign/linked frozen inspection and both
  allowed nonroot and denied peer identities, plus the existing read-only descriptor regressions.
  The allowed nonroot reader also runs the complete export stream through the real Helper socket,
  while direct traversal of the root-owned store is denied. That test seeds a task-bound frozen
  capture and uses a fixed grant authority; it does not launch a process or perform Server login.
- `internal/skillexport/testdata/frozen-export-v1.bin` is produced by the real Go stream. Matching
  copies in Server and CLI tests validate the same large integer binding and binary/text objects.
  `AGENT_REMOTE_TEST_EXPORT_STREAM_PATH=/absolute/path go test ./internal/skillexport -run
  TestExportStreamsExactCompleteFrozenObjectsWithoutChangingRecord -count=1` optionally saves it.

These are HTTP authorization, stream, portable-bundle and Linux Helper proofs. They do not by
themselves establish an end-to-end deployed SSH/Claude lifecycle or Docker/sbx support. No backend
capability is advertised by this export implementation.

## Recovery when local freezing cannot allocate a copy

If frozen inspection fails, the gateway can request the distinct private
`stream_stopped_skill_export` operation with only the original nine-field export binding. The
Helper requires verified absence of the finalization directory itself. Linked, incomplete, legacy
and corrupt existing captures never permit fallback. It reads the sealed original Native snapshot,
ready spec/draft and launch authority. Retained termination evidence preserves its original
classification. If that file is absent, recovery additionally requires a recorded canonical
original invocation and can report only unclean content. Same-boot reads repeatedly require the original
invocation (or noncontradictory unit absence) and an empty whole cgroup. Previous-boot reads also
require absent unit/cgroup/network/mount resources and a stable distinct boot.

The Helper holds the preparation/mutation lock through the complete transfer. Lock acquisition,
initial scanning and final verification each have a 15-minute bound. Disconnect, trailing input,
earlier caller cancellation and stalled output cancel the operation. This can delay other
Helper mutations; no detached read lease survives the connection. Scanning does not fsync content,
write another content copy, create a capture, stop or reconcile a service, or acknowledge durability.
Legacy v1 replaces runtime admission quotas only for this read with its 100000-entry and signed
64-bit expanded-byte constraints. Negotiated recovery uses the bounded format specified below. There is no second fixed 10 GiB byte
ceiling: all retained bytes must be verified. Bounded buffers, frame limits, authorization and
transfer deadlines still apply. Legacy v1 entry overflow remains a failure; the explicitly negotiated recovery format below handles
over-entry sources without widening manifest v1.

The root source rechecks retained identities, complete tree digest and passive writer evidence
before completion. The gateway verifies each private-stream object and footer and requires exact
EOF before forwarding successful completion. Live authorization starts before frozen inspection
and continues through initial scanning, transfer and the final rescan. CLI initial/final scan waits
each allow 15 minutes; partial object/frame reads reset a 30-second progress timeout.
The same portable bundle format records recovered content without claiming a local-durable capture.

Tests cover forced reserve rejection, runtime quota overflow, same/previous-boot reads, corrupt or
linked authority, returning writers, tree changes, missing termination, cancellation and the complete
unprivileged gateway/Helper stream (UID 65534 allowed; UID 65533 denied). These fixtures retain real
private Linux files and use controlled systemctl responses and a fixed grant authority. They do not
start Claude or prove a deployed SSH lifecycle. The additional opt-in runner
`bash tests/linux_skill_export_enospc_test.sh` fills only a disposable, size-checked 16 MiB tmpfs.
It proves termination retention fails with actual ENOSPC and complete read-only export still succeeds
as unclean while the volume stays full. No host filesystem is a fill target. Workspace/runtime
fixtures remain on the normal container filesystem because Docker Desktop tmpfs may lack POSIX ACLs.
Missing both termination evidence and a recorded original invocation, trees above export bounds, and
broader backend support remain unresolved.

## Real CLI and SSH acceptance

From the Server checkout, run:

```sh
AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST=1 uv run pytest -q tests/test_skill_ssh_export_live.py
```

The runner builds current Node/Helper/SSH gateway and host CLI binaries, starts the shipped daemon
units and an isolated OpenSSH server, and uses a fresh host-only SSH key/agent. Production Server
authentication, takeover, deployment, session execution, termination and SSH-key synchronization
produce all runtime evidence. A synthetic tool writes through the actual Native mount. Server
finalization uploads are deliberately refused so export reads retained Node data.

The frozen case exports the original capture after daemon restart. The runtime-quota case configures
1 KiB on Node before preparation, generates over 8 KiB, and requires complete stopped-work export
with no finalization directory. Both verify the full manifest, distinct object hashes/bytes,
binary state, permissions, empty directories and relative links. A second transfer revokes its
registered key during online verification and must leave no published or temporary bundle.
Original work/finalization contents and inode/mode/owner/mtime/ctime inventories remain unchanged.

This does not use model credentials or establish learning by a real model. Capabilities remain
test-only database fixtures; production advertisement is asserted absent. Docker Sandbox,
data above the export protocol bounds and old-boot export remain separate acceptance requirements.

## Opt-in default-entry SSH capacity

The same real CLI/SSH cases can generate exactly 100,000 user manifest entries and 99,997
unique ordinary files (plus the existing empty directories and relative link):

```sh
AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST=1 AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY=1 uv run pytest -s -q tests/test_skill_ssh_export_live.py
```

Run from the Server checkout. The frozen case uses unchanged default limits. The stopped-work
case keeps its deliberate 1 KiB runtime quota, forcing read-only recovery of the full larger
tree. System-reserved mount roots are excluded just as in production capture. Every exported
object is hashed and size checked, complete manifest identity is compared, and an independent
revoked-key export must publish nothing. Before/after source inventories still compare all file
bytes and inode/mode/owner/mtime/ctime metadata. No clone of a captured receipt is used.

Only capacity fixture waits are extended: thirty-minute daemon case, twenty-minute readiness and
coordination phases, and a 960-second test-process owner for capacity cases. Production continuation
is governed by independent scan/progress bounds rather than that test-process deadline.
The actual protocol, grant lifetime, periodic revocation checks, 10 GiB and 100,000-entry bounds
remain unchanged. This is not a test of export above those bounds or of 10 GiB byte capacity.

The first revised stopped-work transfer exported and independently verified all 100,000 entries
in 672.191 seconds. Its later revocation fixture failed, so this is not complete acceptance.
Current frozen and recovery capacity reruns must also pass revocation and source-inventory checks;
see the root implementation status for owned live jobs. Default collection skips these expensive
cases unless explicitly enabled.

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

Linux Helper progress tests exercise cancellation, trailing input and an actual thirty-second
blocked output. Each must join its disconnect watcher, release the lifecycle lock and permit a
fresh complete read of the retained source. Private request submission also keeps its own
thirty-second write deadline. Updated gateways require the matching Server `/renew` endpoint;
a missing endpoint is terminal, so Server must precede Node in an eventual authorized rollout.

Actual >900-second frozen and stopped-work SSH acceptance passed (60917/30245). Both complete
exports lasted about 933 seconds, continued reauthorization more than 930 seconds after initial
verification, verified every byte, rejected the revoked second export and preserved the entire
source inventory. The 32 MiB slow object precedes five further objects, so frozen descriptor
rotation is exercised beyond the first reader's fifteen-minute lifetime. See Server export docs
and root implementation status for the synthetic throttled-link scope and precise tested revisions.
Over-entry recovery remains open.


## Negotiated complete recovery stream

The fixed SSH forced command remains protocol 1. New CLI stdin adds `recovery_version:1` to the
exact `{version:1,grant:...}` handshake. Only integer 1 is supported; duplicates, nulls and unknown
fields fail. Legacy requests retain the existing complete-manifest stream. Negotiated frozen reads
also retain it. Only an absent capture, exact original retained source and independent stopped-writer
proof permit `stream_stopped_skill_recovery`. An existing Server incoming digest forbids recovery;
a later learned capture digest must match or terminate the live authorization chain. No fallback
retries an uncertain grant request or revives expired authority.

Recovery magic is eight bytes `ARSKRC\x00\x01`. Frames have a four-byte unsigned big-endian JSON
length. Header (maximum 4096 bytes) has exactly `version`, `binding`, `recovery_digest`, `unclean`,
`entries`, `file_bytes`, `file_objects`. Counts are nonnegative signed-64-bit bounded; file_objects
counts file entries including identical content at different paths. The header digest uses the
recovery domain and complete entry fields in filesystem enumeration order, never manifest v1 identity.
Private source inode digests are not sent. Complete manifest v1 stays capped at 100,000 entries.

Exactly `entries` entry frames follow, each at most 64 KiB with all eight ordinary entry fields.
Non-files have normal complete metadata. A file start has declared size, empty sha256/content_kind;
it is followed immediately by exactly size bytes and a maximum-4096-byte content frame containing
only `sha256` and `content_kind`. Source hashes during this same read; relay and CLI independently
verify all bytes before accepting trailing claims. Explicit counts bound remaining bytes/objects.
The final frame has exactly `version`, `recovery_digest`, `entries`, `file_bytes`, `file_objects`,
`complete:true`, and must repeat the original header facts after full source and writer verification.
No malformed/duplicate/unknown/null fields or trailing bytes are accepted. Initial/final scan and
ordinary progress budgets, live original authority and revocation behavior remain unchanged.

CLI privately stages format `agent-remote-skill-node-recovery-v1`, original binding, recovery_digest,
termination classification and counts in checkpoint.json; ordered complete entries in recovery.jsonl;
path-SHA256-addressed metadata in entries/; and content-addressed objects/. Its index checks unique
paths, explicit directory parents, depth, internal link targets/cycles and non-directory traversal
without tree-sized in-memory metadata. Files and metadata are verified/fsynced; whole journal digest,
footer, EOF and SSH exit success precede atomic destination publication. JSON command output retains
its common tree_digest field, interpreted under its explicit format. No v1 manifest.json is created.
This is a distinct recovery bundle, not an importable published checkpoint or retention acknowledgement.


Negotiated recovery acceptance uses the existing real SSH test with explicit opt-ins:
`AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST=1` plus
`AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY=1` and
`AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVER_ENTRIES=1`, selecting `-k runtime-quota`.
The 100,001-entry run passed complete independent bundle verification, revoked retry and unchanged
source inventory (88590). A separate `AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_LONG=1` run passed renewal
beyond the original 900-second grant for this format (49223); legacy-format evidence is distinct.

`AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_DESTINATION_FULL=1` instead selects a macOS-only destination
exhaustion scenario, also with `-k runtime-quota`. It owns an 8 MiB HFS disk image and a >32 MiB
stopped-work source, observes actual pending-file growth and remaining space, requires failure
without output/staging, then retries to the ordinary destination and verifies complete content,
revocation and unchanged original source. It always detaches its own image and does not resize,
fill or prune any existing filesystem. This opt-in is incompatible with capacity/long-link modes.
