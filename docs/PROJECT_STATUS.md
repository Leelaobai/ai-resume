# TadpolesAI — 项目整体设计现状

> 整理时间：2026-04-11
> 文档状态：设计阶段（待实现）
> 本文档为横跨所有服务的项目全局视图，细节见各服务设计文档

---

## 目录

1. [项目定位](#1-项目定位)
2. [服务架构总览](#2-服务架构总览)
3. [端口 & 数据库分配](#3-端口--数据库分配)
4. [各服务设计状态](#4-各服务设计状态)
5. [关键架构决策日志](#5-关键架构决策日志)
6. [跨服务接口契约](#6-跨服务接口契约)
7. [已修复的设计问题（本轮 Review）](#7-已修复的设计问题本轮-review)
8. [待决策事项（开发前必须明确）](#8-待决策事项开发前必须明确)
9. [后续迭代可补项](#9-后续迭代可补项)
10. [重大实现风险](#10-重大实现风险)
11. [文档索引](#11-文档索引)

---

## 1. 项目定位

TadpolesAI 是一个 **AI Agent 矩阵平台**，目标是统一底层能力，业务模块可插拔。

```
平台层（所有业务共享）:
  ├── 统一用户账户体系     ← 一次注册，所有 Agent 工具通用
  ├── 统一 LLM 能力网关   ← 多厂商 Key 管理，业务方透明调用
  └── 统一积分计费体系     ← 任何消费行为走同一账本

业务层（可插拔）:
  ├── 简历 Agent          ← 当前唯一业务模块（设计已完成）
  ├── [未来] 面试 Agent
  ├── [未来] 求职信 Agent
  └── ...
```

**架构目标优先级**：正确性 → 安全性 → 可扩展性 → 高并发

---

## 2. 服务架构总览

```
                    用户浏览器 (Vue 3 + TypeScript)
                             │ HTTPS / SSE
                    ┌────────▼────────┐
                    │     APISIX      │  SSL终止 · JWT验证 · 限流
                    └──┬─────────────┘
                       │
          ┌────────────┴────────────┐
          │                         │
┌─────────▼─────────┐   ┌──────────▼──────────────┐
│   User Service     │   │   Resume Agent Service   │
│  注册/登录/JWT     │◄──┤  简历CRUD + ReAct Agent  │
│  DB: user_db       │gRPC│  DB: resume_db           │
└────────────────────┘   └──────────┬──────────────┘
                                    │ HTTP 8083
                         ┌──────────▼──────────────┐
                         │   LLM Gateway Service    │
                         │  OpenAI兼容 · 多厂商Key  │
                         │  Usage计量               │
                         │  DB: tadpoles_gateway    │
                         └──────────┬──────────────┘
                                    │ Redis Streams
                         ┌──────────▼──────────────┐
                         │   Billing Service        │
                         │  积分账本 · PreAuth/Settle│
                         │  DB: tadpoles_billing    │
                         └─────────────────────────┘
```

**技术栈**：Go 1.22 + Gin · MySQL 8.0 · Redis · APISIX · ULID · gRPC · SSE · Handlebars.js · html2pdf.js

---

## 3. 端口 & 数据库分配

### HTTP 端口

| 服务 | 容器内端口 | 说明 |
|------|-----------|------|
| APISIX | 443 (外) | SSL 终止，路由所有流量 |
| User Service | 8082 | 注册、登录、JWT |
| LLM Gateway | 8083 | OpenAI 兼容 API |
| Billing Service | 8084 | 积分查询、充值等 HTTP 接口 |
| Resume Agent | 8081 | 简历 CRUD、AI 对话 |

### gRPC 端口

| 服务 | gRPC 端口 | 调用方 |
|------|----------|--------|
| Billing Service | **9094** | LLM Gateway |
| User Service | **9092** | Resume Agent（校验 token） |

> ⚠️ 历史遗留混淆：llm-gateway.md 曾错误引用 9091，已修正为 9094。

### 数据库

| 服务 | 数据库名 |
|------|---------|
| User Service | `user_db` |
| Resume Agent | `resume_db` |
| LLM Gateway | `tadpoles_gateway` |
| Billing Service | `tadpoles_billing` |

---

## 4. 各服务设计状态

### 4.1 APISIX（API Gateway）

| 项目 | 状态 |
|------|------|
| JWT 验证插件 | ✅ 设计完成 |
| 用户级限流（limit-count + Redis） | ✅ 设计完成 |
| X-User-Id / X-User-Group Header 注入 | ✅ 设计完成 |
| 路由规则（/api/v1/user/*, /api/v1/resume/*） | ✅ 设计完成 |
| 静态资源直接返回（Vue 文件） | ✅ 设计完成 |

### 4.2 User Service

| 项目 | 状态 |
|------|------|
| 注册/登录/登出 | ✅ 设计完成 |
| JWT 双 Token（Access 1h + Refresh 30d） | ✅ 设计完成 |
| 邮箱验证（Tencent Cloud SES，1000 免费额度） | ✅ 设计完成 |
| Saga：注册时自动创建 Billing 钱包（失败→DELETE user→503） | ✅ 设计完成 |
| 用户 group（free/pro/enterprise）基于 JWT group claim | ✅ 设计完成 |

### 4.3 LLM Gateway Service

| 项目 | 状态 |
|------|------|
| OpenAI 兼容 API（/v1/chat/completions） | ✅ 设计完成 |
| 多厂商 Key 池管理 + 负载均衡 | ✅ 设计完成 |
| SSE 流式透传 | ✅ 设计完成 |
| PreAuth / Settle 计费流程（gRPC to Billing） | ✅ 设计完成 |
| Usage 计量 → Redis Streams → Billing 消费 | ✅ 设计完成 |
| 多轮 Agent 幂等 PreAuth（§9.5） | ✅ 设计完成（本轮新增） |
| X-User-Group Header 流向定义 | ✅ 设计完成（本轮补充） |
| credit_transactions.request_id 写入 | ✅ 设计完成（本轮补充） |

### 4.4 Billing Service

| 项目 | 状态 |
|------|------|
| 积分账本（user_credits 表） | ✅ 设计完成 |
| PreAuth / Settle / Rollback gRPC 接口 | ✅ 设计完成 |
| pre_auths 表（TTL=15min） | ✅ 设计完成（TTL 本轮修正） |
| credit_transactions 表 + request_id 字段 | ✅ 设计完成（本轮新增字段） |
| Redis Streams 消费（usage_events） | ✅ 设计完成 |
| 定时清理过期 PreAuth | ✅ 设计完成 |
| ERR_INSUFFICIENT_CREDITS HTTP 状态码 | ✅ 已修正为 402 |
| 赠送积分过期与 PreAuth 冻结竞态 | ✅ 设计完成（第三轮补充） |
| 优雅关闭（Graceful Shutdown） | ✅ 设计完成（第三轮补充） |

### 4.5 Resume Agent Service

| 项目 | 状态 |
|------|------|
| 简历 CRUD（create/update/list/delete） | ✅ 设计完成 |
| ReAct Agent（最多 5 轮工具调用） | ✅ 设计完成 |
| **5个 Agent 工具**（本轮精简，见 §5） | ✅ 设计完成 |
| 自动注入 System Prompt（每轮读 DB） | ✅ 设计完成（本轮新增） |
| modify_template 独立计费（sub-request-id） | ✅ 设计完成（本轮新增） |
| 并发锁 CAS（Redis SET NX EX 300 + Lua 解锁） | ✅ 设计完成（本轮修正） |
| SSE 错误事件格式 | ✅ 设计完成（本轮补充） |
| Handlebars.js 模板渲染（前端） | ✅ 设计完成 |
| html2pdf.js PDF 导出（前端，无后端参与） | ✅ 设计完成 |
| usage_logs.billing_tx_id 来源 | ✅ 从 Gateway SSE x_gateway 取 |
| ZSET 池启动初始化 | ✅ 设计完成（第三轮补充） |
| Settle gRPC 失败重试机制 | ✅ 设计完成（第三轮补充） |
| 流式中断 Token 计数 fallback | ✅ 设计完成（第三轮补充） |
| 优雅关闭（Graceful Shutdown） | ✅ 设计完成（第三轮补充） |

---

## 5. 关键架构决策日志

### 5.1 Agent 工具精简（13 → 5）

**决策**：去除 13 个细粒度工具，改为 5 个语义工具。

| 工具 | LLM调用 | 描述 |
|------|---------|------|
| `replace_section` | 无 | 替换整个章节（仅写 DB） |
| `replace_entry` | 无 | 替换指定 index 的单条记录（仅写 DB） |
| `modify_template` | 有（独立） | 自然语言→后端独立 LLM→存模板覆盖 |
| `analyze_jd` | 有 | 分析 JD，24h 缓存至 resumes.jd_analysis |
| `evaluate_resume` | 部分 | 规则+少量 LLM，替代原 score + suggestions |

**理由**：LLM 自身知道如何构造 JSON 数据，细粒度工具增加了工具数量和 context 复杂度，而 replace_section/replace_entry 让 LLM 构造完整数据后一次写入更高效。

### 5.2 自动注入 System Prompt（Auto-inject）

**决策**：每轮 Agent 调用前，Resume Agent 从 DB 读取 `resumes.content` 和 `resumes.jd_analysis`，动态填充 System Prompt 的 `{resume_content_json}` 和 `{jd_analysis_json}` 占位符，无需 read 类工具。

**理由**：避免工具调用 round-trip，减少 token 消耗；保证 LLM 始终看到最新数据；template HTML 不注入（太长），LLM 通过自然语言描述操作意图。

### 5.3 modify_template 独立计费

**决策**：`modify_template` 使用 `{original_request_id}-tpl` 作为独立 sub-request-id，完整走 PreAuth → 独立 LLM 调用 → Settle/Rollback 流程，与主对话账单分离。

**理由**：模板修改是独立 LLM 调用，成本独立，便于对账；失败时不影响主对话账单。

### 5.4 多轮 Agent 幂等 PreAuth

**决策**：同一个 `X-Request-Id` 的多次 PreAuth 调用，Billing 通过 `UNIQUE KEY uq_pa_request_id` 保证幂等——第 2 次及之后调用返回 `ALREADY_EXISTS`，复用已有 `auth_id`，不重复冻结积分。

**理由**：ReAct Agent 每轮工具调用后可能再次调用 LLM，无需也不应该每次重新冻结积分。

### 5.5 并发锁 CAS 设计

**决策**：
- 锁 key：`resume:chat:lock:{resume_id}`
- 锁 value：`{X-Request-Id}`（非固定常量）
- SET NX EX **300**（TTL 从 60s 提升到 300s，覆盖 5轮×120s）
- 解锁使用 Lua 原子脚本：只有持锁方才能删除

**理由**：防止非持锁方误删锁；TTL 需大于最坏情况总耗时（600s，设 300s + defer 兜底）。

### 5.6 前端渲染与 PDF 导出

**决策**：模板使用 Handlebars.js 在前端渲染（`{{placeholder}}` 替换），PDF 导出使用 html2pdf.js，完全在浏览器端完成，后端无需参与。

**理由**：WYSIWYG 导出效果一致，省去 headless Chrome 后端依赖，降低运维复杂度。

### 5.7 用户分组与模型路由

**决策**：用户 group（free/pro/enterprise）从 JWT Access Token 的 `group` claim 读取，由 APISIX JWT 插件注入为 `X-User-Group` Header，LLM Gateway 根据此 Header 决定路由到不同模型。

**注意**：充值后 group 升级最长有 1 小时延迟（JWT 过期后重签），这是已知风险，设计上接受。

---

## 6. 跨服务接口契约

### 6.1 Resume Agent → LLM Gateway（HTTP）

```
POST http://llm-gateway:8083/v1/chat/completions
Headers:
  Authorization: Bearer {internal-service-token}
  X-Request-Id: {ulid}          // 全链路追踪，PreAuth 幂等 key
  X-User-Id: {user_id}          // 从 APISIX 注入的 JWT claim
  X-User-Group: {group}         // free/pro/enterprise，用于模型路由
  X-Service-Name: resume-agent  // ⚠️ 必填，否则账单 service_name=NULL
```

### 6.2 LLM Gateway → Billing Service（gRPC:9094）

```protobuf
// PreAuth：冻结积分
PreAuth(request_id, user_id, estimated_credits, service_name) → auth_id
// 同一 request_id 多次调用幂等，返回 ALREADY_EXISTS + 复用 auth_id

// Settle：实际扣费
Settle(auth_id, actual_credits, request_id) → transaction_id

// Rollback：释放冻结
Rollback(auth_id) → void
```

### 6.3 Resume Agent → User Service（gRPC:9092）

用于内部 token 校验（服务间调用鉴权）。

### 6.4 SSE 错误事件格式（Resume Agent → 前端）

```
data: {"type":"error","code":"insufficient_credits","message":"积分不足，请充值后重试"}
data: {"type":"error","code":"rate_limited","message":"请求过于频繁，请稍后再试"}
data: {"type":"error","code":"upstream_timeout","message":"AI 响应超时，请重试"}
```

### 6.5 关键 HTTP 状态码约定

| 场景 | 状态码 | code 字段 |
|------|--------|----------|
| 积分不足 | **402** | `insufficient_credits` |
| 并发锁冲突 | 409 | `concurrent_request` |
| LLM 上游超时 | 504 | `upstream_timeout` |

---

## 7. 已修复的设计问题（本轮 Review）

> 详见 `docs/KNOWN_ISSUES.md` ✅ 区域

| # | 问题 | 涉及文档 | 修复内容 |
|---|------|---------|---------|
| 1 | Billing gRPC 端口混乱（9091/9094） | llm-gateway.md | 统一为 `billing-service:9094` |
| 2 | LLM Gateway URL 端口错误（8080→8083） | resume-agent.md | 改为 `http://llm-gateway:8083` |
| 3 | 积分不足状态码 429→402 | resume-agent.md | 统一改为 `402 insufficient_credits` |
| 4 | SSE error 事件格式未定义 | resume-agent.md | 补充 error 事件 JSON 格式 |
| 5 | 并发锁 TTL=60s < LLM 超时 120s | resume-agent.md | TTL 改为 300s；CAS 解锁（Lua + value=X-Request-Id） |
| 6 | PreAuth TTL=10min 无缓冲 | billing-service.md | 改为 15min（覆盖 5轮×120s+余量） |
| 7 | `modify_template` 失败无 Rollback | resume-agent.md | 补充 LLM失败/校验失败/DB失败三条路径的 Rollback |
| 8 | X-User-Group 流向各文档含糊 | llm-gateway.md, resume-agent.md | 明确：APISIX注入→Resume Agent读取→透传Gateway→仅做日志 |
| 9 | X-Service-Name 未标为必填 | resume-agent.md | §12.1 标为 ⚠️ 必填 |
| 10 | `credit_transactions` 无 `request_id` 字段 | billing-service.md | 添加字段、索引、GORM model、SettleRequest proto |
| 11 | 多轮 Agent 计费机制（is_continuation）未在 Gateway 定义 | llm-gateway.md | 新增 §9.5：同一 X-Request-Id 多次 PreAuth 幂等复用 auth_id |

---

## 8. 待决策事项（已全部决策 ✅）

| 项 | 决策 | 日期 |
|----|------|------|
| A. X-Session-Id | Resume Agent 传 `resume_id` 作为 `X-Session-Id` | 2026-04-11 |
| B. 积分不足状态码 | 统一 `402`，billing-service.md §11 已更新 | 2026-04-11 |
| C. modify_template PreAuth | 保持当前设计：工具调用时独立 PreAuth，失败由 LLM 向用户解释 | 2026-04-11 |
| D. billing_tx_id 来源 | 从 Gateway SSE 最后一帧 `x_gateway.billing_transaction_id` 取 | 2026-04-11 |

---

## 9. 后续迭代可补项

| # | 问题 | 建议 |
|---|------|------|
| E | billing-service.md gRPC 错误码区分不够细（积分不足 vs 钱包不存在 vs 钱包冻结） | 补充细化 gRPC Status 说明（FAILED_PRECONDITION 含三种原因） |
| F | billing-service.md 定时任务在多实例下的 leader election 未说明 | 当前用条件 UPDATE 抢占幂等，无需 leader election，文档应说明 |
| G | 全局降级策略缺失（Redis 不可用时 Resume Agent 行为） | Redis 不可用→跳过并发锁（降级允许并发）；Gateway 不可用→直接 503 不重试 |
| H | 用户充值后 group 升级最长 1 小时延迟的影响 | 已知风险，接受；前端充值成功后提示"模型升级最长 1 小时内生效" |
| I | `pre_auths.expires_at` 应基于 `PREAUTH_TTL_MINUTES` 动态配置 | 代码实现时从环境变量读取，不要硬编码 |
| J | resume-agent.md §13 billing-stats 响应格式与 billing-service.md §5.2 契约对比未校验 | 检查 `service_label`、`item_count`、`breakdown` 字段是否一致 |

---

## 10. 重大实现风险

### ⚠️ 风险 1：并发锁的 CAS 解锁必须用 Lua 原子脚本

```go
// ❌ 错误实现（非原子，有竞态）：
val := redis.Get(lockKey)
if val == lockID {
    redis.Del(lockKey)  // GET 和 DEL 之间可能被其他 goroutine 抢占
}

// ✅ 正确实现（Lua 原子脚本）：
const unlockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end`

defer func() {
    redis.Eval(ctx, unlockScript, []string{lockKey}, lockID)
}()
```

**后果**：若用非原子 GET+DEL，可能误删其他请求持有的锁，导致并发防护失效。

---

### ⚠️ 风险 2：`modify_template` 必须在所有失败路径调用 Rollback

三条失败路径均需 Rollback，缺少任何一条都会导致 `{request_id}-tpl` 的 PreAuth 积分永久冻结（最长 15min 后自动过期清理，但体验差）：

- LLM 调用失败 → Rollback(`{request_id}-tpl`)
- LLM 返回校验失败（非合法 HTML）→ Rollback(`{request_id}-tpl`)
- DB 写入失败 → Rollback(`{request_id}-tpl`)

---

### ⚠️ 风险 3：`X-Service-Name: resume-agent` 不传会导致账单对账差异

`credit_transactions.service_name` 为 NULL 的记录在对账时会出现差异，需人工排查。

**Resume Agent 所有 LLM Gateway 调用必须携带此 Header**，包括：主对话调用、`modify_template` 独立调用、`analyze_jd` 调用、`evaluate_resume` 中的 LLM 调用。

---

## 11. 文档索引

| 文档 | 路径 | 版本 | 说明 |
|------|------|------|------|
| 平台架构设计 | `docs/architecture.md` | v2.4 | 整体架构、服务拆分、部署方案 |
| 产品设计文档 | `docs/product-design.md` | — | 产品功能设计 |
| 技术方案文档 | `docs/tech-spec.md` | v1.0 | 技术选型（早期版本，以各服务文档为准） |
| User Service | `docs/services/user-service.md` | — | 注册/登录/JWT/邮箱验证 |
| LLM Gateway | `docs/services/llm-gateway.md` | — | OpenAI兼容API、计费流程 |
| Billing Service | `docs/services/billing-service.md` | — | 积分账本、PreAuth/Settle |
| Resume Agent | `docs/services/resume-agent.md` | v2.2 | Agent工具、AI对话流程、模板、计费 |
| 已知问题追踪 | `docs/KNOWN_ISSUES.md` | 2026-04-10 | 所有设计问题的状态追踪 |
| **本文档** | `docs/PROJECT_STATUS.md` | 2026-04-11 | 全局项目状态视图 |
