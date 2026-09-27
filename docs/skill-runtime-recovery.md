# Managed Native startup and reboot recovery

The root skill-manager design requires retained work to survive runtime and Node shutdown. Kernel
boot identity and process lifetime are separate from Server persistence and publication. Recovery
cannot restore a task lease, broker nonce or historical readiness as current liveness.

## Interrupted startup on the original boot

A durable `starting` launch intent prevents another systemd launch, even if Helper exited before
recording readiness. When the original unit is loaded, recovery verifies the original private
spec/draft and prepared binding, published spec bytes, valid nonzero invocation, transient service,
original user and canonical cgroup. A running unit additionally needs its unchanged private work
mount and system files. An exited unit needs whole-cgroup quiescence; its transient spec may be
absent, but existing spec bytes must match the original draft. Data recovery does not require a
removed runtime artifact to be reinstalled. Unknown/transitional states,
changed authority, missing live mounts and unstable repeated observations remain pending.

After a stable repeated observation, `observed` durably pins the invocation in the existing schema-1
launch record. It does not assert tmux readiness, accept a Server task or restore a broker grant.
Old starting/started records remain readable unchanged; older binaries cannot process the new phase.
Observed and started invocations are immutable. Exact observation/readiness retries sync the parent
again, covering uncertainty after rename. Leased startup recovery pins an unobserved invocation before waiting and must still verify current readiness
before advancing that same invocation to started and requesting exact Server confirmation.

Background running observations pass through the worker's original process-owned admission check.
A missing/revoked grant drains browser-enabled runtimes using the pinned invocation; browser-disabled
runtimes remain running. Normal exit freezes only after repeated unit and whole-cgroup proof. Missing
starting units freeze as unclean after repeated absence and cgroup proof; they never become readiness.
Prior termination/frozen receipts retain their classification and input.

Common managed stop now discovers original private authority and the pinned invocation, including
manual stop, cleanup_resources, failed-launch cleanup, cancellation and missing transient specs.
An unobserved loaded starting unit must pass the same observation proof first. An absent unit without
a pinned invocation is fenced with an absence-only check throughout stop: a later loaded unit cannot
match it. A prepared bundle without any launch intent cannot authorize stopping a loaded service.
Older internal bundles without task-bound launch authority retain their existing compatibility path.

The first observation relies on the durable original intent, the Helper's at-most-once launch rule
and its role as the sole privileged managed-runtime controller. It does not identify arbitrary units
created by an independent malicious root administrator. No new launch, mount repair, nonce or readiness
is issued by background recovery. Frozen capture, Server retention and transient cleanup remain separate.

## Authority and classification

The existing `reconcile_skill_session` and `drain_unadmitted_skill_session` wire shapes are unchanged.
Frozen captures replay unchanged. Otherwise a valid current `/proc/sys/kernel/random/boot_id` that
differs from the immutable prepared runtime selects previous-boot recovery. Empty/malformed current
boot identity never proves reboot.

Helper loads the original private bundle, ready spec intent and digest-verified draft, binding all
Node/user/account/session/snapshot/task identities, preparation digest, boot/unit and UID/GID. The
runtime root must match configured StateRoot. Absent, starting, observed and started launch phases can recover
after reboot; an existing invalid launch or missing private authority cannot mean not-started.

Work is opened without following links and must retain original runtime ownership. After passive
resource proof the common finalizer produces an unclean capture. An earlier immutable termination
receipt takes precedence: proven clean termination is not rewritten when capture was interrupted by
reboot. Normal fsync/atomic-publication and frozen replay apply. Unclean input is detached on Server
and cannot advance a clean account head.

## Passive resource proof

Under the Helper mutation lock, two complete observations precede capture or first transient cleanup:

- Current kernel boot is valid, stable and different from the original boot.
- The systemd unit is absent with no contradictory invocation, transient or user identity. Loaded
  inactive/failed units also count as present resources and remain pending.
- The complete canonical cgroup directory is absent; an empty surviving group is insufficient.
- Successful bounded namespace inventory has no original namespace. Malformed output and command
  failure cannot prove absence.
- Transient spec is absent or has exactly the original published bytes, root ownership, permissions
  and single-link identity. Today's wrapper release need not be installed for data recovery.
- Resource paths have no symlink ancestors. Complete bounded kernel mount inventory contains no
  mount covering or descending from the runtime root/work directory. Device/filesystem-root checks
  reject bind aliases of those subtrees or ancestors elsewhere in the Helper mount namespace.
  Kernel path escapes are decoded; malformed, excessive or truncated inventories remain pending.

A genuine reboot excludes old processes, descriptors and mount namespaces. Helper remains the sole
privileged runtime/mount controller and never relaunches previous-boot work. Serialized mutations
prevent new managed mounts during proof/capture. Detected resources recreated by root administration
remain intact; this is not an orphan-process proof for Docker or an untrusted privileged actor.

Previous-boot recovery issues no stop, signal, unit release, network deletion or unmount. Common
managed stop refuses old/unavailable boot identities. Manual Native stop and `cleanup_resources`
first route retained previous-boot sessions through passive recovery, even after transient spec or
old wrapper artifact removal.

## Acknowledgement and cleanup

Worker still performs exact Server termination, ingestion, publication and Helper acknowledgement.
Before acknowledgement, stop reports `state_pending` and cleanup is forbidden.

First previous-boot cleanup requires the acknowledged terminal capture and original private spec
authority, repeats passive proof, then removes only the obsolete unmounted runtime directory. Parent
sync precedes the existing immutable completion receipt. Missing specs and partially removed roots
resume through retained authority. Work, objects and snapshot references remain outside removal.

Completed replay refuses a reappearing runtime root. Across another boot it additionally requires
passive unit/cgroup/network/mount absence without removing newly present resources. Interrupted
removal without its completion marker repeats proof before recording completion. No new persistent
schema, guessed historical receipt or upload/publication contract is introduced.

## Verification boundary

Linux socket tests construct coherent earlier-boot disk metadata and exercise actual Helper capture,
replay, manual stop and cleanup. Isolated systemd tests additionally use actual unit/network inventory
and bind mounts to prove mount/alias refusal before capture and cleanup. These are controlled
earlier-boot simulations, not physical power-loss or actual kernel-reboot acceptance. The complete
real Worker/Server/Claude flow and real reboot acceptance remain separate requirements.

## Actual kernel reboot acceptance

`tests/linux_skill_kernel_reboot_test.sh` runs two isolated Linux kernels using QEMU TCG inside a
non-privileged disposable Docker container. It requires Docker, Go and network access to build the
Debian test image; it needs no host devices, KVM, exposed ports or guest network. The script selects
the Docker server's arm64/amd64 architecture by default. Set `AGENT_REMOTE_KERNEL_TEST_ARCH=amd64`
or `arm64` to select another architecture; cross-architecture runs require Docker image emulation.
The script builds both Go binaries and the Debian guest image for that architecture, while QEMU
runs in a separate image stage using the Docker host architecture. This avoids emulating QEMU itself
during a cross-architecture test. It creates a private raw ext4 disk and owns cleanup of its VM
processes, container, image and compiled artifacts.

The first boot launches the existing real systemd/bubblewrap/tmux managed Native fixture. The
synthetic tool writes `executions=entered` into its private writable Skill tree and stays running.
The guest syncs its known disk state, then the external runner SIGKILLs QEMU, bypassing guest shutdown
and all finalization hooks. A second kernel boots the same disk. The test requires a different actual
`/proc/sys/kernel/random/boot_id`, reconciles through the Helper protocol, and verifies object-backed
`local_durable` capture with `unclean=true`. The tool's execution marker stays unchanged. Premature
cleanup is refused; a synthetic exact Server retention/publication receipt permits detached cleanup
and exact replay while preserving work.

ARM64 and amd64 TCG acceptance passed with real kernels, cgroups, mounts, systemd and Helper
communication. Both guest architectures ran on an ARM64 Docker host with native QEMU. This is
recovery of a previously running managed Native session after an abrupt VM power cut, using a
synthetic tool and synthetic
Server acknowledgement. It does not establish the complete real Server/worker/Claude acceptance or
all application-specific database transaction guarantees.


When capture fails after independently proven writer exit, Helper reconciliation may return
`capture_pending`. It requires the original durable termination record, fresh quiescence checks
and no existing finalization directory. The bounded cause is `quota_exceeded`,
`insufficient_storage`, `portability_error`, or `capture_failed`; raw host diagnostics never cross
this boundary. No manifest or digest is invented. Missing/corrupt termination or uncertain runtime
resources continue to fail closed. Background inventory reports this stop separately, retains all
work and retries capture on later passes. Successful capture preserves the original clean/unclean
classification and resumes normal transfer. Explicit stop also reconciles after a failed capture;
it completes only after Server commits the exact independent stop observation. The null-digest
process result remains immutable even when later capture succeeds.
