# agent-remote-node

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
```

`install-ssh` 会准备受管 `authorized_keys` 文件。运行时 SSH key 由带 forced-command 限制的 `sync_ssh_keys` 节点任务写入。

`prepare_workspace` 节点任务会在 `workspace_root` 下创建远端 workspace 目录，并写入 `.agent-remote-workspace.json` marker 文件用于对账。

`create_binding_session` 节点任务会在 `account_root` 下创建远端工具账户归档，写入不含 secret 的 `.agent-remote-tool-account.json` marker，并创建带内置 `claude` agent 的 Docker Sandbox。随后 tmux 会持有一个交互式 `docker sandbox exec ... claude login` 进程。Sandbox 会把账户归档暴露在同一路径下，并让 `CLAUDE_CONFIG_DIR` 指向 `<account>/.claude`，因此 Claude 认证和设置会持久化在账户归档中。`verify_tool_account` 任务当前包含 Claude verifier，并且只报告匹配到的 auth 路径，绝不报告文件内容。

`create_browser_session` 节点任务默认启动临时 Kasm Chrome 容器。浏览器运行时会接收时区、locale、启动 URL、incognito Chrome 参数和临时 VNC 密码。它不会挂载 workspace 或工具账户目录。`stop_browser_session` 会删除容器以及 `browser_root` 下的临时 profile 目录。

## 配置

`register` 会把节点 token 写入配置的 JSON 文件：

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.0.2",
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
  "browser_public_base_url": ""
}
```

配置文件包含节点凭据，必须使用部署级文件权限保存。

`browser_public_base_url` 是可选项。为空时，节点会报告 KasmVNC 的本地 Docker 端口映射。在部署环境中，应将其设置为能访问浏览器容器 stream endpoint 的节点侧 HTTPS 反向代理 URL。

## Systemd 安装

直接安装最新 release：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | sudo bash
```

安装指定版本或自定义路径：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh |   sudo bash -s -- --version 0.0.3 --prefix /usr/local --config-dir /etc/agent-remote-node
```

从已下载的发布归档安装：

```sh
sudo ./install.sh
```

安装器会安装节点二进制，创建 `/etc/agent-remote-node`，创建 `/var/lib/agent-remote-node`，在启用 systemd 时安装 `systemd/agent-remote-node.service`，并检查 Docker、OpenSSH、tmux、Mutagen 和 TUN 可用性。它也可以覆盖 GitHub 仓库、版本、target、OS、架构、libc 标签、prefix、配置目录、状态目录、数据目录、服务用户、systemd 安装和 sudo 行为。

在管理控制台创建节点后注册它：

```sh
sudo agent-remote-node register \
  --config /etc/agent-remote-node/config.json \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token>
sudo systemctl enable --now agent-remote-node
```

## 发布打包

```sh
VERSION=0.0.2 scripts/build-release.sh
```

发布流程会构建六个归档：`darwin-amd64`、`darwin-arm64`、`linux-amd64-glibc`、`linux-arm64-glibc`、`linux-amd64-musl` 和 `linux-arm64-musl`。Go 二进制使用 `CGO_ENABLED=0` 构建；glibc 和 musl 标签用于让安装器和用户按部署环境选择包。

每个归档包含节点二进制、安装器、systemd unit、示例配置、license 和 notices。

GitHub Actions 会在 `v*` tag 上运行该打包流程，并把归档上传到 GitHub Release。

## 许可证

agent-remote-node 使用 GPL-3.0-only 许可证。详见 `LICENSE`。

第三方依赖声明见 `THIRD_PARTY_NOTICES.md`。
