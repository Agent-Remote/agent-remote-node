# 不可变 ego-browser Wrapper 与 Skill Artifact

Node release 为远端浏览器工作携带两个独立固定的输入：

- 来自 `Agent-Remote/agent-remote-ego-browser` release `0.1.0` 的 Linux
  `ego-browser` wrapper；
- 来自 `citrolabs/ego-lite` commit
  `36053d07001a910cb806a15d42d00fdea1cdea3d` 的官方 `ego-browser` Skill
  `1.2.3`。

它们是不可变 release 输入，不会由 tool session 下载或修改。wrapper 通过 Node broker
传输有界 heredoc；Skill 定义 Agent 工作流。两者都不包含 browser runtime 或本地浏览器
profile。

## Source 与 release 校验

`release-dependencies.json` 固定 wrapper repository、version 和 signing workflow。Node
release workflow 下载四个 Linux target archive，校验 checksum、绑定 tag 的 Sigstore
identity、provenance 和 wrapper 报告版本，随后才打包。

`ego-browser-skill-source.json` 固定上游 repository、tag、commit、source path、`SKILL.md`
digest 与 canonical tree digest。CI、prepare-release 和 release 都 checkout 准确的上游
commit 并运行 `scripts/check-ego-browser-skill-source.sh`。任何不同字节、路径、大小、
symlink、special file、hard link、缺失文件或额外文件都会使校验失败。

当前 Skill 身份：

```text
version:      1.2.3
commit:       36053d07001a910cb806a15d42d00fdea1cdea3d
tree SHA-256: 262110a09678fd3e0bbb382400588dacb98b24659b3b4a57903703b65d133c7c
```

canonical digest 覆盖按 UTF-8 排序的相对路径、十进制文件大小和准确文件字节，并使用 NUL
分隔；它不是 tar archive checksum。

## Package 布局

每个 Linux Node archive 包含：

```text
ego-browser/
  ego-browser                         已验证 wrapper
  VERSION                             wrapper version
  WRAPPER_SHA256                      准确 wrapper digest
  SKILL_VERSION                       官方 Skill version
  SKILL_TREE_SHA256                   canonical Skill tree digest
  SOURCE_MANIFEST_SHA256              provenance manifest digest
  ego-browser-skill-source.json       上游 provenance
  skill/ego-browser/                  准确官方 Skill tree
scripts/install-ego-browser-runtime.sh
```

安装器在写入前校验全部 digest，拒绝 link 与 special entry，创建
`/opt/agent-remote/ego-browser/releases/VERSION`，把 wrapper 与 Skill 设为只读，再原子
切换 `current` symlink。如果同一 version 的目录已经存在，记录值与实际 digest 必须全部
相等；禁止用同一版本重新发布不同字节。

配置路径指向不可变选择：

```text
ego_browser_wrapper_path=/opt/agent-remote/ego-browser/current/bin/ego-browser
ego_browser_skill_path=/opt/agent-remote/ego-browser/current/skill/ego-browser
```

## Runtime 边界

非特权 Node daemon 管理每个 tool session 的 broker 与 owner-only Unix socket。它选择已
授权 binding/generation、续租、分配单调 sequence、创建一次性 permit、打开 relay，并在
撤销或 policy 漂移时让 pending call 失败。wrapper 不能打开 relay 或声明身份字段。每个
permit 都携带从已认证 session nonce 派生的专用 `agent-remote:<tool_session_id>` Task Space；
不同的 Task Space lock scope 会被拒绝。wrapper 使用 permit 值构造加密请求，不信任环境
覆盖值。

只有 wrapper 所需的有界 metadata 会注入 tool runtime。relay ticket、sealing key、设备
private key、脚本明文和 relay URL 不会持久化到 Skill tree、workspace、argv 或长期环境。
脚本本身从 wrapper stdin 通过 owner-only broker connection 传输。

`native` 与 `docker_sandbox` Claude tool session 均受支持。Native 使用每用户专用 runtime
identity；Docker Sandbox 使用配置的 Node service identity 作为固定非 root UID/GID，并以
`-u UID:GID` 运行 sandbox command。特权 helper 只向 worker 返回所选 UID，worker 会从 task
result 删除该内部字段。broker socket 位于 mode `0700` 目录下且自身 mode 为 `0600`；数字
POSIX ACL 只授予该 UID 目录 traverse 和 socket read/write。broker 还会独立读取 Linux
`SO_PEERCRED`，要求 UID 与进程内 session nonce 同时精确匹配。UID 0 会被拒绝。

对于 Docker Sandbox，helper 会校验并挂载 release-pinned wrapper、Skill 源与 broker 目录，
以 canonical embedded 内容刷新已挂载 account 中的 Skill tree，并注入 wrapper-first `PATH`。
nonce 仅通过 tmux process environment 传递。root-owned `0600` trusted spec 将 session ID 与
kind、UID/GID、tmux name、sandbox name 和已启用 feature path 绑定，但不会存储 nonce。
reconciliation、loopback forwarding 和 device-control path 都不会把 binding spec 当成 tool
session。

真实 Linux 证明命令：

```sh
tests/linux_ego_browser_uid_acl_test.sh
```

该测试在安装了 `acl` 的 rootful Linux container 中运行真实 broker，校验两个 backend identity
model 共用的准确 ACL、以授权
UID 建连、验证第二个 UID 被文件系统拒绝，并在测试临时授予第二个 UID 文件系统权限后，
继续验证 `SO_PEERCRED` 拒绝。

只有 Server 报告明确授权且兼容的 binding 时，wrapper 与 Skill 才可使用。artifact 缺失或
不匹配时 fail closed，绝不选择远端浏览器或其他 control channel。

## 升级与回滚

只通过通过 CI 的新 Node release 及其 dependency/provenance pin 升级 wrapper 和 Skill。
新组合完成 canary 前保留旧不可变目录。回滚时把 `current` 指向之前验证的目录并重启 Node
service；必须丢弃 broker memory、旧 ticket、permit 和 generation。结果未知的脚本不重放。

wrapper 安装完成不代表端到端 capability 已生产就绪。本地 Bridge release 当前声明
`production_ready=false`，因为不存在留存的 Site Learning 签名 private key。只有 root
evidence 记录已验证的非 null learning bundle digest 且所有其他门禁通过后，Server 才能
在生产环境开启该 capability。

指标、告警、containment 与恢复步骤见 `docs/ego-browser-operations.md`。
