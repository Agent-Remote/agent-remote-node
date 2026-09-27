# 06 Commands

## Quality Gate

```sh
scripts/run-quality-checks.sh
```

Expanded commands:

```sh
bash -n scripts/*.sh tests/*.sh
gofmt -l cmd internal
go vet ./...
go test -vet=off -covermode=atomic -coverprofile=coverage.out ./...
scripts/check-coverage.sh coverage.out 45
tests/install_scripts_test.sh
git diff --check
```

Use `gofmt -w cmd internal` to format code and `go run ./cmd/agent-remote-node --help` for local command discovery. Install hooks with `scripts/install-githooks.sh`.

Release and installer commands are documented in `README.md`; do not commit generated archives, binaries, ledgers, or local configuration.

The full `go vet ./...` gate runs once before tests; `-vet=off` avoids repeating its subset inside `go test`. Protocol probe fixtures use isolated unavailable runtime paths so local Docker daemon health cannot delay them.

Installer tests keep fresh private output directories but reuse the normal content-addressed Go build cache, including cross-compiled objects. They still build and validate the real release archive on every invocation.
