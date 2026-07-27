# 04 Code Style

- `gofmt` is authoritative and `go vet ./...` must pass.
- Package names are short and domain-specific; exported names use standard Go conventions.
- Wrap operational errors with useful context while keeping public task errors bounded and non-sensitive.
- Use `context.Context` for network calls, loops, and runtime operations; honor cancellation promptly.
- Avoid background goroutine leaks. Every long-running goroutine needs an owned shutdown path.
- Validate external input before filesystem, process, network, or runtime-helper use.
- Prefer explicit structs over `map[string]any`; use dynamic maps only at protocol boundaries that require them.

Tests belong beside packages. Add regression tests for task dispatch, validation, idempotency, cancellation, and security boundaries.
