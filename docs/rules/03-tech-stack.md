# 03 Tech Stack

- Go 1.26.6, as declared in `go.mod`, is authoritative. Patch-level updates are security gates.
- Prefer the Go standard library.
- Go modules own dependency resolution; commit `go.sum` whenever dependencies require it.
- Linux systemd, cgroup v2, Bubblewrap, network namespaces, nftables, tmux, and optional Docker Sandbox form the deployment environment.
- Bash scripts own installation and release assembly.

New Go dependencies require a concrete justification, maintained provenance, and security review. Keep platform-specific code behind build constraints. Release binaries remain compatible with the targets documented in `README.md` and the release workflow.
