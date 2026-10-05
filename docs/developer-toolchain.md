# Developer toolchain policy

Native sessions use the Debian/Ubuntu packages installed by `scripts/install.sh`.
The installer keeps three profiles:

| Profile | Contents |
| --- | --- |
| `full` | The existing runtime baseline plus CMake/Ninja/Meson, Clang/LLDB/GDB, ShellCheck, fzf, Valgrind, Go, Rust, Java/Maven, Ruby, PHP, Perl, PostgreSQL/MariaDB/Redis clients, and common development headers. |
| `core` | The compiler, debugger, build, shell, text, and diagnostic additions without the extra language runtimes or database clients. |
| `none` | The existing runtime baseline only. |

The default is `full`. Select a profile with:

```sh
./install.sh --developer-toolchain full
```

Native package installation always uses `apt-get --no-upgrade --no-install-recommends`.
The installer does not modify user shell startup files, install language packages into a
user home directory, or give the session host administration capabilities. Claude and Node.js
remain separate checksum-verified managed runtimes.

After a successful Native installation, `/opt/agent-remote/toolchain/MANIFEST` records the
Linux distribution, every selected Debian package version, every verified command path and
its reported version, plus the managed Claude/Node/npm/npx versions. The file is replaced
atomically and is intended for deployment audits and reproduction checks.

Docker Sandbox has a separate image userland. Host packages installed for the Node service do
not become available inside the Sandbox. A Docker deployment must therefore use a managed
image that contains the desired toolchain and should verify it from the actual session with:

```sh
printf '%s\n' "$PATH"
command -v claude git gh python3 rg jq cmake go rustc cargo java tmux
```

The Docker socket and host package manager are intentionally unavailable to Claude. This keeps
the image reproducible and prevents a session from changing the host environment.
