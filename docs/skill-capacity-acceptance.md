# Published-limit storage acceptance

Design sections 8.2 and 12 require actual capacity evidence, separately from small quota boundary
unit tests. The opt-in Linux runner `tests/linux_skill_capacity_test.sh` exercises unchanged default
copy/capture policies with a non-root materialization owner. It limits the disposable container to
2 CPUs and 2 GiB memory, denies network access, and removes only its own resources.

Run each mode explicitly from the Node checkout:

```sh
AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST=1 AGENT_REMOTE_SKILL_CAPACITY_MODE=entries tests/linux_skill_capacity_test.sh
AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST=1 AGENT_REMOTE_SKILL_CAPACITY_MODE=bytes tests/linux_skill_capacity_test.sh
AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST=1 AGENT_REMOTE_SKILL_CAPACITY_MODE=helper tests/linux_skill_capacity_test.sh
```

The entries case creates exactly 100,000 distinct ordinary files with distinct digests on the
container filesystem. The byte cases materialize one and ten valid skill directories, each totaling
exactly 1 GiB including instructions. Their large binary objects differ, so content deduplication
cannot turn the 10 GiB capture into a 1 GiB storage test. All bytes are actually streamed and written;
runtime objects are not sparse and no multi-GiB in-memory buffers are allocated. Byte cases use a
private 40 GiB sparse block image formatted as ext4 and loop-mounted inside their disposable privileged
container. The image is backed by the host filesystem and removed afterward; the user's Docker disk
is not resized or cleaned. A direct macOS shared-directory attempt correctly failed ownership checks.
Filesystem type and available capacity are printed. The helper/byte image installs its required Linux
tools using verified HTTPS; the actual tests run without networking.

Each case seals the original session binding and source/materialized permission baselines, freezes
with default disk reserves, compares the complete manifest, streams and hashes every retained
object, then reopens the bundle and checks idempotent finalization without reclassification. Phase
timings and Go heap/allocation measurements are observations for this environment, not performance
SLAs. Ordinary copies, fsync and full content verification remain in the production path.

This is filesystem/manifest/journal capacity evidence. It does not prove whole-runtime writer exit,
HTTP/SSH upload deadlines, Server user storage limits or concurrent capacity. The separate Helper
case below covers 100,000 individual object descriptors. HTTP/SSH and full lifecycle cases remain required
before claiming default-limit acceptance across the entire product. No backend capability is enabled.

Capacity observations also require the real Helper request boundary. The `helper` mode creates a
100,000-file runtime work tree, submits reconciliation through the authenticated Unix-socket handler,
compares every resulting manifest entry, then opens and hashes every one of the 100,000 distinct
objects through the scoped descriptor reader. Runtime exit/systemctl evidence is a controlled fixture;
this is an IPC/capture test, not another whole-systemd lifecycle claim. Its fixture socket deadline
must exceed the production deadline so the fixture cannot be mistaken for production cancellation.

Reconciliation and admission-loss draining use one 15-minute budget for lock waiting, original
runtime inspection and complete capture, with the caller's earlier deadline preserved. Both client
and Helper enforce that ceiling; socket disconnect cancels work and preserves the original tree.
The generic 30-second Helper budget is too short for the published 100,000-entry default. Normal
CLI stop waiting retains its own deadline and background reconciliation can finish later. This
change adds neither runtime authority nor an upload/publication claim.

## Recorded filesystem evidence (2026-09-25)

Linux ARM64, 2 CPU/2 GiB container limits, default policies unchanged:

| Case | Result | Materialization | Freeze | Complete retained-object verification |
| --- | --- | --- | --- | --- |
| 100,000 distinct files | Passed | 55.121 s | 92.190 s | 7.549 s |
| One 1 GiB skill | Passed | 1.952 s | 3.747 s | 0.670 s |
| Ten distinct 1 GiB skills / 10 GiB directory | Passed | 14.515 s | 35.779 s | 8.301 s |

The large-byte cases ran on the dedicated ext4 image; the entry case ran on the Docker overlay
filesystem. They verify 1,073,741,824 and 10,737,418,240 expanded bytes respectively. The 10 GiB case
retains ten distinct large binary objects; reported peak process RSS was 7,708 KiB (not total cgroup
memory, which also accounts for filesystem cache). The entry case reported 314,588 KiB peak RSS.

Before buffer reuse, the entry case allocated 7,635,568,192 bytes cumulatively by the materialization
phase. The same workload after reuse allocated 1,082,029,280 bytes, about 86% less. Heap/RSS are
separate measures. Elapsed time is not claimed to improve: other verification ran concurrently during
the second measurement. Every copy still streams and hashes its own bytes, verifies ownership/mode,
syncs each object, and publishes with the same atomic and durability checks.

The real Helper socket case failed before the budget fix after 33.71 seconds (including fixture
creation), then passed after the fix in 149.82 seconds with 141.344 seconds spent in the actual
reconciliation call. It compares all 100,000 manifest paths, sizes and digests. Earlier-deadline and
explicit-cancellation lock-wait tests pass for both observation and admission draining, including the
race detector. The opt-in fails rather than skips when root/ACL prerequisites are missing.

Logs: `/tmp/skill-default-capacity-entries.log` (before buffer reuse),
`/tmp/skill-default-capacity-entries-reuse.log`, `/tmp/skill-default-capacity-bytes-ext4.log`,
`/tmp/skill-default-capacity-helper-before.log` and `/tmp/skill-default-capacity-helper-fixed.log`.
Node's full quality gate passed at 62.8% statement coverage. The focused actual Linux storage run
passed 34 top-level tests. These results retain the scope limits stated above.

## Scoped reader capacity (2026-09-25)

The Helper mode now includes all 100,000 individual object-descriptor requests and complete byte
verification. The recorded run passed in 126.91 seconds: reconciliation took 88.556 seconds and the
full object reader loop took 31.298 seconds. It used the same 2 CPU/2 GiB limits, default policy and
unique-content workload. `read_skill_finalization_objects` validates/indexes the complete manifest
once per bounded connection; the old individual-file operation revalidated it for every digest.
There is no measured pre-change full-transfer timing, so these numbers are not a speedup ratio.
See `docs/skill-finalization-readers.md` and `/tmp/skill-reader-capacity-live.log`.

## Preparation transfer budget (2026-09-26)

Real Server default-capacity downloading exceeded 30 minutes before 70,000 files, demonstrating
that the old ten-minute whole-preparation deadline cannot accommodate 100,000 sequential
authenticated file requests. Both snapshot and deployment preparation now use a three-hour
absolute deadline at the Client and Helper stream boundaries. The earlier caller deadline wins;
individual HTTP requests remain bounded, exact task-attempt renewal remains mandatory, and
lease loss, cancellation or socket disconnect closes the stream and cancels filesystem work.
Atomic preparation and original-input retry behavior are unchanged. The lifecycle mutex remains
held for preparation, so other Helper mutations may wait behind a large transfer. This timeout
change does not establish successful whole-pipeline capacity or improve transfer throughput.

Regression coverage inspects the deadline delivered through real client protocol handshakes for
both operations, exercises earlier caller deadlines, and verifies cancellation releases blocked
Helper input. Existing malformed-frame, identity, disconnect, partial-bundle and lease-revocation
checks remain required.

The new deadline tests fail against an overlay of the old ten-minute constant at both client
preparation paths and the Helper stream, and pass against the fix. Actual root Linux regression
passed 22 top-level tests with no skips; the focused race checks passed. Full Node gate passed
at 62.2% coverage, including installer checks. Logs: `/tmp/skill-preparation-budget-before.log`,
`/tmp/skill-preparation-budget-linux-complete.log`, `/tmp/skill-preparation-budget-race.log`,
`/tmp/skill-preparation-budget-node-quality.log`.

The separate Server download run subsequently completed 100,000 file reads in 2494.944 seconds
with 248 lease renewals and zero full-manifest reads (whole case: 2744.16 seconds). A new actual
Native daemon pipeline is running; its scope and reproducible command are in Server
`docs/skill-pipeline-capacity.md`. It includes two clean publications, daemon restart and full
byte verification in an independent materialized session, but has not yet passed.

## Actual SSH byte and combined capacity fixture

The real SSH lifecycle runner also accepts `AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_BYTES=1`.
The synthetic mounted tool writes ten distinct payloads across ten valid skills, with each skill
at or below 1 GiB and the complete directory exactly 10 GiB. Root auxiliary bytes are subtracted
from the learning payload, so the default directory quota is exercised exactly. Ordinary writes
create actual bytes; there are no sparse payloads, reflinks or duplicate-object capacity shortcuts.

With only the byte flag, the capture has 36 entries / 24 unique files. Combining it with
`AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY=1` fills exactly 100,000 entries / 99,988 unique files
while retaining the same 10 GiB total. Both Node and Server fixtures check cardinality, complete
expanded bytes and unique bytes before export. Source inventories and independent bundle checks
hash files with bounded buffers rather than loading GiB-sized files into memory.

Run from the adjacent Server repository, one expensive transfer at a time:

```sh
AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST=1 \
AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_BYTES=1 \
AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY=1 \
uv run pytest -q -s tests/test_skill_ssh_export_live.py -k runtime-quota
```

The runtime-quota case preserves 10 GiB of real stopped work after actual capture rejection at
1 KiB, exports through the ordinary authorized Helper/SSH path, then revokes a second transfer and
checks unchanged source inventories. The frozen case requires space for both work and retained
objects in the Docker VM; the exported bundle uses additional host space. These are opt-in capacity
fixtures, not a successful execution claim. Production capture/export bounds and grant lifetime
remain unchanged. Test-only byte generation may take the capacity preparation phase instead of the
small tool fixture's three-minute terminal timeout.

The first actual combined SSH run failed in 763.33 seconds. Initial transfer terminated at
690.143 seconds after 135 successful verification responses. Error-only diagnostic evidence
identified a real HTTP EOF on the next verification, not a complete capacity pass. A deterministic
HTTP/private-TLS socket regression reproduced uncertain failure when a verification POST reused
a connection closed by its peer. Verification now closes each HTTP/1 connection after the response;
other API requests remain pooled and uncertain authorization is never replayed. API/export, race
and full Node gates passed. The pure-production combined rerun is still pending; neither the
specific server idle-expiry cause nor complete combined acceptance is claimed from these tests.

Subsequent capacity invocations build the current CLI locally with Cargo's ordinary release profile
and print `ssh_export_cli_profile=release`. A stack sample of the development build showed software
SHA-256 compression consuming its active object-verification worker. Optimized compilation matches
the shipping build profile; it does not skip hashing, alter content, bypass fsync or extend the grant
or transfer deadline. Small non-capacity live tests still use debug builds. A local optimized build
is not a release publication, and older debug runs retain their original recorded outcomes.

## Recovery beyond the default byte quota

Export now uses the complete authorized manifest size rather than a second fixed 10 GiB byte cap.
The Node scanner, stream and CLI retain signed byte-count overflow checks, bounded buffers and
unchanged metadata, entry and authorization deadlines. Runtime admission defaults stay finite and
unchanged. This removes the byte-quota recovery defect; it does not solve trees above 100000 entries
or arbitrarily slow transfers beyond the original grant lifetime.

For actual stopped-work proof, add `AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVERSIZE=1` to the byte-capacity
command and select `-k runtime-quota`. The mounted tool writes an additional real 1 MiB into the
learning payload, for exactly 10 GiB + 1 MiB. Cardinality stays 36 entries / 24 distinct files without
the entry flag, or 100000 entries / 99988 distinct files with it. Node and Server independently require
the larger total; no sparse or deduplication shortcut is used. This opt-in is rejected unless byte
capacity and stopped-work quota mode are both selected. Full manifest/hash validation, a second
revoked export and unchanged Node source inventory remain required for a successful acceptance.

Actual combined stopped-work acceptance passed on 2026-09-26: 1 passed / 1 deselected in 676.69 s,
`/tmp/skill-ssh-export-combined-capacity-optimized.log`. The current-production connection fix and
ordinary optimized CLI exported all 10 GiB / 100000 entries / 99988 distinct objects; independent
byte/hash/manifest verification passed, the second export was rejected at the intended revoked-key
HTTP 409, and the complete final source inventory was unchanged. No diagnostic overlay or wrapper
was used. This run preceded byte-ceiling removal and does not certify above-default recovery or
full frozen capture/upload at the byte default. Those remain separate acceptance work.

Actual above-default byte acceptance also passed on 2026-09-26: 1 passed / 1 deselected in 233.89 s,
`/tmp/skill-ssh-export-oversize-byte-acceptance.log`. It exported 10 GiB + 1 MiB / 36 entries / 24
unique files after real quota rejection. Initial CLI completion took 115.008 s; independent full
verification finished by 121.522 s. The second export failed at the intended revoked-key HTTP 409,
no partial/staging output remained, and final source metadata/content inventory was unchanged.
Current production Node/CLI passed without diagnostic overlays. Larger entry counts and transfers
beyond the original grant deadline remain unimplemented; this byte result does not close those gaps.

The complete Native entry-capacity daemon pipeline subsequently passed on 2026-09-26 in 11233.68 s:
first clean publication 4698.5 s, successor materialization after daemon restart 2392.5 s, and second
clean publication 4079.3 s. Every inherited file was independently verified by the successor tool,
and Server confirmed two clean publications with exact 100000-entry manifests and distinct snapshots.
The runner used real nonroot Worker/privileged Helper/Native/HTTP/PostgreSQL transitions and cleaned
its owned resources. See Server `docs/skill-pipeline-capacity.md` for reproduction and resource limits.
This proves the described entry workload; full byte and combined frozen paths remain separate work.

Actual default-byte frozen capture/export passed on 2026-09-26: 1 passed / 1 deselected in
246.35 s, `/tmp/skill-ssh-export-frozen-byte-acceptance.log`. Both the 10 GiB work and independent
frozen objects remained present under the default reserve. The optimized CLI exported 36 entries /
24 distinct files in 75.390 s; full independent verification finished by 80.970 s. The intended
revoked second export failed at HTTP 409 in 5.495 s, leaving no partial/staging output; final source
inventory was unchanged. Current production binaries had no observation overlays or wrappers.
This proves byte-capacity freezing/export; complete byte upload/publication/inheritance and combined
frozen byte/entry acceptance remain separate work.

Actual combined frozen acceptance passed on 2026-09-26: 1 passed / 1 deselected in 775.93 s,
`/tmp/skill-ssh-export-frozen-combined-acceptance.log`. Default reserve remained enabled with both
10 GiB work and 10 GiB independent frozen objects, exactly 100000 entries / 99988 distinct files.
Initial optimized CLI export took 511.385 s; full independent checks finished by 532.305 s. Revocation
rejected the second export at the intended third verification (HTTP 409) in 13.916 s, with no partial
staging and unchanged final source inventory. Current production code used no observation overlay,
wrapper or artificial delay. Full byte/combined Server publication/inheritance remains separate.

The full byte-only Native pipeline passed on 2026-09-26: **1 passed in 380.80 s**,
`/tmp/skill-default-byte-pipeline-acceptance.log`. Exact 10 GiB / 30 entries / 20 distinct files
were cleanly captured/uploaded/published in 124.5 s and normally reclaimed in 50.3 s. After daemon
restart, a new session materialized in 58.9 s, verified every instruction/payload byte, cleanly
published in 68.4 s and reclaimed in 53.1 s. Server checked both clean publications, distinct
snapshots, identical complete trees and exact capacity totals. Original work/object roots were
removed only through normal fresh-authorized reclamation. Default quotas/reserves and ordinary
copies remained unchanged; no observation overlay or seeded transition receipt was used. Runner
cleanup removed all owned containers/images/temp roots. Combined byte/entry pipeline, actual model
inference and Docker Sandbox remain separate acceptance work.


Combined byte/entry daemon run 11363 failed at its second finalization's existing three-hour
phase deadline after first publication, reclamation and complete independent inheritance succeeded.
The cause is unproven. Capacity fixtures now emit bounded local capture states/free filesystem
counters and independent read-only Server state/reservation/HTTP-template summaries. No manifests,
credentials or raw private errors are logged. Byte-only control 24677 passed in 423.12 seconds;
combined reproduction with these observations remains pending. See root implementation status.
