# Changelog

All notable changes to this repository are recorded here.

## v0.1.4 - 2026-07-31

- Release metadata update.

## v0.1.3 - 2026-07-31

- test: include device proxy in installer fixture (5d3a0d1)
- test: run root-owned context coverage with privileges (c29393f)
- feat: add managed device control bridge (e28d439)
- test: cover namespace firewall execution (c4d82b8)

## v0.1.0 - 2026-07-31

- feat: add the managed device control bridge and bundled proxy runtime
- feat: enforce exact session bindings and persist bounded activation manifests
- build: package version-matched standalone and embedded device proxies
- test: run root-owned device context coverage only with Linux privileges
- test: include the managed device proxy in Linux installer fixtures
- test: cover namespace firewall execution (c4d82b8)

## v0.0.6-fix.1 - 2026-07-29

- fix: allow native session loopback traffic (cd59700)
- test: cover invalid forwarding lease expiry (8d578b9)
- test: cover port forwarding failure contracts (693666e)

## v0.0.6 - 2026-07-29

- fix: prioritize cancelled tunnel contexts (a819b1b)
- chore: release v0.0.6 (1a7e25e)
- feat: add isolated session port forwarding gateway (a9dd571)
- ci: add codecov reporting (84b2152)

## v0.0.5-fix.9 - 2026-07-28

- fix: refresh resized and exited claude sessions (85b0d2e)

## v0.0.5-fix.8 - 2026-07-28

- fix: redraw claude after terminal resize (b6101c8)

## v0.0.5-fix.7 - 2026-07-28

- fix: repaint terminal clients after resize (8b1fe2f)

## v0.0.5-fix.6 - 2026-07-28

- fix: signal resized process groups on linux (cd1a64a)
- test: make resize signal check portable (c8c71c0)
- fix: notify terminal agents after client resize (076771b)

## v0.0.5-fix.5 - 2026-07-28

- fix: start terminal agents at the attached client size (9de94e3)

## v0.0.5-fix.4 - 2026-07-28

- fix: install the latest managed Claude runtime (38e92f6)

## v0.0.5-fix.3 - 2026-07-28

- fix: make tmux resizing follow the active client (b4c628f)

## v0.0.5-fix.2 - 2026-07-28

- fix: initialize independent workspace git indexes (9189447)

## v0.0.5-fix.1 - 2026-07-28

- fix: preserve multi-device SSH keys (b8d34ec)

## v0.0.5 - 2026-07-27

- feat: add branding and repository quality gates (01d0b54)
- docs: refresh third-party notices (c503035)

## v0.0.4-fix.21 - 2026-07-26

- test: support unprivileged Linux CI (e592961)

## v0.0.4-fix.20 - 2026-07-26

- fix: stop exited claude sessions (de9b615)
- fix: repair managed AI tool installation (fad8d07)

## v0.0.4-fix.19 - 2026-07-26

- feat: install Node.js and AI tooling (87528d6)

## v0.0.4-fix.18 - 2026-07-25

- fix: resize tmux to the active client (3bf29d5)

## v0.0.4-fix.17 - 2026-07-24

- fix: make runtime identity files readable (9bd0767)

## v0.0.4-fix.16 - 2026-07-24

- fix: enable native developer credentials (617e03c)

## v0.0.4-fix.15 - 2026-07-24

- fix: adapt tmux to the active terminal (b1b60b5)

## v0.0.4-fix.14 - 2026-07-24

- fix: improve remote tmux display (0b9cda8)

## v0.0.4-fix.13 - 2026-07-24

- fix: keep synced workspaces ACL-free (be7979a)

## v0.0.4-fix.12 - 2026-07-24

- fix: preserve workspace file modes (3637039)

## v0.0.4-fix.11 - 2026-07-24

- fix: honor claude binding command (1ee52f3)

## v0.0.4-fix.10 - 2026-07-24

- fix: persist Claude account authentication (bfc7168)

## v0.0.4-fix.9 - 2026-07-24

- fix: make worker loops resilient (9163f5c)

## v0.0.4-fix.8 - 2026-07-23

- fix: connect browser sessions over private Docker network (32cf013)

## v0.0.4-fix.7 - 2026-07-23

- fix: support Mutagen agent bootstrap in sync sandbox (2b854f1)

## v0.0.4-fix.6 - 2026-07-23

- fix: keep native binding sessions attachable (837faae)

## v0.0.4-fix.5 - 2026-07-23

- fix: preserve ssh keys during node upgrades (5ff6719)

## v0.0.4-fix.4 - 2026-07-23

- fix: pass node config to ssh attach helper (1953582)

## v0.0.4-fix.3 - 2026-07-23

- fix node upgrade service restarts (62ec625)

## v0.0.4-fix.2 - 2026-07-23

- add node wireguard peer synchronization (d506d1a)
- fix: replace release script atomically (3a65d4b)

## v0.0.4 - 2026-07-23

- fix: support Go 1.23 in node registration (3520fb5)
- feat: add native runtime backend (ded8a0e)
- fix: avoid GitHub API for latest installer version (c755d27)
- fix: support piped installer execution (6b2eee1)
- fix: keep release version examples in sync (0043fbf)

## v0.0.3 - 2026-07-07

- fix: include config in release prepare (4b85b00)
- feat: add one-click installer (8c4a18a)
- docs: sync Chinese README with English (a318f19)
- chore: standardize release metadata (9cddae6)
- ci: allow manual release dispatch (abefa2c)

## v0.0.2 - 2026-07-07

- ci: allow manual release dispatch (abefa2c)
- chore: release v0.0.2 (601c6d5)
- build: inject node release version (4ef1c6a)
