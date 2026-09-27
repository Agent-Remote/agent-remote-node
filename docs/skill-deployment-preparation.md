# Independent deployment preparation

The authorized worker delegates the original Server deployment input through the private Helper
socket operation `prepare_skill_deployment`. This is separate from session snapshot preparation.
The Helper accepts no host path, runtime UID, session or process identity. It requires its configured
Native Node and an existing matching account fence. The fence's initial epoch is a lower bound;
current Server authorization supplies subsequent directory/member epochs. Preparation never creates
or advances the fence, writes account/session paths, starts a process or changes account heads.

The request header contains only attempt/task UUIDs and a positive input size capped at 64 MiB.
The complete strictly decoded input follows the input handshake. The Helper asks for manifest-bound
ordinary objects by digest, verifies all bytes and requires the downloader's completion byte before
accepting each file. Disconnect and deadline cancel work, including waiting for Helper serialization.
The existing private peer-credential gate and serialized filesystem admission apply.

`deployment-<attempt UUID>/` under independent SkillStateRoot atomically publishes a complete
Helper-owned work tree, protected permission baseline and schema-1 `deployment.json` receipt.
The receipt pins every original deployment identity, complete input digest, directory epoch,
original generation and a Helper-generated receipt UUID. It contains no host path or file bytes.
All records and directories are fsynced before no-replace publication, followed by parent fsync.
Failed unpublished stages are removed; uncertainty after publication retains the complete bundle.

Exact replay validates the original full input, private receipt/baseline and every retained byte.
It neither redownloads nor repairs missing or corrupt retained data. Only an absent bundle permits
first preparation; incomplete records inside an existing bundle are retained errors. Quotas, reserved
system names, portable paths and unverified runtime links are checked before copying. Existing
session preparation continues to require a non-root runtime identity; independent deployments keep
all content owned by the Helper and grant no mount or runtime writer access.

Success is `prepared` local evidence, not Server readiness or tool use. The worker holds one exact
poll lease across manifest/file transfer, Helper preparation and dedicated result confirmation.
It validates the canonical `prepare_account_skills:<attempt UUID>` task and never uses generic
start/complete/fail or their replay cache for deployment. Invalid tasks and transient failures remain
pending until the dedicated termination path below has exact revocation and drain evidence. Public
scheduling and capability advertisement remain disabled.

Before submitting `/node/skill-deployments/{attempt_id}/result`, the worker atomically persists a
schema-1 `deployment_prepared_pending` task-ledger record containing only the original bounded
receipt and poll attempt. Exact Server acceptance advances it to `deployment_prepared_confirmed` by
full-record CAS. Integer generations/epochs retain int64 precision. Lost-response recovery invokes
`/result/inspect` through a background read-only loop; it never reacquires leases or repeats preparation.
Task redelivery first inspects any pending original proposal. Only an unaccepted original observation
under a strictly newer current poll may replace its poll number, keeping the same Helper receipt.
Confirmed results cannot be downgraded. Server acceptance wins a concurrent renewal failure; local
acknowledgement uncertainty remains recoverable through inspection. Neither ledger phase authorizes
local cleanup, running-session mutation or content replacement.

## Permanent local drain

`drain_skill_deployment` accepts only the complete original ten-field deployment binding. Its
authenticated socket handler requires the configured Native Node and existing original account
fence. It uses the same cancel-aware mutation lock as complete preparation: an active copy must end
before drain can seal that attempt. The call has a 30-second limit; disconnect/deadline cancels its
lock wait. It does not stop processes or cancel another request outside that serialization boundary.

First drain checks any existing prepared bundle's safe matching `deployment.json` identity, then
atomically writes `deployment-drain-<attempt UUID>.json` under independent SkillStateRoot. Missing
or corrupt identity in an existing bundle is an error, never permission to invent the original
binding. The schema-1 receipt contains exactly `version`, complete `binding` and Helper-generated
`helper_receipt_id`. No-replace publication and parent fsync precede acknowledgement. Exact restart
or lost-response replay verifies the private receipt and completes the parent fsync again.

Every subsequent preparation checks that fence before requesting object bytes, including requests
that began reading metadata before the drain. Same-attempt changed identities cannot bypass or
replace the fence. Malformed, linked, foreign-owned or non-private drain records block preparation
and are never repaired. The drain preserves all existing bytes, including corrupt content, and
creates no work tree for an absent attempt. Historical preparation reads remain separate evidence.

This receipt proves permanent local preparation exclusion only. It does not report Server failure,
acknowledge supersession or authorize local reclamation. Dedicated Server revocation and terminal
confirmation and durable worker recovery are described below. Ordinary scheduling remains gated.

## Server revocation and terminal transport

`internal/api` now supports the separate Server termination protocol: first request and recover the
immutable original revocation intent, then confirm or inspect that intent with the exact Helper drain.
The client validates all identities, bounded failure codes, Server outcome/retryability and final
observed poll. Canonical fields, explicit booleans and integer precision are mandatory. The original
request survives Server-selected supersession; a superseded intent can never be retryable.

The two Python-generated `internal/api/testdata/skill-deployment-termination-v1.json` vectors cover
failed and superseded observations, including a later poll at final confirmation. A read-only null
lookup means no saved revocation, never authority to prepare without a current lease. Transport does
not obtain Helper evidence or change the worker ledger. The separate worker coordinator below owns
those transitions; public scheduling stays disabled.

## Durable worker termination

Before leased preparation, the worker recovers any original Server revocation. A saved confirmed
success follows only its success inspection path. Known bounded Server failure codes, lease loss,
cancellation, Helper unavailability and network failures can start dedicated termination; unknown
errors remain pending. Renewal preserves typed Server causes so supersession does not become an
invented generic failure. An uncertain success POST retains the original success proposal.

The original task ledger key now has four additional schema-1 phases. Every payload has exactly
`schema_version`, `binding`, `request`, `preparation`, `intent`, `drain`, `confirmation`; the last four
are nullable until their original evidence exists. `preparation` preserves any pending success
proposal, including exact int64 metadata.

| Phase | Durable evidence before the next side effect |
| --- | --- |
| `deployment_termination_requested` | Original bounded request before revocation POST |
| `deployment_termination_revoked` | Exact committed Server intent before Helper drain |
| `deployment_termination_drained` | Exact permanent Helper receipt before terminal confirmation |
| `deployment_termination_confirmed` | Accepted terminal observation, including final poll |

Whole-record CAS refuses stale writers, changed intents/drains/final polls and discarded success
proposals. Confirmed success cannot enter termination. Requested recovery first looks up Server
intent; if none exists, any retained success proposal is inspected before revocation. A competing
accepted success restores the same confirmed success record without draining. A newer delivered
poll can update only an uncommitted request, after lookup proves no saved intent; existing intent
always retains its original poll and classification.

The independent recovery loop checks pending success proposals for revocation, then advances
requested/revoked/drained records without a preparation lease or backend admission. A lost revocation
response uses original intent lookup. A lost Helper response repeats the same binding and recovers
its immutable receipt. Once drain is saved, recovery inspects the original terminal result before
any exact resubmission and never calls Helper again. A malformed or foreign journal stays pending
without blocking later records. The loop does not prepare content, launch processes or release data.

The live opt-in Linux test exercises actual Server revocation, dedicated Worker dispatch, root Helper
drain and saved terminal confirmation, including a post-commit lost response. After stopping Helper
and reopening the worker ledger, independent recovery and task replay use read-only Server evidence.
The reservation, initial poll/account fence and revocation are fixture-seeded; this does not prove
ordinary scheduling, systemd or Claude acceptance.
