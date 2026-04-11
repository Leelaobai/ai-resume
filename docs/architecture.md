# TadpolesAI 平台架构设计文档

> 版本：v2.4
> 最后更新：2026-04-09
> 定位：AI Agent 矩阵平台，简历 Agent 为首个业务模块
> 部署路径：Docker Compose 单机 → Kubernetes 集群

---

## 目录

1. [平台定位与目标](#1-平台定位与目标)
2. [服务拆分与边界](#2-服务拆分与边界)
3. [API Gateway：APISIX](#3-api-gateway-apisix)
4. [User Service（与 Resume Agent 合并期）](#4-user-service)
5. [LLM Gateway Service](#5-llm-gateway-service)
6. [Billing Service](#6-billing-service)
7. [Resume Agent Service](#7-resume-agent-service)
8. [服务间通信](#8-服务间通信)
9. [高并发设计](#9-高并发设计)
10. [数据一致性保障](#10-数据一致性保障)
11. [部署架构与演进](#11-部署架构与演进)
12. [可观测性：OpenTelemetry + Prometheus](#12-可观测性)
13. [安全规范](#13-安全规范)
14. [Interface 设计与可测试性](#14-interface-设计与可测试性)
15. [面试技术深度索引](#15-面试技术深度索引)

---

## 1. 平台定位与目标

TadpolesAI 是一个 **AI Agent 矩阵平台**，不是单一工具。核心思路：统一底层能力，业务模块可插拔。

```
平台层（所有业务共享）:
  ├── 统一用户账户体系     ← 一次注册，所有 Agent 工具通用
  ├── 统一 LLM 能力网关   ← 多厂商 Key 管理，业务方透明调用
  └── 统一积分计费体系     ← 任何消费行为走同一个账本

业务层（可插拔）:
  ├── 简历 Agent          ← 当前唯一业务模块
  ├── [未来] 面试 Agent
  ├── [未来] 求职信 Agent
  └── ...
```

**架构目标优先级**：
1. 正确性（计费不出错，数据不丢失）
2. 安全性（权限不越界，密钥不泄露）
3. 可扩展性（新业务模块接入成本低）
4. 高并发（设计支撑百万 DAU，初期单机够用）

---

## 2. 服务拆分与边界

### 2.1 最终目标：5 个微服务

```
                         ┌──────────────────────────────┐
                         │        用户浏览器 / App        │
                         └──────────────┬───────────────┘
                                        │ HTTPS
                         ┌──────────────▼───────────────┐
                         │           APISIX              │
                         │  SSL终止 · 路由分发            │
                         │  JWT验证插件（jwt-auth）        │
                         │  用户级限流（limit-count+Redis）│
                         │  静态资源（直接返回Vue文件）     │
                         └───┬──────────┬───────────────┘
                             │          │
              ┌──────────────┘          └───────────────────┐
              │                                             │
┌─────────────▼──────────┐               ┌─────────────────▼──────────────┐
│    User Service         │               │      Resume Agent Service       │
│                         │               │                                 │
│  · 注册 / 登录 / 登出   │               │  · 简历 CRUD                    │
│  · JWT 签发与刷新       │◄──gRPC────────│  · ReAct Agent 对话             │
│  · 用户档案             │               │  · 长期记忆管理                 │
│  · 多业务共享账户       │               │  · 模板管理 / PDF 导出          │
│                         │               │                                 │
│  DB: users_schema       │               │  DB: resume_schema              │
└─────────────────────────┘               └──────────────┬──────────────────┘
                                                         │ HTTP (OpenAI兼容)
                                          ┌──────────────▼──────────────────┐
                                          │      LLM Gateway Service        │
                                          │                                 │
                                          │  · OpenAI 兼容 API              │
                                          │  · 多厂商 Key 池管理            │
                                          │  · 负载均衡 / 熔断 / 切换       │
                                          │  · Usage 计量 (Metering)        │
                                          │                                 │
                                          │  DB: gateway_schema             │
                                          │  Redis: Key 可用池              │
                                          └──────────────┬──────────────────┘
                                                         │ Redis Streams
                                                         │ (usage_events)
                                          ┌──────────────▼──────────────────┐
                                          │      Billing Service            │
                                          │                                 │
                                          │  · 积分余额管理                 │
                                          │  · Pre-Auth / Settlement        │
                                          │  · 流水账本（append-only）      │
                                          │  · 充值 / 退款 / 赠送           │
                                          │  · 统计报表                     │
                                          │                                 │
                                          │  DB: billing_schema             │
                                          │  Redis: 余额热缓存              │
                                          └─────────────────────────────────┘
```

### 2.2 当前阶段：User Service 与 Resume Agent 合并

**原因**：减少服务数量，降低初期开发复杂度。

**合并期部署结构**：

```
                         ┌──────────────────────────────┐
                         │           APISIX              │
                         │  (路由 + JWT验证 + 限流)       │
                         └───┬──────────────────────────┘
                             │
              ┌──────────────┼───────────────────┐
              │              │                   │
┌─────────────▼──────────────▼──┐   ┌────────────▼──────────┐
│  Resume Agent Service          │   │  LLM Gateway Service  │
│  (含 User 模块)                │   │                       │
│                                │   │                       │
│  internal/user/    ← 用户模块  │   │                       │
│  internal/resume/  ← 简历模块  │   │                       │
│  internal/auth/    ← 鉴权模块  │   │                       │
│                                │   └──────────┬────────────┘
│  DB: users_schema              │              │ Redis Streams
│      resume_schema             │   ┌──────────▼────────────┐
└────────────────────────────────┘   │  Billing Service      │
                                     └───────────────────────┘
```

### 2.3 拆分条件与步骤

**何时拆分**：
- DAU 超过 5 万，或需要上线第二个 Agent 业务模块时

**拆分能顺利进行的前提（从第一天必须遵守）**：

**前提一：Database 物理隔离**

MySQL 中"Schema"等同于"Database"，每个服务使用独立的 Database，通过 MySQL 用户权限隔离：

```sql
-- 在同一个 MySQL 实例里，为每个服务创建独立 Database
CREATE DATABASE tadpoles_users   CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE tadpoles_resume  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE tadpoles_gateway CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE tadpoles_billing CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- 每个服务对应一个 MySQL 用户，只有本 Database 权限
CREATE USER 'user_svc'@'%'    IDENTIFIED BY '...';
CREATE USER 'resume_svc'@'%'  IDENTIFIED BY '...';
CREATE USER 'gateway_svc'@'%' IDENTIFIED BY '...';
CREATE USER 'billing_svc'@'%' IDENTIFIED BY '...';

GRANT ALL ON tadpoles_users.*   TO 'user_svc'@'%';
GRANT ALL ON tadpoles_resume.*  TO 'resume_svc'@'%';
GRANT ALL ON tadpoles_gateway.* TO 'gateway_svc'@'%';
GRANT ALL ON tadpoles_billing.* TO 'billing_svc'@'%';
-- resume_svc 无法访问 tadpoles_users，权限层面强制隔离
```

**前提二：跨模块调用必须通过 Interface**
```go
// ✅ 正确：Resume Agent 通过 interface 获取用户信息
type UserProvider interface {
    GetUser(ctx context.Context, userID string) (*User, error)
    ValidateToken(ctx context.Context, token string) (*Claims, error)
}

// 合并期实现：直接调用同进程内 users_schema
type localUserProvider struct { db *sqlx.DB }
func (p *localUserProvider) GetUser(...) { /* 查 users_schema */ }

// 拆分后实现：换成 gRPC client，Resume Agent 业务代码不需要改
type grpcUserProvider struct { client pb.UserServiceClient }
func (p *grpcUserProvider) GetUser(...) { /* 调 gRPC */ }

// ❌ 错误：在 resume handler 里直接写
// SELECT * FROM users_schema.users WHERE id = $1
// 这种代码拆分时要改几十处
```

**前提三：跨 Schema 无 JOIN，无跨库事务**
```go
// ❌ 错误：注册时一个事务跨两个 Schema
tx.Exec("INSERT INTO users_schema.users ...")
tx.Exec("INSERT INTO resume_schema.default_resumes ...")  // 不允许

// ✅ 正确：两步操作，通过补偿保证最终一致
userID := createUser(ctx, email, password)           // 写 users_schema
createDefaultWelcomeState(ctx, userID)               // 写 resume_schema
// 第二步失败：记录 failed_initializations，异步重试
```

**实际拆分工作量**（前提满足时约 2-3 天）：
```
1. 把 internal/user/ 复制到新仓库 user-service/
2. 新仓库加 gRPC server main.go（50行）
3. Resume Agent 的 localUserProvider 换成 grpcUserProvider
4. docker-compose.yml 增加 user-service 服务
5. users_schema 数据迁移到独立 DB（pg_dump 导出，pg_restore 导入）
6. 更新 API Gateway 路由规则
```

---

## 3. API Gateway：APISIX

### 3.1 为什么选 APISIX

不自己写 API Gateway，使用 Apache APISIX 作为统一入口。

| 需求 | APISIX 实现方式 |
|------|---------------|
| JWT 验证 | 内置 `jwt-auth` 插件，配置即用 |
| 用户级限流 | 内置 `limit-count` 插件，key 设为 `consumer_name`（即用户ID），走 Redis |
| IP 限流 | 内置 `limit-req` 插件 |
| 路由分发 | Admin API 配置 Route → Upstream |
| SSL 终止 | 内置支持，自动续签 Let's Encrypt |
| 静态资源 | APISIX 直接代理到 Vue dist 目录 |
| 链路追踪 | 内置 `zipkin` / `opentelemetry` 插件 |
| 熔断保护 | 内置 `api-breaker` 插件 |

**选 APISIX 不选 Traefik 的核心原因**：Traefik 社区版的限流只能按 IP，做不到按用户 ID 限流，需要额外写 Auth Middleware 服务。APISIX 的 `limit-count` 插件原生支持按 Consumer（用户）维度限流，且内置 Redis 后端，无需额外开发。

### 3.2 核心概念

APISIX 的四个核心对象，理解了就会配置：

```
Route（路由）      → 匹配规则：哪些请求进来（uri、method、host）
Upstream（上游）   → 后端服务地址（支持负载均衡、健康检查）
Consumer（消费者） → 调用方身份（对应一个用户，绑定 jwt-auth 凭证）
Plugin（插件）     → 挂在 Route 上的功能（jwt-auth、limit-count、...）

关系：Route 挂 Plugin，Plugin 关联 Consumer，Route 指向 Upstream
```

### 3.3 路由与插件配置

**路由规则（通过 Admin API 配置，或 YAML 声明式配置）**：

```yaml
# 公开路由：注册/登录，不需要 JWT 验证
routes:
  - uri: /api/auth/*
    upstream: resume-agent:8082
    plugins:
      limit-req:                  # IP 级限流防爆破
        rate: 5                   # 5 req/s
        burst: 2
        key: remote_addr

  # 受保护路由：简历接口，需要 JWT + 用户级限流
  - uri: /api/resumes/*
    upstream: resume-agent:8082
    plugins:
      jwt-auth: {}                # 验证 JWT，自动解析出 consumer（用户）
      limit-count:
        count: 20                 # 20次/分钟
        time_window: 60
        key: consumer_name        # 按用户 ID 限流（不是 IP）
        rejected_code: 429
        rejected_msg: '{"error":"rate_limited","message":"请求过于频繁，请稍后重试"}'

  - uri: /api/billing/*
    upstream: billing-service:8084
    plugins:
      jwt-auth: {}
      limit-count:
        count: 30
        time_window: 60
        key: consumer_name

  # 管理员接口：LLM Gateway 管理
  - uri: /api/admin/gateway/*
    upstream: llm-gateway:8083
    plugins:
      jwt-auth: {}
      consumer-restriction:       # 限制只有 admin 角色可访问
        whitelist: ["admin"]

  # 静态资源
  - uri: /*
    upstream: nginx-static:80    # 独立的 nginx 只服务静态文件
```

### 3.4 JWT 验证流程

APISIX 的 `jwt-auth` 插件工作原理：

```
1. 用户登录成功后，Resume Agent 签发 JWT（HS256，携带 user_id）
2. APISIX 管理员为每个用户创建 Consumer：
     consumer_name = user_id
     credential: { key: user_id, secret: JWT_SECRET }
   （实际上通过 Resume Agent 注册时自动调用 APISIX Admin API 创建）

3. 请求到来时，jwt-auth 插件：
     a. 提取 Authorization: Bearer <token>
     b. 本地验签（HS256 + JWT_SECRET），无 DB 调用
     c. 验证通过 → 在请求 Header 中注入:
          X-Consumer-Username: {user_id}
          X-Consumer-ID: {consumer_id}
     d. 下游服务直接读取 X-Consumer-Username 获得 user_id

4. 下游服务（Resume Agent / Billing）:
     userID := c.GetHeader("X-Consumer-Username")
     // 不需要再验证 JWT，APISIX 已保证
```

**Consumer 自动注册**（用户注册时由 Resume Agent 调用）：

```go
// 用户注册成功后，调用 APISIX Admin API 创建对应 Consumer
func (s *AuthService) registerAPISIXConsumer(userID string) error {
    body := map[string]interface{}{
        "username": userID,
        "plugins": map[string]interface{}{
            "jwt-auth": map[string]interface{}{
                "key":    userID,
                "secret": s.jwtSecret,
            },
        },
    }
    // PUT http://apisix:9180/apisix/admin/consumers/{userID}
    return s.apisixAdminClient.PutConsumer(userID, body)
}
```

### 3.5 限流设计

APISIX `limit-count` 插件底层使用 Redis（与项目已有的 Redis 复用），实现滑动窗口限流：

```
限流维度        插件配置                    参数
────────────────────────────────────────────────────────────
IP 级防爆破     limit-req (令牌桶)          登录5次/s，注册0.05次/s
用户级限流      limit-count (Redis滑动窗口)  对话20次/min，计费30次/min
全局 IP 保护    limit-req                   全局1000次/min/IP
```

**为什么不需要自己实现滑动窗口**：APISIX 的 `limit-count` 使用 `fixed_window` 或 `sliding_window` 策略（配置项 `policy: sliding`），直接用即可。

### 3.6 Docker Compose 配置

```yaml
services:
  etcd:
    image: bitnami/etcd:3.5
    environment:
      - ALLOW_NONE_AUTHENTICATION=yes
      - ETCD_ADVERTISE_CLIENT_URLS=http://etcd:2379
    volumes: ["etcd_data:/bitnami/etcd"]

  apisix:
    image: apache/apisix:3.8.0-debian
    volumes:
      - "./apisix/config.yaml:/usr/local/apisix/conf/config.yaml"
    ports:
      - "80:9080"    # HTTP 入口
      - "443:9443"   # HTTPS 入口
    depends_on: [etcd]
    environment:
      - APISIX_STAND_ALONE=false

  apisix-dashboard:          # 可选：可视化管理界面
    image: apache/apisix-dashboard:3.0.1-alpine
    ports: ["9000:9000"]
    depends_on: [etcd]

volumes:
  etcd_data:
```

```yaml
# apisix/config.yaml 核心配置
apisix:
  node_listen: 9080
  ssl:
    enable: true
    listen_port: 9443

deployment:
  role: traditional
  etcd:
    host: ["http://etcd:2379"]

plugin_attr:
  limit-count:
    redis_cluster_nodes:
      - "redis:6379"        # 复用项目已有的 Redis
```

### 3.7 APISIX 在 K8s 的演进

```
Docker Compose 阶段：
  APISIX 配置通过 Admin API 动态下发，配置存 etcd

K8s 阶段：
  使用 APISIX Ingress Controller
  路由配置改为 K8s CRD（ApisixRoute 资源），声明式管理
  etcd 替换为 K8s 内置 etcd（或保留独立 etcd）
  服务发现自动通过 K8s Service DNS

迁移成本：
  路由配置格式从 Admin API JSON → K8s YAML CRD
  业务代码（Resume Agent / Billing）零改动
```

---

## 4. User Service

### 4.0 GORM 基础模型约定（全平台统一）

所有服务统一使用两个 Base Model，通过 Go struct 嵌入实现，避免每张表重复定义公共字段。

**通用基础模型（支持软删除）**

```go
// shared/model/base.go — 所有业务表嵌入此 Base
type Base struct {
    ID        string         `gorm:"primarykey;type:char(36)"          json:"id"`
    CreatedAt time.Time      `gorm:"not null"                          json:"created_at"`
    UpdatedAt time.Time      `gorm:"not null"                          json:"updated_at"`
    DeletedAt gorm.DeletedAt `gorm:"index"                             json:"-"`
    // DeletedAt 使用 gorm.DeletedAt 类型：
    //   · db.Delete(&record) → 设置 deleted_at = NOW()，不物理删除
    //   · 所有查询自动追加 WHERE deleted_at IS NULL
    //   · db.Unscoped().Find() 可查包含已删除的记录
}
```

**仅追加模型（财务流水 / 日志类表，禁止修改删除）**

```go
// shared/model/base.go — append-only 表嵌入此 InsertOnlyBase
type InsertOnlyBase struct {
    ID        string    `gorm:"primarykey;type:char(36)" json:"id"`
    CreatedAt time.Time `gorm:"not null"                 json:"created_at"`
    // 无 UpdatedAt：GORM 不会对这类表做 UPDATE 操作
    // 无 DeletedAt：业务禁止删除（credit_transactions / usage_logs / messages）
}
```

**UUID 生成规范**

```go
// 统一使用 UUID v7（时间有序），不用 v4（纯随机，B-Tree 索引性能差）
// UUID v7: 前 48 位 = 毫秒时间戳，单调递增，索引局部性接近自增 ID
import "github.com/google/uuid"

func NewID() string {
    id, _ := uuid.NewV7()
    return id.String()  // "018f1a2b-3c4d-7e5f-a6b7-c8d9e0f1a2b3"
}
```

**哪些表用哪个 Base**

| 表名 | Base 类型 | 原因 |
|------|-----------|------|
| users, resumes, api_keys 等业务表 | `Base`（含 deleted_at） | 需要软删除 |
| sessions, providers | `Base` | 需要时间戳 |
| refresh_tokens | `InsertOnlyBase` + 手动 revoked 字段 | 撤销用标志位，无需软删除 |
| credit_transactions | `InsertOnlyBase` | 财务流水，禁止任何修改 |
| usage_logs | `InsertOnlyBase` | 日志类，append-only |
| messages | `InsertOnlyBase` | 消息不可撤回 |
| conversation_summaries | `InsertOnlyBase` | 只增不改 |

---

### 4.1 数据库设计

```sql
-- Database: tadpoles_users
-- 注：MySQL 使用 CHAR(36) 存储 UUID v7，应用层生成（Go: github.com/google/uuid v7）
--     使用 DATETIME(3) 存储毫秒精度时间戳

CREATE TABLE users (
    id              CHAR(36)     NOT NULL                     COMMENT 'UUID v7 主键，应用层生成，时间有序',
    email           VARCHAR(255) NOT NULL                     COMMENT '用户邮箱，唯一登录凭证',
    password_hash   VARCHAR(255) NOT NULL                     COMMENT 'bcrypt 哈希值，cost=12，明文不落库',
    nickname        VARCHAR(100) NOT NULL DEFAULT ''          COMMENT '昵称，可为空字符串',
    avatar_url      VARCHAR(500)                              COMMENT '头像 URL，可为 NULL',
    status          ENUM('active','banned','beta')
                    NOT NULL DEFAULT 'active'                 COMMENT '账户状态：active=正常 | banned=封禁 | beta=内测灰度',
    source          VARCHAR(50)  NOT NULL DEFAULT 'web'       COMMENT '注册来源：web/ios/android/api',
    email_verified  TINYINT(1)   NOT NULL DEFAULT 0           COMMENT '邮箱验证状态：0=未验证 1=已验证',
    last_login_at   DATETIME(3)                               COMMENT '最后登录时间，NULL 表示从未登录',
    created_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                              COMMENT '注册时间（GORM 自动填充）',
    updated_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                 ON UPDATE CURRENT_TIMESTAMP(3)
                                                              COMMENT '资料最后更新时间（GORM 自动维护）',
    deleted_at      DATETIME(3)                               COMMENT '软删除时间（GORM）；NULL=正常，非NULL=已注销',
    PRIMARY KEY (id),
    UNIQUE KEY uk_users_email (email),
    INDEX idx_users_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='用户账户表，平台所有业务模块共享的统一账户体系';

CREATE TABLE refresh_tokens (
    id            CHAR(36)     NOT NULL                       COMMENT 'UUID v7 主键',
    user_id       CHAR(36)     NOT NULL                       COMMENT '所属用户 ID，级联删除',
    token_hash    VARCHAR(255) NOT NULL                       COMMENT 'SHA-256(raw_token)，仅存哈希，防拖库攻击',
    expires_at    DATETIME(3)  NOT NULL                       COMMENT 'Token 有效期，30天',
    user_agent    VARCHAR(500)                                COMMENT '客户端 User-Agent，用于多设备管理',
    ip_address    VARCHAR(45)                                 COMMENT '登录 IP，支持 IPv4/IPv6',
    revoked       TINYINT(1)   NOT NULL DEFAULT 0             COMMENT 'Token 是否已撤销：0=有效 1=已撤销（Token Rotation 后置1）',
    revoked_at    DATETIME(3)                                 COMMENT '撤销时间，Token Rotation 或主动登出时设置',
    created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                              COMMENT '颁发时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_rt_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE KEY uk_rt_hash (token_hash),
    INDEX idx_rt_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='Refresh Token 表，只存 SHA-256 哈希；支持多设备登录，Token Rotation 防重放';
```

### 4.2 JWT 双 Token 设计

```
Access Token（短期，无状态）:
  · 格式：JWT，HS256 签名
  · 有效期：1 小时
  · Payload: { sub: user_id, email, status, iat, exp }
  · 验证方式：本地验签，不查 DB
  · 存储位置：前端内存（不存 localStorage，防 XSS）

Refresh Token（长期，有状态）:
  · 格式：crypto/rand 32字节 → base64url 编码（不可预测的随机串）
  · 有效期：30 天
  · DB 存储：SHA-256(raw_token) 哈希值，原始值不落库
  · 存储位置：HttpOnly Cookie（防 XSS） + 响应体（兼容移动端）
  · 使用后立即轮换（旧 token 标记 revoked，发放新 token）
```

**Token 轮换流程**：

```
前端请求 API → 401 Access Token 过期
  ↓
POST /api/auth/refresh { refresh_token }
  ↓
后端:
  1. SHA-256(refresh_token) → 查 refresh_tokens 表
  2. 检查 revoked=false，expires_at > NOW()
  3. 验证失败 → 401，要求重新登录（可能是 token 泄露，应告警）
  4. 验证通过:
     a. UPDATE refresh_tokens SET revoked=true, revoked_at=NOW()
     b. 生成新 access_token（JWT）
     c. 生成新 refresh_token（随机串）
     d. INSERT 新 refresh_token 记录
     e. 返回新 token 对
  ↓
前端用新 access_token 重试原请求
```

**为什么 Refresh Token 只存哈希**：

若 DB 泄露，攻击者拿到的是 SHA-256 哈希值，无法反推原始 token，无法冒充用户登录。Access Token 不需要存 DB，因为它是无状态的（验证只需 JWT_SECRET，不查库）。

### 4.3 注册触发链与跨服务事务处理

**注册是唯一的跨服务写操作场景**：写 users 表（User Service）+ 写 user_credits 表（Billing Service）。两张表在不同数据库，无法用本地事务保证原子性。我们用 **Saga 模式（编排式）** 处理，而非分布式事务（2PC 太重且影响可用性）。

```
POST /api/auth/register { email, password, nickname }
  ↓
User Service（本地事务）:
  1. 校验邮箱格式，密码 >= 8 位
  2. 检查邮箱唯一性（SELECT COUNT，时序竞争由 DB UNIQUE 约束兜底返回 409）
  3. bcrypt(password, cost=12)
  4. INSERT users（user 记录写入）
  ↓
  5. 调用 Billing Service gRPC: CreateWallet(user_id, gift_amount=100)
     ┌─ 成功 → 继续
     └─ 失败（网络超时/Billing 宕机）:
          · 标记 users.status = 'pending_billing'（可选）
          · 异步重试（Redis 延迟队列，最多重试 5 次，指数退避）
          · 用户此时可以登录，但发起 LLM 请求时 Pre-Auth 查不到钱包返回 402
          · 重试成功后自动激活，用户刷新页面即可正常使用

  Billing Service（本地事务）:
    a. INSERT user_credits(user_id, balance=100)
    b. INSERT credit_transactions(type='gift', amount=100, desc='新用户注册赠送')
    c. SET Redis credit:{user_id} balance=100 reserved=0
  ↓
  6. 生成 access_token + refresh_token
  7. 返回 { user, access_token, refresh_token }
```

**为什么不用 Outbox Pattern（事务性发件箱）**：Outbox 更适合"写 DB + 发消息队列"的场景。这里 Billing 是同步 gRPC，成功率高，简单重试足够。如果未来改为异步（Redis Streams），再引入 Outbox。

---

## 5. LLM Gateway Service

这是平台技术含量最高的模块，核心价值是屏蔽上游 LLM 厂商的差异，为业务层提供统一、高可用的 AI 调用能力。

### 5.1 对外接口：OpenAI 兼容协议

```
POST /v1/chat/completions    支持 stream=true（SSE）
POST /v1/embeddings          向量嵌入

上游业务调用示例（Resume Agent）:
  client := openai.NewClient(
      BaseURL: "http://llm-gateway:8083/v1",
      APIKey:  "internal-service-token",   // 内部服务间鉴权，非用户 token
      Headers: {
          "X-Caller-Service": "resume-agent",
          "X-User-ID":        userID,       // 透传，用于 usage 记录
          "X-Request-ID":     requestID,    // 链路追踪
      },
  )
```

**好处**：业务层与 LLM 厂商完全解耦。切换厂商、更换模型，业务代码零改动。

### 5.2 支持的 LLM 厂商

```sql
-- DB 初始化数据
INSERT INTO gateway_schema.providers VALUES
  ('openai',    'OpenAI',    'https://api.openai.com/v1',              100),
  ('anthropic', 'Anthropic', 'https://api.anthropic.com/v1',          90),
  ('deepseek',  'DeepSeek',  'https://api.deepseek.com/v1',           80),
  ('qwen',      'Qwen',      'https://dashscope.aliyuncs.com/v1',     70),
  ('moonshot',  'Moonshot',  'https://api.moonshot.cn/v1',            60),
  ('zhipu',     'Zhipu',     'https://open.bigmodel.cn/api/paas/v4/', 50);
-- priority 越小越优先
```

### 5.3 数据库设计

```sql
-- Database: tadpoles_gateway

CREATE TABLE providers (
    id          VARCHAR(50)  NOT NULL                     COMMENT '厂商唯一标识，如 openai/anthropic/deepseek',
    name        VARCHAR(100) NOT NULL                     COMMENT '显示名称，如 OpenAI / 深度求索',
    base_url    VARCHAR(500) NOT NULL                     COMMENT 'OpenAI 兼容 API Base URL',
    is_active   TINYINT(1)   NOT NULL DEFAULT 1           COMMENT '是否启用：0=禁用 1=启用',
    priority    INT          NOT NULL DEFAULT 100          COMMENT '故障切换优先级，数值越小越优先',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                          COMMENT '创建时间',
    updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                             ON UPDATE CURRENT_TIMESTAMP(3)
                                                          COMMENT '更新时间',
    deleted_at  DATETIME(3)                               COMMENT '软删除时间',
    PRIMARY KEY (id),
    INDEX idx_providers_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='LLM 厂商配置表，记录平台接入的 AI 服务提供商';

CREATE TABLE api_keys (
    id                     CHAR(36)     NOT NULL                COMMENT 'UUID v7 主键',
    provider_id            VARCHAR(50)  NOT NULL                COMMENT '所属厂商 ID，关联 providers.id',
    key_encrypted          BLOB         NOT NULL                COMMENT 'AES-256-GCM 加密存储的完整 API Key',
    key_prefix             VARCHAR(12)  NOT NULL                COMMENT 'Key 明文前缀（如 sk-abc12），用于日志排查，不可用于鉴权',
    alias                  VARCHAR(100)                         COMMENT '人工备注别名，便于管理识别',
    status                 ENUM('active','paused','depleted')
                           NOT NULL DEFAULT 'active'            COMMENT 'Key 状态：active=正常 | paused=手动暂停 | depleted=配额耗尽',
    -- 配额限制
    rpm_limit              INT                                  COMMENT '每分钟请求上限（Requests Per Minute），NULL 表示不限',
    tpm_limit              INT                                  COMMENT '每分钟 Token 上限（Tokens Per Minute），NULL 表示不限',
    daily_budget_usd_cents BIGINT                               COMMENT '每日费用上限（美分），NULL 表示不限',
    -- 熔断健康状态（Redis 为实时热数据，DB 存持久化备份）
    consecutive_errors     INT          NOT NULL DEFAULT 0      COMMENT '连续错误次数，达阈值触发熔断（CLOSED→OPEN）',
    circuit_state          ENUM('closed','open','half_open')
                           NOT NULL DEFAULT 'closed'            COMMENT '熔断器状态：closed=正常 | open=熔断拒绝 | half_open=探测恢复',
    circuit_opened_at      DATETIME(3)                          COMMENT '熔断触发时间，用于计算 OPEN→HALF_OPEN 超时',
    last_error_at          DATETIME(3)                          COMMENT '最后一次报错时间',
    last_used_at           DATETIME(3)                          COMMENT '最后一次被调用时间',
    -- 累计用量统计（最终一致，定时从 Redis 同步写回）
    total_requests         BIGINT       NOT NULL DEFAULT 0      COMMENT '历史总请求次数',
    total_tokens           BIGINT       NOT NULL DEFAULT 0      COMMENT '历史总消耗 Token 数（prompt + completion）',
    total_cost_usd_cents   BIGINT       NOT NULL DEFAULT 0      COMMENT '历史总费用估算，单位：美分',
    created_at             DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                                COMMENT '录入时间',
    updated_at             DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                           ON UPDATE CURRENT_TIMESTAMP(3)       COMMENT '最后更新时间（熔断状态变更时自动更新）',
    deleted_at             DATETIME(3)                          COMMENT '软删除时间（软删除保留历史统计，不影响对账）',
    PRIMARY KEY (id),
    CONSTRAINT fk_key_provider FOREIGN KEY (provider_id) REFERENCES providers(id),
    INDEX idx_keys_provider (provider_id, status),
    INDEX idx_keys_deleted  (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='LLM API Key 管理表，支持多厂商 Key 池、熔断状态、用量统计';

CREATE TABLE usage_logs (
    id                CHAR(36)     NOT NULL                     COMMENT 'UUID v7 主键',
    api_key_id        CHAR(36)     NOT NULL                     COMMENT '本次调用使用的 API Key ID',
    provider_id       VARCHAR(50)  NOT NULL                     COMMENT 'LLM 厂商 ID',
    model             VARCHAR(100) NOT NULL                     COMMENT '模型名称，如 gpt-4o / deepseek-chat',
    caller_user_id    CHAR(36)                                  COMMENT '调用方用户 ID，平台内部调用时为 NULL',
    caller_service    VARCHAR(50)                               COMMENT '调用方服务名，如 resume-agent',
    request_id        VARCHAR(100)                              COMMENT '全链路追踪 Request ID（OpenTelemetry Trace ID）',
    prompt_tokens     INT          NOT NULL                     COMMENT '输入 Token 数（实际计费依据）',
    completion_tokens INT          NOT NULL                     COMMENT '输出 Token 数（实际计费依据）',
    cost_usd_cents    INT          NOT NULL                     COMMENT '本次调用费用，单位：美分',
    latency_ms        INT          NOT NULL                     COMMENT '端到端延迟毫秒数（含网络传输）',
    status            ENUM('success','error','timeout') NOT NULL COMMENT '调用结果：success=成功 | error=API报错 | timeout=超时',
    error_code        VARCHAR(50)                               COMMENT '错误码，如 rate_limit / server_error / context_length',
    created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                                COMMENT '请求发起时间，append-only 不可修改',
    PRIMARY KEY (id),
    INDEX idx_usage_key  (api_key_id, created_at),
    INDEX idx_usage_user (caller_user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='LLM 调用明细日志，append-only，用于用量分析、计费对账、Key 健康监控';
```

### 5.4 Key 调度：Redis ZSET

**数据结构**：

```
Redis ZSET:  gateway:keys:{provider_id}
  Member:    {key_id}（UUID）
  Score:     available_at（Unix 毫秒时间戳）
             Score = 0 表示立即可用
             Score = future 表示限流冷却中

Redis SET:   gateway:keys:circuit_open
  Member:    {key_id}（熔断中的 key，拒绝接收请求）

Redis HASH:  gateway:key:{key_id}:stats
  Field:     consecutive_errors, last_error_ms, request_count_today
```

**Key 选择算法**：

```go
func (p *KeyPool) SelectKey(ctx context.Context, providerID string) (*APIKey, error) {
    zsetKey := "gateway:keys:" + providerID

    for attempts := 0; attempts < 10; attempts++ {
        // ZPOPMIN 是原子操作：取出 score 最小（最久未使用）的 key
        // 100 个并发请求同时调用，各自拿到不同的 key，天然互斥，无需锁
        results, err := redis.ZPopMin(ctx, zsetKey, 1).Result()
        if err != nil || len(results) == 0 {
            return nil, ErrNoAvailableKey
        }

        keyID := results[0].Member.(string)
        availableAt := int64(results[0].Score)

        // 还在冷却期（限流中）
        if availableAt > time.Now().UnixMilli() {
            // 放回去，等待
            redis.ZAdd(ctx, zsetKey, redis.Z{Score: results[0].Score, Member: keyID})
            time.Sleep(10 * time.Millisecond)
            continue
        }

        // 检查是否在熔断状态
        isBroken, _ := redis.SIsMember(ctx, "gateway:keys:circuit_open", keyID).Result()
        if isBroken {
            // 放回去跳过
            redis.ZAdd(ctx, zsetKey, redis.Z{Score: float64(time.Now().UnixMilli()), Member: keyID})
            continue
        }

        // 选中此 key，解密后返回（keyID 用于后续归还）
        key := p.decryptKey(keyID)
        return &APIKey{ID: keyID, Key: key}, nil
    }
    return nil, ErrAllKeysExhausted
}

func (p *KeyPool) ReturnKey(ctx context.Context, keyID string, result KeyUseResult) {
    zsetKey := "gateway:keys:" + result.ProviderID
    now := time.Now().UnixMilli()

    switch result.Status {
    case "success":
        // 立即归还，score=now（公平轮转）
        redis.ZAdd(ctx, zsetKey, redis.Z{Score: float64(now), Member: keyID})
        redis.HSet(ctx, "gateway:key:"+keyID+":stats", "consecutive_errors", 0)

    case "rate_limited":  // 429
        cooldown := int64(60 * 1000)  // 60 秒冷却
        redis.ZAdd(ctx, zsetKey, redis.Z{Score: float64(now + cooldown), Member: keyID})

    case "error":  // 5xx
        errors, _ := redis.HIncrBy(ctx, "gateway:key:"+keyID+":stats", "consecutive_errors", 1).Result()
        if errors >= 5 {
            // 触发熔断
            redis.SAdd(ctx, "gateway:keys:circuit_open", keyID)
            redis.ZAdd(ctx, zsetKey, redis.Z{Score: float64(now), Member: keyID})
            // 启动后台健康检查（60 秒后探测）
            go p.scheduleHealthCheck(keyID, 60*time.Second)
        } else {
            redis.ZAdd(ctx, zsetKey, redis.Z{Score: float64(now), Member: keyID})
        }
    }
}
```

### 5.5 熔断器状态机

```
每个 API Key 独立熔断，互不影响

         ┌──────────────────────────────────────────────────┐
         │                                                  │
    CLOSED（正常）                                          │
    连续错误 >= 5 ──────────────────────────────► OPEN（熔断）
                                                  冷却 60s 后
                                                      │
                                                      ▼
                                               HALF_OPEN（探测）
                                               发送一个探测请求
                                                  │       │
                                               成功      失败
                                                  │       │
                                                  ▼       ▼
                                              CLOSED    OPEN
                                              （恢复）  （延长冷却 120s）
```

### 5.6 跨 Provider 故障切换

```go
// 模型映射表：当 Provider A 不可用时，自动切换到 Provider B 的等价模型
var modelFallbacks = map[string][]ModelTarget{
    "claude-3-5-sonnet": {
        {Provider: "anthropic", Model: "claude-3-5-sonnet-20241022"},
        {Provider: "openai",    Model: "gpt-4o"},          // fallback
        {Provider: "deepseek",  Model: "deepseek-chat"},   // 最后兜底
    },
    "gpt-4o": {
        {Provider: "openai",    Model: "gpt-4o"},
        {Provider: "anthropic", Model: "claude-3-5-sonnet-20241022"},
    },
}

// 切换时通知调用方
sseEvent := SSEEvent{
    Type:    "provider_switched",
    Message: "主要服务商暂时不可用，已自动切换至备用服务商，性能可能略有差异",
}
```

### 5.7 Usage 计量事件（Metering，非计费）

LLM Gateway 在每次调用完成后，**异步**发布计量事件到 Redis Streams。注意：这个事件**不用于扣款**（扣款在 Pre-Auth/Settlement 阶段已完成），只用于统计分析和日志。

```go
type UsageEvent struct {
    EventID          string    `json:"event_id"`    // = request_id + "_usage"，幂等ID
    UserID           string    `json:"user_id"`
    CallerService    string    `json:"caller_service"`
    SessionID        string    `json:"session_id"`
    Model            string    `json:"model"`
    ProviderID       string    `json:"provider_id"`
    PromptTokens     int       `json:"prompt_tokens"`
    CompletionTokens int       `json:"completion_tokens"`
    CostUSDCents     int       `json:"cost_usd_cents"`  // 网关实际美元成本
    LatencyMs        int       `json:"latency_ms"`
    CreatedAt        time.Time `json:"created_at"`
}

// 发布（异步，不阻塞响应）
go func() {
    redis.XAdd(ctx, &redis.XAddArgs{
        Stream: "stream:usage_events",
        ID:     "*",
        Values: usageEvent,
    })
}()
```

---

## 6. Billing Service

### 6.1 核心设计原则

1. **Pre-Auth + Settlement**（预授权 + 结算）：调用 LLM 前冻结积分，调用完成后按实际用量结算，从根本上杜绝超用
2. **Redis 热路径，DB 冷路径**：余额操作走 Redis Lua 脚本（微秒级），DB 是最终数据源
3. **Append-only 流水**：credit_transactions 表永不 UPDATE/DELETE，只 INSERT
4. **幂等保护**：每笔操作有唯一 idempotency_key，重复操作数据库层兜底

### 6.2 数据库设计

```sql
-- Database: tadpoles_billing

-- 用户积分钱包（一用户一行，MySQL 是余额最终 Source of Truth，Redis 是热缓存）
CREATE TABLE user_credits (
    user_id         CHAR(36)    NOT NULL                     COMMENT '用户 ID，与 tadpoles_users.users.id 一致，无跨库外键（服务隔离）',
    balance         BIGINT      NOT NULL DEFAULT 0           COMMENT '当前积分余额（≥0，Redis 缓存同步此值）',
    total_recharged BIGINT      NOT NULL DEFAULT 0           COMMENT '历史累计充值积分（不含赠送）',
    total_gifted    BIGINT      NOT NULL DEFAULT 0           COMMENT '历史累计赠送积分（注册礼、活动赠送）',
    total_consumed  BIGINT      NOT NULL DEFAULT 0           COMMENT '历史累计消耗积分',
    version         BIGINT      NOT NULL DEFAULT 0           COMMENT '乐观锁版本号，UPDATE 时 WHERE version=? AND version=version+1',
    created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                             COMMENT '钱包初始化时间（用户注册时由 Billing Service 创建）',
    updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                    ON UPDATE CURRENT_TIMESTAMP(3)           COMMENT '余额最后变动时间',
    PRIMARY KEY (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='用户积分钱包，一用户一行。DB 是余额最终来源，Redis Hash 是高并发热路径缓存';

-- 积分流水（财务核心账本，append-only，永不 UPDATE/DELETE，通过 idempotency_key 保证幂等）
CREATE TABLE credit_transactions (
    id                CHAR(36)     NOT NULL                  COMMENT 'UUID v7 主键',
    user_id           CHAR(36)     NOT NULL                  COMMENT '所属用户 ID',
    type              ENUM('recharge','consume','gift','refund','adjust') NOT NULL
                                                             COMMENT '交易类型：recharge=充值 | consume=LLM消耗 | gift=赠送 | refund=退款 | adjust=管理员调整',
    amount            BIGINT       NOT NULL                  COMMENT '变动积分量：正值=入账，负值=出账',
    balance_after     BIGINT       NOT NULL                  COMMENT '操作后账户余额快照（用于人工对账，无需重算历史）',
    description       VARCHAR(500) NOT NULL                  COMMENT '交易描述（用户可见），如：简历对话消耗 30 积分',
    idempotency_key   VARCHAR(255)                           COMMENT '幂等键，防重复记账；DB UNIQUE 约束为最后防线',
    -- 关联上下文（便于追溯和对账）
    order_id          VARCHAR(100)                           COMMENT '关联充值订单 ID（recharge 类型）',
    service_name      VARCHAR(50)                            COMMENT '发起服务名，如 resume-agent / billing-admin',
    session_id        CHAR(36)                               COMMENT '关联 AI 对话会话 ID（consume 类型）',
    request_id        VARCHAR(100)                           COMMENT '关联 LLM 请求 ID，用于与 usage_logs 对账',
    -- LLM 消耗明细（consume 类型时填写，便于账单明细展示）
    model             VARCHAR(100)                           COMMENT '消耗的模型名称，如 gpt-4o',
    prompt_tokens     INT                                    COMMENT 'LLM 输入 Token 数',
    completion_tokens INT                                    COMMENT 'LLM 输出 Token 数',
    cost_usd_cents    INT                                    COMMENT '对应美元成本（美分），用于平台成本核算',
    created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                             COMMENT '记账时间，append-only 此行永不修改',
    PRIMARY KEY (id),
    UNIQUE KEY uk_tx_idempotency (idempotency_key),
    INDEX idx_tx_user    (user_id, created_at),
    INDEX idx_tx_session (session_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='积分流水明细表（财务核心），append-only，永不 UPDATE/DELETE，是计费体系的最终真相';
```

### 6.3 Redis 数据结构

```
Key:   credit:{user_id}
Type:  Hash
Field:
  balance    → 账户总积分（含冻结部分）
  reserved   → 已冻结积分（pre-auth 锁定，等待结算释放）

可用积分 = balance - reserved

说明：
  · balance 和 DB user_credits.balance 保持最终一致（秒级延迟）
  · 初始化：用户注册时 SET；Cache Miss 时从 DB 加载
  · reserved 只存在于 Redis，DB 不存（settled 后归零，无需持久化）
```

### 6.4 三个核心 Lua 脚本

**脚本一：Pre-Auth（LLM 调用前，冻结积分）**

```lua
-- KEYS[1] = "credit:{user_id}"
-- ARGV[1] = estimated_cost（基于 max_tokens 的估算，乘以 1.2 安全系数）
-- ARGV[2] = min_threshold（最低保留积分，如 10，防止余额耗尽时误拒）
-- 返回: {0, available_after} 成功 | {-1, current_available} 余额不足 | {-2, 0} key不存在

local balance  = redis.call('HGET', KEYS[1], 'balance')
local reserved = redis.call('HGET', KEYS[1], 'reserved')

if balance == false then
    return {-2, 0}  -- Cache Miss，调用方需从 DB 加载后重试
end

balance  = tonumber(balance)
reserved = tonumber(reserved or '0')
local cost      = tonumber(ARGV[1])
local available = balance - reserved

if available < cost then
    return {-1, available}  -- 余额不足，拒绝
end

redis.call('HINCRBY', KEYS[1], 'reserved', cost)
return {0, available - cost}  -- 成功，返回冻结后的可用余额
```

**脚本二：Settlement（LLM 调用完成后，结算真实费用）**

```lua
-- KEYS[1] = "credit:{user_id}"
-- ARGV[1] = estimated_cost（pre-auth 时冻结的估算值）
-- ARGV[2] = actual_cost（LLM 返回的真实 token 用量对应积分）
-- 返回: new_balance

local estimated = tonumber(ARGV[1])
local actual    = tonumber(ARGV[2])

-- 释放冻结，扣除真实费用
-- 净效果：balance 减少 actual_cost，reserved 减少 estimated_cost
-- 若 actual < estimated：balance 比 pre-auth 时少扣，差额自动归还可用余额
-- 若 actual > estimated：超出部分从可用余额扣（概率低，靠 1.2x 系数规避）
redis.call('HINCRBY', KEYS[1], 'balance',  -actual)
redis.call('HINCRBY', KEYS[1], 'reserved', -estimated)

return tonumber(redis.call('HGET', KEYS[1], 'balance'))
```

**脚本三：充值**

```lua
-- KEYS[1] = "credit:{user_id}"
-- ARGV[1] = amount（充值积分数）
-- 返回: new_balance

redis.call('HINCRBY', KEYS[1], 'balance', tonumber(ARGV[1]))
return tonumber(redis.call('HGET', KEYS[1], 'balance'))
```

### 6.5 完整扣费链路

```
用户发消息
    │
    ▼
1. [Resume Agent] 快速余额预检
   GET Redis credit:{user_id} balance, reserved
   if (balance - reserved) <= 0 → 立即 402，不进入后续流程
    │
    ▼
2. [Resume Agent → Billing gRPC] Pre-Auth
   estimated_cost = max_tokens × unit_price × 1.2
   调用 Billing.PreAuthorize(user_id, estimated_cost, request_id)

   Billing Service:
     a. 执行 Lua 脚本一（原子冻结）
     b. 冻结成功 → 返回 OK
     c. 冻结失败（Cache Miss）→ 从 DB 加载余额写入 Redis → 重试 Lua
     d. 余额不足 → 返回 INSUFFICIENT，Resume Agent 返回 402
    │
    ▼
3. [Resume Agent → LLM Gateway] 发起 LLM 调用（SSE 流式）
    │
    ▼（LLM 响应完成）
4. [Resume Agent → Billing gRPC] Settlement
   actual_cost = prompt_tokens × 3.3/1K + completion_tokens × 16/1K（向上取整）
   调用 Billing.Settle(user_id, estimated_cost, actual_cost, request_id)

   Billing Service:
     a. 执行 Lua 脚本二（原子结算）
     b. 异步写 DB:
        - INSERT credit_transactions(idempotency_key=request_id)
        - UPDATE user_credits SET balance = balance - actual_cost
     c. 推送 balance_update 事件（通知前端更新显示）
    │
    ▼
5. LLM Gateway 异步发布 usage_event → Redis Streams（用于统计分析，非扣款）
```

### 6.6 积分定价规则

```
单位：1000 积分 = ¥10（1 积分 = ¥0.01）

LLM 成本换算示例（Claude Sonnet，USD/CNY=7.25，加价 1.5x）:
  Input：$3/1M tokens → ¥21.75/1M → 平台加价后 ≈ 3.3 积分/1K tokens（向上取整）
  Output：$15/1M tokens → ¥108.75/1M → 平台加价后 ≈ 16 积分/1K tokens（向上取整）

估算安全系数：1.2x（Pre-Auth 时用，防止低估导致超用）

充值套餐（¥ → 积分）:
  ¥5   →   500 积分
  ¥10  →  1000 积分
  ¥30  →  3200 积分（含赠 200）
  ¥50  →  5500 积分（含赠 500）
  ¥100 → 11500 积分（含赠 1500）
  ¥300 → 36000 积分（含赠 6000）

新用户注册：赠送 100 积分（约 6-10 次普通对话）
```

### 6.7 超用完全防御证明

```
场景：用户余额 50 积分，同时发起 3 个对话（各估算需 30 积分）

请求 A Pre-Auth：
  Lua 执行：balance=50, reserved=0, available=50, cost=30
  50 >= 30 ✅ → reserved=30, available=20

请求 B Pre-Auth（并发，Lua 原子执行，串行处理）:
  Lua 执行：balance=50, reserved=30, available=20, cost=30
  20 < 30 ❌ → 返回 {-1, 20}，Resume Agent 返回 402

请求 C Pre-Auth:
  Lua 执行：balance=50, reserved=30, available=20, cost=30
  20 < 30 ❌ → 返回 {-1, 20}，Resume Agent 返回 402

结论：只有请求 A 成功，B、C 被正确拒绝。永远不会超用。

关键：Redis 单线程执行 Lua 脚本，无论多少并发请求同时到达，
      Lua 脚本都是串行执行的，保证原子性。
```

---

## 7. Resume Agent Service

### 7.1 数据库设计

**关于 RAG / 向量检索**：当前不做 RAG，表结构中不包含 rag_documents。如果未来需要，引入开源 RAG 模块（如 Dify、RAGFlow），作为独立服务接入，不侵入当前业务代码。

```sql
-- Database: tadpoles_resume

CREATE TABLE resumes (
    id           CHAR(36)     NOT NULL                     COMMENT 'UUID v7 主键',
    user_id      CHAR(36)     NOT NULL                     COMMENT '所属用户 ID（来自 tadpoles_users，无跨库外键，服务隔离）',
    name         VARCHAR(255) NOT NULL DEFAULT '未命名简历'  COMMENT '简历名称，用户可自定义',
    template_id  VARCHAR(50)  NOT NULL DEFAULT 'classic'   COMMENT '使用的渲染模板 ID，关联 resume_templates.id',
    data         JSON         NOT NULL                     COMMENT '简历结构化数据（基本信息/工作经历/教育背景/技能等）',
    status       ENUM('active','archived') NOT NULL DEFAULT 'active'
                                                           COMMENT '状态：active=正常 | archived=归档（用户主动归档）',
    version      INT          NOT NULL DEFAULT 1           COMMENT '乐观锁版本号，UPDATE 时 WHERE version=? 防并发覆盖',
    created_at   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                           COMMENT '简历创建时间',
    updated_at   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                 ON UPDATE CURRENT_TIMESTAMP(3)            COMMENT '最后修改时间（GORM 自动维护）',
    deleted_at   DATETIME(3)                               COMMENT '软删除时间（GORM）；前端"删除"操作只设此字段，数据不物理删除',
    PRIMARY KEY (id),
    INDEX idx_resumes_user    (user_id, updated_at),
    INDEX idx_resumes_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='简历数据表，每份简历对应唯一一个 AI 对话会话（1:1 关系）';

-- 会话（严格 1:1 对应简历，对用户完全透明，用户感知的是"简历"而非"会话"）
CREATE TABLE sessions (
    id         CHAR(36)    NOT NULL                        COMMENT 'UUID v7 主键',
    resume_id  CHAR(36)    NOT NULL                        COMMENT '关联简历 ID；UNIQUE 约束保证严格 1:1',
    user_id    CHAR(36)    NOT NULL                        COMMENT '所属用户 ID（冗余存储，避免 JOIN resumes 表）',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                           COMMENT '会话创建时间（与简历同时创建）',
    PRIMARY KEY (id),
    CONSTRAINT fk_session_resume FOREIGN KEY (resume_id) REFERENCES resumes(id) ON DELETE CASCADE,
    UNIQUE KEY uk_sessions_resume (resume_id),
    INDEX idx_sessions_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='AI 对话会话表，与 resumes 严格 1:1，简历删除时级联删除';

CREATE TABLE messages (
    id                CHAR(36)    NOT NULL                 COMMENT 'UUID v7 主键',
    session_id        CHAR(36)    NOT NULL                 COMMENT '所属会话 ID',
    role              ENUM('user','assistant','tool') NOT NULL
                                                           COMMENT '消息角色：user=用户输入 | assistant=AI 回复 | tool=工具调用结果',
    content           MEDIUMTEXT  NOT NULL                 COMMENT '消息正文（MEDIUMTEXT 支持最大 16MB，兼容长工具结果和大段简历内容）',
    tool_calls        JSON                                 COMMENT 'AI 发起的工具调用列表（OpenAI tool_calls 格式），NULL 表示普通消息',
    tool_call_id      VARCHAR(255)                         COMMENT '工具调用 ID，role=tool 时有值，对应 tool_calls 中的 id 字段',
    prompt_tokens     INT                                  COMMENT '输入 Token 数，仅 role=assistant 的消息有值',
    completion_tokens INT                                  COMMENT '输出 Token 数，仅 role=assistant 的消息有值',
    created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                           COMMENT '消息发送时间，append-only 不可修改',
    PRIMARY KEY (id),
    CONSTRAINT fk_msg_session FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    INDEX idx_messages_session (session_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='对话消息表，含 tool 调用记录，append-only，随会话级联删除';

-- 用户长期记忆（用户级，跨所有简历和会话共享，UPSERT by unique key）
CREATE TABLE memory_facts (
    id                CHAR(36)     NOT NULL               COMMENT 'UUID v7 主键',
    user_id           CHAR(36)     NOT NULL               COMMENT '所属用户 ID，记忆跨该用户所有简历共享',
    category          VARCHAR(50)  NOT NULL               COMMENT '记忆分类，如 career/education/skills/preference/personality',
    `key`             VARCHAR(255) NOT NULL               COMMENT '记忆键名（key 是 MySQL 保留字，需反引号），如 current_job_title',
    value             TEXT         NOT NULL               COMMENT '记忆值，自然语言描述，如：字节跳动高级工程师，工作5年',
    confidence        FLOAT        NOT NULL DEFAULT 1.0   COMMENT '置信度 0.0~1.0，LLM 提取时评估，低置信度记忆优先级较低',
    source_session_id CHAR(36)                            COMMENT '记忆来源会话 ID，会话被删除时自动置 NULL',
    created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                          COMMENT '首次记录时间',
    updated_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                      ON UPDATE CURRENT_TIMESTAMP(3)      COMMENT '最后更新时间（UPSERT 时自动更新）',
    deleted_at        DATETIME(3)                         COMMENT '软删除时间（用户可清除特定记忆）',
    PRIMARY KEY (id),
    UNIQUE KEY uk_memory_user_key (user_id, category, `key`),
    CONSTRAINT fk_memory_session FOREIGN KEY (source_session_id)
        REFERENCES sessions(id) ON DELETE SET NULL,
    INDEX idx_memory_user    (user_id, category),
    INDEX idx_memory_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='用户长期记忆表，跨所有简历共享，UPSERT by (user_id, category, key)';

-- 对话摘要（长对话上下文压缩，避免超出 LLM 上下文窗口限制）
CREATE TABLE conversation_summaries (
    id              CHAR(36)    NOT NULL               COMMENT 'UUID v7 主键',
    session_id      CHAR(36)    NOT NULL               COMMENT '所属会话 ID',
    summary         TEXT        NOT NULL               COMMENT 'LLM 生成的对话摘要，替代原始消息历史送入上下文',
    last_message_id CHAR(36)                           COMMENT '摘要覆盖到的最后一条消息 ID（用于确定从哪条消息开始读取新消息）',
    created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                       COMMENT '摘要生成时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_summary_session FOREIGN KEY (session_id)
        REFERENCES sessions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='对话摘要表，用于长上下文压缩；随会话级联删除';

-- 简历模板（管理员维护，用户选择后作为 resume.template_id）
CREATE TABLE resume_templates (
    id            VARCHAR(50)  NOT NULL                  COMMENT '模板唯一标识，如 classic/modern/minimal/tech',
    name          VARCHAR(100) NOT NULL                  COMMENT '模板显示名称',
    description   TEXT                                   COMMENT '模板描述和适用场景说明',
    thumbnail_url VARCHAR(500)                           COMMENT '预览图 URL',
    tags          JSON         NOT NULL DEFAULT (JSON_ARRAY())
                                                         COMMENT '适用标签数组，如 ["互联网","校招","设计"]，用于筛选',
    is_active     TINYINT(1)   NOT NULL DEFAULT 1        COMMENT '是否上架：0=下架 1=上架（前端仅显示上架模板）',
    sort_order    INT          NOT NULL DEFAULT 0        COMMENT '排序权重，数值越小越靠前',
    created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                         COMMENT '创建时间',
    updated_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                  ON UPDATE CURRENT_TIMESTAMP(3)         COMMENT '更新时间',
    deleted_at    DATETIME(3)                            COMMENT '软删除时间（软删除保留已用此模板的历史简历引用）',
    PRIMARY KEY (id),
    INDEX idx_templates_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='简历模板表，管理员维护，支持软删除';

-- RAG：当前不做，未来引入开源 RAG 模块（Dify / RAGFlow）作为独立服务
```

### 7.2 简历 JSONB 数据结构

```json
{
  "basics": {
    "name": "张三",
    "email": "zhang@example.com",
    "phone": "138xxxx1234",
    "location": "北京",
    "gender": "男",
    "birth_year": 1995,
    "linkedin": "",
    "github": "github.com/zhangsan"
  },
  "summary": "10年互联网产品经验，专注于 B 端产品设计...",
  "experience": [
    {
      "company": "某科技公司",
      "title": "高级产品经理",
      "start": "2020-03",
      "end": "present",
      "location": "北京",
      "highlights": ["负责...", "主导..."]
    }
  ],
  "education": [
    {
      "school": "北京大学",
      "degree": "本科",
      "major": "计算机科学",
      "start": "2013-09",
      "end": "2017-06"
    }
  ],
  "skills": ["产品设计", "数据分析", "SQL", "Axure"],
  "projects": [],
  "certificates": [],
  "languages": []
}
```

### 7.3 ReAct Agent 工具集

```
工具名                    说明
─────────────────────────────────────────────────────────
update_resume_section     更新简历某个字段或板块
get_resume_data           读取当前完整简历 JSON
search_memory             查找用户长期记忆（跨简历）
update_memory             更新用户记忆（异步，不阻塞响应）
optimize_expression       优化某段表述的措辞
analyze_jd                解析 JD，提取关键技能和要求
match_resume_to_jd        简历与 JD 匹配度分析，给出改进建议
suggest_improvements      全局改进建议（结构、内容、措辞）
```

### 7.4 SSE 消息协议

```typescript
// 前端接收的 SSE 事件类型定义

// 流式文本 delta
{ type: "delta",            content: "..." }

// Agent 工具调用通知（展示"正在更新简历..."）
{ type: "tool_call",        tool: "update_resume_section", status: "running" }

// 工具执行完成，推送最新简历数据（前端实时更新预览）
{ type: "resume_updated",   data: { section: "experience", resume: {...} } }

// 余额更新（每次对话完成后推送）
{ type: "balance_update",   balance: 245, warning: false }

// 余额低警告（低于 50 积分）
{ type: "balance_warning",  balance: 42, threshold: 50 }

// 余额耗尽（pre-auth 失败）
{ type: "balance_exhausted", balance: 0 }

// Provider 切换通知
{ type: "provider_switched", message: "已自动切换至备用服务商" }

// 对话完成
{ type: "done", usage: { prompt: 450, completion: 230, cost_credits: 8 } }

// 错误
{ type: "error", code: "rate_limited", message: "请求过于频繁，请稍后重试" }
```

---

## 8. 服务间通信

### 8.1 同步通信：gRPC

**为什么选 gRPC 而非内部 REST**：
- `.proto` 文件即接口文档，契约强类型，编译期发现接口不兼容
- 比 JSON over HTTP 序列化性能高 30-50%
- 原生支持双向流（未来实时推送场景可用）

```protobuf
// billing.proto
service BillingService {
    // 创建用户钱包（注册时调用）
    rpc CreateWallet(CreateWalletRequest) returns (WalletResponse);

    // 查询余额（快速读，走 Redis 缓存）
    rpc GetBalance(GetBalanceRequest) returns (BalanceResponse);

    // 预授权（LLM 调用前，原子冻结积分）
    rpc PreAuthorize(PreAuthRequest) returns (PreAuthResponse);

    // 结算（LLM 调用完成后，按真实用量扣费）
    rpc Settle(SettleRequest) returns (SettleResponse);

    // 充值（当前 mock，后续接入支付）
    rpc Recharge(RechargeRequest) returns (TransactionResponse);
}

// user.proto（User Service 独立后启用）
service UserService {
    rpc GetUser(GetUserRequest) returns (UserResponse);
    rpc ValidateCredentials(ValidateRequest) returns (UserResponse);
}
```

### 8.2 异步通信：Redis Streams

Redis Streams 选择理由：
- 项目已引入 Redis（限流 + Key 池），不增加新基础设施
- 支持 Consumer Group + ACK 语义（与 Kafka 类似，但更轻量）
- 消息持久化（AOF 开启后）
- 未来流量增长到需要 Kafka 时，Producer/Consumer 接口不变，只换实现

```
Stream 名称                  生产者             消费者              用途
───────────────────────────────────────────────────────────────────────────
stream:usage_events          LLM Gateway        Billing Service     用量统计分析
stream:balance_alerts        Billing Service    Resume Agent        余额变更通知
stream:memory_jobs           Resume Agent       Memory Worker       异步记忆提取
```

**可靠消费（Consumer Group 模式）**：

```go
// Consumer Group 保证：
// 1. 消息只被一个 Consumer 处理（多个 Worker 不会重复消费）
// 2. 处理完成后 XACK，未 ACK 的消息会在超时后重新投递
// 3. 支持 Pending 列表查询（排查堆积问题）

// Worker 启动
redis.XGroupCreateMkStream("stream:usage_events", "billing-group", "0")

// 消费循环
for {
    messages := redis.XReadGroup(&XReadGroupArgs{
        Group:    "billing-group",
        Consumer: "billing-worker-1",
        Streams:  []string{"stream:usage_events", ">"},
        Count:    10,
        Block:    5 * time.Second,
    })

    for _, msg := range messages {
        if err := processUsageEvent(msg); err == nil {
            redis.XAck("stream:usage_events", "billing-group", msg.ID)
        }
        // 处理失败：不 ACK，等待重新投递
        // 连续失败 N 次：移入 Dead Letter Queue
    }
}
```

---

## 9. 高并发设计

### 9.1 LLM Key 调度：ZPOPMIN 无锁并发

```
问题：100 个并发请求同时选 Key，如何保证不冲突？

方案：Redis ZPOPMIN 是原子操作
  100 个并发调用 ZPOPMIN，Redis 单线程逐个处理，
  每个请求拿到不同的 key，天然互斥，无需分布式锁。

吞吐：Redis 单机 100K+ ops/s
  每次请求 2 次 Redis ops（ZPOPMIN + ZADD）
  → 支撑 50K+ 并发 Key 调度/s，远超实际需求
```

### 9.2 积分扣减：Lua 脚本原子 CAS

```
问题：同一用户同时发起多个对话，并发扣减如何保证余额不为负？

方案：Redis 单线程 + Lua 脚本
  无论多少并发，Redis 都是串行执行 Lua 脚本，
  每次 Lua 执行都看到最新的 balance 和 reserved，
  不可能出现两个 Lua 脚本同时读到 "balance=50" 然后都扣 30 的情况。

吞吐：单个 Lua 脚本（3-4 条命令）约 40K ops/s
  按每用户每分钟 5 次对话（高频场景），
  → 单 Redis 实例支撑约 24 万 DAU 的并发扣减
  → 超过后：Redis Cluster 水平扩展（按 user_id hash 分片）
```

### 9.3 SSE 长连接：Goroutine 池 + Context 取消链

```go
// Semaphore 控制最大并发 LLM 调用数
var llmSemaphore = make(chan struct{}, MaxConcurrentLLMCalls) // 如 200

func (s *Server) Chat(c *gin.Context) {
    // 非阻塞抢占槽位
    select {
    case llmSemaphore <- struct{}{}:
        defer func() { <-llmSemaphore }()
    default:
        c.JSON(429, gin.H{"error": "server_busy", "retry_after": 5})
        return
    }

    // Context 取消链：用户关闭浏览器 → c.Request.Context() 自动取消
    // → 传递给 LLM 调用 → HTTP 请求中断 → Goroutine 自然退出
    // 不需要任何额外的清理代码
    ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
    defer cancel()

    // ... SSE 处理逻辑
}
```

### 9.4 APISIX 限流：limit-count 插件

APISIX `limit-count` 插件内置 Redis 滑动窗口实现，无需自己编写限流代码。
`key: consumer_name` 实现按用户 ID 维度限流，精确控制每个用户的请求速率。
详见 §3.5。

---

## 10. 数据一致性保障

### 10.1 Redis 与 DB 的一致性模型

这套设计是**最终一致性**，不是强一致性。核心权衡：用微秒级的不一致窗口，换取高性能的实时扣减能力。

```
Redis（热路径）                     DB（冷路径，Source of Truth）
──────────────────────────────────────────────────────────────────
credit:{user_id}.balance            billing_schema.user_credits.balance
credit:{user_id}.reserved           （无对应字段，reserved 是瞬态）

正常情况：
  Redis.balance ≈ DB.balance（差异 < 1s）
  SUM(credit_transactions.amount) = DB.user_credits.balance（强一致）

一致性对账公式：
  ① Redis.balance = DB.user_credits.balance（允许 <1s 延迟）
  ② DB.user_credits.balance = SUM(credit_transactions.amount)（必须严格一致）
  ② 不一致 → 数据异常，立即告警
```

### 10.2 故障场景分析与应对

**场景 1：Settlement Lua 成功，DB 写失败**

```
状态：Redis 已扣款，DB 无记录 → Redis 与 DB 不一致

应对：
  Settlement 完成后，往 Redis List（pending_settlements）推任务：
    LPUSH pending_settlements {user_id, estimated, actual, request_id, timestamp}

  后台 Worker 消费这个 List，写 DB（幂等：idempotency_key=request_id）：
    INSERT credit_transactions ON CONFLICT(idempotency_key) DO NOTHING
    UPDATE user_credits SET balance = balance - actual_cost WHERE ...

  DB 写失败：指数退避重试（1s, 2s, 4s, 8s...）
  连续失败 10 次：告警，人工介入

最坏结果：DB 延迟几秒同步，账单最终一致
```

**场景 2：进程崩溃，Redis 数据完好，DB 未写**

```
应对：Redis List pending_settlements 持久化（AOF）
  进程重启后，Worker 自动恢复消费未处理的 DB 写任务
  幂等保护确保不会重复写入
```

**场景 3：Redis 宕机，数据丢失**

```
应对：
  必须开启 Redis AOF 持久化：
    appendonly yes
    appendfsync everysec  （每秒 fsync，平衡性能与数据安全）

  Redis 重启后如果 credit:{user_id} key 不存在（Cache Miss）：
    从 DB 重新计算：balance = user_credits.balance
    重建 Hash：HSET credit:{user_id} balance {db_balance} reserved 0
    注意：reserved 重置为 0（进行中的 Pre-Auth 会因 Settlement 超时而退款）
```

**场景 4：Pre-Auth 成功，但 LLM 调用超时/失败，Settlement 未执行**

```
问题：reserved 一直不释放，用户可用余额被永久占用

应对：Pre-Auth 时记录冻结时间戳
  HSET credit:{user_id}:reserve:{request_id} estimated_cost timestamp

  后台扫描任务（每5分钟）：
    扫描所有超时（>10分钟）未 settled 的 reserve 记录
    自动执行 Settlement(estimated_cost=X, actual_cost=0)（全额退款）
    清理 reserve 记录
```

### 10.3 定期对账任务

```
执行频率：每小时一次（低峰期）

对账逻辑：
  1. 取所有 user_ids
  2. 对每个用户：
     db_balance  = SELECT balance FROM user_credits WHERE user_id = ?
     tx_sum      = SELECT SUM(amount) FROM credit_transactions WHERE user_id = ?
     redis_balance = HGET credit:{user_id} balance

     if tx_sum != db_balance:
         严重告警（数据损坏，人工介入）
     if abs(redis_balance - db_balance) > 100:  // 允许 100 积分以内的瞬态差异
         告警 + 用 DB 覆盖 Redis

  3. 记录对账结果到日志（用于审计）
```

---

## 11. 部署架构与演进

### 11.1 当前阶段：Docker Compose 单机

```yaml
# docker-compose.yml（服务间用 Docker 内网 DNS 通信）

services:
  etcd:
    image: bitnami/etcd:3.5
    environment:
      - ALLOW_NONE_AUTHENTICATION=yes
      - ETCD_ADVERTISE_CLIENT_URLS=http://etcd:2379
    volumes: ["etcd_data:/bitnami/etcd"]

  apisix:
    image: apache/apisix:3.8.0-debian
    ports:
      - "80:9080"    # HTTP 入口（开发期）
      - "443:9443"   # HTTPS 入口（生产期）
      - "9180:9180"  # Admin API（内网访问，生产环境不对外暴露）
    volumes:
      - "./apisix/config.yaml:/usr/local/apisix/conf/config.yaml"
    depends_on: [etcd, redis]

  nginx-static:
    image: nginx:alpine
    volumes: ["./frontend/dist:/usr/share/nginx/html"]
    # 只服务静态文件，不对外暴露，由 APISIX 代理

  resume-agent:
    build: ./services/resume-agent
    environment:
      - DATABASE_URL=postgres://resume_user:${DB_PASS}@postgres:5432/tadpoles?search_path=resume_schema
      - LLM_GATEWAY_URL=http://llm-gateway:8083
      - BILLING_GRPC_URL=billing-service:9084
      - JWT_SECRET=${JWT_SECRET}
      - REDIS_URL=redis://redis:6379
    depends_on: [postgres, redis, llm-gateway, billing-service]

  llm-gateway:
    build: ./services/llm-gateway
    environment:
      - DATABASE_URL=postgres://gateway_user:${DB_PASS}@postgres:5432/tadpoles?search_path=gateway_schema
      - REDIS_URL=redis://redis:6379
      - ENCRYPTION_KEY=${ENCRYPTION_KEY}   # API Key 加密密钥
    depends_on: [postgres, redis, fake-llm]

  fake-llm:
    build: ./services/fake-llm
    ports: ["8099:8099"]
    environment:
      - PORT=8099
      - FAKE_RESPONSE_DELAY_MS=200    # 模拟厂商网络延迟，测 TTFT 用
      - FAKE_ERROR_RATE=0             # 0~1，注入错误比例，测熔断/重试用

  billing-service:
    build: ./services/billing-service
    environment:
      - DATABASE_URL=postgres://billing_user:${DB_PASS}@postgres:5432/tadpoles?search_path=billing_schema
      - REDIS_URL=redis://redis:6379
    ports: ["9084:9084"]   # gRPC port
    depends_on: [postgres, redis]

  mysql:
    image: mysql:8.4
    environment:
      - MYSQL_ROOT_PASSWORD=${MYSQL_ROOT_PASS}
      - MYSQL_CHARACTER_SET_SERVER=utf8mb4
      - MYSQL_COLLATION_SERVER=utf8mb4_unicode_ci
    command: >
      --default-authentication-plugin=caching_sha2_password
      --innodb-buffer-pool-size=512M
      --slow-query-log=1
      --long-query-time=1
    volumes: ["mysqldata:/var/lib/mysql", "./mysql/init:/docker-entrypoint-initdb.d"]
    # init/ 目录下放各 DB 和用户的初始化 SQL

  redis:
    image: redis:7-alpine
    command: >
      redis-server
      --appendonly yes
      --appendfsync everysec
      --maxmemory 512mb
      --maxmemory-policy allkeys-lru
    volumes: ["redisdata:/data"]

volumes:
  mysqldata:
  redisdata:
  etcd_data:
```

### 11.2 Fake LLM Server（开发 / 测试专用）

开发和测试阶段不依赖真实厂商 API Key，通过 Fake LLM Server 模拟 LLM 厂商，实现全链路联调。

**设计原则**：Gateway 不感知 fake，只需在 `providers` 表中插入一条记录，`base_url` 指向 Fake Server 即可。Gateway 正常走调度、熔断、计费全流程。

**初始化 SQL（开发环境）**

```sql
-- 注册 fake 厂商
INSERT INTO providers (id, name, base_url, priority, billing_currency) VALUES
('fake', 'Fake LLM', 'http://fake-llm:8099/v1', 99, 'CNY');

-- 注册 fake API Key（明文随意，Gateway 加密后存储）
-- 通过 Admin API POST /admin/v1/keys 写入，api_key 填 "sk-fake-key-for-dev"

-- 注册 fake 模型（复用真实模型 ID，覆盖 provider_id）
INSERT INTO model_configs (id, provider_id, provider_model, display_name,
    context_window, max_output_tokens, input_credits_per_1k, output_credits_per_1k)
VALUES
('deepseek-chat', 'fake', 'fake-chat', 'Fake Chat (Dev)', 128000, 4096, 5, 20),
('gpt-4o',        'fake', 'fake-gpt4', 'Fake GPT-4o (Dev)', 128000, 4096, 25, 100);
```

**Fake Server 实现**（`services/fake-llm/`，Go，~150 行）

```
实现接口：
  POST /v1/chat/completions   支持 stream=true / stream=false
  POST /v1/embeddings
  GET  /v1/models

行为配置（环境变量）：
  FAKE_RESPONSE_DELAY_MS   整体响应延迟，默认 200ms（测 latency_ms）
  FAKE_TTFT_DELAY_MS       首 token 延迟，默认 80ms（测 ttft_ms），仅流式生效
  FAKE_ERROR_RATE          0.0~1.0，随机注入 500 错误，默认 0（测熔断/重试）
  FAKE_RATE_LIMIT_RATE     0.0~1.0，随机返回 429，默认 0（测限流冷却）
  FAKE_TOKENS_PER_REPLY    模拟输出 token 数，默认 100

非流式响应格式（标准 OpenAI 格式）：
{
  "id": "fake-{uuid}",
  "object": "chat.completion",
  "model": "{request.model}",
  "choices": [{
    "index": 0,
    "message": { "role": "assistant", "content": "这是一个 Fake 响应，用于开发测试。" },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": {按请求消息字符数/4估算},
    "completion_tokens": {FAKE_TOKENS_PER_REPLY},
    "total_tokens": {两者之和}
  }
}

流式响应格式（标准 SSE）：
  data: {"id":"fake-xxx","choices":[{"delta":{"role":"assistant"},"index":0}]}
  （等待 FAKE_TTFT_DELAY_MS）
  data: {"id":"fake-xxx","choices":[{"delta":{"content":"这是"},"index":0}]}
  data: {"id":"fake-xxx","choices":[{"delta":{"content":"一个 Fake 响应"},"index":0}]}
  data: {"id":"fake-xxx","choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{...}}
  data: [DONE]
```

**典型测试场景**

| 场景 | 环境变量配置 |
|------|-------------|
| 正常全链路联调 | 默认配置 |
| 测试熔断触发 | `FAKE_ERROR_RATE=0.8`，连续请求触发 consecutive_errors ≥ 5 |
| 测试限流冷却 | `FAKE_RATE_LIMIT_RATE=1.0`，Key 进入 60s 冷却 |
| 测试 TTFT 监控 | `FAKE_TTFT_DELAY_MS=2000`，验证 ttft_ms 字段记录正确 |
| 测试积分不足 | 正常配置，通过 Billing Admin API 将用户积分清零 |

---

### 11.3 Kubernetes 演进路径

```
阶段 1（当前，0-1万 DAU）:
  Docker Compose 单机，所有服务同一台机器
  APISIX + etcd 作为网关层
  预计支撑：< 1K 并发

阶段 2（1-10万 DAU）:
  迁移到 K8s，APISIX 替换为 APISIX Ingress Controller
  路由配置从 Admin API JSON → K8s CRD（ApisixRoute）声明式管理
  LLM Gateway + Resume Agent HPA 水平扩展（无状态服务）
  Billing Service 多副本（幂等保证正确性）
  MySQL 主从，读写分离（binlog replication）
  Redis Sentinel（主从 + 哨兵，高可用）

阶段 3（10-100万 DAU）:
  MySQL 按 Database 拆分为独立实例
  Redis Cluster（按 user_id hash 分片）
  APISIX 多节点集群（配置共享 etcd，扩容只需加节点）
  引入 Kafka 替换 Redis Streams

迁移成本：
  服务地址从 "http://llm-gateway:8083" 改为 K8s Service 地址
  已抽离到环境变量，代码零改动
  DB 连接串中的 Database 名在独立实例阶段替换为独立实例 URL
```

---

## 12. 可观测性

可观测性三支柱：**Traces（链路追踪）、Metrics（指标）、Logs（日志）**，统一使用 OpenTelemetry SDK 采集，通过 OTEL Collector 分发到各后端。所有观测能力设计为**可开关的**，本地开发时全部关闭，生产环境按需开启。

### 12.1 整体架构

```
各业务服务（Go OTEL SDK）
  │  Traces ──► OTLP/gRPC
  │  Metrics ──► OTLP/gRPC 或 Prometheus /metrics 端点
  │  Logs ──► 结构化 JSON stdout
  ▼
OTEL Collector（可选，统一转发）
  ├── Traces ──► Jaeger / Tempo（链路存储）
  ├── Metrics ──► Prometheus（指标存储）
  └── Logs ──► Loki / ELK（日志存储）

Grafana（统一可视化：Traces + Metrics + Logs 联动）
```

### 12.2 开关设计（环境变量驱动）

所有观测组件通过环境变量控制，**代码中只判断 interface，不判断具体实现**：

```bash
# 可观测性总开关
OTEL_ENABLED=false            # 本地开发：关闭所有追踪和指标
OTEL_ENABLED=true             # 生产环境：开启

# 分项控制
OTEL_TRACES_ENABLED=true      # 是否启用分布式追踪
OTEL_METRICS_ENABLED=true     # 是否启用指标采集
OTEL_EXPORTER=otlp            # otlp | stdout | noop
OTEL_ENDPOINT=http://otel-collector:4317

# Prometheus 独立开关（可以只开 Prometheus，不开 Jaeger）
PROMETHEUS_ENABLED=true
PROMETHEUS_PORT=9090           # /metrics 端点暴露端口

# 日志级别
LOG_LEVEL=info                 # debug | info | warn | error
LOG_FORMAT=json                # json | text（本地开发用 text 更易读）
```

### 12.3 Go 实现：Provider 模式

```go
// internal/observability/provider.go
// 通过 interface 隔离，切换实现不改业务代码

type TracerProvider interface {
    Tracer(name string) trace.Tracer
}

type MeterProvider interface {
    Meter(name string) metric.Meter
}

// 工厂函数：根据配置返回真实实现或 Noop
func NewTracerProvider(cfg Config) (TracerProvider, func(), error) {
    if !cfg.OTELEnabled || !cfg.TracesEnabled {
        // 关闭时返回 Noop，业务代码完全不感知
        return noop.NewTracerProvider(), func() {}, nil
    }

    exporter, err := otlptraceGrpc.New(ctx,
        otlptraceGrpc.WithEndpoint(cfg.OTELEndpoint),
        otlptraceGrpc.WithInsecure(),
    )
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(newResource(cfg.ServiceName)),
    )
    cleanup := func() { tp.Shutdown(context.Background()) }
    return tp, cleanup, nil
}

func NewMeterProvider(cfg Config) (MeterProvider, func(), error) {
    if !cfg.OTELEnabled || !cfg.MetricsEnabled {
        return noop.NewMeterProvider(), func() {}, nil
    }

    if cfg.PrometheusEnabled {
        // Prometheus pull 模式：暴露 /metrics HTTP 端点
        exporter, _ := prometheus.New()
        mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
        go serveMetrics(cfg.PrometheusPort) // 启动 /metrics HTTP server
        return mp, func() { mp.Shutdown(context.Background()) }, nil
    }

    // OTLP push 模式：推送到 OTEL Collector
    exporter, _ := otlpmetricgrpc.New(ctx,
        otlpmetricgrpc.WithEndpoint(cfg.OTELEndpoint),
    )
    mp := sdkmetric.NewMeterProvider(
        sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
    )
    return mp, func() { mp.Shutdown(context.Background()) }, nil
}
```

### 12.4 关键业务指标（Metrics）

```go
// 各服务注册的业务指标

// LLM Gateway
llm_request_total          Counter   {provider, model, status}    // LLM 请求总数
llm_request_duration_ms    Histogram {provider, model}            // 请求耗时分布
llm_tokens_total           Counter   {provider, model, type}      // token 消耗量
llm_key_circuit_open       Gauge     {provider, key_prefix}       // 熔断中的 key 数

// Billing Service
billing_preauth_total      Counter   {result}                     // pre-auth 成功/失败
billing_settle_total       Counter   {status}                     // settlement 数量
billing_credits_deducted   Counter   {}                           // 总扣减积分数
billing_balance_redis_lag  Histogram {}                           // Redis 与 DB 余额差值

// Resume Agent
chat_request_total         Counter   {status}                     // 对话请求数
chat_duration_ms           Histogram {}                           // 端到端对话耗时
memory_extract_duration_ms Histogram {}                           // 记忆提取耗时
```

### 12.5 分布式追踪（Traces）

每个请求自动生成 trace，跨服务调用通过 HTTP Header 传播上下文：

```
APISIX 入口
  │  生成 trace_id，注入 traceparent Header（W3C Trace Context 标准）
  ▼
Resume Agent（Span: handle_chat）
  ├── Span: billing.pre_auth         ← gRPC 调用 Billing
  ├── Span: load_memory_facts        ← DB 查询
  ├── Span: llm_gateway.completion   ← HTTP 调用 LLM Gateway
  │     └── Span: openai.request     ← 真实 LLM 调用
  ├── Span: update_resume            ← DB 写入
  └── Span: billing.settle           ← gRPC 调用 Billing
```

Gin 框架接入（一行代码）：

```go
// 使用 otelgin 中间件，自动为所有 HTTP 请求创建 Span
router.Use(otelgin.Middleware(serviceName))

// GORM 接入（自动追踪所有 DB 查询）
db.Use(otelgorm.NewPlugin())
```

### 12.6 结构化日志格式

```json
{
  "timestamp": "2026-04-09T10:23:45.123Z",
  "level":     "info",
  "service":   "resume-agent",
  "trace_id":  "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id":   "00f067aa0ba902b7",
  "user_id":   "user-uuid",
  "action":    "chat_complete",
  "duration_ms": 1247,
  "llm": {
    "model":             "claude-3-5-sonnet",
    "provider":          "anthropic",
    "prompt_tokens":     450,
    "completion_tokens": 230,
    "latency_ms":        1180
  },
  "billing": {
    "pre_auth_credits": 12,
    "settled_credits":  8,
    "balance_after":    237
  },
  "http_status": 200
}
```

`trace_id` 与 Jaeger/Grafana Tempo 关联，可从日志直接跳转到完整调用链。

### 12.7 Docker Compose 可观测栈（可选）

```yaml
# 本地开发时完全不需要，生产环境按需开启

  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.96.0
    volumes: ["./otel/config.yaml:/etc/otelcol-contrib/config.yaml"]
    ports: ["4317:4317"]   # OTLP gRPC

  prometheus:
    image: prom/prometheus:v2.50.0
    volumes: ["./prometheus/prometheus.yml:/etc/prometheus/prometheus.yml"]
    ports: ["9090:9090"]

  jaeger:
    image: jaegertracing/all-in-one:1.55
    ports: ["16686:16686"]  # Jaeger UI

  grafana:
    image: grafana/grafana:10.3.0
    ports: ["3000:3000"]
    volumes: ["./grafana/provisioning:/etc/grafana/provisioning"]
    # 预置 Dashboard：LLM Gateway 吞吐、Billing 余额趋势、服务 P99 延迟
```

---

## 14. Interface 设计与可测试性

### 14.1 设计原则

**凡是有外部依赖、有副作用、有可替换实现的地方，都必须抽象为 Interface。**

两个收益：
1. **单元测试**：用内存实现替换真实 DB / Redis / LLM，测试不依赖任何外部服务
2. **功能开关**：注入 Nop 实现即可关闭某个功能，无需改业务逻辑

**统一工厂函数模式**：所有 interface 实现通过工厂函数根据配置/环境变量注入，业务代码只依赖 interface，不 import 具体实现包。

```go
// 统一模式：工厂函数根据环境变量返回对应实现
func NewXxx(cfg Config) XxxInterface {
    switch cfg.XxxProvider {
    case "real":  return NewRealXxx(cfg)
    case "fake":  return NewFakeXxx(cfg)
    case "nop":   return NewNopXxx()
    default:      panic("unknown provider: " + cfg.XxxProvider)
    }
}
```

---

### 14.2 Interface 全景表

| Interface | 所在服务 | 真实实现 | Fake / Nop 实现 | 开关环境变量 |
|-----------|---------|---------|----------------|------------|
| `LLMClient` | Resume Agent | `GatewayLLMClient` | `FakeLLMClient`（可配置延迟/错误） | `LLM_PROVIDER` |
| `KeyPool` | LLM Gateway | `RedisKeyPool` | `FakeKeyPool`（预置故障场景） | `KEY_POOL_PROVIDER` |
| `BillingClient` | Resume Agent | `GrpcBillingClient` | `FreeBillingClient`（永远返回余额充足） | `BILLING_PROVIDER` |
| `ResumeStore` | Resume Agent | `MySQLResumeStore` | `InMemoryResumeStore` | - |
| `UserStore` | Resume Agent | `MySQLUserStore` | `InMemoryUserStore` | - |
| `MessageStore` | Resume Agent | `MySQLMessageStore` | `InMemoryMessageStore` | - |
| `MemoryStore` | Resume Agent | `MySQLMemoryStore` | `InMemoryMemoryStore` | - |
| `APIKeyStore` | LLM Gateway | `MySQLAPIKeyStore` | `InMemoryAPIKeyStore` | - |
| `CreditStore` | Billing | `MySQLCreditStore` | `InMemoryCreditStore` | - |
| `CacheClient` | 所有服务 | `RedisCacheClient` | `InMemoryCacheClient` (miniredis) | `CACHE_PROVIDER` |
| `EventPublisher` | LLM Gateway | `RedisStreamPublisher` | `InMemoryPublisher` / `NopPublisher` | `EVENT_PROVIDER` |
| `EventConsumer` | Billing | `RedisStreamConsumer` | `InMemoryConsumer` | `EVENT_PROVIDER` |
| `PDFExporter` | Resume Agent | `ChromedpExporter` | `MockPDFExporter`（返回空 PDF） | `PDF_PROVIDER` |
| `FileStorage` | Resume Agent | `LocalFileStorage` | `InMemoryFileStorage` | `STORAGE_PROVIDER` |
| `APISIXClient` | Resume Agent | `HttpAPISIXClient` | `NopAPISIXClient`（注册直接返回成功） | `APISIX_PROVIDER` |
| `MemoryExtractor` | Resume Agent | `LLMMemoryExtractor` | `NopMemoryExtractor` | `MEMORY_EXTRACT_ENABLED` |
| `EmailSender` | Resume Agent | `SMTPEmailSender` | `ConsoleEmailSender`（打印到日志） | `EMAIL_PROVIDER` |

---

### 14.3 LLM Client Interface

```go
// internal/llm/client.go
type LLMClient interface {
    ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// 真实实现：调用 LLM Gateway OpenAI 兼容接口
type GatewayLLMClient struct { baseURL, serviceToken string; http *http.Client }

// Fake 实现：可配置延迟和错误注入，用于开发/压测/熔断测试
type FakeLLMClient struct {
    MinLatency time.Duration   // 首 token 延迟下限（模拟网络）
    MaxLatency time.Duration   // 首 token 延迟上限
    ErrorRate  float64         // 错误概率 0.0~1.0
    ErrorType  FakeErrorType   // rate_limit | server_error | timeout | network
    MinTokens  int             // 模拟 completion tokens 下限
    MaxTokens  int             // 模拟 completion tokens 上限
}

type FakeErrorType string
const (
    ErrRateLimit   FakeErrorType = "rate_limit"   // 429，触发 Key 冷却
    ErrServerError FakeErrorType = "server_error" // 500，计入连续错误触发熔断
    ErrTimeout     FakeErrorType = "timeout"      // 请求超时
    ErrNetworkFail FakeErrorType = "network"      // 网络中断
)
```

### 14.4 Fake Key Pool（测试 Key 调度与熔断）

```go
// internal/llm/fake_key_pool.go
// 预置测试场景，通过 FAKE_KEY_SCENARIO 环境变量加载

var Scenarios = map[string][]FakeKeyConfig{
    // 正常并发压测：快/慢/偶发限流 三种 key
    "concurrent_stress": {
        {ID: "key-1", Alias: "fast",  MinMs: 100,  MaxMs: 300,  ErrorRate: 0.00},
        {ID: "key-2", Alias: "slow",  MinMs: 800,  MaxMs: 2000, ErrorRate: 0.05, ErrType: ErrServerError},
        {ID: "key-3", Alias: "flaky", MinMs: 200,  MaxMs: 500,  ErrorRate: 0.30, ErrType: ErrRateLimit},
    },
    // 熔断测试：1个正常，1个必然500，1个必然429
    "circuit_breaker": {
        {ID: "key-1", Alias: "healthy",     ErrorRate: 0.00},
        {ID: "key-2", Alias: "always-500",  ErrorRate: 1.00, ErrType: ErrServerError},
        {ID: "key-3", Alias: "always-429",  ErrorRate: 1.00, ErrType: ErrRateLimit},
    },
    // 全部熔断测试：验证降级逻辑
    "all_failing": {
        {ID: "key-1", ErrorRate: 1.0, ErrType: ErrServerError},
        {ID: "key-2", ErrorRate: 1.0, ErrType: ErrServerError},
        {ID: "key-3", ErrorRate: 1.0, ErrType: ErrTimeout},
    },
}
```

### 14.5 Billing Client Interface

```go
// internal/billing/client.go
type BillingClient interface {
    PreAuthorize(ctx context.Context, userID string, estimated int64, reqID string) error
    Settle(ctx context.Context, userID string, estimated, actual int64, meta SettleMeta) error
    GetBalance(ctx context.Context, userID string) (int64, error)
    CreateWallet(ctx context.Context, userID string, giftAmount int64) error
}

// 真实实现：gRPC 调用 Billing Service
type GrpcBillingClient struct { conn *grpc.ClientConn }

// Fake 实现：用于开发环境，余额永远充足，不实际扣减
// 好处：本地开发对话不会消耗积分，也不需要启动 Billing Service
type FreeBillingClient struct {
    Balance int64  // 固定返回此余额，默认 999999
}
func (f *FreeBillingClient) PreAuthorize(_ context.Context, _ string, _ int64, _ string) error {
    return nil  // 永远通过
}
func (f *FreeBillingClient) Settle(_ context.Context, _ string, _, _ int64, _ SettleMeta) error {
    return nil  // 不实际扣减
}
func (f *FreeBillingClient) GetBalance(_ context.Context, _ string) (int64, error) {
    return f.Balance, nil
}
```

### 14.6 Store Interfaces（数据库层）

所有 Store 定义在服务内部，实现与 interface 解耦，单测时注入 InMemory 实现（不需要 MySQL）：

```go
// Resume Agent：internal/store/interfaces.go

type ResumeStore interface {
    GetByID(ctx context.Context, id string) (*Resume, error)
    GetByIDForUser(ctx context.Context, id, userID string) (*Resume, error)
    List(ctx context.Context, userID string, page, size int) ([]*Resume, error)
    Create(ctx context.Context, r *Resume) error
    Update(ctx context.Context, r *Resume) error  // 含乐观锁版本号校验
    Delete(ctx context.Context, id, userID string) error
}

type MessageStore interface {
    GetBySession(ctx context.Context, sessionID string, limit int) ([]*Message, error)
    Create(ctx context.Context, m *Message) error
    GetLastN(ctx context.Context, sessionID string, n int) ([]*Message, error)
}

type MemoryStore interface {
    GetByUser(ctx context.Context, userID string) ([]*MemoryFact, error)
    Upsert(ctx context.Context, userID, category, key, value string, confidence float32) error
    DeleteByUser(ctx context.Context, userID string) error
}

type UserStore interface {
    GetByID(ctx context.Context, id string) (*User, error)
    GetByEmail(ctx context.Context, email string) (*User, error)
    Create(ctx context.Context, u *User) error
    Update(ctx context.Context, u *User) error
}

// InMemory 实现示例（用于单测，线程安全）
type InMemoryResumeStore struct {
    mu      sync.RWMutex
    records map[string]*Resume
}
func (s *InMemoryResumeStore) GetByID(_ context.Context, id string) (*Resume, error) {
    s.mu.RLock(); defer s.mu.RUnlock()
    if r, ok := s.records[id]; ok { return r, nil }
    return nil, ErrNotFound
}
// ... 其余方法类似
```

### 14.7 Cache & Event Interfaces

```go
// CacheClient：屏蔽 Redis 细节，单测用 miniredis 或 InMemory 替换
type CacheClient interface {
    Get(ctx context.Context, key string) (string, error)
    Set(ctx context.Context, key string, val string, ttl time.Duration) error
    HGet(ctx context.Context, key, field string) (string, error)
    HSet(ctx context.Context, key string, values ...interface{}) error
    Eval(ctx context.Context, script *redis.Script, keys []string, args ...interface{}) (interface{}, error)
    Del(ctx context.Context, keys ...string) error
}

// EventPublisher：屏蔽 Redis Streams，单测用 InMemory Channel 替换
type EventPublisher interface {
    Publish(ctx context.Context, stream string, event interface{}) error
}

type EventConsumer interface {
    Subscribe(ctx context.Context, stream, group, consumer string,
              handler func(msg StreamMessage) error) error
}

// NopPublisher：关闭时使用，静默丢弃所有事件
type NopPublisher struct{}
func (n *NopPublisher) Publish(_ context.Context, _ string, _ interface{}) error { return nil }

// InMemoryPublisher：单测使用，发布的消息存入 channel 供断言
type InMemoryPublisher struct {
    Messages chan StreamMessage
}
```

### 14.8 其他 Interface

**PDFExporter**（chromedp 很重，测试时用 Mock）：
```go
type PDFExporter interface {
    Export(ctx context.Context, htmlContent string) ([]byte, error)
}
// ChromedpExporter：真实实现，启动 headless Chrome
// MockPDFExporter：返回预先准备好的 PDF 字节，< 1ms，用于测试
type MockPDFExporter struct{ FixedContent []byte }
func (m *MockPDFExporter) Export(_ context.Context, _ string) ([]byte, error) {
    return m.FixedContent, nil
}
```

**FileStorage**（上传简历文件）：
```go
type FileStorage interface {
    Save(ctx context.Context, key string, data []byte) (url string, err error)
    Get(ctx context.Context, key string) ([]byte, error)
    Delete(ctx context.Context, key string) error
}
// LocalFileStorage：存本地磁盘
// OSSFileStorage：阿里云 OSS（将来扩展，不改业务代码）
// InMemoryFileStorage：单测使用，存 map
```

**APISIXClient**（本地开发没有 APISIX 时跳过）：
```go
type APISIXClient interface {
    CreateConsumer(ctx context.Context, userID string) error
    DeleteConsumer(ctx context.Context, userID string) error
}
// HttpAPISIXClient：调用 APISIX Admin API
// NopAPISIXClient：直接返回 nil，本地开发时使用
type NopAPISIXClient struct{}
func (n *NopAPISIXClient) CreateConsumer(_ context.Context, _ string) error { return nil }
func (n *NopAPISIXClient) DeleteConsumer(_ context.Context, _ string) error { return nil }
```

**MemoryExtractor**（异步 LLM 调用，开发时噪音）：
```go
type MemoryExtractor interface {
    Extract(ctx context.Context, userID, sessionID string) error
}
// LLMMemoryExtractor：调用 LLM 从对话中提取记忆事实
// NopMemoryExtractor：直接返回，不做任何事
type NopMemoryExtractor struct{}
func (n *NopMemoryExtractor) Extract(_ context.Context, _, _ string) error { return nil }
```

**EmailSender**（本地开发打日志，不真发邮件）：
```go
type EmailSender interface {
    Send(ctx context.Context, to, subject, body string) error
}
// SMTPEmailSender：真实发邮件
// ConsoleEmailSender：打印到日志，本地开发使用
type ConsoleEmailSender struct{ Logger *slog.Logger }
func (c *ConsoleEmailSender) Send(_ context.Context, to, subject, body string) error {
    c.Logger.Info("📧 [DEV EMAIL]", "to", to, "subject", subject, "body", body)
    return nil
}
```

---

### 14.9 功能开关（Feature Flags）

所有开关通过环境变量配置，服务启动时读取一次，注入对应实现。**代码中不出现 `if cfg.XxxEnabled` 的条件判断**，开关体现在注入的实现类型上。

```bash
# ── LLM 调用 ──────────────────────────────────────────────
LLM_PROVIDER=gateway       # gateway | fake
FAKE_KEY_SCENARIO=concurrent_stress   # fake 时：预置场景名
FAKE_LLM_MIN_LATENCY_MS=200
FAKE_LLM_MAX_LATENCY_MS=1500
FAKE_LLM_ERROR_RATE=0.05

# ── 计费 ──────────────────────────────────────────────────
BILLING_PROVIDER=grpc      # grpc | free（free=本地开发免费模式）

# ── 缓存 ──────────────────────────────────────────────────
CACHE_PROVIDER=redis       # redis | memory（memory=单测模式，无需 Redis）

# ── 消息队列 ───────────────────────────────────────────────
EVENT_PROVIDER=redis       # redis | memory | nop

# ── PDF 导出 ───────────────────────────────────────────────
PDF_PROVIDER=chromedp      # chromedp | mock

# ── 文件存储 ───────────────────────────────────────────────
STORAGE_PROVIDER=local     # local | oss | memory

# ── APISIX 集成 ────────────────────────────────────────────
APISIX_PROVIDER=http       # http | nop（nop=本地无 APISIX 时跳过）

# ── 异步功能开关 ───────────────────────────────────────────
MEMORY_EXTRACT_PROVIDER=llm        # llm | nop（nop=关闭记忆提取）
CONVERSATION_SUMMARY_PROVIDER=llm  # llm | nop（nop=关闭对话压缩）

# ── 邮件 ────────────────────────────────────────────────
EMAIL_PROVIDER=console     # smtp | console（console=打印到日志）

# ── 可观测性（见 §12）──────────────────────────────────────
OTEL_ENABLED=false
PROMETHEUS_ENABLED=false
```

**典型本地开发配置（`.env.local`）**：
```bash
LLM_PROVIDER=fake
FAKE_LLM_MIN_LATENCY_MS=300
FAKE_LLM_MAX_LATENCY_MS=1000
FAKE_LLM_ERROR_RATE=0

BILLING_PROVIDER=free       # 不扣积分
APISIX_PROVIDER=nop         # 不需要启动 APISIX
MEMORY_EXTRACT_PROVIDER=nop # 不做记忆提取，减少日志噪音
EMAIL_PROVIDER=console      # 邮件打日志
OTEL_ENABLED=false          # 不需要 Jaeger/Prometheus

# 只需要 MySQL + Redis 两个真实服务
```

**典型生产配置（`.env.prod`）**：
```bash
LLM_PROVIDER=gateway
BILLING_PROVIDER=grpc
APISIX_PROVIDER=http
MEMORY_EXTRACT_PROVIDER=llm
EMAIL_PROVIDER=smtp
OTEL_ENABLED=true
PROMETHEUS_ENABLED=true
```

---

## 13. 安全规范

### 13.1 强制规则（不可妥协）

| 规则 | 实现 |
|------|------|
| 所有写操作验证资源归属 | Handler 层显式校验 `resource.UserID == jwtUserID` |
| 密码必须哈希存储 | bcrypt，cost=12 |
| Refresh Token 只存哈希 | SHA-256(raw) 存 DB |
| API Key 只存加密值 | AES-256-GCM，密钥来自环境变量 |
| SQL 注入防御 | 全程参数化查询，禁止字符串拼接 |
| 余额扣减必须原子 | Redis Lua 脚本，无法绕过 |
| 跨服务调用必须鉴权 | 内部服务 token（非用户 JWT）放 Header |
| CORS 生产环境收窄 | 只允许前端域名，禁止 `*` |

### 13.2 敏感信息处理

```
环境变量（不进 git）:
  JWT_SECRET        — >= 32 字节随机串
  DATABASE_URL      — 含密码的 DB 连接串
  ENCRYPTION_KEY    — LLM API Key 加密密钥（AES-256，32字节）
  REDIS_URL         — Redis 连接串

日志禁止记录:
  password, token, api_key, credit_card
  用户消息内容（隐私保护）

可以记录:
  user_id, session_id, token_count, latency_ms, status_code
```

### 13.3 统一错误响应格式

```go
// 对外不暴露内部错误细节
type ErrorResponse struct {
    Error   string `json:"error"`    // 机器可读码
    Message string `json:"message"`  // 用户可读描述
}

// 错误码规范
// unauthorized          → 401  未登录或 token 失效
// forbidden             → 403  无权访问此资源
// not_found             → 404  资源不存在
// insufficient_credits  → 402  积分不足
// rate_limited          → 429  请求过于频繁
// server_busy           → 429  并发超限，稍后重试
// internal_error        → 500  服务器内部错误（不暴露原因）
```

---

## 15. 面试技术深度索引

| 话题 | 文档位置 | 核心亮点 |
|------|---------|---------|
| 服务拆分策略 | §2 | 可裂变设计：合并期 → 拆分期零业务代码改动 |
| 网关选型 | §3 | APISIX vs 自研：选成熟方案，用户级限流内置 Redis 原生支持 |
| LLM Key 高并发调度 | §5.4 | Redis ZSET + ZPOPMIN 原子操作，无锁并发 |
| 熔断器状态机 | §5.5 | CLOSED/OPEN/HALF_OPEN 三态，per-key 独立熔断 |
| 积分防超用设计 | §6.5/6.7 | Pre-Auth 冻结 + Settlement 结算，Lua 原子保证 |
| Redis+DB 一致性 | §10 | 最终一致性设计，对账机制，5种故障场景应对 |
| 高并发限流 | §3.5 | APISIX limit-count 插件，按用户 ID 维度，Redis 滑动窗口 |
| SSE 连接管理 | §9.3 | Semaphore 池 + Context 取消链，零泄漏 |
| 异步解耦 | §8.2 | Redis Streams Consumer Group，幂等消费 |
| JWT 安全设计 | §4.2 | Refresh Token 只存哈希，Token 轮换防重放 |
| 可观测性设计 | §12 | OpenTelemetry Provider 模式，环境变量开关，Prometheus/Jaeger 可插拔 |
| Interface + 可测试性 | §14 | Fake LLM Client 模拟延迟/错误/熔断，零成本压测 |
| 部署演进路径 | §11 | Docker Compose → K8s → 分布式，环境变量隔离迁移成本 |
