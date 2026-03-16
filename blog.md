# Celeris主机平台


## 序
目前主机平台一眼看过去都是whmcs, 虽然稳定，但是并发很差

**包括但不限于下面的逻辑**
- 一遇到大流量，直接连接数一高直接打死数据库
- 母鸡同时并发开机直接爆母鸡
- 邮件系统卡死导致占用连接
- 下单的时候还要去查询母鸡是否占用某个主机名
- 最重要的是不是开源，拓展强烈依赖插件接口，无原生源码，难以拓展
- PHP


等等这些逻辑，都表明whmcs作为一个云计算主机管理平台，已经是过时的架构，因此我写了这个项目


项目优势：
- golang，可以编译成二进制，编译简单部署简单，gmp很轻量，原生就能承载很大流量
- 拓展包方便，成熟，如本项目直接使用了字节的hertz加强epoll，并且使用字节的sonic包优化json反序列
- 单机/集群可拓展架构，单机sqlite就能承载读+写约4296RPS
- ddd架构，接口高度解耦，方便更换消息队列/redis/数据库，代码易修改不史山
- 参照了主流的并发设计，包括但不限于自适应动态同步/异步，缓存提升，令牌桶限流，熔断降级，singleflight, 异步事件分发，接口优先级策略


### 为什么go？

#### GMP模型
说到go就不得不说gmp模型了

go的gmp模型是一个让人不得不佩服的设计, 主要的概念如下

```
G: goroutine
M: mechine
P: processor
```

**用我自己的话说一次调度流程**

1. 程序启动，创建若干个P（一般是等于cpu核心数），创建第一个M，启动主协程第一个G放入**当前P**中等待调度
2. 向操作系统申请M，绑定P, 然后M开始循环执行schedule
3. schedule首先查有没有饿死（61次调度去拿G），接下来让目前拥有P的M按照优先级跑G的逻辑，从本地队列、全局队列、查网络轮询器（有netpoll的时候）、偷别的P队列
4. M找到G以后，可能有以下情况：可能会正常结束，可能会产生系统调用（此时M会和P解绑，P给另一个M，自身阻塞，等待系统调用结束后会尝试获取P），可能会被网络io阻塞（netpoll），可能会因为执行时间过长被抢占

![](https://bucket.voidval.com/upload/2026/03/2bbf6876aa7d1e1e6ee6ddaabfa1b29d.png)

**为什么go强？**

- GMP模型可以让每个协程都能获得平均的运行时间（抢占+窃取）
- netpoll天然的epoll逻辑，使得处理网络io有得天独厚的优势
- G很轻量，启一个G只有2KB左右的占用，不需要拷贝上下文，由P管理
- M和P可以动态解绑，G不会因为频繁系统调用饿死或者创建过多的线程，不同线程的竞争队列几乎没有

#### 比较同类型语言
**比较java**

java原生处理网络io，若不使用java21用轻量的virtual thread, 将会一个网络请求对应一个线程，创建运行开销大，若使用Reactor模型，异步难以调试问题

而go则结合了异步和同步的两者优点，既原生支持非阻塞代码，又便于调试维护

**比较php**

php8.4虽然已经算是很快的，但解释语言，意味着性能仍然取决于解释器性能和调优，且环境部署复杂

go则可以单文件二进制直接部署，不需要任何依赖，运行速度接近C++


### 项目定位
本项目强调可拓展，易于部署，高并发可用，针对中小心idc优化，既可以纵向拓展到大型重量级部署环境，也可以横向直接拓展集群，集成vps售卖/管理完整逻辑


## 设计思路

在刚开始的时候，我规划的蓝图很大，想把虚拟机的整个生命周期管理也写进去，也想把整个whmcs的无缝迁移工具也写进去，但这样看代码会膨胀很多，而且作为开发者，我必须要在有限的时间内实现对应主题的功能，我的主题是**并发系统**, 那么我就只做并发即可

### mvc和ddd的架构权衡
一般标准架构是mvc，但mvc写的话可能会遇到一个问题，serivce某个方法如果要添加项目逻辑，需要直接在那个service的方法里面写，这样就会导致代码块可能会及其膨大，你可能会看到一个方法里面有几万行，根本不敢乱改动

在我的项目中，我选择了DDD模型，DDD模型采用充血模型，会把业务逻辑直接写在底层domain对象中，**service层只管编排就行**，根本不需要管底层是怎么做的

如果需要拓展逻辑，比如 `购买产品上下文`中的`user domain`，添加一个`折扣码`的功能，在ddd中你只需要修改 `产品` domain 下的user实体； 如果用的是mvc模型，就要在service加一行，如此滚雪球，service将会越来越大

ddd好处说完了，坏处呢？

ddd在刚开发的时候，难以界定上下文边界，容易出现service跨domain直接调度的问题，会导致代码腐化，回落成史山代码，一个domain里面参杂多种逻辑

说白了就是，**起步的时候很复杂，但后期维护很方便**

对于主机平台这种架构，天生就是很复杂的，与其用mvc，不如一开始复杂，后期维护舒服，随时可拆分：每个限界上下文有独立的 domain/app/infra/interfaces 四层

```mermaid
graph TB
    subgraph "Each Bounded Context follows this layered structure"
        direction TB
        
        subgraph "interfaces/ — Delivery Layer"
            HTTP_Handler["HTTP Handler<br/>(Hertz routes)"]
            GRPC_Handler["gRPC Handler<br/>(Provisioning only)"]
            WS_Handler["WebSocket Hub<br/>(Provisioning + Perf)"]
        end

        subgraph "app/ — Application Layer"
            AppService["AppService<br/>(Orchestration, Use Cases)"]
            Ports["Ports (interfaces)<br/>- Repository<br/>- IDGenerator<br/>- EventPublisher"]
        end

        subgraph "domain/ — Domain Layer"
            Aggregates["Aggregates & Entities<br/>(business rules, invariants)"]
            ValueObjects["Value Objects<br/>(Money, BillingCycle, etc.)"]
            DomainEvents["Domain Events<br/>(raised by aggregates)"]
            RepoInterface["Repository Interface<br/>(defined in domain)"]
        end

        subgraph "infra/ — Infrastructure Layer"
            GormRepo["GORM Repository<br/>(implements domain repo)"]
            Adapters["Anti-Corruption Adapters<br/>(cross-context calls)"]
            CBWrapper["Circuit Breaker Wrappers<br/>(fault isolation)"]
            Workers["Background Workers<br/>(delayed event handlers)"]
        end

        HTTP_Handler --> AppService
        GRPC_Handler --> AppService
        WS_Handler --> AppService
        AppService --> Aggregates
        AppService --> Ports
        Aggregates --> DomainEvents
        GormRepo -.->|implements| RepoInterface
        Adapters --> AppService
        CBWrapper -->|wraps| Adapters
    end

    style HTTP_Handler fill:#4CAF50,color:#fff
    style GRPC_Handler fill:#4CAF50,color:#fff
    style WS_Handler fill:#4CAF50,color:#fff
    style AppService fill:#2196F3,color:#fff
    style Aggregates fill:#FF9800,color:#fff
    style ValueObjects fill:#FF9800,color:#fff
    style DomainEvents fill:#FF9800,color:#fff
    style GormRepo fill:#9C27B0,color:#fff
    style Adapters fill:#9C27B0,color:#fff
    style CBWrapper fill:#9C27B0,color:#fff
```

主要架构思路：按业务分domain上下文, 8 个限界上下文各自职责

`identity` — 用户认证（JWT + bcrypt）、RBAC 角色管理
`catalog` — 产品目录管理、库存控制、Singleflight 缓存合并
`ordering` — 订单生命周期状态机（pending→active→suspended→cancelled→terminated）
`payment` — 支付网关抽象、PostPaymentOrchestrator 跨域编排
`billing` — 发票领域模型（Draft→Issued→Paid→Void）、账单周期
`instance` — 虚拟机实例管理、Channel-based 异步开通队列
`provisioning` — 宿主机节点、IP池、资源池、区域、gRPC Agent 通信
`checkout` — 统一下单入口、自适应 Sync/Async 切换

### 并发架构落地
在并发实现中，我设计了多个抗并发的中间件，并使用高效的并发包及golang自带的并发原语

**关键实现如下**

- Checkout 根据qps检测，切换支付的同步/异步
- Seckill 秒杀管线
- TokenBucket 双层令牌桶分层限流api
- qps检测的Adaptive Cache 动态缓存
- singleflight防止大量读请求击穿数据库
- 熔断器分层api保护数据库
- eventbus异步开机

#### Celeris 并发组件架构图

##### 图 1：中间件到 Singleflight 请求处理链路

```mermaid
%%{init: {'theme': 'dark'}}%%
graph LR
    A[HTTP Request] --> B[Rate Limiter<br/>Token Bucket<br/>Global + Per-IP]
    B --> C[Timeout<br/>context.WithTimeout]
    C --> D[Adaptive Cache<br/>QPS Monitor<br/>动态缓存切换]
    D --> E[Handler]
    E --> F[AppService]
    F --> G[Singleflight Repo<br/>并发读去重]
    G --> H[GORM Repo]
    H --> I[(Database)]
```

##### 图 2：并发组件架构总览

```mermaid
%%{init: {'theme': 'dark'}}%%
graph TB
    subgraph Middleware Layer
        RL[Rate Limiter<br/>6-Tier Token Bucket]
        TM[Timeout Middleware]
        AC[Adaptive Cache]
    end

    subgraph Application Layer
        AD[Adaptive Dispatcher<br/>QPS 阈值 Sync/Async 切换]
        PPO[PostPayment Orchestrator<br/>WaitGroup 并行编排]
        SK[Seckill Engine<br/>Gate → Dedup → Stock 管线]
    end

    subgraph Infrastructure Layer
        SF[Singleflight Repo<br/>读操作并发合并]
        CB[Circuit Breaker<br/>3-state 熔断保护]
        EB[EventBus<br/>Sync / Async 事件分发]
        DQ[Delayed Queue<br/>定时任务调度]
        PB[Provisioning Bus<br/>Channel Worker Pool]
    end

    subgraph Data Layer
        DB[(Database<br/>连接池 + R/W Split)]
    end

    RL --> TM --> AC
    AC --> AD
    AC --> PPO
    AC --> SK

    AD -->|Sync| SF
    AD -->|Async Channel| SF
    PPO --> CB
    SK --> SF

    CB --> SF
    SF --> DB
    EB --> PB
    PPO --> DQ
```

##### 图 3：关键并发原语与组件映射

```mermaid
%%{init: {'theme': 'dark'}}%%
graph TB
    subgraph Go Concurrency Primitives
        ATOMIC[sync/atomic]
        MUTEX[sync.Mutex]
        CHAN[channel]
        WG[sync.WaitGroup]
        CTX[context.Context]
        SMAP[sync.Map]
    end

    subgraph Concurrent Components
        TB_C[Token Bucket Rate Limiter]
        CB_C[Circuit Breaker]
        QPS_C[QPS Monitor]
        GATE_C[Seckill Gate]
        STOCK_C[Seckill Stock]
        DEDUP_C[Seckill Dedup]
        ASYNC_C[Async Processor / EventBus]
        ORCH_C[PostPayment Orchestrator]
        TO_C[Timeout Middleware]
        SF_C[Singleflight Repo]
    end

    MUTEX --> TB_C
    MUTEX --> CB_C
    ATOMIC --> QPS_C
    ATOMIC --> STOCK_C
    CHAN --> GATE_C
    CHAN --> ASYNC_C
    SMAP --> DEDUP_C
    WG --> ORCH_C
    WG --> ASYNC_C
    CTX --> TO_C
```

接下来将分层说明并发组件以及为什么要这样设计

#### checkout 200/202 设计

结账的时候的200方案，是最直接的方案，后端返回200，用户可以直接跳转到支付页面，付款成功，此方案是最标准的 **(同步)** 方案

但这里必须得思考一个问题：假设此时请求流量过大，数据库io卡死了，你的service层挂起等待返回结果，前端也要卡在这等待，那给客户的反馈体验不就差很多了吗

于是就引入了结账的202方案，返回正在处理，前端需要轮询/开个ws查看是否下单成功，再跳转到支付页面 **（异步）** 方案，这样，即便在后端忙的时候，可以让用户提前得到反馈，虽然可能会慢了一点或者返回失败，但这样至少后端压力没那么大

| 比较维度 | HTTP 200 (OK) - 同步结账 | HTTP 202 (Accepted) - 异步结账 |
| :--- | :--- | :--- |
| **处理模式** | **同步处理**。服务器处理完所有逻辑（扣减库存、创建订单、调用支付网关等）后才返回。 | **异步处理**。服务器仅做基础校验（如参数合法性），将请求丢入消息队列后立即返回。 |
| **响应时间** | **长**。响应时间等于所有依赖服务处理时间的总和，容易发生超时。 | **极短**。通常在几十毫秒内返回，用户感知极快。 |
| **用户体验** | **明确但需等待**。前端显示 Loading 转圈，一旦返回，用户立刻知道最终结果（成功或失败）。 | **流畅但需轮询**。瞬间跳转“订单处理中”页面，前端需通过轮询 (Polling) 或 WebSocket 获取最终状态。 |
| **高并发与扩展性** | **较弱**。高并发下，长时间的阻塞会导致服务器线程池/连接数耗尽，系统容易雪崩。 | **极强**。通过消息队列（如 Kafka/RabbitMQ）实现“削峰填谷”，系统吞吐量大幅提升。 |
| **架构复杂度** | **低**。典型的 Request-Response 模型，易于开发、测试和追踪调试。 | **高**。引入了中间件，需要处理消息防重、丢失补偿、最终一致性以及前端的轮询逻辑。 |
| **错误反馈时效** | **即时**。如遇到库存不足或支付拒绝，前端可以直接向用户展示具体的报错提示。 | **滞后**。如果后台处理时发现失败，需要通过 App 推送、短信或站内信通知用户。 |
| **典型适用场景** | 访问量平稳的中小型电商、B2B 内部交易系统、对实时性要求极高的虚拟商品发货。 | **秒杀大促 (Flash Sales)**、调用第三方支付/风控接口极慢的场景、大型微服务电商平台。 |


**关键在于，api层怎么知道后端压力大不大呢？你总不能直接某个商品写死200或者202吧**

这就引入了**动态qps监控的设计**，只需要在后端写一个中间件，计算每秒的qps，大于某个阈值，自动升级成202，前端只需要根据后端status code判断后就行了

还有一个小细节，如果升级后202开ws轮询的话，还是会对后端数据库造成开销，接下来就到了浏览器新特性，**SSE登场的时候啦**

SSE是浏览器主动发起的长连接，浏览器监听此接口，但不发送任何数据，只有在后端完成，主动推送数据的时候，做出event响应。这就把消耗从n个请求，降低到了0

#### seckill组件

既然是并发，就少不了我们大厂最爱聊的秒杀环节了，**这一部分主要叙述如何应对极端大流量场景，但很多关键接口其实都通用的，这一部分也会介绍常见的应对方法**

首先说理论模型

**在秒杀这种极限场景下，必须把接口层层保护，这就是常说的漏斗模型**

1. 先用jwt校验/签名等，把关键的接口保护起来，过滤掉爬虫等非法流量
2. 限制ip/userid的请求次数，基于令牌桶的限流，关键接口严格限制次数
3. 用布隆过滤器，过滤掉非法请求流量，因为布隆过滤器说不存在，就直接返回了，根本不会打到数据库
4. 到了这一层，流量已经很少了，这里就可以写我们关键的接口逻辑

**业务接口部分**

1. **动静分离**，cdn层直接把静态资源分流掉，不要打到后端占用流量资源
2. **可以下放业务逻辑到nginx**，内存直接缓存库存状态，或者用lua直接过滤缓解掉一部分请求，不打到redis上，或者lua配合redis/MQ减少链路长度，这一层nginx也可以顺便做上面的说的ip/请求限流
3. **关键数据预热**，库存直接拉到redis中，用lua原子更新库存，逻辑在redis直接打回去，并且大部分请求都只是读而不是写，在高峰请求时，读/写数据比可以高达100：1，不要让读打到数据库层
4. **读写分离数据库**，把读请求路由到一个单独的数据库节点/集群承载流量，而写请求则写主库
5. **业务接口代码操作数据库的时候用原子更新**，这样库存不会出现多次扣减的情况，防止竞态（读改写）多连接等待锁直接卡死，虽说性能一般，适当取舍
6. 如果还是实在扛不住，就横向拓展一个MQ+数据库嘛，再怎么说也不能违反物理学定律，单机IO就那么点，不能强人所难嘛
7. 具体的业务流程中不要开长事务等等，已经说腻了

我在项目里如何做的？

由于我是个单应用项目，上面除了nginx，我都考虑到了，基本全部实现落地，主要靠redis的lua实现**原子更新库存，快速扣减库存，防止超卖**

主要流程为： Gate → Dedup → Stock → Execute → Hooks

```mermaid
flowchart LR
    subgraph 单节点 默认
        S1["Stock\n(atomic CAS int64)"]
        D1["DedupStore\n(sync.Map + TTL GC)"]
        G1["Gate\n(buffered channel)"]
    end

    subgraph 集群部署 注入Driver
        S2["RedisStock\n(DECR + Lua)"]
        D2["RedisDedup\n(SETNX + TTL)"]
        G2["RedisGate\n(分布式信号量)"]
    end

    IF_S["StockDriver 接口"]
    IF_D["DedupDriver 接口"]
    IF_G["GateDriver 接口"]

    S1 -.->|实现| IF_S
    S2 -.->|实现| IF_S
    D1 -.->|实现| IF_D
    D2 -.->|实现| IF_D
    G1 -.->|实现| IF_G
    G2 -.->|实现| IF_G

    IF_S --> ENGINE["Engine[Req, Res]"]
    IF_D --> ENGINE
    IF_G --> ENGINE
```

先预热（限流一定请求数量inflight），然后放 inflight数量进来，后面请求全拒绝，接着排队dedup防重复订单，stock执行快速扣减库存，放到execute后续逻辑，为了方便拓展，单机我采用了内存部署，不需要redis，集群则上升到redis，只需要配置driver后注入即可