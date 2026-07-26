# Third-Party Notices

This repository is licensed under GPL-3.0-only. See `LICENSE`.

## Installed or Referenced Components

| Component | Use | License or notice |
| --- | --- | --- |
| Node.js 22 | Managed JavaScript runtime | MIT with bundled third-party notices. The installer verifies the official SHA256 checksum. Source: https://github.com/nodejs/node/blob/main/LICENSE |
| Claude Code | Managed agent runtime | Proprietary software provided by Anthropic. Installation and use are subject to Anthropic's applicable terms; this repository does not grant redistribution rights. Source: https://www.anthropic.com/legal/commercial-terms |
| Kasm Chrome image | Optional remote browser runtime | External configurable image. Mirrors and derived images must retain notices from the exact image digest. Source: https://hub.docker.com/r/kasmweb/chrome |
| wireguard-tools | Node tunnel configuration | GPL-2.0-only. Installed from the host distribution rather than embedded in the node release. Source: https://git.zx2c4.com/wireguard-tools/tree/COPYING |
| tmux | Persistent terminal sessions | ISC. Installed from the host distribution rather than embedded in the node release. Source: https://github.com/tmux/tmux/blob/master/COPYING |

The installer also uses host-distribution packages such as Bubblewrap, nftables,
iproute2, OpenSSH, ACL, Git, and GitHub CLI. They are installed by the system
package manager and retain the notices supplied by that distribution.

The node release archive contains only project-built Go binaries and installer
scripts. Node.js and Claude Code are downloaded separately during installation.

## Distribution Requirements

When a release artifact redistributes third-party software, it must include:

- the exact component name and version;
- the source URL and checksum;
- the applicable license and notice text;
- any required source code, source offer, or relinking instructions.

Do not mirror or bundle Claude Code unless the distributor has explicit rights
to do so under Anthropic's terms.
