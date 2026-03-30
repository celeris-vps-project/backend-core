# backend-core

`backend-core` 是 Celeris 的 Go 后端仓库，包含 API 服务、宿主机 Agent、压测工具，以及支付、订单、实例编排等后端模块。

## 组件

- `cmd/api`
  - HTTP API 服务
  - gRPC `AgentService`
  - 数据库迁移
  - 后台任务与事件处理
- `cmd/agent`
  - 宿主机 Agent
  - bootstrap 注册
  - 心跳上报
  - 任务执行与结果回传
- `cmd/perftest`
  - 场景化压测工具

## 模块

- `internal/identity`
  - 用户、认证、JWT、中间件
- `internal/catalog`
  - 商品、商品线、库存、资源池查询
- `internal/ordering`
  - 订单创建与状态流转
- `internal/billing`
  - 发票、账单项、支付入账
- `internal/payment`
  - 支付单、支付提供方、Webhook、支付后编排
- `internal/instance`
  - 实例生命周期
- `internal/provisioning`
  - 区域、宿主机、IP 池、资源池、bootstrap token、Agent 通信
- `internal/checkout`
  - 统一结账流程
- `internal/agent`
  - Agent 配置、任务处理、虚拟化驱动

## 功能

- HTTP API，基于 `cloudwego/hertz`
- gRPC Agent 通信
- SQLite 与 PostgreSQL
- JWT 认证与管理员中间件
- 自动建表迁移
- 分层限流
- 自适应结账分发
- 自适应缓存
- 熔断器
- 延迟任务调度
- WebSocket 节点与性能推送
- SSE 结账状态流
- 可选消息服务 gRPC 客户端

## 架构

```text
Frontend / API Client
  -> HTTP API (Hertz)
  -> Application Services
  -> GORM Repositories
  -> SQLite / PostgreSQL

Cross-cutting
  -> JWT / RBAC
  -> Rate Limit / Timeout
  -> Adaptive Cache / Adaptive Dispatch
  -> EventBus / Delayed Tasks
  -> Performance Tracking
  -> MessageService Client (optional)

Host Node
  -> celeris-agent
  -> stub / pve / libvirt / incus
```

## 仓库结构

| 路径 | 说明 |
| --- | --- |
| `cmd/api` | API 服务入口 |
| `cmd/agent` | Agent 入口 |
| `cmd/perftest` | 压测工具 |
| `internal/api` | API 配置与装配 |
| `internal/*` | 业务模块 |
| `pkg/*` | 通用组件 |
| `proto/*` | Proto 定义 |
| `deploy/*` | Docker、Compose、systemd、安装脚本 |
| `test_data/*` | 压测输出样例 |

## 运行要求

- Go `1.25`
- 可选：Docker / Docker Compose
- 可选：`buf`
- 可选：虚拟化环境
  - `libvirt` 需要 Linux 和 `-tags libvirt`
  - `incus` 需要 Linux 和 `-tags incus`
  - `pve` 需要可访问的 Proxmox VE API

## 快速开始

### 启动 API

1. 复制 `api.example.yaml` 为 `api.yaml`
2. 按需修改配置
3. 运行

```bash
go run ./cmd/api --config api.yaml
```

默认示例配置对应的服务地址：

- HTTP: `http://localhost:8080`
- gRPC: `localhost:50051`
- Health Check: `http://localhost:8080/healthz`

启动行为：

- 自动执行主要表结构迁移
- 若管理员账号不存在，则按 `admin.email` 初始化管理员账号
- 若未配置 `message.address` 和 `message.service_token`，则不启用消息服务集成

### 启动 Agent

1. 复制 `agent.example.yaml` 为 `agent.yaml`
2. 配置 `grpc_address`
3. 配置 `bootstrap_token`
4. 配置 `virt_backend`
5. 运行

```bash
go run ./cmd/agent --config agent.yaml
```

首次注册成功后，Agent 会把 `node_token` 写入 `credential_file`。

## 配置

示例配置文件：

- `api.example.yaml`
- `agent.example.yaml`

### API 环境变量

| 变量 | 说明 |
| --- | --- |
| `API_DATABASE_DSN` | 数据库 DSN |
| `API_JWT_SECRET` | JWT Secret |
| `API_GRPC_LISTEN` | gRPC 监听地址 |
| `API_MESSAGE_ADDRESS` | 消息服务地址 |
| `API_MESSAGE_SERVICE_TOKEN` | 消息服务令牌 |
| `API_MESSAGE_TIMEOUT` | 消息调用超时 |
| `CRYPTO_MOCK_MODE` | 加密货币支付 mock 开关 |
| `PROVISION_MOCK_MODE` | 编排 mock 开关 |

### Agent 环境变量

| 变量 | 说明 |
| --- | --- |
| `AGENT_BOOTSTRAP_TOKEN` | 首次注册使用的一次性令牌 |
| `AGENT_GRPC_ADDRESS` | API gRPC 地址 |
| `AGENT_VIRT_BACKEND` | `stub` / `pve` / `libvirt` / `incus` |
| `AGENT_LIBVIRT_URI` | libvirt 连接串 |
| `AGENT_INCUS_PROJECT` | Incus project |
| `AGENT_INCUS_SOCKET` | Incus socket |
| `AGENT_PVE_API_URL` | Proxmox VE API 地址 |
| `AGENT_PVE_TOKEN_ID` | Proxmox VE Token ID |
| `AGENT_PVE_TOKEN_SECRET` | Proxmox VE Token Secret |
| `AGENT_PVE_NODE` | Proxmox VE 节点名 |
| `AGENT_PVE_TEMPLATE_VMID` | 模板 VMID |
| `AGENT_PVE_STORAGE` | 目标存储池 |

## 构建

### 本地构建

```bash
go build ./cmd/api
go build ./cmd/agent
go build ./cmd/perftest
```

带版本号构建：

```bash
go build -ldflags="-X main.version=dev" -o celeris-api ./cmd/api
go build -ldflags="-X main.version=dev" -o celeris-agent ./cmd/agent
```

### 虚拟化后端

`stub` 和 `pve` 不需要额外 build tag。

`libvirt` 与 `incus` 需要按对应 tag 编译：

```bash
go build -tags libvirt ./cmd/agent
go build -tags incus ./cmd/agent
```

### 嵌入前端构建

仓库不包含前端源码仓库本体。嵌入式构建依赖 `internal/web/dist/` 中的前端产物。

本地构建方式：

1. 在前端仓库构建 `dist/`
2. 拷贝到 `internal/web/dist/`
3. 执行

```bash
go build -tags frontend ./cmd/api
```

`deploy/docker/Dockerfile.embed` 和 `.github/workflows/release.yml` 也包含前端拉取与嵌入构建流程。

## 测试

运行全部测试：

```bash
go test ./...
```

Proto 代码生成：

```bash
buf generate
```

主要 proto：

- `proto/agent/v1/agent.proto`
- `proto/message/v1/message.proto`

生成结果：

- `pkg/agentpb`
- `pkg/messagepb`

## 压测

`cmd/perftest` 提供以下场景：

- `full`
- `baseline`
- `ratelimit`
- `adaptive`
- `circuitbreaker`

示例：

```bash
go run ./cmd/perftest --base http://localhost:8080 --scenario full
go run ./cmd/perftest --base http://localhost:8080 --scenario baseline --duration 120s --workers 1000
```

仓库提供了 `api-perftest.yaml` 作为压测配置示例：

```bash
go run ./cmd/api --config api-perftest.yaml
```

## 部署

### Docker

Dockerfile：

- `deploy/docker/Dockerfile.api`
- `deploy/docker/Dockerfile.embed`
- `deploy/docker/Dockerfile.agent`
- `deploy/docker/Dockerfile.frontend`

Compose：

- `deploy/compose/docker-compose.yml`
- `deploy/compose/docker-compose.embed.yml`
- `deploy/compose/docker-compose.agent.yml`

### systemd

systemd service：

- `deploy/systemd/celeris-api.service`
- `deploy/systemd/celeris-agent.service`

安装脚本：

```bash
sudo ./deploy/install.sh api
sudo ./deploy/install.sh api-embed
sudo ./deploy/install.sh agent
```

## 前端集成

- `internal/web/embed.go` 在未启用 `frontend` build tag 时返回空实现
- `internal/web/embed_frontend.go` 在启用 `frontend` build tag 时提供静态资源与 SPA handler
- `deploy/docker/Dockerfile.embed` 会在构建阶段拉取前端仓库并嵌入产物

## 许可证

本仓库采用 `MIT` 许可证，详见根目录 [LICENSE](LICENSE)。
