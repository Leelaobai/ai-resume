# Billing Service — 详细设计文档

> 版本：v1.0
> 最后更新：2026-04-10
> 服务端口：8084
> 数据库：tadpoles_billing（MySQL）
> 依赖服务：User Service（gRPC，双向：注册 Saga + 充值后升级分组）、Redis、MySQL

---

## 目录

1. [服务定位与能力边界](#1-服务定位与能力边界)
2. [对外接口（用户端 HTTP）](#2-对外接口)
3. [Admin 管理接口](#3-admin-管理接口)
4. [内部 gRPC 接口](#4-内部-grpc-接口)
5. [业务模块费用统计契约（可拔插）](#5-业务模块费用统计契约)
6. [数据库设计](#6-数据库设计)
7. [Redis 数据结构](#7-redis-数据结构)
8. [核心流程](#8-核心流程)
9. [对账机制](#9-对账机制)
10. [支付渠道集成（预留）](#10-支付渠道集成预留)
11. [错误码规范](#11-错误码规范)
12. [环境变量配置](#12-环境变量配置)

---

## 1. 服务定位与能力边界

### 1.1 定位

Billing Service 是平台**唯一的积分账户管理服务**，负责用户钱包的创建、余额维护、积分预扣、结算与充值。业务层（Resume Agent、未来其他 Agent）通过 gRPC 与 Billing 交互，**不直接操作任何账户数据**。

```
User Service ──Saga──► Billing Service ◄──gRPC── LLM Gateway
                              │
              ──UpgradeGroup──► User Service（充值后升级分组）
                              │
                    ┌─────────┼─────────┐
                    ▼         ▼         ▼
                wallets  credit_       topup_
                         transactions  orders
                              │
                    ┌─────────┘
                    ▼
              支付渠道（预留：微信/支付宝/Stripe）
```

### 1.2 能力清单

| 能力 | 说明 |
|------|------|
| 钱包管理 | 用户注册时自动创建钱包（Saga 触发），同步发放注册赠送积分 |
| 赠送积分（Grant） | 支持注册/促销/邀请等场景的限时赠送积分，到期自动失效 |
| 余额查询 | 返回可用余额（含赠送积分）、冻结中积分、即将到期赠送信息 |
| 积分预扣（Pre-Auth） | 冻结估算积分，返回 auth_id |
| 积分结算（Settle） | 按实际用量扣除，释放多余冻结 |
| 预扣回滚（Rollback） | LLM 调用失败时释放冻结积分 |
| 积分充值 | 对接支付渠道（当前为 Stub），到账后写入 credit_transactions |
| 流水查询 | 用户查看积分明细，支持分页与类型筛选 |
| 用户维度统计 | 近 N 天总消耗、充值总额 |
| 对账支持 | service_name 字段 + transaction_id 供业务模块交叉核验 |

### 1.3 不负责的边界

- **业务粒度统计**（按简历/按模型）：由各业务模块自己维护，Billing 仅提供 transaction_id 锚点
- **用户认证**：由 APISIX jwt-auth 插件完成，Billing 信任 X-User-Id 请求头
- **LLM 调用定价**：由 LLM Gateway 计算积分用量，Billing 按传入值扣费
- **积分单价换算**：Billing 只认"积分"整数单位，不参与积分与法币的换算逻辑

### 1.4 积分单位约定

- 所有积分数值以 **整数** 存储，单位为"积分"（credits）
- 1 积分 ≈ 0.001 元人民币（最终由产品定价，Billing 不内置此换算）
- 充值金额以 **分（fen）** 存储，避免浮点精度问题

---

## 2. 对外接口

用户端接口通过 APISIX 暴露，需 JWT 认证。APISIX 注入 `X-User-Id` 请求头。

### 2.1 通用请求头

| Header | 说明 |
|--------|------|
| `Authorization` | Bearer JWT（APISIX 验证后透传） |
| `X-User-Id` | APISIX 注入，服务内直接使用 |

### 2.2 查询钱包

```
GET /v1/wallet
```

**响应 200：**

```json
{
  "wallet_id": "01HXXX",
  "balance": 10000,
  "frozen": 350,
  "available": 9650,
  "currency": "credits",
  "grants": [
    {
      "grant_id": "01HGGG",
      "type": "registration",
      "credits": 500,
      "remaining": 320,
      "expires_at": "2026-04-17T00:00:00Z"
    }
  ],
  "stats": {
    "last_30d_used": 2340,
    "last_30d_topup": 5000,
    "total_used": 8200
  }
}
```

| 字段 | 数据来源 | 一致性 |
|------|---------|--------|
| balance | wallets.balance（DB 实时，含赠送积分） | 强一致 |
| frozen | wallets.frozen（DB 实时） | 强一致 |
| available | balance - frozen（计算值） | 强一致 |
| grants | credit_grants 实时查询（未过期且 remaining > 0） | 强一致 |
| stats.last_30d_used | Redis wallet:stats 缓存，Miss 时聚合 credit_transactions | 最终一致，最多 10 分钟延迟 |
| stats.last_30d_topup | 同上 | 最终一致，最多 10 分钟延迟 |
| stats.total_used | wallets.total_used（写时冗余维护） | 强一致 |

> **balance 含义说明**：`wallet.balance` 包含永久积分和赠送积分之和。`grants` 字段展示其中赠送部分的明细和到期时间，便于用户了解哪些积分有有效期。

### 2.3 查询积分流水

```
GET /v1/transactions?type=&page=1&page_size=20&from=&to=
```

| 参数 | 类型 | 说明 |
|------|------|------|
| type | string | 可选：topup / usage / refund / adjustment |
| page | int | 页码，默认 1 |
| page_size | int | 每页条数，默认 20，最大 100 |
| from | string | ISO8601，可选 |
| to | string | ISO8601，可选 |

**响应 200：**

```json
{
  "total": 128,
  "page": 1,
  "page_size": 20,
  "items": [
    {
      "id": "01HYYY",
      "type": "usage",
      "service_name": "resume-agent",
      "amount": -200,
      "balance_after": 9800,
      "description": "AI 优化简历（产品经理）",
      "created_at": "2026-04-10T14:23:00Z"
    },
    {
      "id": "01HZZZ",
      "type": "topup",
      "service_name": null,
      "amount": 5000,
      "balance_after": 10000,
      "description": "微信支付充值",
      "created_at": "2026-04-09T10:00:00Z"
    }
  ]
}
```

> amount 正数为充值/退款，负数为消耗。

### 2.4 发起充值

```
POST /v1/topups
Content-Type: application/json
```

**请求体：**

```json
{
  "credits": 5000,
  "payment_channel": "wechat"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| credits | int | 购买的积分数量（需在预设套餐范围内） |
| payment_channel | string | wechat / alipay / stripe（当前均为 Stub） |

**响应 200：**

```json
{
  "order_id": "01HAAA",
  "credits": 5000,
  "amount_fen": 500,
  "payment_channel": "wechat",
  "payment_url": "https://pay.stub/mock?order=01HAAA",
  "expires_at": "2026-04-10T14:53:00Z"
}
```

> Stub 模式下，`payment_url` 为 Mock 地址，访问后自动触发回调完成充值。

### 2.5 查询充值订单

```
GET /v1/topups?page=1&page_size=20
```

**响应 200：**

```json
{
  "total": 5,
  "items": [
    {
      "order_id": "01HAAA",
      "credits": 5000,
      "amount_fen": 500,
      "payment_channel": "wechat",
      "status": "paid",
      "paid_at": "2026-04-09T10:00:05Z",
      "created_at": "2026-04-09T10:00:00Z"
    }
  ]
}
```

---

## 3. Admin 管理接口

Admin 接口仅对内部运维开放，由 APISIX 限制来源 IP 或通过独立端口暴露（8085）。

### 3.1 手动调整积分

```
POST /admin/v1/adjustments
```

**请求体：**

```json
{
  "user_id": "01HXXX",
  "amount": 1000,
  "description": "活动赠送积分"
}
```

> amount 正为加，负为扣。写入 credit_transactions，type = adjustment。

### 3.2 查询用户钱包（Admin 视角）

```
GET /admin/v1/users/{user_id}/wallet
```

**响应 200：** 同 §2.2，额外包含：

```json
{
  "pending_pre_auths": [
    {
      "auth_id": "01HBBB",
      "service_name": "llm-gateway",
      "frozen_credits": 350,
      "created_at": "2026-04-10T14:22:00Z",
      "expires_at": "2026-04-10T14:32:00Z"
    }
  ]
}
```

### 3.3 手动取消过期预扣（运维用）

```
POST /admin/v1/pre-auths/{auth_id}/cancel
```

> 正常情况由定时任务自动清理过期 pre_auth，此接口用于紧急干预。

### 3.4 充值订单列表

```
GET /admin/v1/topups?user_id=&status=&page=1&page_size=50
```

---

## 4. 内部 gRPC 接口

Billing Service 通过 gRPC 对内部服务（LLM Gateway、User Service）提供接口。

### 4.1 Proto 定义

```protobuf
syntax = "proto3";
package billing.v1;

service BillingService {
  // 用户注册时由 User Service 调用（Saga）
  // 内部自动触发注册赠送积分（由 REGISTRATION_BONUS_CREDITS 配置控制）
  rpc CreateWallet(CreateWalletRequest) returns (CreateWalletResponse);

  // 查询可用余额（Gateway 调用前快速检查）
  rpc GetBalance(GetBalanceRequest) returns (GetBalanceResponse);

  // 预扣积分（LLM 调用前）
  rpc PreAuth(PreAuthRequest) returns (PreAuthResponse);

  // 结算（LLM 调用完成后，按实际用量）
  rpc Settle(SettleRequest) returns (SettleResponse);

  // 回滚预扣（LLM 调用失败时）
  rpc Rollback(RollbackRequest) returns (RollbackResponse);

  // 发放赠送积分（促销/邀请等场景，注册赠送由 CreateWallet 内部处理）
  rpc IssueGrant(IssueGrantRequest) returns (IssueGrantResponse);
}

// ── CreateWallet ──────────────────────────────────
message CreateWalletRequest {
  string user_id = 1;
}
message CreateWalletResponse {
  string wallet_id = 1;
}

// ── GetBalance ────────────────────────────────────
message GetBalanceRequest {
  string user_id = 1;
}
message GetBalanceResponse {
  int64 balance   = 1;  // 总余额
  int64 frozen    = 2;  // 冻结中
  int64 available = 3;  // 可用 = balance - frozen
}

// ── PreAuth ───────────────────────────────────────
message PreAuthRequest {
  string user_id          = 1;
  string service_name     = 2;  // "llm-gateway"
  string request_id       = 3;  // 幂等键，调用方生成
  int64  estimated_credits = 4;
}
message PreAuthResponse {
  string auth_id         = 1;  // pre_auths.id
  int64  frozen_credits  = 2;
}

// ── Settle ────────────────────────────────────────
message SettleRequest {
  string auth_id         = 1;
  int64  actual_credits  = 2;  // 实际消耗，可小于预扣
  string service_name    = 3;  // 写入 credit_transactions.service_name
  string description     = 4;  // 人类可读描述，由调用方提供
  string request_id      = 5;  // 全链路追踪 ID（X-Request-Id），写入 credit_transactions.request_id
}
message SettleResponse {
  string transaction_id  = 1;  // credit_transactions.id，供业务模块存档
}

// ── Rollback ──────────────────────────────────────
message RollbackRequest {
  string auth_id = 1;
}
message RollbackResponse {
  bool success = 1;
}

// ── IssueGrant ────────────────────────────────────
message IssueGrantRequest {
  string user_id   = 1;
  string type      = 2;  // registration / promotion / referral
  int64  credits   = 3;  // 赠送积分数量
  int64  ttl_days  = 4;  // 有效天数，从发放时刻起计算
}
message IssueGrantResponse {
  string grant_id        = 1;  // credit_grants.id
  string transaction_id  = 2;  // 对应的 credit_transactions.id
}
```

### 4.2 gRPC 错误码映射

| gRPC Status | 含义 |
|-------------|------|
| OK | 成功 |
| NOT_FOUND | 钱包不存在 / auth_id 不存在 |
| FAILED_PRECONDITION | 余额不足 |
| ALREADY_EXISTS | request_id 重复（PreAuth 幂等已处理） |
| INVALID_ARGUMENT | 参数非法（如 actual_credits > frozen_credits） |
| INTERNAL | 服务内部错误 |

---

## 5. 业务模块费用统计契约

### 5.1 设计思路

Billing Service 只管用户维度的总账，**业务粒度统计（如按简历）由各业务模块自己维护**。

为了使计费页面可以动态展示各业务的费用统计卡片（后端可拔插），所有业务模块需实现以下统一接口契约。前端或 API Gateway 读取业务服务配置列表，逐个调用，统一渲染。

### 5.2 标准接口定义

```
GET /v1/internal/billing-stats
```

**请求头（由 API Gateway 注入）：**

| Header | 说明 |
|--------|------|
| X-User-Id | 当前用户 ID |
| X-Stats-From | 统计开始时间（ISO8601） |
| X-Stats-To | 统计结束时间（ISO8601） |

**标准响应结构（每个业务模块返回相同 schema）：**

```json
{
  "service_name": "resume-agent",
  "service_label": "简历优化",
  "period_credits_used": 456,
  "item_count": 8,
  "breakdown": [
    {
      "label": "产品经理简历",
      "sub_label": "已优化 3 次",
      "credits": 200
    },
    {
      "label": "Java 工程师简历",
      "sub_label": "已优化 5 次",
      "credits": 256
    }
  ]
}
```

| 字段 | 必填 | 说明 |
|------|------|------|
| service_name | 是 | 与 credit_transactions.service_name 一致 |
| service_label | 是 | 前端展示用的业务名称 |
| period_credits_used | 是 | 时间段内总消耗积分 |
| item_count | 是 | 业务粒度条目数（如简历数） |
| breakdown | 是 | 明细列表，可为空数组 |

### 5.3 注册机制（后端可拔插）

在 API Gateway（APISIX）或统一配置中心维护业务服务注册表：

```yaml
# services.yaml（配置示例，后续可改为服务注册中心）
billing_stats_providers:
  - name: resume-agent
    endpoint: http://resume-agent:8082/v1/internal/billing-stats
  # 新增业务时追加：
  # - name: interview-agent
  #   endpoint: http://interview-agent:8086/v1/internal/billing-stats
```

前端调用时，通过一个聚合接口获取所有业务统计（或由 API Gateway 扇出后聚合返回），无需前端知道各业务服务地址。

---

## 6. 数据库设计

### 6.1 wallets（用户钱包）

```sql
CREATE TABLE wallets (
  id          VARCHAR(36)  NOT NULL COMMENT 'UUID v7',
  user_id     VARCHAR(36)  NOT NULL COMMENT '关联 users.id，唯一',
  balance     BIGINT       NOT NULL DEFAULT 0 COMMENT '总余额（积分），含冻结',
  frozen      BIGINT       NOT NULL DEFAULT 0 COMMENT '预扣冻结中的积分',
  total_used  BIGINT       NOT NULL DEFAULT 0 COMMENT '历史累计消耗积分，写时冗余，Settle 事务内同步维护，无需聚合查询',
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at  DATETIME(3)  NULL COMMENT 'GORM 软删除，正常业务不会触发',

  PRIMARY KEY (id),
  UNIQUE KEY uq_wallets_user_id (user_id),
  CONSTRAINT chk_wallets_balance CHECK (balance >= 0),
  CONSTRAINT chk_wallets_frozen  CHECK (frozen >= 0),
  CONSTRAINT chk_wallets_frozen_lte_balance CHECK (frozen <= balance)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户积分钱包';
```

> **为什么 balance 含 frozen？**
> available = balance - frozen，事务中只需检查 available，不需要维护第三个字段。
>
> **并发安全：悲观锁（SELECT ... FOR UPDATE）**
> Pre-Auth、Settle、Rollback 均使用 `SELECT wallet ... FOR UPDATE` 锁定钱包行后再修改，防止并发超扣。
> 选择悲观锁而非乐观锁（version 字段 + CAS 重试）的原因：积分是资金，超扣的代价远大于锁等待，安全优先。
> 同一用户同时并发打多个 LLM 请求的概率极低，行锁竞争在实际场景中不会成为瓶颈。
> 若未来出现高并发场景（如批量任务），再引入乐观锁或队列串行化。

### 6.2 credit_transactions（积分流水）

```sql
CREATE TABLE credit_transactions (
  id           VARCHAR(36)   NOT NULL COMMENT 'UUID v7',
  user_id      VARCHAR(36)   NOT NULL COMMENT '用户 ID',
  type         ENUM(
                 'topup',       -- 充值
                 'usage',       -- 消耗
                 'refund',      -- 退款
                 'adjustment'   -- 运营手动调整
               ) NOT NULL,
  service_name VARCHAR(64)   NULL COMMENT '来源业务：resume-agent / llm-gateway，充值时为 NULL',
  amount       BIGINT        NOT NULL COMMENT '积分变化量，正=增加，负=减少',
  balance_after BIGINT       NOT NULL COMMENT '交易后余额快照（可审计）',
  description  VARCHAR(255)  NOT NULL DEFAULT '' COMMENT '人类可读描述，由调用方传入',
  status       ENUM('completed','cancelled') NOT NULL DEFAULT 'completed'
                 COMMENT '目前仅 completed；预留 cancelled 用于对账异常修正',
  auth_id      VARCHAR(36)   NULL COMMENT '关联 pre_auths.id（usage 类型有值），纯审计用：追溯该笔扣费来自哪次预扣，不参与业务逻辑',
  request_id   VARCHAR(128)  NULL COMMENT '全链路追踪 ID（即 X-Request-Id），由 LLM Gateway Settle 时传入；可从 Billing 侧直接回溯到原始用户请求，与 llm_gateway.usage_logs.request_id 对应',
  created_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  PRIMARY KEY (id),
  KEY idx_ct_user_created      (user_id, created_at),
  KEY idx_ct_user_type_created (user_id, type, created_at),
  KEY idx_ct_service           (service_name, created_at),
  KEY idx_ct_auth_id           (auth_id),
  KEY idx_ct_request_id        (request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='积分流水，InsertOnly';
```

> 流水表只写不改，历史记录不可变。如需修正，写一条 adjustment 抵消记录。
>
> **索引说明：**
> - `idx_ct_user_created`：无 type 过滤时的分页查询（`WHERE user_id=? ORDER BY created_at DESC`）
> - `idx_ct_user_type_created`：有 type 过滤时的分页查询（`WHERE user_id=? AND type=? AND created_at BETWEEN ? AND ?`），覆盖最左匹配

### 6.3 pre_auths（积分预扣记录）

```sql
CREATE TABLE pre_auths (
  id               VARCHAR(36)  NOT NULL COMMENT 'UUID v7，即 auth_id',
  user_id          VARCHAR(36)  NOT NULL,
  service_name     VARCHAR(64)  NOT NULL COMMENT '发起预扣的服务',
  request_id       VARCHAR(128) NOT NULL COMMENT '幂等键，由调用方提供',
  frozen_credits   BIGINT       NOT NULL COMMENT '冻结的积分数量',
  status           ENUM('pending','settled','cancelled') NOT NULL DEFAULT 'pending',
  created_at       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at       DATETIME(3)  NOT NULL COMMENT '过期时间，默认 created_at + 15min（PREAUTH_TTL_MINUTES）',
  settled_at       DATETIME(3)  NULL,
  transaction_id   VARCHAR(36)  NULL COMMENT '结算后关联的 credit_transactions.id',

  PRIMARY KEY (id),
  UNIQUE KEY uq_pa_request_id     (request_id),
  KEY idx_pa_user_status          (user_id, status),
  KEY idx_pa_status_expires       (status, expires_at)
  -- 清理任务查询：WHERE status='pending' AND expires_at < NOW()
  -- status 等值在左，expires_at 范围在右，符合最左匹配
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='积分预扣记录';
```

### 6.4 topup_orders（充值订单）

```sql
CREATE TABLE topup_orders (
  id                VARCHAR(36)   NOT NULL COMMENT 'UUID v7',
  user_id           VARCHAR(36)   NOT NULL,
  credits           BIGINT        NOT NULL COMMENT '购买的积分数量',
  amount_fen        BIGINT        NOT NULL COMMENT '记账口径金额，单位：分（人民币 CNY），用于财务对账',
  pay_currency      VARCHAR(8)    NOT NULL DEFAULT 'CNY' COMMENT '用户实际支付币种，如 CNY/USD/EUR',
  pay_amount_fen    BIGINT        NOT NULL COMMENT '用户实际支付金额（该币种最小单位，如美分/分）',
  payment_channel   ENUM('wechat','alipay','stripe') NOT NULL,
  payment_order_id  VARCHAR(128)  NULL COMMENT '第三方支付平台的订单号',
  status            ENUM('pending','paid','failed','refunded') NOT NULL DEFAULT 'pending',
  paid_at           DATETIME(3)   NULL,
  transaction_id    VARCHAR(36)   NULL COMMENT '到账后关联的 credit_transactions.id',
  created_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at        DATETIME(3)   NULL COMMENT 'GORM 软删除',

  PRIMARY KEY (id),
  KEY idx_to_user_status    (user_id, status),
  KEY idx_to_payment_order  (payment_order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='充值订单';
```

> **多币种说明：** 微信/支付宝只支持 CNY，`pay_currency='CNY'` 且 `pay_amount_fen=amount_fen`。Stripe 支持用户本地货币支付，`pay_currency` 记录用户实际支付币种（如 USD），`pay_amount_fen` 记录美分数，`amount_fen` 记录 Stripe 结算后的 CNY 等值（由汇率换算写入），作为统一记账口径。

### 6.5 credit_grants（赠送积分包）

```sql
CREATE TABLE credit_grants (
  id             VARCHAR(36)  NOT NULL COMMENT 'UUID v7',
  user_id        VARCHAR(36)  NOT NULL COMMENT '用户 ID',
  type           ENUM(
                   'registration',  -- 注册赠送
                   'promotion',     -- 促销活动
                   'referral'       -- 邀请好友
                 ) NOT NULL,
  credits        BIGINT       NOT NULL COMMENT '初始赠送积分总量',
  remaining      BIGINT       NOT NULL COMMENT '剩余未消耗积分（Settle 时递减）',
  expires_at     DATETIME(3)  NOT NULL COMMENT '过期时间，到期后 remaining 强制清零',
  expired_at     DATETIME(3)  NULL     COMMENT '实际过期处理时间（后台任务写入）；NULL=未过期',
  transaction_id VARCHAR(36)  NULL     COMMENT '发放时对应的 credit_transactions.id（type=adjustment）',
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),

  PRIMARY KEY (id),
  KEY idx_cg_user_active   (user_id, expires_at, remaining),
  KEY idx_cg_expire_job    (expires_at, remaining)
  -- 后台过期任务：WHERE expires_at < NOW() AND remaining > 0 AND expired_at IS NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='赠送积分包，记录各类限时积分的剩余量和到期时间';
```

> **设计说明**：
> - `wallets.balance` 包含赠送积分（发放时 `balance += credits`），是用户实际可用的总积分。
> - `credit_grants.remaining` 追踪赠送积分的剩余量，仅用于到期时计算应扣除多少。
> - Settle 扣款时优先消耗 `remaining`（按 `expires_at ASC`），从根上减少积分因过期被浪费。
> - 过期时：`wallet.balance -= remaining`，写负数 adjustment 交易，`remaining = 0`。

### 6.6 GORM Model 定义

公共基础结构（定义在 `pkg/model/base.go`，各服务共用）：

```go
// Base：有软删除，适用于需要 updated_at 和 deleted_at 的表
type Base struct {
    ID        string         `gorm:"primaryKey;type:varchar(36)"`
    CreatedAt time.Time
    UpdatedAt time.Time
    DeletedAt gorm.DeletedAt `gorm:"index"`
}

// InsertOnlyBase：只写不改，无 updated_at / deleted_at
type InsertOnlyBase struct {
    ID        string    `gorm:"primaryKey;type:varchar(36)"`
    CreatedAt time.Time
}
```

各表 Model：

```go
// wallets：使用 Base（有 updated_at + deleted_at）
type Wallet struct {
    Base
    UserID    string `gorm:"uniqueIndex;type:varchar(36);not null"`
    Balance   int64  `gorm:"not null;default:0"`
    Frozen    int64  `gorm:"not null;default:0"`
    TotalUsed int64  `gorm:"not null;default:0"` // 写时冗余，Settle 事务内顺带维护
}

func (w *Wallet) Available() int64 {
    return w.Balance - w.Frozen
}

// credit_transactions：使用 InsertOnlyBase（只写不改）
type CreditTransaction struct {
    InsertOnlyBase
    UserID       string  `gorm:"index;type:varchar(36);not null"`
    Type         string  `gorm:"type:enum('topup','usage','refund','adjustment');not null"`
    ServiceName  *string `gorm:"type:varchar(64)"`
    Amount       int64   `gorm:"not null"`
    BalanceAfter int64   `gorm:"not null"`
    Description  string  `gorm:"type:varchar(255);not null;default:''"`
    Status       string  `gorm:"type:enum('completed','cancelled');not null;default:'completed'"`
    AuthID       *string `gorm:"type:varchar(36)"`   // 关联 pre_auths.id，纯审计用
    RequestID    *string `gorm:"index;type:varchar(128)"` // 全链路追踪 ID（X-Request-Id），Settle 时由 LLM Gateway 传入
}

// pre_auths：使用 Base（status 会被更新，需要 updated_at；软删除预留）
type PreAuth struct {
    Base
    UserID        string     `gorm:"index;type:varchar(36);not null"`
    ServiceName   string     `gorm:"type:varchar(64);not null"`
    RequestID     string     `gorm:"uniqueIndex;type:varchar(128);not null"`
    FrozenCredits int64      `gorm:"not null"`
    Status        string     `gorm:"type:enum('pending','settled','cancelled');not null;default:'pending'"`
    ExpiresAt     time.Time
    SettledAt     *time.Time
    TransactionID *string    `gorm:"type:varchar(36)"`
}

// credit_grants：使用 Base（remaining 会被更新）
type CreditGrant struct {
    Base
    UserID        string     `gorm:"index;type:varchar(36);not null"`
    Type          string     `gorm:"type:enum('registration','promotion','referral');not null"`
    Credits       int64      `gorm:"not null"`
    Remaining     int64      `gorm:"not null"`
    ExpiresAt     time.Time
    ExpiredAt     *time.Time
    TransactionID *string    `gorm:"type:varchar(36)"`
}

// topup_orders：使用 Base（status 会被更新）
type TopupOrder struct {
    Base
    UserID         string     `gorm:"index;type:varchar(36);not null"`
    Credits        int64      `gorm:"not null"`
    AmountFen      int64      `gorm:"not null"` // 记账口径，CNY 分
    PayCurrency    string     `gorm:"type:varchar(8);not null;default:'CNY'"` // 用户实际支付币种
    PayAmountFen   int64      `gorm:"not null"` // 用户实际支付金额（该币种最小单位）
    PaymentChannel string     `gorm:"type:enum('wechat','alipay','stripe');not null"`
    PaymentOrderID *string    `gorm:"type:varchar(128)"`
    Status         string     `gorm:"type:enum('pending','paid','failed','refunded');not null;default:'pending'"`
    PaidAt         *time.Time
    TransactionID  *string    `gorm:"type:varchar(36)"`
}
```

---

## 7. Redis 数据结构

### 7.1 钱包余额缓存

```
Key:    wallet:balance:{user_id}
Type:   HASH
Fields:
  balance    int64   总余额
  frozen     int64   冻结中
  updated_at int64   Unix 毫秒时间戳
TTL:    300s（5 分钟）
```

**读策略：** Cache Aside
- 读时先查 Redis，Miss 则读 DB，写入 Redis
- 写时先写 DB（事务），成功后 DEL 缓存（而非更新，避免并发脏写）

**为什么 DEL 而不是 SET？**
Pre-Auth、Settle、Rollback 均在 DB 事务内操作 wallet，事务提交后 DEL 缓存，下次读时从 DB 重新加载，保证一致性。

### 7.2 钱包统计缓存（滑动窗口类数据）

```
Key:    wallet:stats:{user_id}
Type:   HASH
Fields:
  last_30d_used    int64   近 30 天消耗积分
  last_30d_topup   int64   近 30 天充值积分
TTL:    10 分钟
```

**读策略：** Cache Aside，Miss 时查 DB 聚合写入，TTL 到期后重算。

**为什么不在写时失效？**
- 每笔消费都 DEL 缓存，则每次打开账单页都要聚合 30 天数据，代价高
- 滑动窗口本身就是近似统计，10 分钟的延迟对用户完全无感知
- 允许缓存自然过期，简单可靠

**total_used 不在此缓存，而是 wallets.total_used 字段实时维护。**

### 7.3 充值回调幂等键

```
Key:    topup:callback:idempotent:{payment_order_id}
Type:   String
Value:  "1"
TTL:    7 days
```

> 支付回调可能重复推送，写入前检查此 Key 是否存在，存在则直接返回 200 而不重复到账。

### 7.4 Pre-Auth 幂等键

```
Key:    preauth:idempotent:{request_id}
Type:   String
Value:  {auth_id}   // 已存在时直接返回该 auth_id
TTL:    15 min
```

> 与 pre_auths.request_id UNIQUE KEY 双保险。Redis 先判断，避免 DB 唯一键冲突的异常日志。

---

## 8. 核心流程

### 8.1 Pre-Auth 预扣流程

```
LLM Gateway                    Billing Service                   DB / Redis
    │                                │                               │
    │── gRPC PreAuth(request_id) ───►│                               │
    │                                │── Check Redis idempotent ────►│
    │                                │◄─ Miss ───────────────────────│
    │                                │                               │
    │                                │── BEGIN TRANSACTION ─────────►│
    │                                │── SELECT wallet FOR UPDATE ──►│
    │                                │◄─ balance, frozen ────────────│
    │                                │                               │
    │                                │  available = balance - frozen  │
    │                                │  if available < estimated:    │
    │◄─ FAILED_PRECONDITION ─────────│    ROLLBACK; return error     │
    │                                │                               │
    │                                │── INSERT pre_auths ──────────►│
    │                                │── UPDATE wallet               │
    │                                │   SET frozen += estimated ───►│
    │                                │── COMMIT ────────────────────►│
    │                                │                               │
    │                                │── SET Redis idempotent ──────►│
    │                                │── DEL wallet cache ──────────►│
    │◄─ PreAuthResponse(auth_id) ────│                               │
```

**关键约束：**
- `SELECT ... FOR UPDATE` 锁定钱包行，防止并发超扣
- Pre-Auth 默认 15 分钟过期（PREAUTH_TTL_MINUTES=15，覆盖 5 轮×120s 最坏情况，见 §8.4）
- `request_id` 使用全链路统一的 `X-Request-Id`：由前端生成，经 APISIX → Resume Agent → LLM Gateway 透传，LLM Gateway 调用 PreAuth 时直接传入，保证可从 pre_auth 追溯到原始用户请求

### 8.2 结算（Settle）流程

```
LLM Gateway                    Billing Service                   DB / Redis
    │                                │                               │
    │── gRPC Settle(auth_id,         │                               │
    │     actual_credits, desc) ────►│                               │
    │                                │── BEGIN TRANSACTION ─────────►│
    │                                │── SELECT pre_auth             │
    │                                │   WHERE id=auth_id            │
    │                                │   AND status='pending'        │
    │                                │   FOR UPDATE ────────────────►│
    │                                │◄─ pre_auth row ───────────────│
    │                                │                               │
    │                                │  if not found / expired:      │
    │◄─ NOT_FOUND ───────────────────│    return error               │
    │                                │                               │
    │                                │── SELECT wallet FOR UPDATE ──►│
    │                                │◄─ balance, frozen ────────────│
    │                                │                               │
    │                                │  // 校验：实际用量不能超预扣  │
    │                                │  if actual > pre_auth.frozen: │
    │◄─ INVALID_ARGUMENT ────────────│    ROLLBACK; return error     │
    │                                │                               │
    │                                │── SELECT credit_grants        │
    │                                │   WHERE user_id=?             │
    │                                │   AND remaining > 0           │
    │                                │   AND expires_at > NOW()      │
    │                                │   ORDER BY expires_at ASC     │
    │                                │   FOR UPDATE ────────────────►│
    │                                │                               │
    │                                │  // 优先消耗即将到期的赠送积分 │
    │                                │  to_deduct = actual           │
    │                                │  for each grant:              │
    │                                │    used = min(remaining,      │
    │                                │               to_deduct)      │
    │                                │    UPDATE credit_grants       │
    │                                │      remaining -= used ───── ►│
    │                                │    to_deduct -= used          │
    │                                │    if to_deduct == 0: break   │
    │                                │  // 剩余从永久余额扣           │
    │                                │                               │
    │                                │  balance_after =              │
    │                                │    wallet.balance - actual    │
    │                                │── UPDATE wallet               │
    │                                │   balance    -= actual        │
    │                                │   frozen     -= frozen_credits│
    │                                │   total_used += actual   ────►│
    │                                │── INSERT credit_transactions  │
    │                                │   amount        = -actual     │
    │                                │   balance_after = balance_after►│
    │                                │── UPDATE pre_auth             │
    │                                │   status='settled' ──────────►│
    │                                │── COMMIT ────────────────────►│
    │                                │── DEL wallet cache ──────────►│
    │◄─ SettleResponse(txn_id) ──────│                               │
```

**LLM Gateway 在收到 transaction_id 后**，将其写入 `usage_logs.billing_transaction_id`，供后续对账使用。

### 8.3 回滚（Rollback）流程

```
LLM Gateway                    Billing Service
    │                                │
    │── gRPC Rollback(auth_id) ─────►│
    │                                │── BEGIN TRANSACTION
    │                                │── SELECT pre_auth FOR UPDATE
    │                                │   if status != 'pending': ROLLBACK; return OK
    │                                │── UPDATE wallet
    │                                │   frozen -= frozen_credits
    │                                │── UPDATE pre_auth status='cancelled'
    │                                │── COMMIT
    │                                │── DEL wallet cache
    │◄─ RollbackResponse(success) ───│
```

> Rollback 是幂等的：pre_auth 已 settled/cancelled 时直接返回 OK，不报错。

### 8.4 过期 Pre-Auth 清理（定时任务）

```
每分钟执行一次：
SELECT * FROM pre_auths
WHERE status = 'pending' AND expires_at < NOW()
LIMIT 100;

FOR EACH expired pre_auth:
  BEGIN TRANSACTION
    -- 先用条件 UPDATE 抢占状态，多实例并发时只有一个实例能成功
    affected = UPDATE pre_auths
               SET status = 'cancelled'
               WHERE id = pre_auth.id AND status = 'pending'

    IF affected == 0:
      ROLLBACK  -- 已被其他实例处理，跳过
      CONTINUE

    -- 只有抢占成功才更新 wallet，防止多实例双重释放 frozen
    UPDATE wallet SET frozen -= frozen_credits WHERE user_id = pre_auth.user_id
  COMMIT
  DEL wallet:balance:{user_id} cache

记录日志：auth_id, user_id, frozen_credits（用于异常排查）
```

> **多实例安全**：`UPDATE pre_auths WHERE status='pending'` 是原子 CAS，多个实例同时扫到同一条记录时，只有一个能 affected=1，其余跳过，从根本上避免 wallet.frozen 被多次释放。

### 8.4.2 赠送积分过期清理（定时任务）

```
每小时执行一次：
SELECT * FROM credit_grants
WHERE expires_at < NOW() AND remaining > 0 AND expired_at IS NULL
LIMIT 100;

FOR EACH expired grant:
  BEGIN TRANSACTION
    -- 先检查冻结竞态：如果扣除赠送积分后 balance < frozen，跳过本轮
    SELECT balance, frozen FROM wallets WHERE user_id = grant.user_id FOR UPDATE
    IF balance - grant.remaining < frozen:
      ROLLBACK   -- 有 in-flight PreAuth，等其 Settle/Rollback 后再清理
      CONTINUE

    -- CAS 抢占，防止多实例重复处理
    affected = UPDATE credit_grants
               SET expired_at = NOW()
               WHERE id = grant.id AND expired_at IS NULL AND remaining > 0

    IF affected == 0:
      ROLLBACK
      CONTINUE

    -- 从 wallet 扣除未消耗的赠送积分
    UPDATE wallet
    SET balance -= grant.remaining
    WHERE user_id = grant.user_id

    -- 写审计流水（负数 adjustment）
    INSERT credit_transactions(
      user_id      = grant.user_id,
      type         = 'adjustment',
      amount       = -grant.remaining,
      balance_after = wallet.balance - grant.remaining,
      description  = '赠送积分过期',
      service_name = NULL
    )

    -- 清零 remaining
    UPDATE credit_grants SET remaining = 0 WHERE id = grant.id
  COMMIT
  DEL wallet:balance:{user_id} cache

记录日志：grant_id, user_id, expired_credits（用于运营分析）
```

> 同样采用 CAS 抢占防多实例竞争，与 Pre-Auth 清理任务模式一致。

### 8.5 充值流程

```
用户                  Billing Service           支付渠道（Stub）
 │                          │                        │
 │── POST /v1/topups ──────►│                        │
 │                          │── 创建 topup_order     │
 │                          │── 调用支付渠道 API ───►│
 │                          │◄─ payment_url ─────────│
 │◄─ {order_id, payment_url}│                        │
 │                          │                        │
 │── 用户完成支付 ──────────────────────────────────►│
 │                          │◄─ 回调通知（async） ───│
 │                          │                        │
 │                          │── 幂等检查①（Redis）   │
 │                          │   key存在 → 直接200    │
 │                          │── 幂等检查②（DB兜底）  │
 │                          │   SELECT topup_order   │
 │                          │   status=paid → 200    │
 │                          │   （应对Redis TTL到期  │
 │                          │    后的重复回调）       │
 │                          │── BEGIN TRANSACTION    │
 │                          │── UPDATE topup_order   │
 │                          │   status='paid'        │
 │                          │── UPDATE wallet        │
 │                          │   balance += credits   │
 │                          │── INSERT credit_       │
 │                          │   transactions         │
 │                          │   type='topup'         │
 │                          │── COMMIT               │
 │                          │── SET Redis幂等键      │
 │                          │── DEL wallet cache     │
 │                          │                        │
 │                          │  （异步，不阻塞回调响应）│
 │                          │── gRPC UserService     │
 │                          │   .UpgradeGroup(       │
 │                          │     user_id,           │
 │                          │     target="default")  │
 │                          │   失败仅 warn log，    │
 │                          │   不影响充值结果        │
 │                          │                        │
 │                          │── 返回 200 给支付渠道  │
```

> **UpgradeGroup 调用策略：** 在事务 COMMIT + 缓存清理完成后，异步（goroutine）调用 `UserService.UpgradeGroup`。失败时仅记录警告日志，**不回滚充值**。用户下次登录或刷新 Token 时即可获得最新 group。
> 
> 若需保证实时性，可改为同步调用（先调 User Service，再返回 200），但增加了对 User Service 的强依赖风险。一期选择异步方案，对用户体验影响微乎其微（最多延迟 1 小时内 Token 轮换后生效）。

### 8.7 赠送积分发放流程

#### 8.7.1 注册赠送（CreateWallet 内部触发）

```
User Service                Billing Service                   DB
    │                             │                            │
    │── gRPC CreateWallet ────────►│                            │
    │                             │── BEGIN TRANSACTION ──────►│
    │                             │── INSERT wallets ─────────►│
    │                             │                            │
    │                             │  if REGISTRATION_BONUS > 0:│
    │                             │── UPDATE wallet            │
    │                             │   balance += BONUS ───────►│
    │                             │── INSERT credit_grants     │
    │                             │   type='registration'      │
    │                             │   credits = BONUS          │
    │                             │   remaining = BONUS        │
    │                             │   expires_at = NOW()+DAYS─►│
    │                             │── INSERT credit_           │
    │                             │   transactions             │
    │                             │   type='adjustment'        │
    │                             │   amount = +BONUS ────────►│
    │                             │── COMMIT ─────────────────►│
    │◄── CreateWalletResponse ────│                            │
```

> 注册赠送在 `CreateWallet` 同一事务内完成，原子性保证：要么钱包+积分一起创建，要么都不创建。赠送有效期由 `REGISTRATION_BONUS_DAYS` 环境变量控制。

#### 8.7.2 手动/促销赠送（IssueGrant gRPC）

```
Admin / 促销系统            Billing Service
    │                             │
    │── gRPC IssueGrant ──────────►│
    │   (user_id, type,           │── BEGIN TRANSACTION
    │    credits, ttl_days)       │── 查询 wallet 是否存在
    │                             │── UPDATE wallet balance += credits
    │                             │── INSERT credit_grants
    │                             │── INSERT credit_transactions
    │                             │   type='adjustment'
    │                             │   description='促销赠送积分'
    │                             │── COMMIT
    │                             │── DEL wallet cache
    │◄── IssueGrantResponse ──────│
```

---

## 9. 对账机制

### 9.1 对账逻辑

Billing Service 的 `credit_transactions` 记录每一笔扣费的 `service_name`，各业务模块的 `usage_logs` 记录 `billing_transaction_id`。

**验证公式：**

```sql
-- 1. Billing 侧：resume-agent 在某时间段内的总扣费
SELECT ABS(SUM(amount)) AS billing_total
FROM credit_transactions
WHERE user_id = ?
  AND service_name = 'resume-agent'
  AND type = 'usage'
  AND created_at BETWEEN ? AND ?;

-- 2. Resume Agent 侧：同时间段内的总消耗（Resume Agent 自己的 DB）
SELECT SUM(credits_used) AS agent_total
FROM usage_logs
WHERE user_id = ?
  AND created_at BETWEEN ? AND ?;

-- 两者应相等
-- 差异 = 对账异常，排查 billing_transaction_id 为 NULL 的 usage_logs
```

### 9.2 transaction_id 传递链路

```
LLM Gateway usage_logs
  billing_transaction_id ──────────► credit_transactions.id
                                             │
Resume Agent usage_logs                      │
  billing_transaction_id ──────────►─────────┘
```

- LLM Gateway 调用 Billing Settle → 得到 `transaction_id` → 写入 `gateway_usage_logs.billing_transaction_id`
- Resume Agent 调用 LLM Gateway → Gateway 返回 `billing_transaction_id` → Resume Agent 写入自己的 `usage_logs.billing_transaction_id`

### 9.3 异常场景处理

| 异常 | 原因 | 处理 |
|------|------|------|
| `billing_transaction_id` 为 NULL | Settle 失败（网络超时等），Gateway 已记录 usage 但未拿到 txn_id | 告警 + 人工对账，必要时手动写 adjustment |
| Billing 有扣费，业务侧无记录 | Settle 后 Gateway 写 usage_log 失败 | 同上，通过 auth_id 关联 pre_auths 排查 |
| 金额不一致 | actual_credits 计算逻辑 bug | 比对 usage_logs.credits_used 与 credit_transactions.amount |

---

## 10. 支付渠道集成（预留）

当前阶段所有支付渠道均为 **Stub 实现**，接口结构已按真实渠道设计，后续替换实现即可。

### 10.1 渠道接口抽象

```go
type PaymentChannel interface {
    // 创建支付订单，返回支付 URL
    CreateOrder(ctx context.Context, req CreateOrderReq) (CreateOrderResp, error)

    // 验证回调签名（防伪造）
    VerifyCallback(ctx context.Context, payload []byte, signature string) error

    // 解析回调，提取 payment_order_id 和支付状态
    ParseCallback(ctx context.Context, payload []byte) (CallbackData, error)
}

type CreateOrderReq struct {
    OrderID     string  // topup_orders.id
    AmountFen   int64   // 支付金额（分）
    Description string  // 商品描述
    NotifyURL   string  // 回调地址
}

type CreateOrderResp struct {
    PaymentOrderID string  // 第三方订单号
    PaymentURL     string  // 跳转/二维码 URL
}
```

### 10.2 Stub 实现（开发/测试用）

```go
type StubPaymentChannel struct{}

func (s *StubPaymentChannel) CreateOrder(ctx context.Context, req CreateOrderReq) (CreateOrderResp, error) {
    return CreateOrderResp{
        PaymentOrderID: "stub_" + req.OrderID,
        PaymentURL:     "http://localhost:8084/stub/pay?order=" + req.OrderID,
    }, nil
}
```

访问 `GET /stub/pay?order={order_id}` 时，Billing 直接触发内部支付完成逻辑，模拟用户完成支付。

### 10.3 回调端点

```
POST /v1/topups/callback/wechat
POST /v1/topups/callback/alipay
POST /v1/topups/callback/stripe
```

每个渠道独立路由，由对应 Channel 实现解析与验签。

---

## 11. 错误码规范

HTTP 接口遵循 `{"code": "ERR_XXX", "message": "..."}` 格式。

| HTTP 状态 | Code | 说明 |
|-----------|------|------|
| 400 | ERR_INVALID_PARAM | 参数非法（如 credits <= 0） |
| 400 | ERR_INVALID_CHANNEL | 不支持的支付渠道 |
| 404 | ERR_WALLET_NOT_FOUND | 用户钱包不存在（正常不应出现，注册时创建） |
| 404 | ERR_ORDER_NOT_FOUND | 充值订单不存在 |
| 402 | ERR_INSUFFICIENT_CREDITS | 积分不足（HTTP 端快速检查用，gRPC 用 FAILED_PRECONDITION） |
| 409 | ERR_ORDER_ALREADY_PAID | 订单已支付（幂等场景） |
| 500 | ERR_PAYMENT_CREATE_FAILED | 调用支付渠道失败 |
| 500 | ERR_INTERNAL | 内部错误 |

---

## 12. 环境变量配置

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SERVER_PORT` | `8084` | HTTP 服务端口 |
| `GRPC_PORT` | `9094` | gRPC 服务端口 |
| `ADMIN_PORT` | `8085` | Admin HTTP 端口 |
| `DB_DSN` | — | MySQL DSN，`tadpoles_billing` 库 |
| `REDIS_ADDR` | `redis:6379` | Redis 地址 |
| `PREAUTH_TTL_MINUTES` | `15` | Pre-Auth 过期时间（分钟）；需覆盖最坏情况：5轮×120s=600s≈10min，留5min缓冲 |
| `WALLET_CACHE_TTL_SECONDS` | `300` | 余额缓存 TTL（秒） |
| `PAYMENT_CHANNEL` | `stub` | 支付渠道：stub / wechat / alipay / stripe |
| `PAYMENT_NOTIFY_BASE_URL` | — | 支付回调基础 URL（如 `https://api.tadpoles.ai`） |
| `WECHAT_APP_ID` | — | 微信支付 AppID（PAYMENT_CHANNEL=wechat 时必填） |
| `WECHAT_MCH_ID` | — | 微信商户号 |
| `WECHAT_API_KEY` | — | 微信支付 API 密钥 |
| `ALIPAY_APP_ID` | — | 支付宝 AppID（预留） |
| `STRIPE_SECRET_KEY` | — | Stripe 密钥（预留） |
| `USER_SERVICE_GRPC_ADDR` | `user-service:9092` | User Service gRPC 地址（UpgradeGroup 调用） |
| `USER_GRPC_TIMEOUT_MS` | `3000` | UpgradeGroup gRPC 超时（毫秒） |
| `CLEANUP_INTERVAL_SECONDS` | `60` | 过期 Pre-Auth 清理任务间隔（秒） |
| `CLEANUP_BATCH_SIZE` | `100` | 每次清理最大条数 |
| `REGISTRATION_BONUS_CREDITS` | `500` | 新用户注册赠送积分数，0 表示不赠送 |
| `REGISTRATION_BONUS_DAYS` | `7` | 注册赠送积分有效天数 |
| `GRANT_CLEANUP_INTERVAL_SECONDS` | `3600` | 赠送积分过期清理任务间隔（秒） |
| `SHUTDOWN_TIMEOUT_SECONDS` | `15` | 优雅关闭最长等待时间（秒） |

---

## 13. 优雅关闭（Graceful Shutdown）

```
收到 SIGTERM / SIGINT →
  1. 停止接受新请求
     - HTTP listener 停止接受新连接（http.Server.Shutdown）
     - gRPC server 调用 GracefulStop()（等待 in-flight RPC 完成）

  2. 停止定时任务
     - 停止 Pre-Auth 过期清理 ticker
     - 停止赠送积分过期清理 ticker
     - 等待正在执行的清理批次完成（当前事务提交）

  3. 等待 in-flight 请求完成
     - 最长等待 SHUTDOWN_TIMEOUT_SECONDS（默认 15s）
     - 主要等待：正在进行的 PreAuth/Settle/Rollback gRPC 事务

  4. 关闭连接
     - 关闭 DB 连接池
     - 关闭 Redis 连接
     - 退出进程
```

> Billing 的关闭比 Gateway 简单：所有操作都是 DB 事务，要么提交成功要么回滚，不存在中间态。Pre-Auth 过期清理也是幂等的，重启后继续执行即可。

---

## 14. 水平扩展与架构演进（未实现，面试讨论用）

> 当前设计面向中小规模场景（万级用户），以下是用户量增长到百万/千万级时的演进路线。

### 14.1 Billing Service 水平扩展

**当前瓶颈**：`SELECT wallet FOR UPDATE` 行锁 — 同一用户并发请求串行化。

**为什么当前没问题**：单用户同时发多个 LLM 请求的概率极低（正常使用场景是一问一答），行锁竞争在实际场景中不会成为瓶颈。瓶颈更可能出现在 LLM 厂商的响应延迟上。

**演进方案（用户量 > 100万时）**：

| 阶段 | 方案 | 适用规模 |
|------|------|---------|
| 当前 | 单实例 MySQL + 悲观锁 | < 10万用户 |
| 阶段 1 | **多实例无状态部署** + MySQL 主从 | 10万~50万 |
| 阶段 2 | **分库分表（按 user_id 取模）** | 50万~500万 |
| 阶段 3 | **独立账户引擎 + 事件驱动** | > 500万 |

#### 阶段 1：多实例无状态 + 读写分离

```
Billing Service ×3（无状态，Kubernetes Deployment）
       │ 写
       ▼
   MySQL 主库 ────► MySQL 从库（读）
       │
       ▼
   Redis（缓存、幂等键）
```

- Billing Service 本身是无状态的，直接水平扩展多实例
- gRPC 负载均衡用 Kubernetes Service（L4）或 Envoy
- 读请求（GetBalance、ListTransactions）走 MySQL 从库
- 写请求（PreAuth/Settle/Rollback）走主库
- 定时清理任务通过条件 UPDATE CAS 保证多实例幂等（已实现）
- **不需要改任何业务代码**

#### 阶段 2：按 user_id 分库分表

当单表行数 > 5000万（credit_transactions 是增长最快的表）时，考虑分库：

```
Sharding Key: user_id
分片策略: user_id 取模 → 路由到对应分片

Shard 0: wallets_0, credit_transactions_0, pre_auths_0
Shard 1: wallets_1, credit_transactions_1, pre_auths_1
...
Shard N
```

**为什么用 user_id 做 Sharding Key：**
- 所有核心操作（PreAuth/Settle/Rollback）都在单用户维度，不需要跨用户 JOIN
- 同一用户的 wallet + transactions + pre_auths 落在同一分片，事务不跨库
- credit_transactions 是 InsertOnly，天然适合分片

**分库中间件选择：**
- **推荐 Vitess**（YouTube 背景，Go 生态友好）或 **TiDB**（兼容 MySQL 协议，应用层零改动）
- 不推荐应用层手动路由（GORM 插件方案），维护成本高

#### 阶段 3：独立账户引擎 + 事件驱动

用户量 > 500万，Billing 演变为独立的「虚拟账户平台」：

```
              事件总线（Kafka）
                  │
    ┌────────────┼────────────┐
    ▼            ▼            ▼
 账户引擎    对账引擎    风控引擎
（PreAuth/   （T+1对账   （异常检测
  Settle）   报表生成）   冻结账户）
```

- PreAuth/Settle 走同步 RPC（当前模式不变）
- credit_transactions 写入后发布事件到 Kafka
- 下游消费者异步生成对账报表、风控告警
- 这一步本质上是把 Billing 升级为一个「支付中台」

### 14.2 LLM Gateway 水平扩展

**当前瓶颈**：Key 调度池在 Redis ZSET 中，天然支持多实例。

| 阶段 | 方案 |
|------|------|
| 当前 | 单实例 | 
| 阶段 1 | **多实例 + Redis ZSET 共享调度** — ZPOPMIN 原子操作，天然无锁多消费者 |
| 阶段 2 | **按厂商分组部署** — 每个厂商一组 Gateway 实例，独立熔断和扩缩容 |

Gateway 的扩展比 Billing 简单得多，因为 ZSET 调度已经是多消费者安全的设计（ZPOPMIN 原子操作），唯一需要注意的是 in-flight 请求归属（用 `gateway:inflight:{request_id}` 中记录实例 ID，对账 Job 只清理自己的）。

### 14.3 面试谈话要点

面试官问 "如果用户量到了 XX 量级怎么办" 时的回答框架：

1. **先说清楚当前设计的瓶颈在哪**（不是所有地方都是瓶颈）
2. **说明当前设计已经为扩展留了余地**（无状态服务、CAS 幂等、Redis 共享状态）
3. **给出分阶段演进路线**（不要一上来就说分库分表，那是最后手段）
4. **强调 "不过度设计" 原则** — 当前用户量级下，优化的投入产出比不合理

> 具体数字参考：单 MySQL 实例 + 8 核 16G → 10000+ TPS（简单事务）。Billing 的 PreAuth/Settle 都是 2~3 条 SQL 的短事务，单实例轻松支撑万级并发用户。
