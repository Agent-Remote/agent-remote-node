# Immutable ego-browser Wrapper and Skill Artifacts

The Node release carries two separate, pinned inputs for remote browser work:

- the Linux `ego-browser` wrapper from `Agent-Remote/agent-remote-ego-browser`
  release `0.1.11`;
- the official `ego-browser` Skill `1.2.3` from `citrolabs/ego-lite` commit
  `36053d07001a910cb806a15d42d00fdea1cdea3d`.

They are immutable release inputs, not files downloaded or modified by a tool
session. The wrapper transports a bounded heredoc through the Node broker. The
Skill defines the Agent workflow. Neither package includes a browser runtime or
a local browser profile.

## Source and release verification

`release-dependencies.json` pins the wrapper repository, version, and signing
workflow. The Node release workflow downloads all four Linux target archives,
verifies their checksums, verifies the tag-bound Sigstore identity, verifies
provenance, and checks the wrapper's reported version before packaging it.

`ego-browser-skill-source.json` pins the upstream repository, tag, commit,
source path, `SKILL.md` digest, and canonical tree digest. CI, prepare-release,
and release check out that exact upstream commit and run
`scripts/check-ego-browser-skill-source.sh`. A different byte, path, size,
symlink, special file, hard link, missing file, or extra file fails the check.

Current Skill identity:

```text
version:      1.2.3
commit:       36053d07001a910cb806a15d42d00fdea1cdea3d
tree SHA-256: 262110a09678fd3e0bbb382400588dacb98b24659b3b4a57903703b65d133c7c
```

The canonical digest covers sorted UTF-8 relative paths, decimal file sizes,
and exact file bytes with NUL separators. It is not a tar archive checksum.

## Package layout

Every Linux Node archive contains:

```text
ego-browser/
  ego-browser                         verified wrapper
  VERSION                             wrapper version
  WRAPPER_SHA256                      exact wrapper digest
  SKILL_VERSION                       official Skill version
  SKILL_TREE_SHA256                   canonical Skill tree digest
  SOURCE_MANIFEST_SHA256              provenance-manifest digest
  ego-browser-skill-source.json       upstream provenance
  skill/ego-browser/                  exact official Skill tree
scripts/install-ego-browser-runtime.sh
```

The installer validates all digests before writing, rejects links and special
entries, creates `/opt/agent-remote/ego-browser/releases/VERSION`, makes the
wrapper and Skill read-only, and atomically changes the `current` symlink. If a
directory for the same version already exists, every recorded and actual
digest must match; republishing different bytes under one version is rejected.

Configured paths point to the immutable selection:

```text
ego_browser_wrapper_path=/opt/agent-remote/ego-browser/current/bin/ego-browser
ego_browser_skill_path=/opt/agent-remote/ego-browser/current/skill/ego-browser
```

## Runtime boundary

The unprivileged Node daemon owns the per-tool-session broker and its owner-only
Unix socket. It selects the authorized binding and generation, renews the
lease, allocates monotonic sequences, creates one-time permits, opens the relay,
and fails pending calls on revocation or policy drift. The wrapper cannot open
the relay or declare identity fields. Each permit carries the dedicated
`agent-remote:<tool_session_id>` Task Space derived from the authenticated
session nonce; a different Task Space lock scope is rejected. The wrapper uses
the permit value for the encrypted request rather than trusting its environment.

Only bounded metadata required by the wrapper is injected into the tool
runtime. Relay tickets, sealing keys, device private keys, plaintext scripts,
and relay URLs are never persisted in the Skill tree, workspace, argv, or
long-lived environment. The script itself travels from wrapper stdin through
the owner-only broker connection.

Both `native` and `docker_sandbox` Claude tool sessions are supported. Native
uses a dedicated per-user runtime identity. Docker Sandbox uses the configured
Node service identity as a fixed non-root UID/GID and runs sandbox commands with
`-u UID:GID`. The privileged helper reports the selected UID only to the worker,
which removes it from the task result. The broker socket is mode `0600` under a
mode `0700` directory; numeric POSIX ACLs grant that UID only directory traversal
and socket read/write. The broker independently reads Linux `SO_PEERCRED` and
requires an exact UID plus the process-local session nonce. UID 0 is rejected.

For Docker Sandbox, the helper verifies and mounts the release-pinned wrapper,
Skill source, and broker directory, refreshes the canonical embedded Skill tree
in the mounted account, and injects a wrapper-first `PATH`. The nonce is passed
only through the tmux process environment. A root-owned `0600` trusted spec
binds the session ID to its kind, UID/GID, tmux name, sandbox name, and enabled
feature paths; it never stores the nonce. Binding specs cannot be mistaken for
tool sessions by reconciliation, loopback forwarding, or device-control paths.

The real Linux proof is:

```sh
tests/linux_ego_browser_uid_acl_test.sh
```

It runs the broker in a rootful Linux container with `acl`, validates the exact
ACL entries shared by both backend identity models, connects as the authorized UID, verifies filesystem denial for a
second UID, and then proves `SO_PEERCRED` denial even after the test grants that
second UID filesystem access.

The wrapper and Skill are available only when the Server reports a compatible,
explicitly authorized binding. Missing or mismatched artifacts fail closed and
never select a remote browser or a different control channel.

## Upgrade and rollback

Upgrade the wrapper and Skill only through a new Node release whose dependency
and provenance pins have passed CI. Preserve prior immutable directories until
the new combination has completed a canary. A rollback changes `current` to a
previously verified directory and restarts the Node services; broker memory,
old tickets, permits, and generations must be discarded. Scripts with unknown
results are never replayed.

Wrapper installation does not by itself authorize the end-to-end capability.
The local Bridge `0.1.11` release declares `production_ready=true` with its
retained Site Learning evidence, but the Server capability must remain disabled
until the root evidence records the exact Node/Bridge composition and all
artifact-bound canaries pass.

Operational metrics, alert conditions, containment, and recovery are defined
in `docs/ego-browser-operations.md`.
