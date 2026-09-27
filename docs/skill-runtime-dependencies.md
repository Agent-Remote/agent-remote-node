# Native skill runtime dependencies

The Native Helper recognizes Python interpreter links created in a writable skill directory. It
records them as manifest `runtime_link` entries, with the exact absolute target and a dependency
identifier; no interpreter bytes become uploaded objects. Relative links within the complete skill
directory retain ordinary symlink semantics. Unknown external links still produce `portability_error`
and retain local work.

## Supported interpreter discovery

Only `/usr/bin/python3` and `/usr/local/bin/python3` are initial probe candidates. Each candidate and
its final same-directory `python3.N` target may be recorded. Directory ancestors are opened without
following symlinks and must be root-owned and non-writable by group/others. Interpreter links must
also be root-owned and point to another Python basename in the same directory, within eight hops.
The final file must be a protected regular ELF executable for the Helper's Linux amd64/arm64
architecture, readable/executable by the session and without set-ID bits. Task/manifest paths, PATH
search, installed skill scripts and imported executables never select the probe command.

The probe runs as the non-root session UID/GID, with no inherited environment or supplementary
groups, directory `/`, Python `-I -S`, a fixed built-in `sys` query, two-second execution timeout and
128-byte stdout bound. Its identifier binds CPython major/minor, ABI flags, byte order, architecture
and exact target path, for example `cpython-3.11-little-linux-arm64-usr-bin-python3`. Package patch
versions do not change this compatibility identity. Executable identity is checked again after the
probe. This verifies the interpreter boundary, not arbitrary third-party native extension libraries.

Missing or unsupported optional interpreters are omitted. A source manifest requiring any omitted,
unknown or differently identified runtime link fails materialization before object transfer. The
adapter does not install dependencies, execute hooks, rewrite paths or migrate Python environments.
Programs under custom prefixes and `/opt/agent-remote/runtime` are not currently recognized.

## Preparation, replay and recovery

New preparation seals the discovered map in the existing capture policy and uses the identical map
for materialization. An exact preparation retry reuses the original map, including historical empty
maps; it still verifies all original snapshot/runtime/policy fields and never replaces learned work.
The mount path re-probes the sealed dependencies before changing mount state. Missing or changed
versions fail with `runtime_dependency_missing`; newly installed optional interpreters do not alter
the sealed map. Historical generic `python3` identifiers are not upgraded automatically.

Finalization and export use the sealed preparation mapping without probing today's host. Removing an
interpreter must not make the original link metadata or ordinary work bytes unrecoverable. Operators
must keep system interpreter installations stable while sessions use them; the map describes the
prepared environment and does not virtualize or freeze host package upgrades.

## Verification boundary

`tests/linux_skill_mount_test.sh` installs real Python in a disposable Linux container and runs
`TestNativeSkillPythonVenvSurvivesCaptureAndNextMountedSession`. Non-root Bubblewrap creates a venv
using the production mount/argument paths, executes its interpreter, freezes complete learning,
restores the manifest and frozen objects into a new session, then executes the restored venv and
module. The test supplies no dependency map. Separate cases check unsafe interpreter files,
cancellation, bounded output, immutable retries, and failed mount with successful historical capture.

This is a Native adapter and mount proof. It does not run real Claude, Server publication,
unprivileged Worker orchestration, SSH transport or Docker Sandbox. Managed backend capability
advertisement remains gated by those independent acceptance requirements.
