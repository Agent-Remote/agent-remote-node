# Account import fence and helper execution

Configuration writes move from the unprivileged worker into the serialized runtime helper. The
worker still obtains fresh exact-task authorization through `internal/api`; it passes only typed
account identity, backend, directory mode/epoch and files. The helper derives all paths from its own
configuration and chooses the runtime UID/GID using the existing backend identity rules. Workers
cannot select a host path, UID/GID, helper root or arbitrary command.

The privileged Linux SkillStateRoot stores an immutable account fence once a nonlegacy directory
mode is observed or takeover explicitly closes imports. The fence binds Node/user/account and the
first positive directory epoch. It persists across helper restart; corrupt or unsafe existing records
fail closed. Privileged deletion or relocation of the store is not a supported rollback. A later
legacy grant cannot remove it. The fence denies
account-level skill imports but permits unrelated configuration. No generic release/reset operation
is exposed. Verified abort/rollback must be designed with takeover before any reopening is added.

The same fence rejects new legacy Native/Docker session launches, account-binding launches and
backend migrations before account or workspace mutation. Docker callers cannot redirect the checked
account through `account_remote_path`; the Helper derives that path from the checked identity.
Existing account ancestors and discovery-root components are preflighted with no-follow directory
handles so a stored filesystem alias cannot substitute another account. A configured root alias
remains supported. This preflight does not replace takeover's exclusion of existing runtime writers.
The Native launch boundary checks again so an internal caller cannot launch a legacy spec directly.
Only a prepared managed Native snapshot may continue through its existing exact binding/mount
verification. A payload claiming a snapshot or managed mode cannot bypass the legacy task entrypoint.
These denials report `MIGRATION_PENDING`. Stops, inspection and cleanup retain their existing paths;
takeover never force-stops an existing session merely to make the directory available. Cached completed
tasks still replay their original outcome without launching another process.

An absent SkillStateRoot preserves legacy compatibility without creating the store. If a store exists,
unsafe metadata, another Node/user binding or an unsupported platform cannot silently reopen launches.
This local admission boundary excludes delayed launches; it does not prove pre-existing writers exited.

Helper import receipts bind exact task ID, account, backend and a digest of immutable import input.
They contain no config bytes. `started` is fsynced before writes; `succeeded` or a content-free failed
outcome is fsynced afterward. Success retries return the original result without writing files;
input changes under the same task ID are rejected. A `started` receipt after restart is explicitly
pending and cannot silently repeat writes over later configuration. Worker/transport failure is not
proof that the serialized helper operation stopped.

The worker preserves `CONFIG_IMPORT_PENDING` and `CONFIG_IMPORT_FAILED` as content-free task error
codes. A terminal Server task does not resolve a pending Helper receipt or prove writer quiescence.

Only config-import helper frames and task-poll responses may use a 16 MiB encoded transport limit;
other helper/control responses keep their 1 MiB limits. Import files retain the 1 MiB per-file and
8 MiB raw aggregate bounds and gain a 12 MiB encoded file-list bound at Server and Node. The helper
preflights the whole batch and writes through no-follow handles with its selected non-root ownership.

These are takeover prerequisites. No public takeover task or runtime capability is enabled here.
Takeover must still prove legacy sessions and binding processes are gone, drain/inspect prior import
receipts, close the fence under the same helper serialization, capture stable original content,
upload it and commit Server directory authority once. It cannot infer quiescence from task expiry.
