# Synchronization preparation boundary

The configured workspace root is administrator-controlled. Below that root, Node
accepts only the exact `<user>/workspaces/<workspace>/files` target; each directory
is created/opened relative to an already-open directory with `O_NOFOLLOW`.
The control-plane root may map to the operator-configured root without changing
user/workspace identity. Invalid IDs and different users/workspaces are rejected.

The advisory marker is written through a newly created exclusive file, synced,
renamed relative to the pinned directory, and followed by directory sync. An
existing marker symlink or hard link cannot redirect the write to its target.
The marker is not an authorization record and remains user-controlled afterward.

Linux synchronization preparation traverses pinned directory descriptors, uses
`Fchown`, `Fchmod` and POSIX ACL `Fsetxattr`, and skips symbolic links without
following them. Hard-linked regular files and special files fail preparation.
Ancestor access adds only needed traversal while preserving existing effective access.
Before widening an ACL mask, existing group-class entries are limited to their old
effective permissions, so unrelated users/groups gain no previously masked rights.
The walk checks cancellation, limits depth to 256 and total entries to 1,000,000,
and reads directory entries in bounded batches. No recursive `setfacl` subprocess
or absolute user-controlled path is used on this preparation path. Filesystems
without POSIX ACL support fail rather than silently weakening access policy.
Failure can leave partially prepared permissions; it does not report prepared.
Other legacy runtime permission paths remain outside this change.

Updated SSH sync gateways pass the actual forced-command SSH key ID to Server and
require its exact verification echo before runtime dispatch. They refuse an older
Server without the echo. Upgrade Server before Node; older gateways retain only
legacy device-level checks, not exact-key revocation protection. Existing live
transports are not revoked by this per-connection check.

Validation uses temporary files only. macOS package tests cover marker/link/path
behavior; the Linux test binary additionally verifies real POSIX ACLs, pinned
ancestor replacement, hard-link rejection, and preservation of existing named ACLs.
`TestSyncWorkspaceRuntimeIntegration` is opt-in with
`AGENT_REMOTE_SYNC_RUNTIME_INTEGRATION=1` and must run in a disposable root Linux
container: it creates a synthetic runtime identity, prepares a temporary workspace
twice, and verifies ownership and modes. It must never run against a user's host.

## Real synchronization chain

`tests/linux_sync_chain_test.sh` is an opt-in integration check using the real
CLI desktop-control protocol, temporary FastAPI Server with a SQLite database,
OpenSSH forced-command gateway, unprivileged Node task worker, privileged helper
Unix socket, Bubblewrap runtime, and official Mutagen client/agent. It creates a
disposable Linux container with namespace support; it never mounts the Docker
socket, host accounts, or host network into that container. Only temporary built
artifacts are mounted read-only, and SSH is published on a random loopback port.

Run from a macOS Docker Desktop controller with Go and the Server development
virtualenv installed. The CLI executable must have its matching built
`agent-remote-ssh` and `agent-remote-scp` siblings. The test copies those artifacts
before executing and reports the CLI SHA-256 so a concurrent rebuild cannot
silently replace the tested executable.

```sh
SYNC_CLI_BINARY=/absolute/build/agent-remote \
SYNC_SERVER_REPO=/absolute/agent-remote-server \
SYNC_MUTAGEN_SOURCE=/absolute/reviewed-mutagen-v0.18.1 \
  bash tests/linux_sync_chain_test.sh
```

The test checks actual workspace marker/ownership/ACL preparation, file delivery
in both directions, pause/resume, preservation of conflicting edits under
two-way-safe synchronization, explicit resolve after manual reconciliation,
confirmed reset, and rejection of unknown or revoked exact SSH key
identities even with a stale installed authorized_keys entry. SSH identity
selection uses a thin fixed-argument wrapper around `/usr/bin/ssh` and
`/usr/bin/scp`; all transport, authentication, authorization and agent deployment
remain real. Synthetic credentials and private keys exist only in temporary
memory/files and are removed with the container. No existing user SSH/Mutagen
state is changed. The temporary Mutagen directory is short to fit Darwin's Unix
socket path limit.

After the first bidirectional transfer, the fixture also stops the current real
`poll-once` worker loop, stops the isolated Mutagen daemon, and verifies that
`MUTAGEN_DISABLE_AUTOSTART=1` prevents a stopped daemon from being observed.
It then starts Mutagen again, confirms that the original session remains in the
daemon inventory, starts a fresh worker loop, and completes another pause/resume
transfer. This proves post-restart persistence and a subsequent real mutation;
it does not claim recovery from an arbitrary in-flight task interruption. The
desktop wrapper only records `restart_recovery=passed` after the exact
`RESTART_RECOVERY_STATUS=passed` marker is emitted, and fails closed when the
marker is absent.

This check does not prove Windows/Linux desktop native behavior, installed
systemd service lifecycle, WireGuard network setup, PostgreSQL concurrency,
revocation of already-connected transports, or delivery to a user's real Node.
The controller uses Docker Desktop's host gateway to reach a loopback-only
Server; a Linux controller is not yet implemented.

The 2026-10-18 focused run passed all six synchronization operations against the
current Server and Node source, using Mutagen v0.18.1 and CLI SHA-256
`7c1acee6867af8a31ec90a7e8cd0e630f237d593cf8b5db3966686931c526cbb`.
Both invalid-key checks reached the Server's `HTTP 403 (SSH_KEY_REVOKED)` boundary;
the revoked key remained in authorized_keys throughout the check. The disposable
container and local Mutagen daemon were stopped, and temporary state was removed.
