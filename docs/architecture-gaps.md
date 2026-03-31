# 整体架构缺失组件分析

> **说明**：本文档对应 GitHub Issue 草稿，分析在 Issue #1 基础上整体架构层面尚未覆盖的缺失组件。

## 背景

在 Issue #1 的基础上，本文档聚焦于从**整体架构视角**审视当前系统，梳理缺失的关键技术组件与工程能力，作为后续迭代的参考路线图。

Issue #1 已覆盖：安全审查关键链路、pkg 重写练手、测试覆盖提升。  
本文档补充：**Issue #1 未涵盖的系统性架构缺口**。

---

## 一、可观测性体系（Observability Stack）⚠️ 最高优先级

当前状态：仅有 `pkg/perf/` 的请求延迟直方图，日志使用标准库 `log`，`OpenTelemetry` 依赖已引入但几乎未启用。

### 1.1 结构化日志（Structured Logging）

- 当前 `log.Printf` 散落全局，无日志级别、无请求 ID 关联、无结构化字段
- 建议：引入 `slog`（Go 1.21+ 标准库）或 `zap`/`zerolog`
- 日志字段需包含：`trace_id`、`user_id`、`request_id`、`duration_ms`、模块名
- 区分 `DEBUG/INFO/WARN/ERROR` 级别

### 1.2 Prometheus 指标导出（Metrics）

- 当前无 `/metrics` 端点，无法接入 Prometheus + Grafana 监控体系
- 建议新增以下指标：
  - HTTP 请求总数、延迟分布（按路由）
  - 各限流 tier 触发次数
  - 熔断器状态转换次数
  - 待处理异步任务数（Asynq 队列深度）
  - 宿主机心跳在线率
  - 实例开通成功率 / 失败率

### 1.3 分布式追踪激活（OpenTelemetry Tracing）

- `go.sum` 中已有 OTel 依赖，但未进行实际 Span 上下文传播
- 建议：在 Hertz 中间件层注入 Trace Context，跨模块调用（checkout → payment → provisioning）传递 `trace_id`
- 支持导出到 Jaeger / Tempo

### 1.4 请求 ID 传播（X-Request-ID）

- 当前没有统一的请求 ID 中间件
- 所有响应应携带 `X-Request-ID` Header，日志应关联此 ID

---

## 二、API 工程化

### 2.1 OpenAPI / Swagger 文档生成

- 当前无 API 文档，前端对接和第三方集成缺乏规范参考
- 建议：使用 `swaggo/swag` 或 `hertz-swagger` 从注释自动生成 OpenAPI 3.0 文档
- 文档端点：`GET /api/docs`

### 2.2 分页 / 过滤 / 排序标准化

- 当前列表接口（`/orders`, `/invoices`, `/instances`）分页机制不统一
- 建议统一查询参数：
  ```
  ?page=1&size=20&sort=created_at:desc&filter[status]=active
  ```
- 统一响应结构：`{ data: [], pagination: { total, page, size } }`

### 2.3 统一错误响应格式

- 建议统一 `4xx/5xx` 错误体结构：
  ```json
  { "code": "USER_NOT_FOUND", "message": "用户不存在", "request_id": "..." }
  ```
- 当前 `pkg/apperr/` 已有基础，需在 Handler 层统一序列化输出

### 2.4 API 版本化策略

- 建议明确 `/api/v1/` 路由前缀并在代码中显式维护
- 制定向后兼容策略，防止破坏性变更影响外部客户端

---

## 三、安全补全（Issue #1 未覆盖部分）🔐

### 3.1 JWT Refresh Token 机制

- 当前 JWT 签发后无刷新机制，Token 过期即需重新登录
- 建议实现 `POST /auth/refresh`，使用短期 `access_token`（15m）+ 长期 `refresh_token`（7d）
- `refresh_token` 需持久化（数据库或 Redis），支持主动吊销

### 3.2 登录失败账号锁定（Account Lockout）

- 当前无失败计数，暴力破解无防护（除了 auth tier 限速）
- 建议：连续失败 5 次锁定账号 15 分钟，记录失败事件

### 3.3 密码重置流程（Password Reset）

- 当前无密码重置功能，用户忘记密码无解
- 建议：`POST /auth/forgot-password` → 发送含限时 Token 的邮件 → `POST /auth/reset-password`
- 重置 Token 一次性使用，有效期 30 分钟

### 3.4 双因素认证（2FA / TOTP）

- 建议支持基于 TOTP（RFC 6238）的二次验证
- 端点：`POST /user/2fa/enable`，`POST /auth/login/2fa`

### 3.5 API Key 管理（机器到机器认证）

- 针对自动化场景（面板对接、第三方集成），提供 API Key 而非 JWT
- 建议：`POST /user/api-keys` 生成，支持权限范围（scope）和过期时间

### 3.6 操作审计日志（Audit Log）

- 当前缺少对敏感操作的记录（谁在什么时间做了什么）
- 建议记录以下操作：管理员登录、用户封禁/解封、订单状态变更、宿主机上下线
- 存储在独立的 `audit_logs` 表，暴露 `GET /admin/audit-logs` 接口

---

## 四、分布式能力

### 4.1 分布式限流（Redis-backed Rate Limiting）

- 当前 `pkg/ratelimit/` 限流状态纯内存，**多实例部署下各自独立计数，限流完全失效**
- 建议：当配置 Redis 时自动切换为 Redis Lua 脚本实现的令牌桶（如 `go-redis/redis_rate`）
- 单机部署保持内存实现，集群部署自动使用 Redis

### 4.2 分布式锁

- 当前 `singleflight` 仅在进程内去重，多实例下仍有重复执行风险
- 关键操作（checkout、provisioning 槽位分配）需引入 Redis 分布式锁

### 4.3 Refresh Token / Session 存储

- 参见 3.1，`refresh_token` 需持久化，支持跨实例验证

---

## 五、数据运维能力

### 5.1 数据库迁移回滚

- 当前使用 GORM `AutoMigrate`，仅支持向前迁移，无回滚机制
- 建议引入 `golang-migrate` 或 `goose` 管理版本化迁移脚本
- 支持 `migrate up` / `migrate down`

### 5.2 数据备份与恢复

- SQLite 模式下无备份工具
- 建议提供 `cmd/tools/backup.go`，支持定时备份到本地或对象存储
- PostgreSQL 模式可对接 `pg_dump`

### 5.3 WHMCS 数据迁移工具

- 项目定位明确要替代 WHMCS，但缺少迁移路径
- 建议提供用户/订单/产品数据导入工具

---

## 六、通知与消息系统

### 6.1 内置通知事件覆盖

- 当前 `pkg/messageclient` 为可选外部服务，触发点仅有「用户注册」一处
- 建议补全以下通知触发点：

| 事件 | 通知类型 |
|------|----------|
| 订单支付成功 | 邮件 |
| 实例开通完成 | 邮件 + WebSocket |
| 实例即将到期（N 天前） | 邮件 |
| 宿主机离线 | 管理员邮件 + 内部告警 |
| 密码重置请求 | 邮件 |
| 账单生成 | 邮件 |

### 6.2 对外 Webhook 推送

- 当前 Webhook 仅用于**接收**支付通知，无**对外推送**能力
- 建议：允许客户订阅事件（实例状态变更、账单生成），通过 HTTP Webhook 通知其系统
- 设计：`POST /user/webhooks` 注册端点，签名验证（HMAC-SHA256）

---

## 七、资源生命周期完整性（代码中的 TODO）

> 这些问题在 Issue #1 中有提及但未归类为架构级问题

### 7.1 资源释放逻辑

| 文件 | TODO | 风险 |
|------|------|------|
| `provision_dispatcher.go:232` | `TODO: release slot on node, free IP` | 宿主机槽位泄漏，IP 池耗尽 |
| `boot_confirmation_worker.go` | 失败未告警、未重试 | 实例卡在 pending 状态无法感知 |

### 7.2 任务幂等性保障

- 当前 Asynq 任务在 Broker 重启或 Worker 崩溃后可能重复执行
- 关键任务（provision、deprovision）需实现幂等性检查

---

## 八、云原生 / K8s 就绪性

### 8.1 Kubernetes 部署清单

- 当前提供 Docker Compose，但缺少 K8s Deployment/Service/ConfigMap
- 建议提供 `deploy/k8s/` 目录，包含基础 K8s 清单
- 进阶：提供 Helm Chart，支持 values 覆盖

### 8.2 健康检查细化

- 当前 `/healthz` 实现细节不明
- 建议拆分：
  - `GET /healthz/live` — 进程存活（只检查进程是否响应）
  - `GET /healthz/ready` — 就绪（数据库连通、gRPC 监听成功）
  - `GET /healthz/startup` — 启动完成（迁移完毕）

### 8.3 优雅关机配置化

- 当前优雅关机超时疑为硬编码 10s
- 建议作为配置项暴露，K8s `terminationGracePeriodSeconds` 对齐

---

## 九、开发者体验（DX）

### 9.1 一键本地开发环境

- 建议提供 `docker-compose.dev.yml`，包含：
  - API（热重载，使用 `air`）
  - PostgreSQL（开发用）
  - Redis（限流/任务队列）
  - Jaeger（Trace 可视化）

### 9.2 数据库 Seed 工具

- 缺少开发/测试用初始数据填充工具
- 建议提供 `cmd/tools/seed.go`：创建测试用户、产品、宿主机节点

### 9.3 集成测试基础设施

- 当前测试覆盖率约 15.4%，且主要为单元测试
- 建议引入基于真实数据库（`testcontainers-go`）的集成测试
- HTTP Handler 层测试覆盖率目标 > 60%

---

## 十、配置与密钥管理

### 10.1 配置分层（环境区分）

- 建议支持 `api.dev.yaml` / `api.staging.yaml` / `api.prod.yaml` 配置叠加
- 或通过 `APP_ENV=production` 自动加载对应配置文件

### 10.2 密钥管理

- 建议支持从 HashiCorp Vault 或 AWS Secrets Manager 读取敏感配置（JWT Secret、数据库密码、支付密钥）
- 最低要求：启动时校验关键密钥不为默认值（补充 Issue #1 中 CRITICAL 问题的预防机制）

### 10.3 Feature Flags

- 建议引入轻量 feature flag 机制（如 YAML 配置控制），用于灰度发布和 A/B 测试

---

## 优先级汇总

| 优先级 | 组件 | 理由 |
|--------|------|------|
| **P0** | 分布式限流（Redis-backed） | 多实例部署下限流失效，直接影响系统防护能力 |
| **P0** | 结构化日志 + 请求 ID | 生产故障排查基础能力，成本极低 |
| **P0** | 资源释放 TODO 实现 | 现有代码中的宿主机槽位/IP 泄漏 |
| **P1** | Prometheus 指标 | 运营监控，上线后必需 |
| **P1** | 密码重置流程 | 用户基础功能缺失 |
| **P1** | JWT Refresh Token | 用户体验与安全平衡 |
| **P1** | 审计日志 | 合规 + 安全溯源 |
| **P2** | 数据库迁移版本化 | 生产数据安全保障 |
| **P2** | OpenAPI 文档 | 前端 / 第三方集成效率 |
| **P2** | K8s 部署清单 | 生产容器化部署标准化 |
| **P2** | 通知事件补全 | 用户体验完整性 |
| **P3** | 分布式锁 | 中高流量场景多实例一致性 |
| **P3** | 健康检查细化 | 与 K8s 探针对齐 |
| **P3** | 2FA / TOTP | 高价值账户安全加固 |
| **P3** | API Key 管理 | 自动化集成场景支持 |
| **P4** | 对外 Webhook | 生态扩展 |
| **P4** | WHMCS 迁移工具 | 市场定位完整性 |

---

## 参考

- Issue #1：安全审查 + pkg 重写 + 测试覆盖
- `blog.md`：系统架构设计思路
- `internal/provisioning/app/provision_dispatcher.go:232`：资源泄漏 TODO
- `pkg/ratelimit/`：当前内存限流实现
