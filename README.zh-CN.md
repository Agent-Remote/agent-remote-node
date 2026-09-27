# agent-remote-node

<p align="center"><img src="assets/agent-remote-icon.svg" alt="Agent Remote 图标" width="80" height="80"></p>

<p align="center">
  <a href="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://codecov.io/gh/Agent-Remote/agent-remote-node"><img alt="Codecov" src="https://codecov.io/gh/Agent-Remote/agent-remote-node/graph/badge.svg"></a>
  <a href="https://github.com/Agent-Remote/agent-remote-node/stargazers"><img alt="GitHub Stars" src="https://img.shields.io/github/stars/Agent-Remote/agent-remote-node?style=flat&logo=github"></a>
  <img alt="Go 1.26.6" src="https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white">
  <a href="LICENSE"><img alt="License: GPL-3.0" src="https://img.shields.io/github/license/Agent-Remote/agent-remote-node"></a>
</p>

[English](README.md) | 中文

agent-remote 的节点侧运行时。

节点运行在 VPS 上，并通过轮询控制平面与 `agent-remote-server` 通信。它不会暴露公开 HTTP 端口。

## 命令

```sh
go test ./...
```

```sh
go run ./cmd/agent-remote-node --help
```

推荐从已登录的控制工作站发起受管 enrollment：

```sh
agent-remote node install --node <node-id-or-prefix>
```

CLI 会先用 checksum 与 Sigstore bundle 验证固定版本的 Node 归档，通过独立 SSH stdin 传输并
安装成功后，才在另一次 SSH 调用的 stdin 中发送短期加入码。新 Node 默认保持 ego-browser
关闭；只有管理员明确添加 `--enable-ego-browser` 才表达启用意图，重装已有 Node 则保留原设置。

直接使用 `register --registration-token` 仅是高级旧版兼容入口，不是普通 enrollment 流程：

```sh
go run ./cmd/agent-remote-node register \
  --config ./config.json \
  --server-url http://localhost:8000 \
  --node-id <node-id> \
  --registration-token <registration-token>
```

```sh
go run ./cmd/agent-remote-node heartbeat --config ./config.json
```

```sh
go run ./cmd/agent-remote-node poll-once --config ./config.json
```

```sh
go run ./cmd/agent-remote-node run --config ./config.json
```

```sh
go run ./cmd/agent-remote-node install-ssh --config ./config.json
```

```sh
go run ./cmd/agent-remote-attach --config ./config.json --session <session-id> --device <device-id> --dry-run
go run ./cmd/agent-remote-attach --config ./config.json --binding <tool-account-id> --device <device-id> --dry-run
```

`install-ssh` 会准备受管 `authorized_keys` 文件。运行时 SSH key 由带 forced-command 限制的 `sync_ssh_keys` 节点任务原子写入；每个任务只替换指定设备的受管 key，在保留其他设备的同时清理该设备轮换前的旧 key。

`prepare_workspace` 会安装设备的稳定 SSH gateway key，并由特权 runtime helper 使用控制面用户对应的 Linux UID 创建 workspace。Mutagen 命令每次按设备和节点重新鉴权，随后在无网络且只能看到该用户数据的 Bubblewrap 环境中运行。

`create_binding_session` 和 `create_tool_session` 使用控制面固定的 backend。Native session 使用受管 Claude 二进制、独立 Linux UID、systemd cgroup 限额、Bubblewrap 文件隔离、独立 network namespace、nftables 出口规则、带容量上限的临时目录和独立 tmux socket。Docker Sandbox session 使用 Node service 固定的非 root UID/GID，并为 account/workspace 路径设置对应 ownership 与数字 ACL。Docker 生命周期状态记录在 root-owned trusted spec 中，全部 Docker 操作仍经过特权 helper；Node worker 不加入 Docker 组。

两个 runtime backend 都支持持久的 Git 与 GitHub CLI developer credential profile。SSH 私钥始终留在客户端：仅获授权的 `ssh -A` attach 会在连接存活期间通过 session 级 Unix socket 代理到 runtime。Native 与 Docker Sandbox attach 都从 root-owned runtime state 解析 tmux target 和 SSH mode，不信任控制面携带的 resource name。gateway 仍禁止 TCP 转发、X11 转发和用户 SSH rc。

Session 端口转发使用独立的无 PTY forced command，不会开启 OpenSSH TCP forwarding。Node 兑换绑定设备和 SSH key 的一次性 token 后，一条 HTTP/2 隧道只承载一个已授权 runtime loopback 端口的 CONNECT stream。特权 Runtime Helper 自行解析 Native network namespace 或 root-owned Docker Sandbox spec，并仅通过 `SCM_RIGHTS` 返回已经连接的 socket FD；客户端不能提供 host、IP、PID、namespace path、sandbox name 或 container ID。heartbeat 会为每个已启用且健康的 backend 上报该能力。

受管 macOS 设备控制只有在至少一个已启用 runtime backend 和已验证 device proxy 可用时才广告必需的
`observation_mode_v2`、`ax_state_v2`、`adaptive_settle_v2` 基础集合，以及可选的
`clipboard_payload_v2` 扩展。上报的 backend 列表是精确的：Native 还要求 network namespace
probe 通过，Docker Sandbox 则要求完整 runtime probe 通过。Server 会选择完整基础集合并带上双方支持的扩展，或使用空的 v1
fallback；部分集合和同 generation capability 变化都会被拒绝。新 generation 默认选择受支持的 v2 集合，
Server 紧急开关可强制选择空的 v1 集合。Runtime Helper 把协商结果写入
owner-only managed context，以四工具紧凑 MCP 面启动 proxy，并把隔离 session 内的零内容优化指标
固定写到 `/tmp/agent-remote-device-optimization.jsonl`。

两个 runtime backend 的账户绑定都要求使用已注册的设备令牌和活跃 SSH key。绑定 attach 与普通 session 共用 forced-command gateway，每次连接都会重新向控制面鉴权；Docker tmux 只能经 trusted helper 进入。

`create_browser_session` 节点任务默认启动临时 Kasm Chrome 容器。浏览器运行时会接收时区、locale、启动 URL、incognito Chrome 参数和临时 VNC 密码。它不会挂载 workspace 或工具账户目录。`stop_browser_session` 会删除容器以及 `browser_root` 下的临时 profile 目录。

## 配置

不可变 wrapper/Skill 源制品合同见 `docs/ego-browser-artifacts.zh-CN.md`，runtime UID/ACL 诊断、指标和恢复流程见 `docs/ego-browser-operations.md`。Native 与 Docker Sandbox 上符合条件的 Claude tool session 都会收到 runtime-scoped broker capability。Docker 启动会校验并挂载 release-pinned artifact，以准确的受管内容刷新 account 中的 Skill tree，把 wrapper 目录放在 `PATH` 首位，并且只通过进程环境传递 nonce。

高级兼容命令 `register` 会把节点 token 写入配置的 JSON 文件：

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "",
  "supported_tool_types": ["claude"],
  "heartbeat_interval_seconds": 30,
  "poll_interval_seconds": 5,
  "ledger_path": "./agent-remote-node-ledger.json",
  "ssh_authorized_keys_path": "./authorized_keys.agent-remote",
  "attach_binary_path": "agent-remote-attach",
  "workspace_root": "/var/lib/agent-remote/users",
  "account_root": "/var/lib/agent-remote/users",
  "skill_state_root": "/var/lib/agent-remote-skill-state",
  "skill_state_policy": {
    "checkpoint_bytes": 1073741824,
    "directory_bytes": 10737418240,
    "entries": 100000,
    "minimum_free_bytes": 2147483648,
    "reserve_percent": 5
  },
  "docker_binary_path": "docker",
  "tmux_binary_path": "tmux",
  "mutagen_binary_path": "mutagen",
  "browser_root": "/var/lib/agent-remote/browser-sessions",
  "browser_image": "kasmweb/chrome:1.18.0",
  "browser_public_base_url": "",
  "browser_docker_network": "",
  "allowed_runtime_backends": ["docker_sandbox", "native"],
  "runtime_socket_path": "/run/agent-remote/runtime.sock",
  "runtime_binary_path": "/usr/local/bin/agent-remote-runtime",
  "claude_runtime_path": "/opt/agent-remote/runtimes/claude/current/bin/claude",
  "device_proxy_path": "/opt/agent-remote/device/current/bin/agent-remote-device-proxy"
}
```

配置文件包含节点凭据，必须使用部署级文件权限保存。

`skill_state_root` 用于持久化技能副本、journal、账户围栏与导入收据。Linux root Helper 要求该目录由 root
所有、权限为 `0700`，祖先目录也必须满足安全检查，并与 runtime、账户、workspace、browser
和 broker 根目录独立；不能放进安装器授予 worker 所有权的数据目录。未设置时使用上述默认值；
显式设置 policy 时须提供全部字段。字节上限单位是 byte，`entries` 计量 manifest 条目数，
启动保留空间取 `minimum_free_bytes` 与文件系统容量的 `reserve_percent` 百分比中的较大值。
这些配置和本地存储基础尚不启用 managed session；后端挂载、认证传输和 Server finalization
完成集成与验证后，才能公布 skill-manager 能力。


在 Linux 上，安装器安装或升级受管 runtime 后会自动运行 `configure-ego-browser`。它从不可变
的 `current` release 同步 wrapper/Skill 的路径、版本和摘要，同时保留现有的
`ego_browser_enabled` 值；安装或升级 artifact 不会隐式开启 bridge。若 runtime 是单独安装的，
可手动同步：

```sh
sudo agent-remote-node configure-ego-browser \
  --config /etc/agent-remote-node/config.json \
  --runtime-root /opt/agent-remote/ego-browser
```

只有在 Server evidence 和 artifact-bound canary 通过后才启用 bridge：

```sh
sudo agent-remote-node configure-ego-browser \
  --config /etc/agent-remote-node/config.json \
  --runtime-root /opt/agent-remote/ego-browser \
  --enable
sudo systemctl restart agent-remote-runtime.service agent-remote-node.service
```

Native session spec 现在包含由 root 生成且不含敏感信息的 runtime 配置快照。supervisor 和 Claude
子进程不再需要读取受保护的节点配置，因此即使 `/etc/agent-remote-node/config.json` 只有 owner
权限，动态 session 用户也能正常启动。请同时升级 Runtime Helper 和 Node 二进制；已有的
root-owned session spec 可在后续节点配置变化后继续使用。

Session 转发不会创建公网 listener、Docker 端口发布、NAT 规则或动态 WireGuard ACL，现有受限 SSH 端口是唯一数据入口。启用控制面策略前必须先升级 Runtime Helper 和 Node 服务。

`browser_public_base_url` 是可选项。为空时，节点会报告 KasmVNC 的本地 Docker 端口映射。在部署环境中，应将其设置为能访问浏览器容器 stream endpoint 的节点侧 HTTPS 反向代理 URL。

当控制平面和节点运行在同一台 Docker 主机上时，可将 `browser_docker_network` 设置为控制平面的 Compose 网络（例如 `agent-remote_default`）。浏览器容器会加入该私有网络，控制平面通过容器 DNS 访问 KasmVNC，无需向宿主机暴露端口。

## 安装

普通受管流程先在管理控制台创建 Node 及其 SSH 传输信息，再从已登录的控制工作站运行：

```sh
agent-remote node install --node <node-id-or-prefix>
```

该流程会先验证并安装 release，再向控制面申请一次性加入码；release 归档和加入码分别使用
独立的 SSH stdin。持久化 exchange ID 让同一命令可以恢复中断的 enrollment，不会把加入码或
最终 Node token 放入 argv、环境变量、URL、日志或终端输出。新 Node 默认关闭 ego-browser
capability，已有 Node 保留原值；`--enable-ego-browser` 只表达管理员的明确意图，仍必须通过
本机 release 校验。

直接传 registration token 的安装器仅保留给全新 Debian 12+ 或 Ubuntu 22.04+ VPS 上的高级
旧版 provisioning。缺少 `cosign` 时，安装器会在本次运行的私有临时目录下载并校验固定 SHA-256
的官方 verifier；release checksum 或 Sigstore workflow identity 任一校验失败时，直接下载路径
仍会关闭失败：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token>
```

该兼容入口会补齐所选 backend 的依赖但不会升级已经安装的系统包，配置受限 SSH gateway，安装受管 device proxy，注册节点，启动两个 systemd service，并验证 runtime probe 与控制面 heartbeat。它会在 argv 中携带短期 registration token，因此只能用于隔离的人工维护环境，并且不得写入 shell history 或日志。使用默认 `native` backend 时，它还会启用 IPv4 forwarding 和 user namespace，通过 Anthropic 官方 installer 下载 Claude Code `latest`，并在同一个只读受管 runtime 中安装带 `npm` 和 `npx` 的最新已验证 Node.js 22 release。默认配置不要求 KVM 或 Docker。请以 root 或具有 `sudo` 权限的用户运行；安装器只会为系统操作提权。

默认 Native 依赖还会为精简 VPS 镜像补齐一致的 AI 开发命令基线：常用 shell/文本/文件工具、`rg`、`jq`、Git/Git LFS/GitHub CLI、压缩工具、`rsync`、带 pip 和 venv 的 Python 3、SQLite、C/C++ 编译工具链，以及常见的进程、网络和 DNS 排障命令。安装器会在装包后逐项验证命令，并通过重装 `gawk` 修复损坏的 `awk` alternatives 链。这些宿主工具在 Native session 内只读可见，不会授予额外权限。

命令可安全重复执行。再次执行会升级节点二进制、Claude 和所选 Node.js 发布线、刷新系统路径并复用已有 node token；只有明确需要替换注册信息时才添加 `--force-register`。

全新安装时，安装器不再使用知名 WireGuard 端口 `51820`，而是从动态端口段 `49152-65535` 随机选择一个本机未占用的 UDP 端口。选中的端口会一致写入 WireGuard interface、Runtime Helper unit、Node 配置和对外上报 endpoint，后续升级继续复用。已有的旧版 `51820` 安装会自动迁移；只有确实需要保留它时才显式传入 `--wireguard-listen-port 51820`。如果以后某条公网路径封锁了当前端口，可用 `--rotate-wireguard-listen-port` 重新运行安装器，在宿主机或云防火墙中放行输出的 UDP 端口，然后在每台客户端执行 `agent-remote wireguard config` 并重启隧道。本机端口未占用不等于上游链路一定可达，因此轮换后仍需从受影响客户端验证真实握手。

安装指定 node 版本或固定官方 Claude 版本：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --version <node-version> \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token> \
  --claude-version <claude-version>
```

如需严格固定 Claude artifact，同时添加下面三个参数：

```sh
--claude-version <version> --claude-source <artifact-or-url> --claude-sha256 <sha256>
```

Node.js 默认安装经过校验的最新 22.x 版本。可用 `--nodejs-version` 固定官方版本，或同时提供下面三个参数固定自备归档：

```sh
--nodejs-version <version> --nodejs-source <archive-or-url> --nodejs-sha256 <sha256>
```

如果所选 backend 的 probe 不满足要求，安装器会在启用 worker 前明确失败。Native 要求 Linux 5.15+、systemd 249+、cgroup v2、Bubblewrap user namespace 与配置的 locale。Docker Sandbox 要求 Linux、root helper、tmux、Git、POSIX ACL 工具、有效的非 root runtime identity，以及 daemon 和 `docker sandbox` 命令均可用的既有 Docker CLI。使用 `--runtime-backends native,docker_sandbox` 同时启用二者，或使用 `--runtime-backends docker_sandbox` 仅启用 Docker。只安装文件、不注册和启动时，省略三个控制面参数并添加 `--no-start`。

从解压后的 release archive 安装时，使用相同的一键参数：

```sh
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## 发布打包

Node 与 Device 使用彼此独立的发布版本。`release-dependencies.json` 固定每个 Node 发布包内置的
Device proxy 精确 tag、commit 和制品签名 workflow。升级该依赖必须作为可审查的源码变更完成；准备新的 Node 版本
不会改写它。

Linux 发布包要求按架构和 libc 提供对应的受管 device proxy：

```text
$DEVICE_PROXY_DIR/linux-amd64-glibc/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-arm64-glibc/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-amd64-musl/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-arm64-musl/agent-remote-device-proxy
$DEVICE_PROXY_DIR/<target>/VERSION
```

安装器会验证摘要，将其安装到 `/opt/agent-remote/device/releases/<version>/` 并原子切换
`current`。同一版本出现不同内容时会拒绝覆盖；proxy 缺失或不可执行时 capability 保持关闭。

```sh
DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
```

发布流程会构建六个归档：`darwin-amd64`、`darwin-arm64`、`linux-amd64-glibc`、`linux-arm64-glibc`、`linux-amd64-musl` 和 `linux-arm64-musl`。Go 二进制使用 `CGO_ENABLED=0` 构建；glibc 和 musl 标签用于让安装器和用户按部署环境选择包。

每个归档包含节点二进制、安装器、systemd unit、示例配置、license 和 notices；Linux 归档还包含受管 device proxy。

GitHub Actions 会在 `v*` tag 上运行该打包流程，并把归档上传到 GitHub Release。

## 许可证

agent-remote-node 使用 GPL-3.0-only 许可证。详见 `LICENSE`。

第三方依赖声明见 `THIRD_PARTY_NOTICES.md`。

### 配置导入所有权校验

配置导入要求 Server 支持绑定精确任务的
`/api/v1/node-api/tasks/{task_id}/config-import-authorization` 接口；升级时先更新 Server，
再更新 Node。Node 写入前重新检查账户目录模式，授权不完整、过期或服务不可用时拒绝写入，
不能信任排队任务自报的 legacy 模式。账户处于 migrating 或 managed 模式时，包含
`~/.claude/skills` 的整批任务在任何文件写入前返回 `SKILL_MANAGER_OWNS_PATH`。
可用 CLI `account import-config --exclude-skills` 迁移其他配置。插件 skills 与项目历史
继续遵循各自选择规则；成功任务的重试只回报原结果。

Linux worker 将授权后的导入交给特权 Helper；升级时应同时更新两个 Node 二进制。
Helper 决定账户路径和非 root 运行身份。私有 `skill_state_root` 持久保存账户围栏与
精确输入任务收据，应随账户数据一同备份，不能通过删除该目录来清除失败导入。
Helper 一旦观察到 migrating/managed 模式，旧 legacy 授权便不能重新开放技能导入。
同一围栏还以 `MIGRATION_PENDING` 拒绝新的 legacy 会话、绑定进程和后端迁移；不会强停
已有会话，显式停止入口仍然可用。Server 同样拒绝非 legacy 账户的新绑定及迁移规划。
受管绑定和后端迁移须完成各自的快照适配后才能重新开放。
`CONFIG_IMPORT_PENDING` 表示中断写入需要核实，`CONFIG_IMPORT_FAILED` 重放已保存的失败；
两者都不会自动重新写入。非 Linux Helper 明确不支持配置导入。

导入限制为单文件 1 MiB、原始合计 8 MiB、编码后的文件列表 JSON 12 MiB。
仅任务轮询和 Helper 导入消息使用 16 MiB 传输上限，普通调用仍为 1 MiB。
运行 `bash tests/linux_config_import_test.sh` 可验证真实 Linux 权限、重启和满配额 socket
传输。恢复边界见[账户围栏与收据](docs/skill-account-fence.md)。

这些检查不启用目录接管，也不广告 skill-manager 能力。首次接管仍须排空既有导入任务，
并在稳定初始捕获期间排除 legacy 写入者。

内部快照下载客户端核对原始快照、任务记录 UUID 及完整 Node/用户/账户/会话/后端身份，
严格验证清单元数据、摘要和成员解析结果。文件以流方式验证长度、哈希、文本分类及 HTTP
校验头，拒绝重定向和内容转换；取消请求会关闭阻塞读取。下载内容仍须经过 Helper 的原子准备
和固定系统制品校验才能启动。受管 Native 队列现会校验原始指针，串联租约内恢复、启动与
持久化确认；非法标记不能进入旧版启动。收尾传输和完整运行时验收完成前仍不广告受管能力。

内部 worker 准备协调器现会维持精确 snapshot、任务记录和领取轮次的短期租约，串联原始快照下载、
受管 Native spec 创建及 Helper 流式副本准备。续租不确定时取消依赖操作，只返回本地准备收据。
另有内部启动协调器让同一租约贯穿原始启动恢复、副本准备、运行 UID 授权与启动；模糊失败或
租约失效后，会独立停止原始会话，即使 Helper 已先返回成功。恢复要求原 invocation 仍存活，且
broker 中原有 session/nonce/UID 授权仍有效；broker 重启或撤销后会停止并保留原会话，不授予
替代凭据。专用 Native 队列已使用该协调器，保留非终态失败供恢复；仅准备成功不等于会话就绪。

受管启动确认客户端将就绪或停止结果绑定到原始快照、任务记录和领取轮次，只接受已提交且
精确回显的收据。独立后台循环查询待确认收据，不依赖任务再次下发；旧结果只有在新领取轮次
明确排除旧提交后才能被替换。若精确观察确认原提案未被接受且任务已取消，账本保留原提案与
取消证据并将其退休，停止后台重复查询和迟到启动重放；这不授予运行或清理权限，独立收尾
流程仍负责保存原始工作数据。任务账本现会先
同步私有临时文件，再原子替换并同步父目录；发布结果不确定时阻止继续执行任务，直到重新
打开账本。空文件或 null 等损坏账本不能被当作“从未执行过任务”。

最终化 API 客户端现已分别接入收尾计划、流式上传、完整持久化、查询与发布，绑定原始快照、
终止分类、树摘要和上传轮次；异常退出只接受 detached 发布结果。真实 Server 与 Go 联调已验证
持久化、发布和精确重试。Helper 现会将独立私有文件对象与 journal 一起原子保存，并向 worker
传递绑定原始身份的只读文件描述符；原工作目录和临时运行文件删除后仍保留待上传的原字节。
旧 journal 仅在 finalizer 证明写入者退出且原字节校验通过后升级。独立后台循环现会分页扫描 Native
冻结记录、上传原字节，并在 `<ledger_path>.skill-finalizations` 中分别持久化 Server 保存与发布收据。
重启和丢失回复后继续同一输入；冲突及被取代的发布保持保留。精确收据现会先推进 Helper 特权 journal，
再由独立操作核对原写入者已退出并清理临时资源。清理完成另有持久记录，原 work 与冻结对象始终保留。
后台循环也会按原始已封存启动记录核对未冻结的 Native 会话，在确认同一开机周期内整个 cgroup
已无写入者后冻结自然退出数据并传输；临时 spec 丢失不阻止此恢复。同一开机周期内尚未启动或无法核验的记录继续保留；运行中观测不能恢复 broker 授权。首次上传前现会向 Server 独立确认精确终止输入，
不依赖完整运行清单同步或旧准备租约。回复丢失时重用原始冻结输入；stopped 收据不代表内容已保存。
同一开机周期内已封存启动的会话丢失 broker 授权后，后台现会通过独立 Helper 操作停止原运行时并
保留内容。启动与后台核对互斥，新注册不能恢复原授权；未启用浏览器的会话保持运行。确认进入不同
内核 boot 后，准备完成、启动中和已启动副本现会重复核对 unit/cgroup/网络命名空间及挂载缺席，再按
unclean 恢复；已有终止分类保持不变。Server 已确认保存的旧临时目录可继续中断清理，不能停止或移除
新开机周期的资源。同期开机的 starting 记录现可在重复核验后保存独立的 observed invocation，
再进入存活检查、授权丢失停止或自然退出收尾；观察阶段不代表就绪，租约内恢复仍须核验原运行时。
公共停止路径也会使用原 invocation，包括临时 spec 丢失时。详见 `docs/skill-runtime-recovery.md`；
真实内核重启验收仍待完成。

独立的 `prepare_managed_session_spec` 操作现可创建受信任 Native spec，但不会启动运行时。
它先保存私有创建意图和不含 nonce 的不可变完整草稿，再发布 spec 与完成收据；同一启动周期内
可恢复中断的发布。完成后的重试仅核对原始文件和运行身份，缺失或变化时保留现场并报错。
操作要求已有账户围栏和账户目录，不重写账户技能。Native 队列已让同一租约覆盖准备、启动和确认。

`start_managed_session` 现可至多一次启动已准备的 Native 会话：systemd-run 前持久化私有启动意图，
就绪后封存原始 invocation ID。同一启动周期内恢复只核对原服务及挂载，不修复运行中的挂载；
服务缺失或停止则进入收尾，绝不重新执行。完成收据在临时目录清理后仍可重放历史就绪结果，
不代表当前存活。断连取消会停止写入者并保留未清洁副本。以上路径已由隔离的真实 systemd 配合
测试程序验证。`recover_managed_session` 另行检查当前存活，历史启动已结束则要求收尾；worker
已确认运行时在 broker 重启后的处理及旧开机周期副本恢复已有独立路径。正常停止保存与状态报告
已接通，见下文；真实 Server/worker/Claude 完整验收仍待完成。

Helper 的独立 `prepare_skill_snapshot` 流式操作已能为已有受信任 Native spec 准备原始快照。
它只请求清单中的摘要，独立验证每个对象，并原子保存工作副本及完整输入身份；相同输入重试
保留会话学习，固定输入变化则失败。连接断开会取消准备，中断传输清除未发布的暂存目录。
创建受信任 spec 及挂载、启动、租约恢复由专用 Native 队列协调。

准备和 Native 挂载会校验快照固定的系统版本：ego-browser 的版本、commit 与内容树须匹配
内嵌制品，启用的 wrapper 制品另行验证；设备技能须匹配 Helper 编译版本与已选协议，未选择时
不挂载。挂载重试还会核对实际系统副本，不自动修复已挂载文件。缺少固定版本的旧快照仍能恢复
和收尾，但不能使用当前制品重新启动。

Native 接管、稳定捕获与任务恢复见
[`docs/skill-account-capture.md`](docs/skill-account-capture.md)。Helper 先持久化账户围栏，
只检查、不停止历史及本地 Native 写入者，再将完整内容与清单保存在独立私有副本中供重试。
未完成导入意图或无法证明的后端状态会阻止捕获。专用 Native Worker 任务现已用同一当前租约
覆盖捕获和上传，重启后复用 Server 预约与 Helper 原始捕获，不重复导入后来修改的原目录。
Server 普通会话入口已接通原节点 Native 预约；Docker 沙箱及孤儿资源清点、历史后端复制恢复、
可验证回滚和真实运行时验收仍待完成。
原目录始终保留，这些原语不会广告受管能力。

账户后端迁移在备份前持久记录完整迁移意图，复制及目标/回滚 ACL 命令分别由 systemd 监督。
退出状态不明时保留 `STATE_MIGRATION_PENDING`，禁止并发回滚或以新任务绕过。Helper 同步账户和
备份文件系统后才持久记录终态；同一输入重放已保存的成功/失败，不重复复制或权限修改。终态失败
返回 `STATE_MIGRATION_FAILED`，不代表回滚成功。复制阶段仍保留 `STATE_COPY_PENDING` 和
`STATE_COPY_FAILED`。完整迁移及复制回执可满足 Native 接管的后端写入者检查，旧复制阶段记录仍不足。
中断后仍处于 started 的任务恢复、混合后端退出证明尚待完成；普通 Native 会话入口已接通
接管预约，如上所述。原账户与备份继续保留。

Docker Sandbox 的安装及 Helper 探测会核对 create、exec、rm 各命令的实际用法；仅返回退出码 0 的
停用提示不代表支持。若 Docker 已移除旧 sandbox 插件，新账户/会话启动会在写入账户和工作区前失败；
停止与清理保留受信任运行时记录，避免丢失遗留资源证据。该检查不代表整个沙箱已无写入者，
也不代表受管 skill 后端验收通过。独立的新版 sbx 接口尚未替代现有运行时适配器。

受管 Native 停止先冻结原会话，再最多尝试向 Server 保存 10 秒。限时尝试与独立后台恢复共用
同一个收尾日志实例；网络失败保留冻结输入，不会为了等待网络而维持写入进程。停止任务仅回报
不可变的进程终止身份，最新保存进度通过原始快照 UUID 在 Server 查询。

`tests/linux_skill_kernel_reboot_test.sh` 提供独立虚拟机的双内核重启验收：先启动真实受管 Native
运行时，再强制切断虚拟机电源，最后在新内核下恢复持久 Skill 数据。ARM64 与 amd64 验收均已通过；
可用 `AGENT_REMOTE_KERNEL_TEST_ARCH=arm64` 或 `amd64` 选择来宾架构，默认使用 Docker 宿主架构。
测试范围与依赖见[恢复协议](docs/skill-runtime-recovery.md)。

部署传输客户端现通过独立的任务/尝试接口读取原完整账户目录，校验 Server 的规范计划摘要和
目录摘要，并在当前领取租约下流式验证清单文件。专用 Worker 执行现以同一个持续续租覆盖
下载、Helper 准备和精确 Server 确认。

独立的 `prepare_skill_deployment` Helper 操作现按原尝试身份原子保存完整目录。私有回执固定
全部输入，重复执行重新校验保留字节，不改账户或会话状态。Worker 在确认前持久保存原回执，
独立只读查询无需调用 Helper 即可恢复丢失的确认响应。专用失败／取消现使用下述持久恢复流程；
Server 普通轮询现可调度兼容的待部署目标，Node 能力启用仍待验收，详见[独立部署准备](docs/skill-deployment-preparation.md)。

独立 `drain_skill_deployment` Helper 操作现与准备共用序列化锁，持久封禁原尝试；响应丢失或
重启后仍可恢复同一回执，并保留已有内容。Worker 必须先保存 Server 已提交的原撤权意图，才能调用排空。

专用 HTTP 客户端现固定 Server 原撤权意图与精确 Helper 排空凭据，支持终态确认及只读结果
观察；绑定或分类改变会拒绝，不会自动重试不确定的写请求。Worker 现分阶段持久保存原请求、
已提交意图、Helper 排空回执及终态观察；后台恢复无需准备租约，并保留原成功提案。已确认成功
不能降级。Server 公共调度已实现，完整运行时验收及 Node 能力广告仍待完成。

[首次使用验收](docs/skill-first-use-acceptance.md)覆盖普通受理和轮询、真实 systemd 子写入进程、
Helper 初次封禁和捕获、接管确认丢失恢复及完整部署；该证据不代表 Claude 启动或 Docker/sbx 验收。

原 Native Node 的冻结数据可由配套 CLI 的
`skill state export --snapshot UUID --scope account-directory --account-id UUID --output PATH`
经既有 SSH 强制命令导出。只读取 Helper 已保存的完整冻结捕获，持续重验原用户、设备和公钥，
不依赖 Server 上传配额；不会停止会话、确认上传或回收数据。
协议及验收范围见[冻结快照导出](docs/skill-node-export.md)。
