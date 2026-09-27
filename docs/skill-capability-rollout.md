# Native Skill capability and installation defaults

The 0.2.29 release had managed Native task implementations but no Helper report or Worker heartbeat
field for them. Enabling the Server API alone could therefore never admit a managed deployment.
Skill management is now a default-on base feature. This does not claim that real model learning
or Docker Sandbox acceptance has passed.

New installs and configs with no `skill_manager_enabled` field enable it automatically. An explicit
boolean `false` remains disabled through registration, save/load and upgrades; `null` and nonbooleans
are rejected. The Server also defaults to `SKILL_MANAGER_ENABLED=true`. Neither default bypasses
runtime or storage checks or changes an account to managed mode.

Earlier installers wrote `skill_manager_enabled: false`. Because this is indistinguishable from an
administrator's deliberate disable, upgrading preserves it. To migrate that old default, set only
that field to `true`, keep all other settings, upgrade both Worker and Helper and restart both
`agent-remote-runtime` and `agent-remote-node`. Likewise change any old explicit Server
`SKILL_MANAGER_ENABLED=false` to `true` and recreate the Server. Confirm the fresh heartbeat.

Only Native is eligible. The Helper requires the actual Native dependency probe, an executable
runtime binary and an enabled configuration. It opens the production private Skill volume using the
existing root ownership, no-follow ancestry and non-overlap checks; verifies filesystem capacity
against the configured byte/percentage reserve; and writes, fsyncs, renames, reads and removes a
fresh probe file. It does not inspect or change account content. The first enabled probe may create
the private state root. Cancellation or failed checks produce no supported backend.

The Worker accepts only the complete version-1 Helper report for an allowed, currently available
Native backend. Missing, malformed, boolean/string version values, old Helpers and Docker reports
grant no capability. Every heartbeat includes an empty map on failure, withdrawing prior support.
`skill_manager_checks` exposes booleans for `configured_enabled`, `native_available`, `runtime_binary`
and `state_storage`; it contains no host paths or credentials.

Default enablement is not runtime acceptance evidence. Before enabling an existing production
account, verify a disposable Native account's takeover, deployment, session finalization and next
session inheritance. Do not synthesize capability reports or change SQL to bypass checks. Disabling
advertisement must not disable recovery/finalization of already accepted tasks.
