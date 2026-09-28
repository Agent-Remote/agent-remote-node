# Node Skill manager

This is the Node-specific reference. Read the shared
[operator guide](https://github.com/Agent-Remote/agent-remote/blob/main/docs/skill-manager.md),
[mandatory contract](https://github.com/Agent-Remote/agent-remote/blob/main/docs/skill-manager-wire-v1.md)
and [acceptance status](https://github.com/Agent-Remote/agent-remote/blob/main/docs/skill-acceptance-plan.md).
Do not duplicate implementation journals in README or rule files.

## Configuration and authority

- `skill_manager_enabled` defaults to true on fresh installs and missing fields. Explicit false survives
  load/save/registration/upgrades; old installer-generated false needs an intentional operator change.
- Helper probes configured enablement, Native dependencies, runtime executable and private storage.
  Worker forwards only a complete valid `skill_manager.native` report with protocol/manifest/deployment v1
  and writable copies/finalization/recovery. Invalid reports produce no capability; Docker Sandbox is not Skill-capable.
- Upgrade Worker and Helper together. New admission is gated; recovery of accepted data continues independently.
- `skill_state_root` defaults to `/var/lib/agent-remote-skill-state`, outside Worker-owned data.
  Keep it root-owned/private and outside account, session, workspace and artifact roots. Back it up with account data.
  Runtime gets only the intended writable work copy, never journals, immutable packages or Helper authority.
- Worker owns authenticated HTTP and polling; only Helper performs privileged filesystem/runtime operations.
  Account fences persist across restart and block stale legacy launches/imports. Managed config import requires
  `--exclude-skills`; never delete a fence to bypass a failed operation.

## Lifecycle invariants

1. Bind every preparation to original owner/account/Node/snapshot/task/attempt and canonical input digest.
   Keep the current lease through content and startup; renew independently of slow transfers and refuse expired authority.
2. Persist original spec/launch intent before execution; validate selected immutable system Skill artifacts and actual bytes.
   Both Claude discovery aliases see the same work tree; managed system entries remain readonly.
3. Recover readiness only from the exact original evidence. Process-local broker admission cannot be reconstructed
   from a saved receipt or a replacement nonce. Generic legacy task cache and runtime inventory do not manage snapshots.
4. Prove stable original unit/invocation and whole-cgroup quiescence before freezing or cleanup. Clean zero exit is
   separate from forced stop; unknown evidence retains data. Capture failures report `capture_pending`, never durability.
5. Freeze complete ordinary bytes and manifest, persist original capture, then transfer with immutable idempotency.
   Keep Server persistence and publication receipts distinct. Reconcile every inventory candidate despite isolated failures.
6. Cleanup requires exact terminal acknowledgement and passive runtime proof. Local content reclamation additionally
   needs fresh Server availability, no writers/readers/mount aliases and a durable two-phase intent. Shared manifest
   inode locks protect full upload/export lifetimes; never delete substituted, changed or conflicted content.
7. Frozen export uses the restricted SSH gateway and live original-user/device/key authorization. Stopped-work fallback
   requires absent capture, original retained authority and complete pre/post scans; corrupt capture never falls back.
   Negotiated recovery streams support over-entry work without claiming manifest-v1 or Server persistence.
8. After a different kernel boot, prove current resources absent; do not stop or remove replacement resources.
   Backend permission migration recovery follows the shared v1/v2/v3 action contract, preserving original backups,
   failure attestations and repair journals; default passive recovery never writes permissions.

Deployment preparation is independent of a session: an exact private attempt receipt proves preparation only.
Termination requires Server revocation, permanent Helper drain and final Server confirmation. A superseded or
expired task is not proof of drain. Worker journals and Helper journals have separate authority.

## Implementation and verification

| Path | Responsibility |
| --- | --- |
| [internal/skillmanager](../internal/skillmanager) | Canonical manifests, private bundles/fences, capture, descriptors, recovery and reclamation |
| [internal/runtimehelper](../internal/runtimehelper) | Peer-authorized Helper operations, runtime evidence, trusted paths and execution |
| [internal/worker](../internal/worker) | Managed task/lease coordination, exact receipts and independent recovery loops |
| [internal/api](../internal/api) | Strict bounded authenticated transport and immutable response validation |
| [internal/skillexport](../internal/skillexport) | Forced-command export and renewable authority |
| [tests](../tests) | `linux_skill_*` lifecycle, mount, systemd, reboot, capacity and recovery runners |

Run `scripts/run-quality-checks.sh` for the mandatory local gate. Linux/systemd/reboot and actual-model
runners require their documented disposable environment; a local pass does not certify those scenarios.
Current real Claude gaps and historical capacity/recovery evidence live only in the shared acceptance document.
