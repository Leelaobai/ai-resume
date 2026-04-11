# TadpolesAI Resume Agent — 技术方案文档

> 版本：v1.0
> 状态：设计定稿，待实现
> 对应产品文档：`docs/product-design.md`

---

## 目录

1. [架构总览](#1-架构总览)
2. [数据库设计](#2-数据库设计)
3. [鉴权系统](#3-鉴权系统)
4. [API 路由设计](#4-api-路由设计)
5. [积分计费系统](#5-积分计费系统)
6. [Agent 与计费集成](#6-agent-与计费集成)
7. [简历与会话管理](#7-简历与会话管理)
8. [长期记忆系统](#8-长期记忆系统)
9. [模板系统](#9-模板系统)
10. [文件上传与冲突检测](#10-文件上传与冲突检测)
11. [SSE 流式输出](#11-sse-流式输出)
12. [安全规范](#12-安全规范)
13. [迭代策略](#13-迭代策略)

---

## 1. 架构总览

```
┌─────────────────────────────────────────────────────────────────┐
│                         前端 (Vue 3 + TS)                        │
│   Login/Register → Dashboard → Editor (3-col) → Billing         │
└───────────────────────────┬─────────────────────────────────────┘
                            │ HTTPS / SSE
┌───────────────────────────▼─────────────────────────────────────┐
│                    后端 (Go + Gin)                                │
│                                                                   │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────────────┐  │
│  │ Auth Module │  │ Resume/Chat  │  │   Billing Module        │  │
│  │  JWT双Token │  │ ReAct Agent  │  │  积分扣费(原子DB事务)    │  │
│  └─────────────┘  └──────────────┘  └────────────────────────┘  │
└───────────────────────────┬─────────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────────┐
│                    PostgreSQL + pgvector                          │
│  users / refresh_tokens / sessions / messages / resumes          │
│  memory_facts / user_credits / credit_transactions               │
│  rag_documents / conversation_summaries / templates              │
└─────────────────────────────────────────────────────────────────┘
                            │
                    OpenRouter API → Claude Sonnet
```

### 技术栈

| 层次 | 技术 | 说明 |
|------|------|------|
| 前端 | Vue 3 + TypeScript + Vite | Composition API，Pinia状态管理 |
| 后端 | Go 1.22 + Gin | HTTP框架 |
| 数据库 | PostgreSQL 16 + pgvector | 主存储 + 向量检索 |
| LLM | OpenRouter → Claude Sonnet | via LLM_MODEL env var |
| PDF导出 | chromedp | headless Chrome |
| 部署 | 单机 Docker Compose | 初期自建服务器 |

---

## 2. 数据库设计

### 2.1 Migration 002 — 用户与鉴权

```sql
-- 002_auth.up.sql

-- 用户表
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email           VARCHAR(255) NOT NULL UNIQUE,
    password_hash   VARCHAR(255) NOT NULL,       -- bcrypt cost=12
    nickname        VARCHAR(100) NOT NULL DEFAULT '',
    avatar_url      VARCHAR(500),
    status          VARCHAR(20) NOT NULL DEFAULT 'active',  -- active|banned
    email_verified  BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_users_email ON users(email);

-- Refresh Token 表（服务端持久化，支持撤销）
-- Token本身只存哈希，原始token只在发放时返回一次
CREATE TABLE refresh_tokens (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      VARCHAR(255) NOT NULL UNIQUE, -- SHA-256(token)
    expires_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ,
    user_agent      VARCHAR(500),
    ip_address      INET,
    revoked         BOOLEAN NOT NULL DEFAULT false,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens(token_hash);

-- sessions 表：增加 user_id 归属
ALTER TABLE sessions ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE sessions ADD COLUMN name VARCHAR(255) NOT NULL DEFAULT '未命名简历';
CREATE INDEX idx_sessions_user ON sessions(user_id, created_at DESC);

-- memory_facts 表：从 session 级别升级为 user 级别
ALTER TABLE memory_facts DROP COLUMN session_id;
ALTER TABLE memory_facts ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE memory_facts ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE memory_facts ADD COLUMN source_session_id UUID REFERENCES sessions(id) ON DELETE SET NULL;
-- 同一用户同一key唯一
CREATE UNIQUE INDEX idx_memory_user_key ON memory_facts(user_id, category, key);
CREATE INDEX idx_memory_user ON memory_facts(user_id, category);
```

### 2.2 Migration 003 — 计费系统

```sql
-- 003_billing.up.sql

-- 用户积分余额（单行，乐观锁）
CREATE TABLE user_credits (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance         BIGINT NOT NULL DEFAULT 0,    -- 单位：积分（1积分=0.01元）
    version         BIGINT NOT NULL DEFAULT 0,    -- 乐观锁版本号
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 积分流水（append-only，永不修改/删除）
CREATE TABLE credit_transactions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id),
    type            VARCHAR(30) NOT NULL,  -- recharge|consume|refund|gift
    amount          BIGINT NOT NULL,       -- 正数=收入，负数=支出
    balance_after   BIGINT NOT NULL,       -- 操作后余额（快照）
    description     VARCHAR(500) NOT NULL,
    -- 关联信息
    session_id      UUID REFERENCES sessions(id) ON DELETE SET NULL,
    order_id        VARCHAR(100),          -- 充值订单号（支付接入后使用）
    -- Token用量明细（消费类型时填写）
    prompt_tokens   INT,
    completion_tokens INT,
    model           VARCHAR(100),
    -- 时间
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_credit_tx_user ON credit_transactions(user_id, created_at DESC);
CREATE INDEX idx_credit_tx_session ON credit_transactions(session_id) WHERE session_id IS NOT NULL;

-- Token使用记录（每次LLM调用）
CREATE TABLE token_usage (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id),
    session_id      UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_id      UUID REFERENCES messages(id) ON DELETE SET NULL,
    model           VARCHAR(100) NOT NULL,
    prompt_tokens   INT NOT NULL,
    completion_tokens INT NOT NULL,
    cost_credits    BIGINT NOT NULL,       -- 本次扣除积分数
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_token_usage_user ON token_usage(user_id, created_at DESC);
CREATE INDEX idx_token_usage_session ON token_usage(session_id);
```

### 2.3 Migration 004 — 模板系统

```sql
-- 004_templates.up.sql

-- 简历模板
CREATE TABLE resume_templates (
    id              VARCHAR(50) PRIMARY KEY,       -- 'classic','modern','minimal'等
    name            VARCHAR(100) NOT NULL,
    description     TEXT,
    thumbnail_url   VARCHAR(500),
    tags            TEXT[] NOT NULL DEFAULT '{}', -- ['互联网','金融','校招']
    is_active       BOOLEAN NOT NULL DEFAULT true,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 预置模板数据
INSERT INTO resume_templates (id, name, description, tags, sort_order) VALUES
('classic',   '经典简洁',   '清晰的黑白排版，适合传统行业',   ARRAY['通用','传统行业'],    1),
('modern',    '现代双栏',   '左右分栏设计，视觉层次丰富',     ARRAY['互联网','设计'],      2),
('minimal',   '极简风格',   '留白充分，注重内容本身',         ARRAY['创意','自由职业'],    3),
('technical', '技术专项',   '突出技能栈和项目经验',           ARRAY['技术','程序员','校招'],4),
('executive', '高管版',    '商务感强，适合管理岗位',         ARRAY['管理','金融'],        5);
```

### 2.4 数据模型关系图

```
users (1) ──────────────── (N) sessions
  │                               │
  │                               │ (1:1)
  │                               ▼
  │                            resumes
  │                               │ (1:N)
  │                            messages
  │
  ├── (1:N) memory_facts       ← 用户级长期记忆
  │
  ├── (1:1) user_credits        ← 余额快照
  │
  └── (1:N) credit_transactions ← 不可变流水账
                │
                └── (N:1) token_usage
```

---

## 3. 鉴权系统

### 3.1 双Token设计

```
┌──────────┐    POST /api/auth/login     ┌──────────┐
│  前端    │ ──────────────────────────► │  后端    │
│          │ ◄─────────────────────────  │          │
│          │  {                          │          │
│          │    accessToken: JWT(1h),    │          │
│          │    refreshToken: opaque     │          │
│          │  }                          │          │
└──────────┘                            └──────────┘

Access Token:
  - JWT，HS256签名，1小时有效
  - Payload: { sub: user_id, email, iat, exp }
  - 纯无状态验证，不查DB
  - 每个受保护请求携带: Authorization: Bearer <token>

Refresh Token:
  - 随机生成 (crypto/rand, 32字节, base64url编码)
  - 原始值只在发放时返回一次，DB只存 SHA-256 哈希
  - 有效期30天
  - 使用后立即轮换（旧token作废，发放新token）
  - HttpOnly Cookie + 响应体双重返回（兼容移动端）
```

### 3.2 Token 轮换流程

```
前端请求 API → 401 Access Token过期
  ↓
POST /api/auth/refresh (携带 refresh token)
  ↓
后端:
  1. hash(refresh_token) → 查 refresh_tokens 表
  2. 检查 revoked=false, expires_at > NOW()
  3. 验证通过 → 标记旧token revoked=true
  4. 生成新 access_token + refresh_token
  5. 插入新refresh_token记录
  6. 返回新token对
  ↓
前端更新本地存储，重试原请求
```

### 3.3 Go 实现结构

```go
// internal/auth/
├── jwt.go          // GenerateAccessToken, ValidateAccessToken
├── token.go        // GenerateRefreshToken, HashToken
├── middleware.go   // AuthRequired() gin.HandlerFunc
└── store.go        // RefreshTokenStore (DB操作)

// JWT Payload 结构
type Claims struct {
    UserID string `json:"sub"`
    Email  string `json:"email"`
    jwt.RegisteredClaims
}

// Middleware 注入方式
// 通过 gin context 传递，所有 handler 通过此获取当前用户
func GetCurrentUser(c *gin.Context) (userID string, ok bool) {
    val, exists := c.Get("user_id")
    if !exists { return "", false }
    return val.(string), true
}
```

### 3.4 新用户注册流程

```
POST /api/auth/register { email, password, nickname }
  ↓
1. 验证邮箱格式、密码强度（>=8位）
2. 检查邮箱唯一性
3. bcrypt(password, cost=12)
4. INSERT users
5. INSERT user_credits (balance=100, 新用户赠送100积分=1元)
6. INSERT credit_transactions (type='gift', amount=100, description='新用户注册赠送')
7. 生成 access_token + refresh_token
8. 返回 token 对 + user info
```

---

## 4. API 路由设计

### 4.1 路由结构

```go
func (s *Server) setupRoutes() {
    s.router.Use(corsMiddleware())

    api := s.router.Group("/api")

    // ── 公开路由（无需登录）──────────────────────────
    auth := api.Group("/auth")
    {
        auth.POST("/register", s.Register)
        auth.POST("/login",    s.Login)
        auth.POST("/refresh",  s.RefreshToken)
        auth.POST("/logout",   s.Logout)   // 撤销 refresh token
    }

    // 模板（公开，无需登录即可浏览）
    api.GET("/templates", s.ListTemplates)

    // ── 受保护路由（必须登录）───────────────────────
    protected := api.Group("/")
    protected.Use(s.authMiddleware.AuthRequired())
    {
        // 当前用户信息
        protected.GET("/me",          s.GetMe)
        protected.PATCH("/me",        s.UpdateMe)
        protected.POST("/me/avatar",  s.UploadAvatar)

        // 简历（核心资源，必须属于当前用户）
        resumes := protected.Group("/resumes")
        {
            resumes.GET("",           s.ListResumes)         // 当前用户所有简历
            resumes.POST("",          s.CreateResume)        // 创建（含session）
            resumes.GET("/:rid",      s.GetResume)
            resumes.PATCH("/:rid",    s.UpdateResumeName)
            resumes.DELETE("/:rid",   s.DeleteResume)
            resumes.POST("/:rid/copy",s.CopyResume)          // 复制简历

            // 简历内容与导出
            resumes.GET("/:rid/html", s.GetResumeHTML)
            resumes.GET("/:rid/pdf",  s.ExportResumePDF)
            resumes.POST("/:rid/upload",      s.UploadResumeFile)  // 上传已有简历
            resumes.PATCH("/:rid/template",   s.UpdateTemplate)

            // 对话（挂在resume下，session对用户透明）
            resumes.GET("/:rid/messages",     s.GetMessages)
            resumes.POST("/:rid/chat",        s.Chat)       // SSE
            resumes.POST("/:rid/jd",          s.PasteJD)    // 粘贴JD
        }

        // 积分计费
        billing := protected.Group("/billing")
        {
            billing.GET("/balance",       s.GetBalance)
            billing.GET("/transactions",  s.GetTransactions) // 分页流水
            billing.GET("/usage",         s.GetUsageStats)   // 按简历/月汇总
            billing.POST("/recharge",     s.Recharge)        // 暂时mock，支付接入后替换
        }
    }
}
```

### 4.2 资源所有权校验规范

**所有受保护的路由，Handler 必须执行所有权校验，不依赖前端传参。**

```go
// 标准所有权校验模式
func (s *Server) GetResume(c *gin.Context) {
    userID, _ := auth.GetCurrentUser(c)   // 从JWT获取，不信任请求体
    resumeID := c.Param("rid")

    resume, err := s.resumeStore.GetByID(resumeID)
    if err != nil {
        c.JSON(404, gin.H{"error": "not found"})
        return
    }
    // ⚠️ 关键：必须校验归属
    if resume.UserID != userID {
        c.JSON(403, gin.H{"error": "forbidden"})
        return
    }
    // ...
}
```

---

## 5. 积分计费系统

### 5.1 积分定价

```
1000 积分 = ¥10
1 积分 = ¥0.01

充值套餐:
  ¥5   →   500积分
  ¥10  →  1000积分
  ¥30  →  3000积分（+200积分赠送）
  ¥50  →  5000积分（+500积分赠送）
  ¥100 → 10000积分（+1500积分赠送）
  ¥300 → 30000积分（+6000积分赠送）

新用户注册：赠送 100 积分（¥1）

LLM调用积分换算（以Claude Sonnet为例）:
  input:  $3/1M tokens  → ¥21.75/1M tokens → 约2.175积分/1K tokens
  output: $15/1M tokens → ¥108.75/1M tokens → 约10.875积分/1K tokens
  加价系数: 1.5x（覆盖运营成本）
  最终:
    input  ≈ 3.3积分/1K tokens （取整向上）
    output ≈ 16积分/1K tokens  （取整向上）
```

### 5.2 积分扣除：原子事务

扣费必须在数据库事务中完成，绝不在应用层做余额计算。

```go
// internal/billing/deduct.go

type DeductParams struct {
    UserID           string
    SessionID        string
    MessageID        string
    PromptTokens     int
    CompletionTokens int
    Model            string
    Description      string
}

func (s *Store) DeductCredits(ctx context.Context, p DeductParams) error {
    cost := calculateCost(p.PromptTokens, p.CompletionTokens, p.Model)

    return s.db.WithTx(ctx, func(tx *sqlx.Tx) error {
        // 1. 锁定用户余额行（SELECT FOR UPDATE）
        var current struct {
            Balance int64
            Version int64
        }
        err := tx.QueryRowContext(ctx,
            `SELECT balance, version FROM user_credits
             WHERE user_id = $1 FOR UPDATE`,
            p.UserID,
        ).Scan(&current.Balance, &current.Version)
        if err != nil { return err }

        // 2. 检查余额是否充足
        if current.Balance < cost {
            return ErrInsufficientCredits
        }

        newBalance := current.Balance - cost

        // 3. 更新余额（含乐观锁版本号）
        res, err := tx.ExecContext(ctx,
            `UPDATE user_credits
             SET balance = $1, version = version + 1, updated_at = NOW()
             WHERE user_id = $2 AND version = $3`,
            newBalance, p.UserID, current.Version,
        )
        if err != nil { return err }
        if rows, _ := res.RowsAffected(); rows == 0 {
            return ErrConcurrentUpdate // 重试
        }

        // 4. 写流水（append-only）
        _, err = tx.ExecContext(ctx,
            `INSERT INTO credit_transactions
             (user_id, type, amount, balance_after, description,
              session_id, prompt_tokens, completion_tokens, model)
             VALUES ($1,'consume',$2,$3,$4,$5,$6,$7,$8)`,
            p.UserID, -cost, newBalance, p.Description,
            p.SessionID, p.PromptTokens, p.CompletionTokens, p.Model,
        )
        if err != nil { return err }

        // 5. 写token_usage明细
        _, err = tx.ExecContext(ctx,
            `INSERT INTO token_usage
             (user_id, session_id, message_id, model,
              prompt_tokens, completion_tokens, cost_credits)
             VALUES ($1,$2,$3,$4,$5,$6,$7)`,
            p.UserID, p.SessionID, p.MessageID, p.Model,
            p.PromptTokens, p.CompletionTokens, cost,
        )
        return err
    })
}

func calculateCost(prompt, completion int, model string) int64 {
    // 基于模型计算积分，向上取整
    promptCost     := int64(math.Ceil(float64(prompt)     / 1000 * 3.3))
    completionCost := int64(math.Ceil(float64(completion) / 1000 * 16.0))
    return promptCost + completionCost
}
```

### 5.3 余额不足处理

```
Chat 请求进来
  ↓
先查余额（快速读，不加锁）
  ↓
余额 < 安全阈值（如50积分）?
  → SSE 推送 {type:"balance_warning", balance: X}
  → 前端 Navbar 切换至警告/危险样式

余额 = 0 时:
  → 直接返回 HTTP 402 Payment Required
  → 不发起任何LLM调用
  → 前端弹出充值弹窗
```

---

## 6. Agent 与计费集成

### 6.1 ReAct Agent 调用链路

```
POST /resumes/:rid/chat
  ↓
1. 鉴权 (JWT middleware)
2. 所有权校验 (resume.UserID == currentUser)
3. 余额预检 (balance > 0，否则 402)
4. 获取 session 上下文（历史消息 + memory_facts）
5. 构造 Agent 请求，注入 user_id + session_id
6. 启动 SSE 流
7. Agent.Run() → 每次LLM完成后：
     a. 从响应获取 usage.prompt_tokens, usage.completion_tokens
     b. billing.DeductCredits(ctx, params)  // 原子事务
     c. 如果 ErrInsufficientCredits → SSE 推送停止信号
8. Agent 完成后，触发异步任务：
     a. MemoryExtractor.Extract() → 更新 memory_facts
     b. ConversationSummarizer.Run() → 压缩历史（如超过阈值）
```

### 6.2 SSE 消息协议

```typescript
// 前端接收的SSE事件类型

// 正常流式文本
{ type: "delta",   content: "..." }

// Agent工具调用通知（展示给用户看）
{ type: "tool_use", tool: "update_resume", args: {...} }

// 简历更新（工具执行完后推送最新resume JSON）
{ type: "resume_update", data: {...} }

// 余额警告（每次回复后推送当前余额）
{ type: "balance_update", balance: 123, warning: false }

// 余额不足，停止生成
{ type: "balance_exhausted", balance: 0 }

// 流结束
{ type: "done", usage: { prompt: 450, completion: 230, cost: 5 } }

// 错误
{ type: "error", code: "...", message: "..." }
```

---

## 7. 简历与会话管理

### 7.1 创建简历流程

```
POST /api/resumes { name, template_id }
  ↓
DB事务:
  1. INSERT sessions (user_id, name, title=name)
  2. INSERT resumes (session_id, template_id, data='{}')
  → 返回 { resume_id, session_id, name, template_id, ... }
```

### 7.2 复制简历

```
POST /api/resumes/:rid/copy
  ↓
DB事务:
  1. 查出原始 resume.data 快照
  2. INSERT sessions (user_id, name=原名+" 副本")
  3. INSERT resumes (session_id=新, data=快照, template_id=原)
  → 新的 resume/session 对，对话历史为空
  → 原简历完全不变
```

### 7.3 简历 JSONB 结构

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
    "github": ""
  },
  "summary": "10年互联网产品经验，...",
  "experience": [
    {
      "company": "某科技公司",
      "title": "高级产品经理",
      "start": "2020-03",
      "end": "present",
      "location": "北京",
      "highlights": ["负责...", "推动..."]
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
  "skills": ["产品设计", "数据分析", "SQL"],
  "projects": [],
  "certificates": [],
  "languages": []
}
```

---

## 8. 长期记忆系统

### 8.1 设计原则

- 记忆归属**用户级别**，跨所有简历共享
- 每个 key 只保留最新值（UPSERT）
- 定期由异步任务整理（合并矛盾、清理过期）

### 8.2 分类体系

```
category       key示例              说明
─────────────────────────────────────────────────────
personal       name, gender,        基本个人信息
               birth_year, location
career         current_title,       当前职位状态
               years_exp, industry
preference     resume_style,        用户习惯偏好
               language_pref
skills         skill_list           技能标签（JSON数组）
education      highest_degree,      学历信息
               school
```

### 8.3 记忆提取流程

```go
// 每次对话结束后异步触发
// internal/memory/extractor.go

func (e *Extractor) Extract(ctx context.Context, userID, sessionID string) error {
    // 1. 取最近N条消息
    messages := e.msgStore.GetRecent(sessionID, 20)

    // 2. 调用LLM提取结构化事实
    //    Prompt: "从以下对话中提取用户的个人信息事实，返回JSON数组..."
    facts := e.llm.ExtractFacts(messages)

    // 3. UPSERT到 memory_facts
    for _, fact := range facts {
        e.store.Upsert(ctx, userID, fact.Category, fact.Key, fact.Value)
    }
    return nil
}
```

### 8.4 定期整理任务（每日凌晨）

```
1. 查出每个用户的所有 memory_facts
2. 调用LLM："以下是用户的记忆碎片，请合并矛盾项、移除过时信息，
             返回整理后的完整记忆列表"
3. 以事务方式替换（DELETE + INSERT）
```

---

## 9. 模板系统

### 9.1 模板标签与筛选

```go
// GET /api/templates?tags=互联网,校招

type Template struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description"`
    ThumbnailURL string  `json:"thumbnail_url"`
    Tags        []string `json:"tags"`
}
```

### 9.2 模板切换

```
PATCH /api/resumes/:rid/template { template_id }
  ↓
1. 只更新 resumes.template_id
2. resume.data 不变（数据与模板完全分离）
3. 返回新的 HTML 预览
```

模板是纯Go HTML模板 (`text/template`)，接收 `ResumeData` 渲染为 HTML。PDF通过 chromedp 截图该HTML生成。

---

## 10. 文件上传与冲突检测

### 10.1 上传流程

```
POST /api/resumes/:rid/upload (multipart, file=resume.pdf/.docx)
  ↓
1. 解析文件 → 提取结构化数据（parser.Parse）
2. 获取当前 resume.data
3. 调用 ConflictDetector.Detect(existing, uploaded)
4. 冲突评分 < 阈值 → 自动合并 → 推送 merged_report
   冲突评分 ≥ 阈值 → 返回冲突详情 → 前端弹出选择对话框
```

### 10.2 冲突检测规则

```go
// internal/resume/conflict.go

type ConflictLevel int
const (
    LowConflict    ConflictLevel = iota // 自动合并
    HighConflict                         // 用户决策
)

// 高冲突字段（任一字段不同 → 直接升为高冲突）
var criticalFields = []string{
    "basics.name",
    "basics.gender",
    "experience[0].company",  // 最近一份工作
    "education[0].school",    // 最高学历
}

func Detect(existing, uploaded ResumeData) (ConflictLevel, []FieldConflict) {
    conflicts := compareFields(existing, uploaded, criticalFields)
    if len(conflicts) > 0 {
        return HighConflict, conflicts
    }
    return LowConflict, nil
}
```

---

## 11. SSE 流式输出

### 11.1 SSE Handler 结构

```go
func (s *Server) Chat(c *gin.Context) {
    userID, _ := auth.GetCurrentUser(c)
    resumeID  := c.Param("rid")

    // 1. 所有权 + 余额校验
    resume, err := s.resumeStore.GetByIDForUser(resumeID, userID)
    if err != nil { c.JSON(403, ...); return }

    balance, _ := s.billing.GetBalance(userID)
    if balance <= 0 { c.JSON(402, gin.H{"error": "insufficient_credits"}); return }

    // 2. 设置SSE响应头
    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("Connection", "keep-alive")
    c.Header("X-Accel-Buffering", "no") // Nginx: 禁用缓冲

    // 3. 获取 flusher
    flusher, ok := c.Writer.(http.Flusher)
    if !ok { c.JSON(500, ...); return }

    // 4. 解析请求
    var req ChatRequest
    c.ShouldBindJSON(&req)

    // 5. 创建事件通道，启动 Agent
    eventCh := make(chan SSEEvent, 32)
    ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
    defer cancel()

    go s.agent.Run(ctx, AgentParams{
        UserID:    userID,
        SessionID: resume.SessionID,
        Message:   req.Message,
        EventCh:   eventCh,
    })

    // 6. 流式写出
    for event := range eventCh {
        data, _ := json.Marshal(event)
        fmt.Fprintf(c.Writer, "data: %s\n\n", data)
        flusher.Flush()

        if event.Type == "done" || event.Type == "balance_exhausted" {
            break
        }
    }
}
```

---

## 12. 安全规范

### 12.1 强制规则（绝不妥协）

| 规则 | 实现方式 |
|------|----------|
| 所有写操作必须验证资源归属 | Handler 层显式校验 `resource.UserID == jwtUserID` |
| 密码必须哈希存储 | bcrypt, cost=12 |
| Refresh Token 数据库只存哈希 | SHA-256(raw_token) |
| SQL 注入防御 | 全程参数化查询，禁止字符串拼接 |
| 余额扣除必须在DB事务中完成 | `SELECT FOR UPDATE` + 乐观锁 |
| Rate Limiting | 登录接口: 5次/分钟/IP；注册: 3次/小时/IP |
| CORS | 生产环境限定前端域名，禁止 `*` |

### 12.2 敏感信息处理

```
环境变量（不得硬编码）:
  JWT_SECRET        — JWT签名密钥（>=32字节随机串）
  DATABASE_URL      — 数据库连接串
  LLM_API_KEY       — OpenRouter API Key
  ENCRYPTION_KEY    — 预留：敏感字段加密

日志:
  禁止记录: password, token, api_key, credit_card
  Session内容可记录: message_id, token_count（不记录完整内容）
```

### 12.3 API 返回规范

```go
// 统一错误响应格式（不暴露内部细节）
type ErrorResponse struct {
    Error   string `json:"error"`   // 机器可读代码
    Message string `json:"message"` // 人类可读描述
}

// 常用错误码
// "unauthorized"      → 401 未登录或token失效
// "forbidden"         → 403 无权访问该资源
// "not_found"         → 404 资源不存在
// "insufficient_credits" → 402 积分不足
// "rate_limited"      → 429 请求过于频繁
// "internal_error"    → 500 （不暴露具体原因）
```

---

## 13. 迭代策略

### 13.1 当前阶段（无真实用户）

- 不考虑数据迁移，可随时清空DB
- Migration文件必须写好，确保从空库可以完整建立
- 每个migration必须有对应的 `.down.sql`

### 13.2 有真实用户后的迭代原则

```
1. 永远只新增migration，不修改已执行的migration
2. 破坏性变更（改列名/类型）使用三步迁移:
     Step1: 新增列
     Step2: 双写过渡期（同时写新旧列）
     Step3: 切换读取，删除旧列
3. credit_transactions 表永远 append-only，不允许UPDATE/DELETE
4. 功能灰度: 新功能先对内测用户开放（user.status='beta'）
```

### 13.3 开发顺序建议

```
Week 1: 鉴权系统（JWT + Register/Login + Middleware）
  → 验收：curl 测试所有auth接口，Postman集合

Week 2: 简历/会话核心（带用户归属）
  → 迁移现有Session→Resume逻辑，加user_id
  → 验收：创建/查看/删除简历完整流程

Week 3: 计费系统
  → user_credits + credit_transactions + 扣费事务
  → 验收：模拟LLM调用，验证余额原子扣减

Week 4: Agent集成计费 + SSE完善
  → 真实LLM调用完整扣费 + 余额警告推送
  → 验收：完整对话→余额变化→流水记录

Week 5: 前端重构
  → 登录页 → Dashboard → Editor → Billing页
  → 对接所有新API

Week 6: 联调 + 内测准备
  → 错误处理完善，Rate Limiting，日志
  → 邀请朋友内测
```

---

*文档结束 — 下一步：按Week 1开始实现鉴权系统*
