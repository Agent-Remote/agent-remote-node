# agent-remote-node

[English](README.md) | 中文

agent-remote-node 是部署在 VPS 节点上的 Go 运行时。它通过轮询方式与 `agent-remote-server` 控制平面通信，不需要对公网暴露节点 HTTP 服务。

## 主要职责

- 注册节点并保存节点 token。
- 上报心跳、能力、版本和运行状态。
- 轮询并执行节点任务。
- 创建 workspace 目录和 `.agent-remote-workspace.json` 标记文件。
- 创建 Claude tool-account 绑定会话，使用 Docker Sandbox 和 tmux 保持登录 shell。
- 同步 SSH forced-command key，用于本地 CLI attach。
- 启动临时 Kasm Chrome 浏览器会话，用节点网络访问目标站点。

## 常用命令

```sh
go test ./...
go run ./cmd/agent-remote-node --help
go run ./cmd/agent-remote-node register --config ./config.json --server-url http://localhost:8000 --node-id <node-id> --registration-token <registration-token>
go run ./cmd/agent-remote-node run --config ./config.json
go run ./cmd/agent-remote-attach --config ./config.json --session <session-id> --device <device-id> --dry-run
```

## 配置

`register` 会把节点凭据写入配置文件。配置中包含 Docker、tmux、Mutagen、workspace、account、browser 等路径。配置文件包含节点凭据，部署时需要使用合适的文件权限保护。

`browser_public_base_url` 可选。为空时节点返回本地 Docker 端口映射；生产环境建议配置为节点侧 HTTPS 反向代理地址。

## systemd 安装

下载或构建 release 包后执行：

```sh
sudo scripts/install-node.sh
```

安装器会安装二进制、创建 `/etc/agent-remote-node` 和 `/var/lib/agent-remote-node`，安装 systemd unit，并检查 Docker、OpenSSH、tmux、Mutagen 和 TUN 能力。

## 发布包

```sh
VERSION=0.0.2 scripts/build-release.sh
```

发布流程会生成六个包：`darwin-amd64`、`darwin-arm64`、`linux-amd64-glibc`、`linux-arm64-glibc`、`linux-amd64-musl`、`linux-arm64-musl`。Go 二进制使用 `CGO_ENABLED=0` 静态构建；glibc/musl 标签用于部署环境选择和自动安装匹配。

## 许可证

agent-remote-node 使用 GPL-3.0-only 许可证。详见 `LICENSE`。

第三方依赖声明见 `THIRD_PARTY_NOTICES.md`。
