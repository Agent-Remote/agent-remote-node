# Persistent session stop intent

The root Helper owns a permanent negative launch fence for each Node/tool-session
identity. A stop intent prevents future launches on both Native and Docker Sandbox;
it does not prove that an admitted process stopped. A new session needs a new ID.
Never delete the fence to reuse an ID or during Skill content reclamation.

## Storage and ordering

`<runtime StateRoot>/session-lifecycle/v1` is a root-owned private directory, separate
from transient session directories, task caches, and Worker state. An immutable `scope.json` binds the directory to the original Node and format version.
Filename hashes include the `tool_session` domain. Version 1 records
contain only Node/session identity, `scope=tool_session`, and the first authenticated
stop request/operation/backend. Existing trusted runtime or managed bindings must
match before publication. An absent spec permits only a negative intent, never a
guessed runtime identity or resource deletion. Binding/browser identities remain
separate; a tool stop cannot acquire authority over an account-binding runtime.

Descriptor-relative no-follow opens reject symlinks, hardlinks, special files,
unexpected owners/modes, malformed JSON and unknown versions. Publication uses an
exclusive private temporary file, file fsync, no-replace atomic publication, and
directory fsync. Invalid or uncertain state closes admission. Unsafe directories
are never repaired by the probe or silently adopted.

Persistent lock inodes use cross-process flock. A short intent lock serializes
publication and the final launch submission gate. A separate lifecycle lock covers
runtime preparation, launch, observation and stop. Stop publishes under the short
lock before waiting for the Helper mutex or lifecycle lock, then releases it. Never
wait for the lifecycle lock while holding the intent lock. Startup takes the Helper
mutex, lifecycle lock, and finally the short lock for its last check/submission.
The fence linearizes only when Helper durably publishes intent, not when a user
clicks Stop or Server queues a task. Worker polling is currently serial and queued
tasks may wait. If submission wins, stop must drain/observe the original runtime. A marker alone
cannot change that runtime's status. Lock files and stop records are permanent.

## Entry points and receipts

The fence covers legacy Native/Docker starts, managed spec and snapshot preparation,
managed start/recovery/cancellation, ordinary stops, and cleanup batches. Validate a
whole batch before publishing its first intent. Caches cannot bypass publication or
admission checks. Managed cancellation before launch closes future admission, and
marked recovery must not restore readiness, finish launch as running, or return
`not_started` as permission to prepare again. Existing Skill bindings, specs,
invocation journals, immutable receipts, and retention rules remain authoritative.

The original successful start result remains an immutable historical receipt even
after stopping. It cannot grant new broker admission or serve as current liveness.
An upgraded Worker explicitly negotiates private `session_lifecycle_version: 1`
requests. An independent `session_execution` response binds protocol version,
session and backend, and distinguishes historical receipt replay from fresh launch
admission. It stays on the private Helper socket and is never sent to Server.
Old requests retain their old response shape; upgrade Worker and Helper together.

`SESSION_STOP_PENDING` means termination is unproven, including missing original
identity or unavailable observation. Worker treats it as retryable before generic
failure/terminal ledger publication; cleanup is pending if any member is unproven.
Existing terminal ledger entries are never overwritten. Before replaying an old
stop result, publish validated intent and obtain current original-runtime proof.
Without proof, suspend replay. With proof, replay the exact original result, even if
it was failed; never manufacture a successful historical receipt. Server does not
automatically retry historical terminal tasks. Recovering those tasks requires a
separate exact recovery contract; this feature does not claim that loop is closed.
A partially completed cleanup batch may also remain pending on retry if an earlier
member already lost its transient spec. Duplicate IDs are rejected before any
publication. Batch convergence needs durable original-identity termination evidence
in a later contract; neither a marker nor an old task cache supplies that proof.

## Capability and upgrade

The real installed Helper probe verifies private storage, locking, file fsync,
atomic publication and directory fsync inside an isolated probe subdirectory. It
never touches a real session marker. Failure advertises no support and performs no
permission repair. Worker forwards only valid entries intersected with enabled,
healthy backends:

```json
{"session_lifecycle":{"native":{"supported":true,"protocol_version":1},"docker_sandbox":{"supported":true,"protocol_version":1}}}
```

Each entry has exactly those two fields. This v1 capability proves future-start
fencing, not physical termination. Missing legacy records are not retrospective
protection; authenticated replay may install a fence but cannot turn an old cached
stop into termination evidence. Never scan old task caches to infer ownership.

Tests use isolated temporary roots beside Go packages. Required cases include
stop-before-start, in-flight admission, independent Helper processes, restart,
historical cache replay, managed cancellation/recovery, cross-backend closure,
binding isolation, malformed state and failed publication.
