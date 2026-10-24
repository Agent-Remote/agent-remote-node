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

`agent-remote-node` 是部署在 VPS 主机上的 Go 运行时。它轮询 Agent Remote 控制面，通过 runtime helper 执行获准任务，不暴露公开入站控制 API。

## 运行边界

- 无特权 worker 轮询控制面，处理任务 lease 和持久完成记录。
- 特权主机操作统一隔离在 runtime helper 后面。
- Native session 使用独立 Linux 身份、systemd 限制、Bubblewrap、network namespace、nftables 规则、有界临时存储和私有 tmux socket。
- Docker Sandbox session 使用非 root 会话身份和 root-owned trusted spec；worker 不加入 Docker 组。
- forced-command SSH 是唯一接入路径。会话端口转发使用独立的受权 loopback 隧道，不开启 OpenSSH TCP forwarding。
- 浏览器 session 使用隔离的临时容器和 profile，不挂载工作区或工具账户目录。

## 命令

运行检查：

```sh
go test ./...
go run ./cmd/agent-remote-node --help
```

受管 enrollment 从已登录的控制工作站发起：

```sh
agent-remote node install --node <node-id-or-prefix>
```

CLI 会验证固定版本的 Node 归档，通过独立 SSH stdin 通道传输，申请一次性加入码，再在另一次 SSH 请求中发送加入码。新 Node 默认关闭 ego-browser，只有管理员显式提供 `--enable-ego-browser` 才会启用意图；重新安装已有 Node 会保留原设置。

直接注册命令仅用于高级兼容场景：

```sh
go run ./cmd/agent-remote-node register --config ./config.json \
  --server-url http://localhost:8000 --node-id <node-id> \
  --registration-token <registration-token>
go run ./cmd/agent-remote-node heartbeat --config ./config.json
go run ./cmd/agent-remote-node poll-once --config ./config.json
go run ./cmd/agent-remote-node run --config ./config.json
go run ./cmd/agent-remote-node install-ssh --config ./config.json
```

`agent-remote-attach` 要求准确的 session/device 或 binding/device 身份，并在交互接入前执行 dry-run。

在 Linux 主机使用仓库安装器安装最新已验证的 Node 归档：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- --server-url https://agent-remote.example.com \
  --node-id <node-id> --registration-token <token>
```

使用 `--version <version>` 固定发布版本。安装器会先验证归档、配置所选运行时 backend，
并仅在所有探测通过后启动服务。使用
`DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh` 构建归档；校验和与包
不能替代主机、SSH 或 broker 验收。

安装指定版本或已解压的归档：

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- --version <node-version> \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> --registration-token <registration-token> \
  --claude-version <claude-version>
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## 配置与状态

JSON 配置包含服务器 URL、Node 身份、轮询间隔、ledger 路径、SSH 路径、workspace/account 根目录、运行时 backend 策略和受保护 helper 路径。配置包含 Node 凭据，必须只允许 owner 读取。

工作区准备与同步会把目标路径绑定到获准的用户、工作区、设备和 SSH key。特权操作前会校验路径、标识、运行时 backend、资源限制、符号链接、硬链接和特殊文件。Skill 状态、运行时状态、浏览器状态和工作区数据使用分开的受保护根目录。

Native 和 Docker backend 会上报真实能力。只有 backend 和 Device proxy 探测通过时，Server 才能协商 Device v2 capability；能力不完整或未知时 fail-closed。

## 开发与发布

需要 Go 1.26.6 或更高版本。运行：

```sh
bash -n scripts/*.sh
gofmt -l .
go vet ./...
go test ./...
scripts/run-quality-checks.sh
```

使用一次性 server/node home，并在验收后删除测试容器、临时 key 和浏览器 profile。发布归档由 `scripts/build-release.sh` 构建；包和校验和验证不能替代原生运行、SSH、broker 或远程会话证据。

## 脚本入口

仓库保留以下安装、运行时打包和验收入口：

```sh
# 构建发布归档（DEVICE_PROXY_DIR 提供已签名的 device proxy）。
DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
# 准备发布元数据并运行包及源码检查。
scripts/prepare-release.sh
python3 scripts/generate-release-policy.py --help
scripts/check-ego-browser-skill-source.sh
scripts/check-managed-skills.sh
scripts/check-coverage.sh
# 安装 Node 及可选托管运行时。
scripts/install.sh --server-url <url> --node-id <id> --registration-token <token>
scripts/install-claude-runtime.sh
scripts/install-nodejs-runtime.sh
scripts/install-device-proxy.sh
scripts/install-ego-browser-runtime.sh
# 为本地开发安装仓库 hooks。
scripts/install-githooks.sh
# 主机文件系统和配置导入验收。
bash tests/linux_native_filesystem_test.sh
bash tests/linux_config_import_test.sh
```

`install.sh` 是受支持的主机安装器。运行时和 device proxy 安装器由该流程调用，或由管理员
在明确升级组件时调用；它们不会自行登记 Node。

## 文档

- 运行时与包规则：`docs/rules/02-architecture.md`、`docs/rules/09-state-persistence.md`、`docs/rules/10-node-control.md`
- 终端行为：`docs/terminal-interaction.md`
- 会话与附件合同：`docs/session-lifecycle.md`、`docs/session-exact-attach.md`、`docs/session-exact-attachment-transfer.md`
- 工作区安全：`docs/workspace-sync-safety.md`
- Ego-browser 运行时：`docs/ego-browser-artifacts.md`、`docs/ego-browser-operations.md`
- Skills：`docs/skill-manager.md`

这些文档描述当前行为；如果某条说明过时，以代码、测试和控制面合同为准。

## 许可证

GPL-3.0-only。详见 `LICENSE` 和 `THIRD_PARTY_NOTICES.md`。
