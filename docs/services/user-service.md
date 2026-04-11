# User Service 设计文档

## 1. 服务定位与职责

User Service 负责 TadpolesAI 平台的用户身份管理，是认证与授权的唯一入口。

**核心能力：**

- 用户注册（邮箱 + 密码 + 邮箱验证码校验）
- 用户登录，签发 Access Token / Refresh Token
- Token 刷新与登出
- 用户基础信息维护（昵称、头像）
- 用户分组管理（free / default / premium）；充值后由 Billing Service 触发升级

**边界说明：**

| 职责 | 归属 |
|------|------|
| 用户身份、认证、分组 | User Service |
| 积分钱包、扣费 | Billing Service |
| 邮件发送 | 腾讯云 SES（内嵌 User Service，不单独起服务） |
| 模型路由（按分组） | Resume Agent（读 `X-User-Group` header，选模型后传给 LLM Gateway） |
| 简历业务数据 | Resume Agent Service |

---

## 2. 用户分组（User Group）

分组用于控制 LLM 模型路由，**不直接影响积分余额**。

| 分组 | 触发条件 | 模型路由说明 |
|------|----------|-------------|
| `free` | 注册默认 | deepseek-chat（低成本模型） |
| `default` | 首次充值完成后自动升级 | deepseek-chat（一期与 free 相同；后续可按需升级） |
| `premium` | 管理员手动设置（一期预留） | gpt-4o，未来扩展 |

> 具体模型映射由 Resume Agent 的环境变量 `MODEL_FOR_FREE / MODEL_FOR_DEFAULT / MODEL_FOR_PREMIUM` 控制，与 User Service 无耦合。

**升级触发机制：**

Billing Service 在支付回调确认 topup 成功后，通过 gRPC 调用 `UserService.UpgradeGroup`；User Service 幂等处理（`free → default`，已是 `default/premium` 则忽略）。

---

## 3. 外部 HTTP API

Base path: `/v1`，需 JWT Bearer Token（`/auth/*` 系列除外）

### 3.1 发送注册验证码

```
POST /v1/auth/send-code
```

**请求：**

```json
{
  "email": "user@example.com"
}
```

**业务规则：**

- 检查邮箱是否已注册；若已注册返回 `409 Conflict`
- 生成 6 位随机数字验证码，写入 `email_codes` 表（expires_at = NOW()+5min）
- 60 秒内同一邮箱不可重复发送（Redis `email:rate:{email}` INCR，TTL 60s）；超限返回 `429`
- 调用腾讯云 SES 发送验证码邮件（goroutine 异步，fire-and-forget）

**响应：** `200 OK`

```json
{
  "expires_in": 300
}
```

---

### 3.2 用户注册

```
POST /v1/auth/register
```

**请求：**

```json
{
  "email": "user@example.com",
  "password": "Passw0rd!",
  "code": "482917",
  "nickname": "laobai"
}
```

**字段约束：**

| 字段 | 规则 |
|------|------|
| email | RFC 5321 格式；唯一 |
| password | 8–64 字符，至少含大小写字母和数字 |
| code | 6 位数字；有效期 5 分钟；一次性 |
| nickname | 2–20 字符；可重复 |

**业务流程（Saga）：**

1. 校验验证码（DB lookup，验证通过后立即标记已使用）
2. 检查邮箱唯一性（DB `SELECT`）
3. bcrypt hash 密码（cost=12）
4. 写入 `users` 表（group=`free`，email_verified=`true`）
5. 调用 `BillingService.CreateWallet(user_id)`（gRPC，同步等待）
6. 步骤 5 失败 → 回滚步骤 4（DELETE user），返回 `503` 让用户重试

**响应：** `201 Created`

```json
{
  "user_id": "01J...",
  "access_token": "eyJ...",
  "refresh_token": "dGhp...",
  "expires_in": 3600
}
```

---

### 3.3 用户登录

```
POST /v1/auth/login
```

**请求：**

```json
{
  "email": "user@example.com",
  "password": "Passw0rd!"
}
```

**业务规则：**

- 登录失败连续 5 次，锁定账号 15 分钟（Redis counter，key: `login:fail:{email}`，TTL 15min）
- bcrypt.CompareHashAndPassword 校验
- 校验通过：签发 Access Token（1h）+ Refresh Token（30d）
- Refresh Token 明文返回给客户端，DB 仅存其 SHA-256 hash

**响应：** `200 OK`

```json
{
  "user_id": "01J...",
  "access_token": "eyJ...",
  "refresh_token": "dGhp...",
  "expires_in": 3600
}
```

---

### 3.4 刷新 Token

```
POST /v1/auth/refresh
```

**请求：**

```json
{
  "refresh_token": "dGhp..."
}
```

**业务规则：**

- SHA-256 hash 后查 `refresh_tokens` 表
- 检查 `expires_at > NOW()` 且 `revoked_at IS NULL`
- 验证通过：生成新 Access Token；Refresh Token 不轮换（简化实现，一期）
- 验证失败返回 `401`

**响应：** `200 OK`

```json
{
  "access_token": "eyJ...",
  "expires_in": 3600
}
```

---

### 3.5 登出

```
POST /v1/auth/logout
```

**Header：** `Authorization: Bearer {access_token}`

**业务规则：**

- 将当前 Access Token 的 JTI（JWT ID claim）写入 Redis blacklist，TTL = 剩余有效期
- 将 DB 中该用户当前 Refresh Token 标记 `revoked_at = NOW()`（可选传 refresh_token body 精确撤销）

**响应：** `204 No Content`

---

### 3.6 获取当前用户信息

```
GET /v1/users/me
```

**响应：** `200 OK`

```json
{
  "user_id": "01J...",
  "email": "user@example.com",
  "nickname": "laobai",
  "avatar_url": "https://cdn.example.com/avatars/xxx.jpg",
  "group": "default",
  "created_at": "2026-04-10T10:00:00Z"
}
```

---

### 3.7 更新用户信息

```
PUT /v1/users/me
```

**请求（字段均可选，至少一个）：**

```json
{
  "nickname": "newname",
  "avatar_url": "https://cdn.example.com/avatars/new.jpg"
}
```

**响应：** `200 OK`（返回完整用户信息，同 3.6）

---

## 4. 内部 gRPC API

服务名：`user.UserService`  
Proto package：`tadpoles.user.v1`

```protobuf
syntax = "proto3";
package tadpoles.user.v1;

import "google/protobuf/timestamp.proto";

service UserService {
  // LLM Gateway / 其他内部服务查询用户信息（含 group）
  rpc GetUser(GetUserRequest) returns (GetUserResponse);

  // Billing Service 充值成功后触发用户分组升级
  rpc UpgradeGroup(UpgradeGroupRequest) returns (UpgradeGroupResponse);
}

// ── GetUser ──────────────────────────────────────────
message GetUserRequest {
  string user_id = 1;
}

message GetUserResponse {
  string user_id    = 1;
  string email      = 2;
  string nickname   = 3;
  string avatar_url = 4;
  string group      = 5;  // "free" | "default" | "premium"
  google.protobuf.Timestamp created_at = 6;
}

// ── UpgradeGroup ─────────────────────────────────────
message UpgradeGroupRequest {
  string user_id      = 1;
  string target_group = 2;  // Billing 传 "default"
}

message UpgradeGroupResponse {
  string previous_group = 1;
  string current_group  = 2;
  bool   changed        = 3;  // false 表示已是目标组或更高，幂等
}
```

**UpgradeGroup 幂等规则：**

```
current_group 优先级: premium > default > free
若 current_group >= target_group（按优先级），则不更新，changed=false
```

---

## 5. JWT 设计

### 5.1 Access Token Payload

```json
{
  "iss": "tadpoles-user-service",
  "sub": "01J...",
  "jti": "uuid-v4",
  "group": "default",
  "exp": 1744300000,
  "iat": 1744296400
}
```

- 签名算法：HS256（密钥从 env `JWT_SECRET` 读取，≥32 字节随机串）
- 有效期：1 小时
- `group` claim 写入 token，LLM Gateway 直接读取，**无需回调 User Service**
- `jti` 用于登出时写黑名单

### 5.2 Refresh Token

- 格式：随机 32 字节，Base64URL 编码（`crypto/rand`），明文仅下发一次
- DB 存储：SHA-256(refresh_token)，CHAR(64) hex
- 有效期：30 天（DB `expires_at`）

### 5.3 Token 黑名单（Redis）

```
Key:   jwt:blacklist:{jti}
Value: "1"
TTL:   Access Token 剩余有效期（秒）
```

APISIX / API Gateway 在每次请求时可查询此 key 进行前置拦截（可选；也可在 User Service 中间件内检查）。

---

## 6. 数据库 Schema

数据库：`user_db`，使用 GORM + MySQL 8.0

### 6.0 GORM 公共基类

```go
// 标准基类（有软删除）
type Base struct {
    ID        string         `gorm:"type:char(36);primaryKey"`
    CreatedAt time.Time
    UpdatedAt time.Time
    DeletedAt gorm.DeletedAt `gorm:"index"`
}

// 仅插入基类（无更新、无软删除）
type InsertOnlyBase struct {
    ID        string    `gorm:"type:char(36);primaryKey"`
    CreatedAt time.Time
}
```

ID 使用 ULID（`github.com/oklog/ulid/v2`），在 `BeforeCreate` Hook 中自动生成。

---

### 6.1 users

```sql
CREATE TABLE users (
    id              CHAR(36)     NOT NULL,
    email           VARCHAR(255) NOT NULL,
    password_hash   VARCHAR(72)  NOT NULL,          -- bcrypt output (60 chars)，留余量
    nickname        VARCHAR(20)  NOT NULL,
    avatar_url      VARCHAR(512)     NULL,
    `group`         ENUM('free','default','premium') NOT NULL DEFAULT 'free',
    email_verified  TINYINT(1)   NOT NULL DEFAULT 0,
    created_at      DATETIME(3)  NOT NULL,
    updated_at      DATETIME(3)  NOT NULL,
    deleted_at      DATETIME(3)      NULL,

    PRIMARY KEY (id),
    UNIQUE KEY uq_users_email (email),
    KEY        idx_users_deleted (deleted_at)       -- 软删除过滤
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**GORM Model：**

```go
type User struct {
    Base
    Email         string         `gorm:"uniqueIndex;size:255;not null"`
    PasswordHash  string         `gorm:"size:72;not null"`
    Nickname      string         `gorm:"size:20;not null"`
    AvatarURL     *string        `gorm:"size:512"`
    Group         string         `gorm:"type:enum('free','default','premium');default:'free';not null"`
    EmailVerified bool           `gorm:"default:false;not null"`
}
```

---

### 6.2 refresh_tokens

```sql
CREATE TABLE refresh_tokens (
    id         CHAR(36)    NOT NULL,
    user_id    CHAR(36)    NOT NULL,
    token_hash CHAR(64)    NOT NULL,              -- SHA-256 hex
    expires_at DATETIME(3) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    revoked_at DATETIME(3)     NULL,

    PRIMARY KEY (id),
    UNIQUE KEY uq_rt_token_hash (token_hash),
    KEY        idx_rt_user    (user_id),          -- 登出时按 user_id 撤销
    KEY        idx_rt_expires (expires_at)         -- 过期清理 job
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**GORM Model：**

```go
type RefreshToken struct {
    InsertOnlyBase
    UserID    string     `gorm:"type:char(36);not null;index"`
    TokenHash string     `gorm:"type:char(64);uniqueIndex;not null"`
    ExpiresAt time.Time  `gorm:"not null;index"`
    RevokedAt *time.Time
}
```

> `refresh_tokens` 是追加写入（每次登录插入新行），定期 cleanup job 删除 `expires_at < NOW() OR revoked_at IS NOT NULL`。

---

### 6.3 email_codes

```sql
CREATE TABLE email_codes (
    id         CHAR(36)     NOT NULL,
    email      VARCHAR(255) NOT NULL,
    code       CHAR(6)      NOT NULL,
    expires_at DATETIME(3)  NOT NULL,
    used_at    DATETIME(3)      NULL,
    created_at DATETIME(3)  NOT NULL,

    PRIMARY KEY (id),
    KEY idx_ec_email_expires (email, expires_at)  -- 查最新未过期验证码
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**GORM Model：**

```go
type EmailCode struct {
    InsertOnlyBase
    Email     string     `gorm:"size:255;not null"`
    Code      string     `gorm:"type:char(6);not null"`
    ExpiresAt time.Time  `gorm:"not null"`
    UsedAt    *time.Time
}
```

> 验证码验证通过后立即 `UPDATE email_codes SET used_at=NOW() WHERE id=? AND used_at IS NULL`；校验 affected rows=1 防止并发重用。

---

## 7. Redis Key 设计

| Key 模式 | 类型 | TTL | 用途 |
|----------|------|-----|------|
| `email:rate:{email}` | String | 60 s | 发送频率限制，INCR+EXPIRE；≥2 返回 429 |
| `login:fail:{email}` | String | 15 min | 登录失败次数，INCR；≥5 返回 423 Locked |
| `jwt:blacklist:{jti}` | String | Access Token 剩余有效期 | 登出黑名单 |

**验证码存储说明：**

验证码只写 `email_codes` 表（主存储），不写 Redis。Redis 仅承担发送频率限制（`email:rate:{email}`）。校验时查 DB，取最新一条 `used_at IS NULL AND expires_at > NOW()` 记录比对 code，通过后 `UPDATE SET used_at=NOW() WHERE id=? AND used_at IS NULL`（CAS 防并发重放，affected rows=0 则视为已用）。

---

## 8. 核心业务逻辑

### 8.1 注册流程（含 Saga）

```
POST /v1/auth/register
    │
    ├─ 1. DB: SELECT email_codes WHERE email=? AND used_at IS NULL AND expires_at>NOW()
    │        ORDER BY created_at DESC LIMIT 1
    │        → 无记录或 code 不匹配 → 422
    ├─ 2. UPDATE email_codes SET used_at=NOW() WHERE id=? AND used_at IS NULL
    │        → affected=0 → 422（并发重放防护）
    ├─ 3. DB: SELECT COUNT(*) FROM users WHERE email=? AND deleted_at IS NULL
    │        → >0 → 409
    ├─ 4. bcrypt.GenerateFromPassword(password, cost=12)
    ├─ 5. DB INSERT users (group='free', email_verified=true)
    ├─ 6. gRPC BillingService.CreateWallet(user_id)
    │        ✓ 成功 → 继续
    │        ✗ 失败 → DB DELETE users WHERE id=? → 返回 503
    └─ 7. 签发 Access Token + Refresh Token（INSERT refresh_tokens）
         返回 201
```

### 8.2 登录流程

```
POST /v1/auth/login
    │
    ├─ 1. Redis GET login:fail:{email} → ≥5 → 423 Locked（提示剩余锁定时间）
    ├─ 2. DB SELECT users WHERE email=? AND deleted_at IS NULL
    │        → 不存在 → INCR login:fail + 401
    ├─ 3. bcrypt.CompareHashAndPassword
    │        → 不匹配 → INCR login:fail:{email}（EX 900）+ 401
    ├─ 4. 匹配 → DEL login:fail:{email}（清除计数）
    ├─ 5. 签发 Access Token（含 group claim）
    ├─ 6. 生成 Refresh Token（crypto/rand 32B → Base64URL）
    │        INSERT refresh_tokens（存 SHA-256 hash，expires_at = NOW()+30d）
    └─ 7. 返回 200
```

### 8.3 UpgradeGroup（gRPC，被 Billing Service 调用）

```go
func (s *UserServiceServer) UpgradeGroup(ctx context.Context, req *pb.UpgradeGroupRequest) (*pb.UpgradeGroupResponse, error) {
    groupOrder := map[string]int{"free": 0, "default": 1, "premium": 2}

    var user User
    if err := s.db.Where("id = ? AND deleted_at IS NULL", req.UserId).First(&user).Error; err != nil {
        return nil, status.Errorf(codes.NotFound, "user not found")
    }

    previousGroup := user.Group

    // 幂等：已是目标分组或更高，直接返回
    if groupOrder[user.Group] >= groupOrder[req.TargetGroup] {
        return &pb.UpgradeGroupResponse{
            PreviousGroup: previousGroup,
            CurrentGroup:  user.Group,
            Changed:       false,
        }, nil
    }

    if err := s.db.Model(&user).Update("group", req.TargetGroup).Error; err != nil {
        return nil, status.Errorf(codes.Internal, "update group failed")
    }

    return &pb.UpgradeGroupResponse{
        PreviousGroup: previousGroup,
        CurrentGroup:  req.TargetGroup,
        Changed:       true,
    }, nil
}
```

### 8.4 用户分组与 JWT group claim 一致性

用户 group 升级后，**已下发的 Access Token 的 `group` claim 仍为旧值**，直到 token 过期（最多 1 小时）。这是可接受的最终一致性：

- Access Token 有效期仅 1 小时，影响窗口有限
- 用户刷新/重新登录后，新 token 携带最新 group
- 一期不做 token 强制失效（避免引入复杂性）

> 若未来对实时性有更高要求，可在 APISIX 增加 `group` 实时查询插件（以 user_id 查 User Service），按需引入。

---

## 9. 与其他服务的集成

### 9.1 与 Billing Service

| 方向 | 调用 | 时机 |
|------|------|------|
| User → Billing | `BillingService.CreateWallet(user_id)` | 注册 Saga 步骤 7 |
| Billing → User | `UserService.UpgradeGroup(user_id, "default")` | Topup 支付回调确认后 |

### 9.2 与 Resume Agent / LLM Gateway

APISIX 解析 JWT 后将 `group` 注入 `X-User-Group` header 转发给 Resume Agent；Resume Agent 读取该 header 选择对应模型，再调用 LLM Gateway。LLM Gateway 本身**不参与**分组路由决策，User Service 与 LLM Gateway 无直接运行时依赖。

### 9.3 与 APISIX（API Gateway）

- APISIX JWT 验证插件（`jwt-auth`）：校验签名 + exp，将 `sub`（user_id）和 `group` 注入 upstream header 透传给下游
  - `X-User-Id: 01J...`（LLM Gateway、Billing 等所有下游均使用）
  - `X-User-Group: default`（Resume Agent 使用，用于选模型；LLM Gateway 不直接使用）
- JWT 黑名单检查：APISIX 可选接入 Redis `jwt:blacklist:{jti}` 实现即时登出生效

---

## 10. 环境变量

```env
# 服务端口
HTTP_PORT=8083
GRPC_PORT=50051          # UpgradeGroup 等内部 gRPC 接口

# 数据库
USER_DB_DSN=user:password@tcp(mysql:3306)/user_db?charset=utf8mb4&parseTime=True

# Redis
REDIS_ADDR=redis:6379
REDIS_PASSWORD=

# JWT
JWT_SECRET=your-random-32-byte-secret-here
JWT_ACCESS_EXPIRY_SECONDS=3600
JWT_REFRESH_EXPIRY_DAYS=30

# Billing gRPC
BILLING_GRPC_ADDR=billing-service:50051
BILLING_GRPC_TIMEOUT_MS=3000

# 邮件发送（腾讯云 SES，内嵌 User Service）
# 免费额度：累计 1000 封；超出约 ¥2/1000 封
# 域名备案在腾讯云，DNS 配置 SPF/DKIM 在同一控制台完成
TENCENT_SECRET_ID=your-secret-id
TENCENT_SECRET_KEY=your-secret-key
EMAIL_FROM=noreply@yourdomain.com
EMAIL_REGION=ap-guangzhou          # 腾讯云 SES 支持 ap-guangzhou / ap-hongkong

# bcrypt
BCRYPT_COST=12

# 注册验证码
EMAIL_CODE_TTL_SECONDS=300
EMAIL_CODE_RATE_LIMIT_SECONDS=60

# 登录锁定
LOGIN_MAX_FAIL=5
LOGIN_LOCK_SECONDS=900

# 用户分组
DEFAULT_USER_GROUP=free
TOPUP_UPGRADE_TARGET_GROUP=default
```

---

## 11. 错误码约定

| HTTP Status | 场景 |
|-------------|------|
| 400 | 参数格式校验失败 |
| 401 | 未认证（token 无效/过期） |
| 409 | 邮箱已注册 |
| 422 | 验证码错误或已使用 |
| 423 | 账号登录锁定 |
| 429 | 验证码发送频率限制 |
| 503 | Billing 初始化钱包失败，请重试 |
