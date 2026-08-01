# agent-remote-node

<p align="center"><img src="assets/agent-remote-icon.svg" alt="Agent Remote 图标" width="80" height="80"></p>

<p align="center">
  <a href="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://codecov.io/gh/Agent-Remote/agent-remote-node"><img alt="Codecov" src="https://codecov.io/gh/Agent-Remote/agent-remote-node/graph/badge.svg"></a>
  <a href="https://github.com/Agent-Remote/agent-remote-node/stargazers"><img alt="GitHub Stars" src="https://img.shields.io/github/stars/Agent-Remote/agent-remote-node?style=flat&logo=github"></a>
  <img alt="Go 1.26.5" src="https://img.shields.io/badge/Go-1.26.5-00ADD8?logo=go&logoColor=white">
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

`create_binding_session` 和 `create_tool_session` 使用控制面固定的 backend。Docker Sandbox 继续兼容；Native session 使用受管 Claude 二进制、独立 Linux UID、systemd cgroup 限额、Bubblewrap 文件隔离、独立 network namespace、nftables 出口规则、带容量上限的临时目录和独立 tmux socket。Docker 和浏览器操作同样经过 root helper，node worker 不加入 Docker 组。

Native developer credential profile 提供持久 Git 和 GitHub CLI 配置目录。SSH 私钥始终留在客户端：仅获授权的 `ssh -A` attach 会在连接存活期间通过 session 级 Unix socket 代理到 runtime。gateway 仍禁止 TCP 转发、X11 转发和用户 SSH rc。

Session 端口转发使用独立的无 PTY forced command，不会开启 OpenSSH TCP forwarding。Node 兑换绑定设备和 SSH key 的一次性 token 后，一条 HTTP/2 隧道只承载一个已授权 runtime loopback 端口的 CONNECT stream。特权 Runtime Helper 自行解析受管 session network namespace，并仅通过 `SCM_RIGHTS` 返回已经连接的 socket FD；客户端不能提供 host、IP、PID、namespace path 或 container ID。当前 heartbeat 只为 Native Runtime 上报该能力；Docker Sandbox 在具备等价且可审计的 network namespace 路径前保持关闭。

Native 账户绑定要求使用已注册的设备令牌和活跃 SSH key。绑定 attach 与普通 session 共用 forced-command gateway，并在每次连接时重新向控制面鉴权。

`create_browser_session` 节点任务默认启动临时 Kasm Chrome 容器。浏览器运行时会接收时区、locale、启动 URL、incognito Chrome 参数和临时 VNC 密码。它不会挂载 workspace 或工具账户目录。`stop_browser_session` 会删除容器以及 `browser_root` 下的临时 profile 目录。

## 配置

`register` 会把节点 token 写入配置的 JSON 文件：

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.1.7",
  "supported_tool_types": ["claude"],
  "heartbeat_interval_seconds": 30,
  "poll_interval_seconds": 5,
  "ledger_path": "./agent-remote-node-ledger.json",
  "ssh_authorized_keys_path": "./authorized_keys.agent-remote",
  "attach_binary_path": "agent-remote-attach",
  "workspace_root": "/var/lib/agent-remote/users",
  "account_root": "/var/lib/agent-remote/users",
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

Session 转发不会创建公网 listener、Docker 端口发布、NAT 规则或动态 WireGuard ACL，现有受限 SSH 端口是唯一数据入口。启用控制面策略前必须先升级 Runtime Helper 和 Node 服务。

`browser_public_base_url` 是可选项。为空时，节点会报告 KasmVNC 的本地 Docker 端口映射。在部署环境中，应将其设置为能访问浏览器容器 stream endpoint 的节点侧 HTTPS 反向代理 URL。

当控制平面和节点运行在同一台 Docker 主机上时，可将 `browser_docker_network` 设置为控制平面的 Compose 网络（例如 `agent-remote_default`）。浏览器容器会加入该私有网络，控制平面通过容器 DNS 访问 KasmVNC，无需向宿主机暴露端口。

## 一条命令完成安装

先在管理控制台创建节点，然后在全新的 Debian 12+ 或 Ubuntu 22.04+ VPS 上执行一条命令：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token>
```

该命令只补装缺失的 Native Runtime 依赖，不会升级已经安装的系统包；随后启用 IPv4 forwarding 和 user namespace、配置受限 SSH gateway、通过 Anthropic 官方 installer 下载 Claude Code `latest`，并把经过 SHA256 校验的最新 Node.js 22 及 `npm`、`npx` 安装到同一个只读受管 runtime。安装器记录两个 runtime 的版本与校验和，然后注册节点、启动两个 systemd 服务，最后验证 runtime probe 和控制面 heartbeat。默认 backend 是 `native`，不需要 KVM 或 Docker。可以直接使用 root 执行，也可以使用具备 `sudo` 权限的普通用户执行；安装器只对系统操作提权。

默认 Native 依赖还会为精简 VPS 镜像补齐一致的 AI 开发命令基线：常用 shell/文本/文件工具、`rg`、`jq`、Git/Git LFS/GitHub CLI、压缩工具、`rsync`、带 pip 和 venv 的 Python 3、SQLite、C/C++ 编译工具链，以及常见的进程、网络和 DNS 排障命令。安装器会在装包后逐项验证命令，并通过重装 `gawk` 修复损坏的 `awk` alternatives 链。这些宿主工具在 Native session 内只读可见，不会授予额外权限。

命令可安全重复执行。再次执行会升级节点二进制、Claude 和所选 Node.js 发布线、刷新系统路径并复用已有 node token；只有明确需要替换注册信息时才添加 `--force-register`。

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

如果主机不满足 Linux 5.15+、systemd 249+、cgroup v2、Bubblewrap user namespace 或 locale 要求，安装器会在启用 worker 前明确失败。只安装文件、不注册和启动时，省略三个控制面参数并添加 `--no-start`。如需同时兼容 Docker Sandbox，使用 `--runtime-backends native,docker_sandbox`；主机必须已经安装带 `docker sandbox` 命令的 Docker CLI。

从解压后的 release archive 安装时，使用相同的一键参数：

```sh
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## 发布打包

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
VERSION=0.1.7 DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
```

发布流程会构建六个归档：`darwin-amd64`、`darwin-arm64`、`linux-amd64-glibc`、`linux-arm64-glibc`、`linux-amd64-musl` 和 `linux-arm64-musl`。Go 二进制使用 `CGO_ENABLED=0` 构建；glibc 和 musl 标签用于让安装器和用户按部署环境选择包。

每个归档包含节点二进制、安装器、systemd unit、示例配置、license 和 notices；Linux 归档还包含受管 device proxy。

GitHub Actions 会在 `v*` tag 上运行该打包流程，并把归档上传到 GitHub Release。

## 许可证

agent-remote-node 使用 GPL-3.0-only 许可证。详见 `LICENSE`。

第三方依赖声明见 `THIRD_PARTY_NOTICES.md`。
