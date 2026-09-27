# Native Skill capability opt-in

The 0.2.29 release had managed Native task implementations but no Helper report or Worker heartbeat
field for them. Enabling the Server API alone could therefore never admit a managed deployment.
The following opt-in replaces the former unconditional advertisement gate; it does not claim that
real model learning or Docker Sandbox acceptance has passed.

Set the boolean `skill_manager_enabled` to `true` in the deployed Node config, keeping all other
fields. The default, including configs written by older installers, remains `false`. Upgrade both
Worker and Helper before opting in, then restart both services. The Server independently requires
`SKILL_MANAGER_ENABLED=true` and a fresh compatible heartbeat.

Only Native is eligible. The Helper requires the actual Native dependency probe, an executable
runtime binary and the explicit opt-in. It opens the production private Skill volume using the
existing root ownership, no-follow ancestry and non-overlap checks; verifies filesystem capacity
against the configured byte/percentage reserve; and writes, fsyncs, renames, reads and removes a
fresh probe file. It does not inspect or change account content. The first enabled probe may create
the private state root. Cancellation or failed checks produce no supported backend.

The Worker accepts only the complete version-1 Helper report for an allowed, currently available
Native backend. Missing, malformed, boolean/string version values, old Helpers and Docker reports
grant no capability. Every heartbeat includes an empty map on failure, withdrawing prior support.
`skill_manager_checks` exposes booleans for `configured_enabled`, `native_available`, `runtime_binary`
and `state_storage`; it contains no host paths or credentials.

This is a rollout opt-in, not runtime acceptance evidence. Before enabling an existing production
account, verify a disposable Native account's takeover, deployment, session finalization and next
session inheritance. Do not synthesize capability reports or change SQL to bypass checks. Disabling
advertisement must not disable recovery/finalization of already accepted tasks.
