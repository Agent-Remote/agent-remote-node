# Native stop diagnostic fixture

The real synthetic CLI lifecycle has intermittently retained an unclean/detached stop even though
its synchronized `stop-byte` file recorded interrupt byte `3`. That confirms delivery to the test
tool, not the reason for the subsequent exit classification. A matching tmux child-reaping failure
has now been reproduced and repaired with real systemd evidence below. Current full CLI revalidation
remains blocked by the Mutagen download route, so historical CLI failures are not all relabeled.

For this investigation only, the disposable CLI/SSH image supports
`AGENT_REMOTE_TEST_NATIVE_STOP_OBSERVATIONS=1`. From the Server checkout:

```sh
AGENT_REMOTE_RUN_SKILL_CLI_LIFECYCLE_TEST=1 AGENT_REMOTE_TEST_NATIVE_STOP_OBSERVATIONS=1 uv run pytest -q tests/test_skill_cli_lifecycle_live.py
```

`tests/native_stop_observation.sh` installs wrappers inside that disposable image. They preserve
original command stdout, stderr and exit status. The managed-pane exit query, interrupt/cleanup commands, session unit state query and systemd
stop/signal results are observed. Records contain timestamps, numeric pane exit status, fixed systemd
load/active/substate/result/exit fields and command exit status. Unit observations also sample only
the fixed session cgroup population (0, 1, absent or unknown), without recording its path. This is
a nearby diagnostic sample, not the exact population read by the Helper. No command arguments, paths, tokens,
configuration, tool output or session content enter the record. Logging stops at a bounded file size.
Cleanup copies the record to the test's `node/control/native-observations` and restricts it to 0600
before removing the owned container/image. The flag is off by default and affects no shipped daemon.

The first observed run passed in 133.11 seconds (`/tmp/skill-native-stop-observed.log`). Its two
normal exits were observed as pane `1|0` followed by systemd success, code 1/status 0. The bounded
record is preserved in `/tmp/skill-native-stop-observations-pass.log`. This successful run verifies
the diagnostic fixture and provides a normal comparison; it does not resolve the intermittent stop.

Three further observed CLI runs passed in 126.85, 127.83 and 125.64 seconds; all six pane exits
were normal `1|0`. Logs and traces: `/tmp/skill-native-stop-repeat-{1,2,3}.{log,trace}`. The failure
remains unresolved. The extra cleanup-result and population fields were added after these runs.

The three enhanced diagnostic cases also passed (124.51, 122.47 and 122.96 seconds), with
logs/traces `/tmp/skill-native-stop-detailed-{1,2,3}.{log,trace}`. All pane exits were `1|0`;
interrupt and cleanup commands returned zero, and exited-unit population samples were absent
or zero. The loop exited 0, was reaped and cleaned its own resources. These normal observations
do not explain the earlier failure; no production stop-classification change was made.

A separate deterministic review found that managed stop previously accepted unit success observed
only after forced `systemctl stop`. Forced cleanup may kill other writers even if the supervisor
returns zero. Clean managed input now requires normal exit and an empty whole cgroup before that
cleanup. A Linux regression reproduced the old false-clean result and passes with the fix; its
graceful control remains clean. All six real systemd lifecycle cases also passed (34.60 seconds).
This addresses a different failure direction and does not explain the historical intermittent
unclean stop. The two concurrent-fixture actual CLI cases passed before this change (375.93 seconds).


A dead tmux pane can precede collection of its process exit status. The managed supervisor waits
up to one second for that exact pane's initially missing status, without sending further terminal
interrupts after observing closure. Within this window, at most once per 200 ms, it asks the same
tmux server to run the fixed `/bin/true` child, waking child reaping on versions observed to retain
a zombie without a final pane status. This child does not access skill content and supplies no
termination authority; only the original pane's independently observed status is accepted.
Only a subsequently observed canonical zero exit can succeed;
a missing/nonzero/malformed status, a revived pane or transport failure stays unclean. This wait
never replaces the independent pre-cleanup whole-cgroup and original-invocation checks.


On 2026-09-26, actual systemd graceful-stop repetition failed 3/30 times with missing pane status.
A bounded wait alone still failed 4/100 times. A failure-only diagnostic found the pane process a
zombie and tmux sleeping; starting a fixed no-op child made that same tmux server collect the
original zero exit status. The diagnostic deliberately retained the original failed outcome.
This is evidence for a child-reaping failure, not permission to infer success from a zombie.

With the bounded child-event recovery, **100/100** actual systemd graceful stops passed. All six
lifecycle cases passed separately, including canonical interruption, signal-kill and forced cleanup.
Ten repetitions of the five actual tmux cases also passed (50/50): normal, early pty closure with
later normal exit, error exit, SIGKILL and lost server. The early-close fixture explicitly ignores
HUP so that terminal closure does not itself signal-kill the process. Its original observer fails
and the final observer passes. Tests also reject missing, late, malformed and revived statuses,
and prove that a successful no-op alone cannot certify original success.

Evidence logs: `/tmp/skill-stop-repeat-current.log`, `/tmp/skill-stop-repeat-fixed.log` (wait-only
candidate, still failed), `/tmp/skill-stop-reap-observation.log`, `/tmp/skill-stop-reaping-current.log`,
`/tmp/skill-stop-six-reaping.log`, `/tmp/skill-pane-reaping.log`. The full Node host gate and Linux
Helper package pass; host statement coverage is 62.9%. These component runs use a cached disposable
Native dependency image and current production binaries, with no Docker Sandbox substitution.

Current full CLI acceptance now passes both `normal` and `upload-unavailable` cases (2026-09-26,
65.20 s), using the current stop/probe binaries and real checksum-verified Mutagen 0.18.1. The
runner reused the existing disposable Native dependency image and mounted current proof binaries
from the host; fresh package installation was not tested. The tool remains a synthetic lifecycle
fixture, not real Claude inference. Log: `/tmp/skill-current-cli-lifecycle-cached-retry.log`.
This certifies the current tested lifecycle without assigning one cause to every historical failure.
