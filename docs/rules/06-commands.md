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
go test -covermode=atomic -coverprofile=coverage.out ./...
scripts/check-coverage.sh coverage.out 45
tests/install_scripts_test.sh
git diff --check
```

Use `gofmt -w cmd internal` to format code and `go run ./cmd/agent-remote-node --help` for local command discovery. Install hooks with `scripts/install-githooks.sh`.

Release and installer commands are documented in `README.md`; do not commit generated archives, binaries, ledgers, or local configuration.
