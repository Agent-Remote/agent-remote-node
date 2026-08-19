# 08 Collaboration

Use short-lived `feature/`, `fix/`, `refactor/`, `chore/`, or `docs/` branches with lowercase descriptive topics.

Commits follow Conventional Commits: `type(scope): subject` or `type: subject`. Allowed types are `feat`, `fix`, `refactor`, `chore`, `docs`, `perf`, `test`, `build`, `ci`, and `style`. Use a concise lowercase English imperative subject, no trailing period, and at most 120 characters.

Install hooks with `scripts/install-githooks.sh`. `pre-commit` and `pre-push` run the full quality gate; `commit-msg` validates the subject.

Pull requests must describe task compatibility, privilege and isolation impact, deployment assumptions, rollback behavior, and test coverage. Do not bypass hooks or weaken CI to merge a change.

Node versions are independent from Device versions. `release-dependencies.json` is the immutable
source of truth for the Device proxy version, commit, and signing workflow embedded in Node release archives. Change
that pin through a reviewed source commit; release preparation must not infer it from the Node
version or from a moving branch.
