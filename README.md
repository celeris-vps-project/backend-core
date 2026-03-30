# backend-core

`backend-core` 是 Celeris VPS 平台的后端核心仓库。它不是单一 HTTP 服务，而是一个围绕 VPS 交易与资源编排构建的 Go 单仓库，包含控制面 API、宿主机 Agent、性能压测工具，以及一组面向高并发和故障隔离的基础组件。

当前仓库已经覆盖了一条完整的业务主链路：用户注册登录、浏览商品、创建订单、发起支付、分配实例、下发宿主机任务、接收 Agent 回报，并通过管理端接口观察节点状态与性能数据。

## 项目结论

从代码结构和入口程序来看，这个仓库的定位很明确：

- `cmd/api` 是平台控制面，负责 HTTP API、gRPC Agent 通信、领域服务装配、数据库迁移、后台任务和管理接口。
- `cmd/agent` 是部署在宿主机上的执行代理，通过 gRPC 注册、心跳和任务回传与控制面交互。
- `cmd/perftest` 是场景化压测工具，用于验证限流、自适应分发、缓存和熔断等机制在高并发下的行为。

整个系统采用按业务域拆分的单仓库结构，主要围绕这些上下文展开：

- `identity`：用户、认证、JWT、中间件、管理员初始化。
- `catalog`：商品、商品线、价格、库存、资源池展示。
- `ordering`：订单创建与状态流转。
- `billing`：发票、账单项目、税费和支付入账。
- `payment`：支付单、支付提供方管理、Webhook、加密货币支付、EPay 集成。
- `instance`：实例生命周期与客户实例视图。
- `provisioning`：区域、宿主机、IP 池、资源池、引导令牌、任务编排、Agent 通信。
- `checkout`：统一结账入口，在高负载下在同步和异步流程之间自适应切换。

工程上，这个仓库不是“只做 CRUD”，而是显式实现了多种运行时保护和伸缩策略：

- 分层限流：基线、公共读、结账、认证、普通业务、管理端六档限流。
- 自适应分发：统一结账在 QPS 超阈值时切换到异步队列处理。
- 自适应缓存：商品和部分公共查询在高 QPS 时进入短 TTL 缓存。
- 熔断与降级：跨域调用适配器外包一层 circuit breaker。
- 防穿透与去重：商品仓储使用 Bloom filter 和 singleflight；秒杀引擎支持 gate、dedup、stock。
- 延迟任务：订单支付超时和开机确认使用延迟事件调度。
- 实时观测：节点状态和性能排行通过 WebSocket 推送到管理端。

## 系统架构

典型请求链路如下：

```text
Frontend / API Client
  -> Hertz HTTP API (/api/v1/*)
  -> Application Services (identity/catalog/ordering/payment/instance/provisioning/checkout)
  -> GORM Repositories
  -> SQLite or PostgreSQL

Cross-cutting:
  -> JWT / RBAC / rate limit / timeout / adaptive cache / performance tracking
  -> EventBus + delayed task router
  -> gRPC AgentService
  -> MessageService gRPC client (optional)

Host Node:
  -> celeris-agent
  -> stub / libvirt / incus / pve hypervisor driver
```

控制面中的关键集成点：

- API 启动时会自动 `AutoMigrate` 主要表结构。
- 若管理员账号不存在，会按配置中的邮箱自动创建一个管理员，并把随机初始密码打印到日志一次。
- Agent 通过 bootstrap token 首次注册，控制面返回永久 `node_token`，后续请求走节点令牌鉴权。
- 支付模块通过领域适配器串联订单、账单、库存和实例创建，避免直接跨域耦合。
- `internal/web` 支持 `-tags frontend` 的嵌入式构建，将外部前端产物打包进单一二进制。

## 核心能力

### 1. API 控制面

`cmd/api` 暴露 HTTP API 和 gRPC Agent 服务，负责：

- 用户注册、登录、个人资料和密码修改。
- 商品、商品线、区域、宿主机、资源池等公开查询。
- 订单、发票、支付、实例相关的业务接口。
- 管理端资源池、节点、商品、支付提供方、Bootstrap Token 管理接口。
- 节点状态和性能看板的 WebSocket 推送。
- Agent gRPC 通信：注册、心跳、任务结果回传。

主要对外通道：

- HTTP：由 Hertz 提供，默认监听 `server.listen:server.port`，示例配置为 `0.0.0.0:8080`。
- gRPC：用于 Agent 通信，默认监听 `:50051`。
- Health Check：`GET /healthz`

### 2. Host Agent

`cmd/agent` 部署在宿主机上，负责：

- 通过 bootstrap token 首次注册到控制面。
- 将 `node_token` 保存到 `credential_file`，后续自动复用。
- 定时上报 CPU、内存、磁盘、运行时长、虚拟机数量等心跳数据。
- 接收控制面下发的任务并执行，再回传结果。

支持的虚拟化后端：

- `stub`：纯内存驱动，适合本地开发和测试。
- `pve`：通过 Proxmox VE HTTP API 进行远程管理，不需要额外 build tag。
- `libvirt`：需要 Linux 环境，并以 `-tags libvirt` 编译。
- `incus`：需要 Linux 环境，并以 `-tags incus` 编译。

### 3. 统一结账与高并发保护

`checkout` 模块并不是简单地包一层接口，而是显式承担流量切换职责：

- 正常负载下同步创建订单。
- 高负载下返回排队中的占位订单，并由后台 worker 异步执行结账。
- 提供订单状态查询和 SSE 流式状态接口，便于前端轮询或流式展示。

### 4. 支付与支付后编排

支付相关实现包括：

- 加密货币支付流程，默认支持多链 USDT 网络配置。
- `CRYPTO_MOCK_MODE` 开关，可在开发环境自动确认支付。
- 动态支付提供方管理，可通过管理端启用或禁用。
- EPay Webhook 接入。
- 支付成功后通过 orchestrator 串联订单、账单、库存和实例创建。

### 5. 节点与资源编排

`provisioning` 和 `instance` 共同完成资源侧流程：

- 管理区域、宿主机、IP 池、资源池和引导令牌。
- 将商品与资源池、容量检查关联起来。
- 通过事件桥将“节点开通完成”同步到实例状态。
- 使用轮询器和延迟任务作为兜底机制，避免 Agent 回调丢失后状态永久悬挂。

## 技术栈

- Go `1.25`
- HTTP 框架：`cloudwego/hertz`
- ORM：`gorm`
- 数据库：SQLite（默认）或 PostgreSQL
- RPC：`grpc`
- 消息调度：内存延迟任务，预留 `asynq` 扩展点
- 认证：JWT + bcrypt
- WebSocket：`hertz-contrib/websocket`
- Proto 生成：`buf`

数据库层的现状：

- SQLite 是默认开发数据库，启动时会开启 WAL、busy timeout、外键约束等优化。
- PostgreSQL 支持连接池参数配置。
- 配置中预留了 `replica_dsns`，代码也预留了读写分离插件入口，但当前实现仍是轻量 stub，并未直接接入官方 `gorm.io/plugin/dbresolver`。

## 仓库结构

| 路径 | 作用 |
| --- | --- |
| `cmd/api` | API 控制面入口 |
| `cmd/agent` | 宿主机 Agent 入口 |
| `cmd/perftest` | 场景化压测工具 |
| `internal/api` | API 配置与 HTTP 入口装配 |
| `internal/identity` | 用户、认证、JWT、中间件 |
| `internal/catalog` | 商品与商品线 |
| `internal/ordering` | 订单领域 |
| `internal/billing` | 发票与账单 |
| `internal/payment` | 支付单、支付提供方、Webhook、支付后编排 |
| `internal/instance` | 实例生命周期 |
| `internal/provisioning` | 宿主机、资源池、IP、Bootstrap Token、Agent gRPC |
| `internal/checkout` | 统一结账、自适应同步/异步流程 |
| `internal/agent` | Agent 配置、心跳、任务处理、虚拟化驱动 |
| `internal/web` | 前端静态资源嵌入开关 |
| `pkg/*` | 通用组件，如限流、熔断、事件总线、延迟任务、性能跟踪、消息客户端 |
| `proto/*` | Agent 与消息服务的 proto 定义 |
| `deploy/*` | Docker、Compose、systemd、安装脚本 |
| `test_data/*` | 压测场景输出样例 |

## 本地开发

### 环境要求

- Go `1.25`
- 可选：Docker / Docker Compose
- 可选：`buf`，当你修改 proto 后需要重新生成代码
- 可选：真实虚拟化环境
  - `libvirt` 需要 Linux + `-tags libvirt`
  - `incus` 需要 Linux + `-tags incus`
  - `pve` 需要可访问的 Proxmox VE API

### 启动 API

1. 复制 `api.example.yaml` 为 `api.yaml` 并按需修改。
2. 确认数据库、JWT、gRPC 和支付相关配置。
3. 运行：

```bash
go run ./cmd/api --config api.yaml
```

启动后可用的默认入口：

- HTTP：`http://localhost:8080`
- gRPC：`localhost:50051`
- 健康检查：`http://localhost:8080/healthz`

首次启动的注意事项：

- 若数据库为空，主要表会自动迁移创建。
- 若不存在管理员账号，会按照 `admin.email` 自动创建管理员。
- 初始密码只会打印到日志一次，建议首次登录后立刻修改。
- 若未配置 `message.address` 和 `message.service_token`，消息服务集成默认关闭。

### 启动 Agent

1. 复制 `agent.example.yaml` 为 `agent.yaml`。
2. 为 Agent 准备 bootstrap token。
3. 至少配置以下字段：
   - `grpc_address`
   - `bootstrap_token`
   - `virt_backend`
   - `credential_file`
4. 运行：

```bash
go run ./cmd/agent --config agent.yaml
```

如果使用不同虚拟化后端：

```bash
go build -tags libvirt ./cmd/agent
go build -tags incus ./cmd/agent
go build ./cmd/agent
```

说明：

- `stub` 和 `pve` 不需要额外 build tag。
- `libvirt` / `incus` 若未按要求编译，运行时会直接返回“不支持该驱动”的错误。

### 配置真实编排

代码默认更偏向开发环境：

- 支付默认启用 mock 模式。
- Provisioning 默认启用 mock 模式。

要接入真实支付与真实 Agent，请重点关注：

- `CRYPTO_MOCK_MODE=false`
- `PROVISION_MOCK_MODE=false`
- `message.address`
- `message.service_token`
- `grpc.listen`
- `agent.bootstrap_token`
- `agent.virt_backend`
- `agent.virt_opts`

## 配置说明

完整模板见：

- `api.example.yaml`
- `agent.example.yaml`

常用 API 环境变量覆盖项：

| 变量 | 作用 |
| --- | --- |
| `API_DATABASE_DSN` | 覆盖数据库 DSN |
| `API_JWT_SECRET` | 覆盖 JWT Secret |
| `API_GRPC_LISTEN` | 覆盖 gRPC 监听地址 |
| `API_MESSAGE_ADDRESS` | 消息服务地址 |
| `API_MESSAGE_SERVICE_TOKEN` | 消息服务令牌 |
| `API_MESSAGE_TIMEOUT` | 消息调用超时 |
| `CRYPTO_MOCK_MODE` | 强制开启或关闭加密货币 mock 支付 |
| `PROVISION_MOCK_MODE` | 强制开启或关闭 mock 编排 |

常用 Agent 环境变量覆盖项：

| 变量 | 作用 |
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

### 本地编译

```bash
go build ./cmd/api
go build ./cmd/agent
go build ./cmd/perftest
```

带版本号构建示例：

```bash
go build -ldflags="-X main.version=dev" -o celeris-api ./cmd/api
go build -ldflags="-X main.version=dev" -o celeris-agent ./cmd/agent
```

### 嵌入前端构建

此仓库不包含前端源码，只在 `internal/web/dist/` 下预留了嵌入目录。要构建单二进制嵌入版，有两种方式：

1. 先在外部 `frontend` 仓库构建 `dist/`，再复制到 `internal/web/dist/`，最后执行：

```bash
go build -tags frontend ./cmd/api
```

2. 直接使用仓库内的 Dockerfile 或 GitHub Actions release 工作流，它们会自动拉取前端仓库并嵌入构建。

## 测试与压测

### 单元测试

```bash
go test ./...
```

仓库中存在较多应用层、领域层和基础组件测试，尤其集中在：

- `internal/*/app`
- `internal/*/domain`
- `pkg/adaptive`
- `pkg/bloom`
- `pkg/circuitbreaker`
- `pkg/delayed`
- `pkg/eventbus`

### 压测

`cmd/perftest` 是一个场景化工具，不只是简单打流量。它会验证限流、自适应分发、熔断和缓存策略。

常见命令：

```bash
go run ./cmd/perftest --base http://localhost:8080 --scenario full
go run ./cmd/perftest --base http://localhost:8080 --scenario baseline --duration 120s --workers 1000
go run ./cmd/perftest --base http://localhost:8080 --scenario adaptive
```

可选场景：

- `full`
- `baseline`
- `ratelimit`
- `adaptive`
- `circuitbreaker`

如果你要做纯吞吐测试，可以先使用仓库提供的 `api-perftest.yaml`：

```bash
go run ./cmd/api --config api-perftest.yaml
```

注意：`api-perftest.yaml` 会显著放宽限流配置，不适合生产环境。

## Proto 与代码生成

仓库使用 `buf` 管理 proto：

```bash
buf generate
```

主要 proto：

- `proto/agent/v1/agent.proto`
- `proto/message/v1/message.proto`

生成结果位于：

- `pkg/agentpb`
- `pkg/messagepb`

## 部署

### Docker

仓库提供了三类镜像和 Compose 参考：

- `deploy/docker/Dockerfile.api`
  - 纯后端 API 镜像
- `deploy/docker/Dockerfile.embed`
  - 将前端嵌入到 API 二进制中的 all-in-one 镜像
- `deploy/docker/Dockerfile.agent`
  - Host Agent 镜像
- `deploy/docker/Dockerfile.frontend`
  - 前后端分离模式下的前端镜像

Compose 文件：

- `deploy/compose/docker-compose.yml`
  - 前后端分离部署
- `deploy/compose/docker-compose.embed.yml`
  - 嵌入式 all-in-one 部署参考
- `deploy/compose/docker-compose.agent.yml`
  - Agent 部署参考

### systemd

仓库提供了现成的 systemd service 模板：

- `deploy/systemd/celeris-api.service`
- `deploy/systemd/celeris-agent.service`

同时提供 `deploy/install.sh`，支持从 GitHub Release 安装：

```bash
sudo ./deploy/install.sh api
sudo ./deploy/install.sh api-embed
sudo ./deploy/install.sh agent
```

## 运行时特性

对接手这个仓库的人，以下几点最值得先知道：

- HTTP 请求默认统一经过 CORS、超时中间件、性能跟踪与可选基线限流。
- 公开查询接口和管理接口使用不同限流档位。
- Agent 接口与支付 Webhook 故意不加限流，避免关键回调被误杀。
- 控制面同时提供 WebSocket 与 SSE，说明它不仅服务管理后台，也考虑了实时反馈体验。
- 支付、编排、实例状态之间主要通过事件桥和适配器解耦，而不是在 handler 里直接串业务。
- 当前仓库对“真实生产依赖”做了保守默认：
  - 默认 SQLite
  - 默认支付 mock
  - 默认 provisioning mock
  - 消息服务默认关闭

## 前端关系

此仓库是后端核心，不包含前端源码仓库本体。

当前与前端相关的事实是：

- `internal/web/embed*.go` 负责控制是否把前端 `dist/` 打进二进制。
- Release workflow 和 `Dockerfile.embed` 会从外部 `frontend` 仓库拉取代码并构建。
- 如果本地只运行 `backend-core`，你依然可以单独验证所有后端接口、gRPC、Agent 和压测链路。

## 适合如何理解这个仓库

如果你第一次接手，建议按下面顺序阅读：

1. `cmd/api/main.go`
2. `internal/api/config`
3. `internal/identity` / `internal/catalog` / `internal/payment`
4. `internal/provisioning` / `internal/instance` / `internal/checkout`
5. `cmd/agent/main.go` 与 `internal/agent/vm`
6. `pkg/adaptive` / `pkg/ratelimit` / `pkg/circuitbreaker` / `pkg/delayed`

这样可以最快把业务主链路和系统保护机制一起串起来。
