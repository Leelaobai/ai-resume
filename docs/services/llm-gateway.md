# LLM Gateway Service — 详细设计文档

> 版本：v1.2（参考 new-api 后简化：移除 abilities 路由表（过度设计），保留余额监控和 Tag；用户分组路由逻辑下沉至业务层）
> 最后更新：2026-04-09
> 服务端口：8083
> 数据库：tadpoles_gateway（MySQL）
> 依赖服务：Billing Service（gRPC）、Redis、MySQL

---

## 目录

1. [服务定位与能力边界](#1-服务定位与能力边界)
2. [对外接口（业务层调用）](#2-对外接口)
3. [Admin 管理接口](#3-admin-管理接口)
4. [数据库设计](#4-数据库设计)
5. [Redis 数据结构](#5-redis-数据结构)
6. [请求处理全链路](#6-请求处理全链路)
7. [熔断器状态机](#7-熔断器状态机)
8. [跨厂商故障切换](#8-跨厂商故障切换)
9. [积分预扣与结算](#9-积分预扣与结算)
10. [错误码规范](#10-错误码规范)
11. [环境变量配置](#11-环境变量配置)
12. [测试接口（Fake 模式）](#12-测试接口)

---

## 1. 服务定位与能力边界

### 1.1 定位

LLM Gateway 是平台统一的 AI 调用入口，对上游业务服务暴露 **OpenAI 兼容协议**，对下游屏蔽多厂商差异。所有 LLM 请求必须经过 Gateway，业务层不直接调用任何厂商 API。

```
Resume Agent / 未来其他 Agent
          │  OpenAI 兼容 HTTP
          ▼
    LLM Gateway（本服务）
     ├── Key Pool（ZSET调度）
     ├── Circuit Breaker（熔断器）
     ├── Pre-Auth + Settlement（积分扣费）
     └── Provider Adapters
          ├── OpenAI
          ├── Anthropic
          ├── DeepSeek
          └── ...（可扩展）
```

### 1.2 能力清单

| 能力 | 说明 |
|------|------|
| 统一 LLM 接口 | OpenAI 兼容 /v1/chat/completions、/v1/embeddings |
| 流式输出 | SSE 流式透传，支持 stream=true |
| 多厂商 Key 池 | 多厂商、每厂商多 Key，ZSET 原子调度 |
| RPM/TPM 感知 | 每 Key 每分钟请求数/Token 数限制，超限冷却 |
| 熔断器 | 每 Key 独立三态熔断（CLOSED→OPEN→HALF_OPEN） |
| 跨厂商切换 | 主厂商熔断后按优先级自动切备用厂商 |
| 积分预扣结算 | 调用前 Pre-Auth 冻结，调用后按实际用量结算 |
| Usage 计量 | 精确 Token 计数，事件发布到 Redis Streams |
| 详细日志 | 每次调用写 usage_logs，含延迟/Token/费用 |
| 模型配置 | model_configs 表管理模型与厂商映射、积分定价 |
| Admin API | 厂商/Key/模型 CRUD，用量统计，熔断手动干预 |

### 1.3 不负责的边界

- **用户认证**：由 APISIX jwt-auth 插件完成，Gateway 信任 X-User-Id 请求头
- **积分账户管理**：由 Billing Service 负责，Gateway 只调用 Pre-Auth/Settle 接口
- **业务逻辑**：简历内容处理、Agent 编排等，由 Resume Agent 负责
- **用户限流**：全局 RPM 限流由 APISIX limit-count 插件完成，Key 级限流由 Gateway 管理

---

## 2. 对外接口

对外接口供内部业务服务（Resume Agent 等）调用，**不对用户直接开放**。

### 2.1 通用请求头

所有对外接口均需携带以下请求头：

| Header | 必填 | 说明 |
|--------|------|------|
| `Authorization` | 是 | `Bearer {INTERNAL_SERVICE_TOKEN}`，服务间鉴权密钥 |
| `X-User-Id` | 是 | 调用方用户 ID（由 APISIX 从 JWT 解析后注入，Resume Agent 透传） |
| `X-User-Group` | 否 | 用户分组（由 APISIX 从 JWT group claim 注入，Resume Agent 透传）；Gateway 不用于路由决策，仅写入 usage_logs 供审计 |
| `X-Session-Id` | 否 | 会话 ID，用于计费记录关联（对话场景必填） |
| `X-Service-Name` | **强烈建议** | 调用方服务名，如 `resume-agent`；Gateway 原样透传至 Billing PreAuth/Settle，写入 `credit_transactions.service_name`，账单页和对账依赖此字段正确标注来源 |
| `X-Request-Id` | 是 | 全链路追踪 ID，由前端生成，经 APISIX → Resume Agent 透传；Gateway 用此值作为 Billing PreAuth 幂等键，保证链路可追溯 |

---

### 2.2 POST /v1/chat/completions

发起 LLM 对话请求，支持流式和非流式。

**Request Body**

```json
{
  "model": "gpt-4o",
  "messages": [
    {
      "role": "system",
      "content": "你是一个专业的简历优化助手..."
    },
    {
      "role": "user",
      "content": "帮我优化工作经历部分"
    },
    {
      "role": "assistant",
      "content": "好的，请提供您当前的工作经历..."
    },
    {
      "role": "tool",
      "tool_call_id": "call_abc123",
      "content": "{\"resume_section\": \"...\"}"
    }
  ],
  "stream": true,
  "max_tokens": 2000,
  "temperature": 0.7,
  "top_p": 1.0,
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "update_resume_section",
        "description": "更新简历指定章节内容",
        "parameters": {
          "type": "object",
          "properties": {
            "section": { "type": "string", "enum": ["basics","experience","education","skills"] },
            "content": { "type": "object" }
          },
          "required": ["section", "content"]
        }
      }
    }
  ],
  "tool_choice": "auto"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| model | string | 是 | 逻辑模型名，如 `gpt-4o`、`deepseek-chat`，Gateway 内部映射到厂商实际模型名 |
| messages | array | 是 | 对话历史，遵循 OpenAI messages 格式 |
| messages[].role | string | 是 | `system` / `user` / `assistant` / `tool` |
| messages[].content | string/array | 是 | 消息内容；vision 模型支持 array（含图片 URL） |
| messages[].tool_call_id | string | 条件必填 | role=tool 时必填 |
| messages[].tool_calls | array | 条件必填 | role=assistant 且有工具调用时有值 |
| stream | boolean | 否 | 默认 false；true 时返回 SSE 流 |
| max_tokens | integer | 否 | 默认使用模型 max_output_tokens；用于积分预扣估算 |
| temperature | float | 否 | 0.0~2.0，默认 1.0 |
| top_p | float | 否 | 默认 1.0 |
| tools | array | 否 | Function Calling 工具定义列表 |
| tool_choice | string/object | 否 | `none` / `auto` / `required` / 指定函数对象 |
| allow_provider_failover | boolean | 否 | 是否允许跨厂商故障切换，默认 `true`；传 `false` 时主厂商不可用直接返回 503，不尝试切换 |

**Response（非流式，stream=false）**

HTTP 200

```json
{
  "id": "chatcmpl-018f1a2b3c4d7e5fa6b7",
  "object": "chat.completion",
  "created": 1744166400,
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "以下是优化后的工作经历...",
        "tool_calls": null
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 523,
    "completion_tokens": 312,
    "total_tokens": 835
  },
  "x_gateway": {
    "provider_id": "deepseek",
    "key_prefix": "sk-abc12",
    "request_id": "018f1a2b-3c4d-7e5f-a6b7-c8d9e0f1a2b3",
    "latency_ms": 1834,
    "cost_credits": 18,
    "switched_provider": false
  }
}
```

| 响应字段 | 说明 |
|---------|------|
| id | Gateway 生成的请求 ID |
| choices[].finish_reason | `stop` / `length` / `tool_calls` / `content_filter` |
| usage.prompt_tokens | 实际输入 Token 数（厂商返回） |
| usage.completion_tokens | 实际输出 Token 数（厂商返回） |
| x_gateway.provider_id | 实际使用的厂商 ID |
| x_gateway.key_prefix | 使用的 Key 前缀（不含完整密钥） |
| x_gateway.cost_credits | 本次消耗积分数 |
| x_gateway.switched_provider | 是否触发了跨厂商切换 |

**Response（流式，stream=true）**

Content-Type: text/event-stream

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1744166400,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"以下"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1744166400,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"是优化"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1744166400,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":523,"completion_tokens":312,"total_tokens":835},"x_gateway":{"provider_id":"deepseek","cost_credits":18,"latency_ms":1834}}

data: [DONE]
```

> **注意**：usage 字段仅在最后一个非 `[DONE]` chunk 中出现，Gateway 汇总后注入。

**Tool Calls 流式示例**

```
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"update_resume_section","arguments":""}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"section\":"}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{...}}

data: [DONE]
```

**错误响应**

```json
{
  "error": {
    "code": "insufficient_credits",
    "message": "积分余额不足，请充值后继续使用",
    "request_id": "018f1a2b-3c4d-7e5f-a6b7-c8d9e0f1a2b3"
  }
}
```

| HTTP 状态码 | error.code | 触发场景 |
|------------|-----------|---------|
| 400 | `invalid_request` | 请求体格式错误、model 不存在 |
| 401 | `unauthorized` | INTERNAL_SERVICE_TOKEN 无效 |
| 402 | `insufficient_credits` | 积分余额不足，Pre-Auth 失败 |
| 429 | `rate_limited` | 用户级 RPM 超限（APISIX 层触发） |
| 503 | `no_available_key` | 所有 Key 均熔断或耗尽 |
| 503 | `provider_unavailable` | 所有厂商均不可用 |
| 504 | `upstream_timeout` | LLM 厂商超时（默认 120s） |
| 500 | `internal_error` | Gateway 内部错误 |

---

### 2.3 POST /v1/embeddings

生成文本向量嵌入（用于未来 RAG 模块，当前 Resume Agent 不调用）。

**Request Body**

```json
{
  "model": "text-embedding-3-small",
  "input": "北京大学计算机科学与技术专业",
  "encoding_format": "float"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| model | string | 是 | embedding 模型名 |
| input | string/array | 是 | 单个字符串或字符串数组（批量） |
| encoding_format | string | 否 | `float`（默认）或 `base64` |

**Response**

HTTP 200

```json
{
  "object": "list",
  "model": "text-embedding-3-small",
  "data": [
    {
      "object": "embedding",
      "index": 0,
      "embedding": [0.0023064255, -0.009327292, 0.015797347, "..."]
    }
  ],
  "usage": {
    "prompt_tokens": 12,
    "total_tokens": 12
  },
  "x_gateway": {
    "provider_id": "openai",
    "cost_credits": 1
  }
}
```

---

### 2.4 GET /v1/models

查询当前可用的模型列表（过滤 is_active=1 且所属厂商有健康 Key）。

**Response**

HTTP 200

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-4o",
      "object": "model",
      "display_name": "GPT-4o",
      "provider_id": "openai",
      "context_window": 128000,
      "max_output_tokens": 4096,
      "supports_streaming": true,
      "supports_tools": true,
      "supports_vision": true,
      "input_credits_per_1k": 25,
      "output_credits_per_1k": 100
    },
    {
      "id": "deepseek-chat",
      "object": "model",
      "display_name": "DeepSeek V3",
      "provider_id": "deepseek",
      "context_window": 64000,
      "max_output_tokens": 8000,
      "supports_streaming": true,
      "supports_tools": true,
      "supports_vision": false,
      "input_credits_per_1k": 1,
      "output_credits_per_1k": 3
    }
  ]
}
```

---

## 3. Admin 管理接口

Admin 接口通过独立 Token 鉴权，不经过 APISIX 的 jwt-auth 插件。

**Admin 鉴权 Header**

```
X-Admin-Token: {GATEWAY_ADMIN_TOKEN}
```

所有 Admin 接口返回统一格式：

```json
{
  "code": 0,
  "message": "ok",
  "data": { ... }
}
```

错误时：

```json
{
  "code": 40001,
  "message": "provider not found",
  "data": null
}
```

---

### 3.1 厂商管理（Provider）

#### GET /admin/v1/providers — 查询厂商列表

**Query Params**：无

**Response data**

```json
[
  {
    "id": "openai",
    "name": "OpenAI",
    "base_url": "https://api.openai.com/v1",
    "is_active": true,
    "priority": 10,
    "billing_currency": "USD",
    "key_count": 3,
    "healthy_key_count": 2,
    "created_at": "2026-04-01T10:00:00.000Z",
    "updated_at": "2026-04-09T08:00:00.000Z"
  }
]
```

#### POST /admin/v1/providers — 新增厂商

**Request Body**

```json
{
  "id": "anthropic",
  "name": "Anthropic",
  "base_url": "https://api.anthropic.com/v1",
  "priority": 20,
  "billing_currency": "USD"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 唯一标识，如 openai/anthropic/deepseek，创建后不可修改 |
| name | string | 是 | 显示名称 |
| base_url | string | 是 | OpenAI 兼容 API Base URL |
| priority | integer | 否 | 默认 100，数字越小优先级越高 |
| billing_currency | string | 否 | 计费货币：`CNY`（默认）或 `USD` |

**Response data**：创建的 provider 对象

#### PATCH /admin/v1/providers/{id} — 修改厂商

**Request Body**（仅含需要修改的字段）

```json
{
  "name": "OpenAI（国内镜像）",
  "base_url": "https://openai-mirror.example.com/v1",
  "priority": 5
}
```

| 字段 | 类型 | 可修改 | 说明 |
|------|------|--------|------|
| name | string | 是 | |
| base_url | string | 是 | |
| priority | integer | 是 | |
| billing_currency | string | 是 | 修改后影响后续余额查询的货币单位展示 |
| is_active | boolean | **否**（用 enable/disable 接口）| |

#### DELETE /admin/v1/providers/{id} — 删除厂商（软删除）

前置检查：该厂商下有 active 状态的 Key 时拒绝删除，返回 409。

**Response data**：null

#### POST /admin/v1/providers/{id}/enable — 启用厂商

将 `is_active` 设为 true，并将该厂商下所有 `status=active` 的 Key 重新加入 ZSET。

#### POST /admin/v1/providers/{id}/disable — 禁用厂商

将 `is_active` 设为 false，并从 ZSET 中移除该厂商所有 Key，正在进行的请求不受影响（完成后不再调度）。

---

### 3.2 API Key 管理

#### GET /admin/v1/keys — 查询 Key 列表

**Query Params**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| provider_id | string | 否 | 过滤指定厂商 |
| status | string | 否 | `active`/`paused`/`depleted` |
| circuit_state | string | 否 | `closed`/`open`/`half_open` |
| page | integer | 否 | 默认 1 |
| page_size | integer | 否 | 默认 20，最大 100 |

**Response data**

```json
{
  "total": 5,
  "page": 1,
  "page_size": 20,
  "items": [
    {
      "id": "018f1a2b-3c4d-7e5f-a6b7-c8d9e0f1a2b3",
      "provider_id": "openai",
      "key_prefix": "sk-abc12",
      "alias": "主力Key-1",
      "status": "active",
      "circuit_state": "closed",
      "rpm_limit": 500,
      "tpm_limit": 100000,
      "daily_budget_amount": 1000,
      "consecutive_errors": 0,
      "total_requests": 12483,
      "total_tokens": 8234123,
      "total_cost_amount": 24680,
      "last_used_at": "2026-04-09T09:58:00.000Z",
      "created_at": "2026-04-01T10:00:00.000Z"
    }
  ]
}
```

#### POST /admin/v1/keys — 新增 Key

**Request Body**

```json
{
  "provider_id": "openai",
  "api_key": "sk-abcdefghijklmnopqrstuvwxyz123456",
  "alias": "备用Key-2",
  "tag": "production",
  "rpm_limit": 500,
  "tpm_limit": 100000,
  "daily_budget_amount": 500
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| provider_id | string | 是 | 所属厂商 ID；Key 会被加入该厂商的 ZSET 调度池，自动服务该厂商下所有模型 |
| api_key | string | 是 | 完整 API Key 明文，Gateway 加密后存储，不返回原文 |
| alias | string | 否 | 备注别名 |
| tag | string | 否 | 分组标签，如 production/test/backup，用于批量管理 |
| rpm_limit | integer | 否 | 每分钟请求上限，NULL 表示不限 |
| tpm_limit | integer | 否 | 每分钟 Token 上限，NULL 表示不限 |
| daily_budget_amount | integer | 否 | 每日费用上限（厂商计费货币最小单位，如分），NULL 表示不限 |

**Response data**：创建的 key 对象（不含 api_key 明文，只返回 key_prefix）

#### PATCH /admin/v1/keys/{id} — 修改 Key

```json
{
  "alias": "修改后的备注",
  "rpm_limit": 1000,
  "tpm_limit": 200000,
  "daily_budget_amount": 2000
}
```

> api_key（密钥本身）不支持修改，如需更换请删除后重新添加。

#### DELETE /admin/v1/keys/{id} — 删除 Key（软删除）

同时从 Redis ZSET 池中移除，从 circuit:open SET 中移除。

#### POST /admin/v1/keys/{id}/pause — 暂停 Key

将 status 置为 `paused`，从 ZSET 池移除，不参与调度。

#### POST /admin/v1/keys/{id}/resume — 恢复 Key

将 status 置为 `active`，重新加入 ZSET 池（score=0，立即可用）。

#### POST /admin/v1/keys/batch — 按 Tag 批量操作

```json
{
  "tag": "test",
  "action": "pause"
}
```

| action 值 | 说明 |
|-----------|------|
| `pause` | 批量暂停该 tag 下所有 Key |
| `resume` | 批量恢复该 tag 下所有 Key |
| `circuit_reset` | 批量重置该 tag 下所有 Key 的熔断器 |

**Response data**：`{ "affected": 3 }`

#### GET /admin/v1/keys/{id}/balance — 主动刷新厂商余额

触发一次对该 Key 所属厂商的余额查询（调厂商 API），结果写入 `api_keys.provider_balance`。

**Response data**

```json
{
  "key_id": "018f...",
  "provider_id": "deepseek",
  "billing_currency": "CNY",
  "provider_balance": 88.50,
  "queried_at": "2026-04-09T10:00:00.000Z"
}
```

> 注意：不是所有厂商都提供余额查询 API，不支持时返回 `{ "provider_balance": null, "message": "provider not supported" }`

#### POST /admin/v1/keys/{id}/circuit/reset — 手动重置熔断器

强制将熔断器状态置为 `CLOSED`：

- 清空 `gateway:circuit:open` SET 中的该 Key
- Redis HASH `gateway:key:{id}:stats` 中 `consecutive_errors` 清零
- DB 更新 `circuit_state='closed'`, `consecutive_errors=0`
- 重新加入 ZSET 池

---

### 3.3 模型配置管理

#### GET /admin/v1/models — 查询模型列表

**Query Params**

| 参数 | 说明 |
|------|------|
| provider_id | 过滤指定厂商 |
| is_active | `true`/`false` |

**Response data**

```json
[
  {
    "id": "gpt-4o",
    "provider_id": "openai",
    "provider_model": "gpt-4o",
    "display_name": "GPT-4o",
    "context_window": 128000,
    "max_output_tokens": 4096,
    "supports_streaming": true,
    "supports_tools": true,
    "supports_vision": true,
    "input_credits_per_1k": 25,
    "output_credits_per_1k": 100,
    "is_active": true,
    "sort_order": 1
  }
]
```

#### POST /admin/v1/models — 新增模型配置

```json
{
  "id": "deepseek-reasoner",
  "provider_id": "deepseek",
  "provider_model": "deepseek-reasoner",
  "display_name": "DeepSeek R1",
  "context_window": 64000,
  "max_output_tokens": 32000,
  "supports_streaming": true,
  "supports_tools": false,
  "supports_vision": false,
  "input_credits_per_1k": 5,
  "output_credits_per_1k": 20,
  "sort_order": 3
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 逻辑模型名，业务层使用此 ID，创建后不可修改 |
| provider_id | string | 是 | 所属厂商 |
| provider_model | string | 是 | 厂商侧实际模型名（调用时传给厂商 API） |
| display_name | string | 是 | 前端显示名 |
| context_window | integer | 是 | 最大上下文 Token 数 |
| max_output_tokens | integer | 是 | 最大输出 Token 数 |
| supports_streaming | boolean | 否 | 默认 true |
| supports_tools | boolean | 否 | 默认 true |
| supports_vision | boolean | 否 | 默认 false |
| supports_prompt_cache | boolean | 否 | 默认 false，是否支持 Prompt Cache 计费 |
| input_credits_per_1k | integer | 是 | 正常输入 Token 单价（缓存未命中，积分/千Token） |
| output_credits_per_1k | integer | 是 | 输出 Token 单价（积分/千Token） |
| cached_input_credits_per_1k | integer | 否 | 缓存命中输入 Token 单价；NULL=不支持缓存计费 |
| cache_write_credits_per_1k | integer | 否 | 缓存写入 Token 单价（Anthropic）；NULL=无此项 |
| sort_order | integer | 否 | 排序权重，默认 0 |

#### PATCH /admin/v1/models/{id}/{provider_id} — 修改模型配置

可修改字段：display_name、context_window、max_output_tokens、supports_*、credits_per_1k、is_active、sort_order。

不可修改：id（模型标识）、provider_id（联合主键，变更等于新增一条记录）、provider_model。

#### DELETE /admin/v1/models/{id}/{provider_id} — 下架模型（软删除）

仅下架指定厂商的该模型；若该逻辑模型 id 在其他厂商下仍有配置，不影响其他厂商的路由。

---

### 3.4 用量统计查询

#### GET /admin/v1/stats/overview — 整体概览

**Response data**

```json
{
  "period": "today",
  "total_requests": 4821,
  "success_requests": 4783,
  "error_requests": 38,
  "total_prompt_tokens": 12483920,
  "total_completion_tokens": 3821034,
  "total_cost_provider_amount": 38291,
  "avg_latency_ms": 1823,
  "provider_breakdown": [
    {
      "provider_id": "deepseek",
      "billing_currency": "CNY",
      "requests": 4501,
      "cost_provider_amount": 5021
    },
    {
      "provider_id": "openai",
      "billing_currency": "USD",
      "requests": 320,
      "cost_provider_amount": 33270
    }
  ],
  "key_health": [
    {
      "key_id": "018f...",
      "key_prefix": "sk-abc12",
      "provider_id": "openai",
      "circuit_state": "closed",
      "consecutive_errors": 0
    }
  ]
}
```

#### GET /admin/v1/stats/usage — 用量明细查询

**Query Params**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| start | string | 是 | 开始时间，ISO8601，如 `2026-04-01T00:00:00Z` |
| end | string | 是 | 结束时间 |
| provider_id | string | 否 | 过滤指定厂商 |
| user_id | string | 否 | 过滤指定用户 |
| model | string | 否 | 过滤指定模型 |
| status | string | 否 | `success`/`error`/`timeout` |
| page | integer | 否 | 默认 1 |
| page_size | integer | 否 | 默认 50，最大 200 |

**Response data**

```json
{
  "total": 4821,
  "page": 1,
  "page_size": 50,
  "items": [
    {
      "id": "018f...",
      "api_key_prefix": "sk-abc12",
      "provider_id": "deepseek",
      "model": "deepseek-chat",
      "caller_user_id": "018e...",
      "caller_service": "resume-agent",
      "request_id": "018f...",
      "prompt_tokens": 523,
      "completion_tokens": 312,
      "cost_provider_amount": 3,
      "latency_ms": 1834,
      "status": "success",
      "error_code": null,
      "created_at": "2026-04-09T09:58:00.000Z"
    }
  ]
}
```

---

## 4. 数据库设计

数据库：`tadpoles_gateway`

### 4.1 providers — 厂商配置表

```sql
CREATE TABLE providers (
    id          VARCHAR(50)  NOT NULL                     COMMENT '厂商唯一标识，如 openai/anthropic/deepseek，创建后不可修改',
    name        VARCHAR(100) NOT NULL                     COMMENT '显示名称，如 OpenAI / 深度求索',
    base_url    VARCHAR(500) NOT NULL                     COMMENT 'OpenAI 兼容 API Base URL，如 https://api.openai.com/v1',
    is_active        TINYINT(1)   NOT NULL DEFAULT 1      COMMENT '是否启用：0=禁用（所有 Key 从调度池移除）1=启用',
    priority         INT          NOT NULL DEFAULT 100     COMMENT '故障切换优先级，数值越小越优先（如主力=10，备用=50）',
    billing_currency ENUM('CNY','USD') NOT NULL DEFAULT 'CNY'
                                                          COMMENT '厂商计费货币：CNY=人民币（元/分）USD=美元（元/美分）',
    created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                          COMMENT '创建时间',
    updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                             ON UPDATE CURRENT_TIMESTAMP(3)
                                                          COMMENT '更新时间',
    deleted_at  DATETIME(3)                               COMMENT '软删除时间',
    PRIMARY KEY (id),
    INDEX idx_providers_active   (is_active, priority),
    INDEX idx_providers_deleted  (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='LLM 厂商配置表，记录平台接入的所有 AI 服务提供商';
```

**初始数据**

```sql
INSERT INTO providers (id, name, base_url, priority, billing_currency) VALUES
('deepseek',  'DeepSeek', 'https://api.deepseek.com/v1',  10, 'CNY'),
('openai',    'OpenAI',   'https://api.openai.com/v1',    20, 'USD'),
('anthropic', 'Anthropic','https://api.anthropic.com/v1', 30, 'USD');
```

---

### 4.2 api_keys — API Key 管理表

```sql
CREATE TABLE api_keys (
    id                     CHAR(36)     NOT NULL                COMMENT 'UUID v7 主键',
    provider_id            VARCHAR(50)  NOT NULL                COMMENT '所属厂商 ID，关联 providers.id',
    key_encrypted          BLOB         NOT NULL                COMMENT 'AES-256-GCM 加密存储的完整 API Key（含 nonce）',
    key_prefix             VARCHAR(12)  NOT NULL                COMMENT 'Key 明文前缀（如 sk-abc12，前8位），用于日志排查',
    alias                  VARCHAR(100)                         COMMENT '人工备注别名',
    status                 ENUM('active','paused','depleted')
                           NOT NULL DEFAULT 'active'            COMMENT 'active=正常 | paused=手动暂停 | depleted=配额耗尽',
    rpm_limit              INT                                  COMMENT '每分钟最大请求数，NULL=不限',
    tpm_limit              INT                                  COMMENT '每分钟最大 Token 数，NULL=不限',
    daily_budget_amount    BIGINT                               COMMENT '每日费用上限（厂商计费货币最小单位，如分），NULL=不限',
    consecutive_errors     INT          NOT NULL DEFAULT 0      COMMENT '连续错误计数，达到阈值触发熔断',
    circuit_state          ENUM('closed','open','half_open')
                           NOT NULL DEFAULT 'closed'            COMMENT '熔断器状态：closed=正常 | open=熔断 | half_open=探测中',
    circuit_opened_at      DATETIME(3)                          COMMENT '熔断触发时间，用于计算 OPEN→HALF_OPEN 超时',
    last_error_at          DATETIME(3)                          COMMENT '最后一次报错时间',
    last_used_at           DATETIME(3)                          COMMENT '最后一次被调度时间',
    -- 厂商账户余额（定时查询厂商 Balance API 写入，用于余额告警）
    provider_balance       DECIMAL(12,4)                        COMMENT '厂商侧账户实际余额（厂商计费货币，单位：元），NULL=未查询或不支持',
    provider_balance_at    DATETIME(3)                          COMMENT '余额最后查询时间',
    -- 用量统计（定时从 Redis 同步到 DB）
    total_requests         BIGINT       NOT NULL DEFAULT 0      COMMENT '历史总请求数（定时从 Redis 同步）',
    total_tokens           BIGINT       NOT NULL DEFAULT 0      COMMENT '历史总 Token 数',
    total_cost_amount      BIGINT       NOT NULL DEFAULT 0      COMMENT '历史总费用（厂商计费货币最小单位，如分）',
    -- 标签（用于批量管理，如按 tag 批量启停）
    tag                    VARCHAR(100)                         COMMENT '分类标签，如 production/test/backup，支持按 tag 批量操作',
    created_at             DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                                COMMENT '录入时间',
    updated_at             DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                           ON UPDATE CURRENT_TIMESTAMP(3)       COMMENT '最后更新时间',
    deleted_at             DATETIME(3)                          COMMENT '软删除时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_key_provider FOREIGN KEY (provider_id) REFERENCES providers(id),
    INDEX idx_keys_provider (provider_id, status),
    INDEX idx_keys_circuit  (circuit_state),
    INDEX idx_keys_tag      (tag),
    INDEX idx_keys_deleted  (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='LLM API Key 管理表，含多厂商 Key 池、熔断状态、余额监控、用量统计';
```

---

### 4.3 model_configs — 模型配置表

```sql
CREATE TABLE model_configs (
    id                    VARCHAR(100) NOT NULL               COMMENT '逻辑模型 ID（业务层使用），如 gpt-4o/deepseek-chat',
    provider_id           VARCHAR(50)  NOT NULL               COMMENT '所属厂商 ID，关联 providers.id',
    provider_model        VARCHAR(100) NOT NULL               COMMENT '厂商实际模型名（调用厂商 API 时使用）',
    display_name          VARCHAR(100) NOT NULL               COMMENT '前端展示名称',
    context_window        INT          NOT NULL               COMMENT '最大上下文 Token 数（含输入和输出）',
    max_output_tokens     INT          NOT NULL               COMMENT '单次最大输出 Token 数',
    supports_streaming    TINYINT(1)   NOT NULL DEFAULT 1     COMMENT '是否支持流式输出',
    supports_tools        TINYINT(1)   NOT NULL DEFAULT 1     COMMENT '是否支持 Function Calling/Tools',
    supports_vision            TINYINT(1)   NOT NULL DEFAULT 0  COMMENT '是否支持图片输入（Vision）',
    supports_prompt_cache      TINYINT(1)   NOT NULL DEFAULT 0  COMMENT '是否支持 Prompt Cache（各厂商实现不同）：0=不支持 1=支持',
    -- 积分定价（单位：积分/千Token）
    input_credits_per_1k       INT          NOT NULL             COMMENT '正常输入 Token 单价（缓存未命中）',
    output_credits_per_1k      INT          NOT NULL             COMMENT '输出 Token 单价',
    cached_input_credits_per_1k INT                              COMMENT '缓存命中输入 Token 单价（通常远低于正常价）；NULL=该模型不支持缓存计费',
    cache_write_credits_per_1k  INT                              COMMENT '缓存写入 Token 单价（仅 Anthropic，写入时额外收费）；NULL=无此项',
    is_active             TINYINT(1)   NOT NULL DEFAULT 1        COMMENT '是否对业务层开放：0=下架 1=上架',
    sort_order            INT          NOT NULL DEFAULT 0        COMMENT '列表排序，数字越小越靠前',
    created_at            DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                              COMMENT '创建时间',
    updated_at            DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                          ON UPDATE CURRENT_TIMESTAMP(3)      COMMENT '更新时间',
    deleted_at            DATETIME(3)                         COMMENT '软删除时间',
    PRIMARY KEY (id, provider_id),          -- 联合主键：同一逻辑模型可配置多个厂商实现（用于跨厂商故障切换）
    CONSTRAINT fk_model_provider FOREIGN KEY (provider_id) REFERENCES providers(id),
    INDEX idx_model_active  (is_active, sort_order),
    INDEX idx_model_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='模型配置表，管理逻辑模型名→厂商模型名映射、积分定价（含缓存命中/写入分段定价）。同一逻辑模型 id 可对应多个厂商，主键为 (id, provider_id)';

**用户分组与模型选择说明**

Gateway 不参与"哪个用户用哪个模型"的决策，这是业务层的责任。Resume Agent 根据用户分组选好模型后直接告诉 Gateway 用哪个，Gateway 只管找 Key、调厂商。

```
Resume Agent 环境变量配置:
  MODEL_FOR_FREE=deepseek-chat      # 免费用户
  MODEL_FOR_DEFAULT=deepseek-chat   # 普通用户
  MODEL_FOR_PREMIUM=gpt-4o          # 付费用户

Resume Agent 调用逻辑:
  model := config.ModelForGroup(user.Group)   // 根据 group 取对应模型
  gateway.ChatCompletions(model, messages...) // 直接传给 Gateway
```

---

### 4.5 usage_logs — 调用明细日志表

```sql
CREATE TABLE usage_logs (
    id                CHAR(36)     NOT NULL                     COMMENT 'UUID v7 主键',
    api_key_id        CHAR(36)     NOT NULL                     COMMENT '本次使用的 API Key ID',
    provider_id       VARCHAR(50)  NOT NULL                     COMMENT '厂商 ID',
    model             VARCHAR(100) NOT NULL                     COMMENT '逻辑模型 ID（业务层传入的）',
    provider_model    VARCHAR(100) NOT NULL                     COMMENT '实际调用的厂商模型名',
    caller_user_id    CHAR(36)                                  COMMENT '调用方用户 ID（X-User-Id header）',
    caller_service    VARCHAR(50)                               COMMENT '调用方服务名（X-Service-Name header）',
    session_id        CHAR(36)                                  COMMENT '会话 ID（X-Session-Id header）',
    request_id        CHAR(36)     NOT NULL                     COMMENT '全链路追踪 ID',
    -- Token 用量明细（从厂商响应解析，各厂商字段名不同，Gateway 适配层统一转换）
    prompt_tokens       INT          NOT NULL                   COMMENT '总输入 Token 数（厂商返回，含缓存命中部分）',
    completion_tokens   INT          NOT NULL                   COMMENT '输出 Token 数（厂商返回）',
    cached_tokens       INT          NOT NULL DEFAULT 0         COMMENT '命中缓存的输入 Token 数（OpenAI: prompt_tokens_details.cached_tokens；DeepSeek: prompt_cache_hit_tokens；不支持缓存则为0）',
    cache_write_tokens  INT          NOT NULL DEFAULT 0         COMMENT '写入缓存的 Token 数（Anthropic: cache_creation_input_tokens；其他厂商为0）',
    -- 费用（基于实际 Token 分段定价计算，非简单估算）
    cost_credits          INT          NOT NULL                 COMMENT '本次实际消耗积分数（按缓存命中/未命中分段计算）',
    cost_provider_amount  INT          NOT NULL                 COMMENT '本次实际费用（厂商计费货币最小单位，如分；按厂商定价分段计算，用于平台成本核算）',
    latency_ms        INT          NOT NULL                     COMMENT '端到端总耗时（ms），从收到请求到返回最后一个字节（流式为最后一个 SSE chunk）',
    ttft_ms           INT                                       COMMENT '首 Token 延迟（ms），从收到请求到发出第一个有内容的 SSE chunk；非流式请求为 NULL',
    status            ENUM('success','error','timeout') NOT NULL COMMENT '调用结果',
    error_code        VARCHAR(50)                               COMMENT '错误码，如 rate_limit/server_error/context_length，成功时为 NULL',
    switched_provider       TINYINT(1)   NOT NULL DEFAULT 0      COMMENT '是否触发了跨厂商切换：0=否 1=是',
    billing_transaction_id  CHAR(36)                             COMMENT '关联 credit_transactions.id，Billing Settle 成功后写入；NULL 表示结算失败，需人工对账',
    created_at        DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                                                COMMENT '请求时间，append-only 永不修改',
    PRIMARY KEY (id),
    INDEX idx_usage_key     (api_key_id, created_at),
    INDEX idx_usage_user    (caller_user_id, created_at),
    INDEX idx_usage_session (session_id),
    INDEX idx_usage_model   (model, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='LLM 调用明细日志，append-only，用于用量分析、计费对账和 Key 健康监控';
```

---

## 5. Redis 数据结构

### 5.1 Key 调度池（ZSET）

```
Key:     gateway:pool:{provider_id}
Type:    Sorted Set
Member:  {key_id}（CHAR(36) UUID）
Score:   available_at（Unix 毫秒时间戳）
         Score = 0 → 立即可用
         Score = future → 正在冷却（限流惩罚期）
TTL:     无（持久化）

操作语义:
  调度（取出最早可用的 Key）:
    ZPOPMIN gateway:pool:{provider_id} 1
    → 返回 [{key_id, score}]
    → 若 score > NOW()ms：Key 仍在冷却，还回并返回"无可用Key"

  归还（调用成功）:
    ZADD gateway:pool:{provider_id} 0 {key_id}
    → score=0，立即参与下次调度

  归还（触发限流 429）:
    ZADD gateway:pool:{provider_id} {NOW()+60000} {key_id}
    → 冷却 60 秒后再参与调度

  归还（服务端错误 5xx）:
    ZADD gateway:pool:{provider_id} {NOW()+30000} {key_id}
    → 冷却 30 秒

  移除（Key 暂停/删除/熔断）:
    ZREM gateway:pool:{provider_id} {key_id}

并发正确性：ZPOPMIN 是原子操作，100 个并发 goroutine 各自拿到不同 Key，天然无锁。
```

### 5.2 熔断器（SET）

```
Key:     gateway:circuit:open
Type:    Set
Member:  {key_id}

操作语义:
  开熔断（CLOSED → OPEN）:
    SADD gateway:circuit:open {key_id}

  检查是否熔断:
    SISMEMBER gateway:circuit:open {key_id}
    → 1 = 熔断中，拒绝此 Key，选下一个

  关熔断（OPEN → HALF_OPEN → CLOSED）:
    SREM gateway:circuit:open {key_id}

说明：
  · 熔断期间 Key 仍在 ZSET 中（score=future），双重过滤：
    先 ZPOPMIN 拿到 Key，再 SISMEMBER 检查是否熔断
  · HALF_OPEN 状态：Key 从 circuit:open 移除，允许一次探测请求
    成功→正式 CLOSED；失败→重新 SADD 回去并延长冷却
```

### 5.3 Key 错误统计（HASH）

```
Key:     gateway:key:{key_id}:stats
Type:    Hash
TTL:     无（持久化，由熔断器重置）

Fields:
  consecutive_errors  INT    连续错误计数，成功时清零
  circuit_opened_at   INT    熔断触发时间（Unix 毫秒），用于判断是否到 HALF_OPEN 时间
  last_error_ms       INT    最后一次错误的 Unix 毫秒时间戳

操作:
  错误时:   HINCRBY gateway:key:{id}:stats consecutive_errors 1
             HSET    gateway:key:{id}:stats last_error_ms {NOW()}
  成功时:   HSET    gateway:key:{id}:stats consecutive_errors 0
  熔断时:   HSET    gateway:key:{id}:stats circuit_opened_at {NOW()}
```

### 5.4 RPM 限流计数器

```
Key:     gateway:key:{key_id}:rpm:{minute_bucket}
Type:    String（计数器）
Value:   本分钟桶内的请求次数
TTL:     120 秒（2倍窗口，保证旧桶能被自然清理）

minute_bucket = floor(unix_ms / 60000)（分钟级时间桶）

操作流程（请求前检查）:
  current_bucket  = floor(NOW() / 60000)
  previous_bucket = current_bucket - 1

  current_count  = GET gateway:key:{id}:rpm:{current_bucket}  or 0
  previous_count = GET gateway:key:{id}:rpm:{previous_bucket} or 0
  elapsed_ratio  = (NOW() % 60000) / 60000  -- 当前分钟已过去的比例

  -- 滑动窗口估算
  sliding_count = previous_count * (1 - elapsed_ratio) + current_count

  if sliding_count >= rpm_limit:
    → 此 Key 触发限流，返回 ZSET 并加冷却分数

  else:
    INCR gateway:key:{id}:rpm:{current_bucket}
    EXPIRE gateway:key:{id}:rpm:{current_bucket} 120
```

### 5.5 TPM 限流计数器

```
Key:     gateway:key:{key_id}:tpm:{minute_bucket}
Type:    String（计数器）
Value:   本分钟桶内的 Token 消耗数
TTL:     120 秒

操作：同 RPM，但在请求完成后才 INCRBY actual_tokens，
      预检时用 prompt_tokens + max_tokens 做保守估算。
```

### 5.6 API Key 日消费计数器

```
Key:     gateway:key:{key_id}:daily_cost:{date}
Type:    String（计数器）
Value:   当日累计厂商费用（厂商计费货币最小单位，如分）
TTL:     25 小时（保证跨日后自动清理，25h > 24h 留余量）

date = UTC 日期字符串，如 "2026-04-10"

操作流程：
  请求前检查（有 daily_budget_amount 时）:
    current = GET gateway:key:{key_id}:daily_cost:{today}  or 0
    if current >= daily_budget_amount:
      → 此 Key 日预算耗尽，标记为 depleted，移出 ZSET

  请求完成后更新:
    INCRBY gateway:key:{key_id}:daily_cost:{today} {cost_provider_amount}
    SET EX 25h（若 Key 不存在则顺带设置 TTL）
```

> **精度说明**：请求前 GET 与请求后 INCRBY 不是原子操作，可能轻微超出日预算（约一次最大请求的费用）。日预算是运营控制手段，非财务硬约束，此精度可接受。

### 5.7 In-flight 请求跟踪（HASH）

```
Key:     gateway:inflight:{request_id}
Type:    Hash
TTL:     300 秒（5分钟，超过最长 LLM 响应时间即视为超时）

Fields:
  user_id          CHAR(36)   发起请求的用户 ID
  estimated_cost   INT        预扣的积分数（用于异常时释放）
  key_id           CHAR(36)   正在使用的 Key ID（用于异常时归还）
  provider_id      VARCHAR    正在使用的厂商 ID
  started_at       INT        Unix 毫秒，用于延迟统计

生命周期:
  请求开始 → SET（含 TTL）
  请求完成 → DEL
  对账 Job → 扫描超过 300s 未删除的 key → 释放 Pre-Auth reservation
```

### 5.8 Usage 事件流（Redis Streams）

```
Key:     gateway:stream:usage_events
Type:    Stream
Group:   billing-consumer-group（Billing Service 消费）

每条事件字段:
  version           "1"         事件版本，向后兼容用
  request_id        CHAR(36)    全链路追踪 ID
  user_id           CHAR(36)    用户 ID
  session_id        CHAR(36)    会话 ID（可为空）
  model             VARCHAR     逻辑模型 ID
  provider_id       VARCHAR     实际使用厂商
  key_id            CHAR(36)    使用的 Key ID
  prompt_tokens     INT         输入 Token 数
  completion_tokens INT         输出 Token 数
  cost_credits          INT         消耗积分
  cost_provider_amount  INT         厂商实际费用（计费货币最小单位，如分）
  latency_ms        INT         端到端总耗时（ms）
  ttft_ms           INT         首 Token 延迟（ms），非流式为 0
  status            VARCHAR     success/error/timeout
  ts                INT         Unix 毫秒时间戳

发布时机：请求完成后（成功或失败）异步发布，不阻塞响应链路。
保留策略：MAXLEN ~ 100000（约保留最近 10 万条，Billing 消费后 ACK）
```

### 5.9 启动初始化（Redis 状态重建）

Gateway 启动时必须从 DB 重建 Redis 调度状态，确保 Redis 重启或数据丢失后可恢复。

```
启动流程：
  1. 读取所有活跃厂商
     SELECT * FROM providers WHERE is_active=1 AND deleted_at IS NULL

  2. 对每个厂商，清空旧 ZSET 并重建
     DEL gateway:pool:{provider_id}         // 清空可能残留的旧数据
     SELECT * FROM api_keys
       WHERE provider_id=? AND status='active' AND deleted_at IS NULL
     对每个 key：
       ZADD gateway:pool:{provider_id} 0 {key_id}   // score=0 立即可用

  3. 重建熔断器状态
     DEL gateway:circuit:open               // 清空旧数据
     SELECT id, circuit_state, circuit_opened_at FROM api_keys
       WHERE circuit_state='open' AND status='active' AND deleted_at IS NULL
     对每个熔断中的 key：
       SADD gateway:circuit:open {key_id}
       HSET gateway:key:{key_id}:stats circuit_opened_at {circuit_opened_at_ms}

  4. 加载 model_configs 内存缓存
     SELECT * FROM model_configs WHERE is_active=1 AND deleted_at IS NULL
     构建 map[modelID][]ModelConfig（同一逻辑模型可能有多个厂商实现）

  5. 启动后台 goroutine
     - Settle 重试 worker（每 5s，见 §9.7）
     - In-flight 对账 Job（每 5min，见 §9.6）
     - model_configs 缓存刷新（每 5min）
```

> **为什么先 DEL 再全量重建？** Gateway 重启时 Redis 可能有上次运行的残留数据（如已删除的 Key 仍在 ZSET 中），全量重建保证一致性。DEL + ZADD 在启动时一次性执行，耗时可忽略。

---

## 6. 请求处理全链路

### 6.1 非流式请求（stream=false）

```
① 接收请求
   POST /v1/chat/completions
   验证 Authorization: Bearer {INTERNAL_SERVICE_TOKEN}
   提取 X-User-Id、X-Session-Id、X-Request-Id

② 参数校验
   model → 查 model_configs 内存缓存（TTL=5min）
   model 不存在或 is_active=false → 返回 400 invalid_model
   model.supports_tools=false 但请求含 tools → 返回 400
   // 不校验用户分组权限，模型选择由业务层（Resume Agent）负责

③ 积分预扣 Pre-Auth
   prompt_tokens = 估算（用 tiktoken 或字符数/4 估算，需引入 go-tiktoken 库）
   estimated_output = min(max_tokens 请求值, model.max_output_tokens)
   estimated_cost = ceil((prompt_tokens * input_price + estimated_output * output_price) / 1000) * 1.3
   // 安全系数 1.3（高估 30%），与 §9.1 estimateCost() 保持一致；防止因缓存未命中、实际输出超出估算导致冻结不足
   gRPC → billing.PreAuth(user_id, estimated_cost, request_id)
   → 返回 insufficient_credits: 响应 402
   → 返回 wallet_not_found: 响应 503（触发异步初始化）

④ 存储 in-flight 记录
   HSET gateway:inflight:{request_id}
        user_id {user_id}
        estimated_cost {estimated_cost}
        ...
   EXPIRE 300

⑤ Key 选择（最多重试 10 次）
   ZPOPMIN gateway:pool:{provider_id}
   → score > NOW() → Key 仍冷却，放回 ZSET，继续找下一个
   SISMEMBER gateway:circuit:open {key_id} → 1 → 此 Key 熔断，放回 ZSET，继续找
   检查 RPM 滑动窗口（见 §5.4）→ 超限 → 放回 ZSET 并加冷却分数，继续找
   10 次全部失败 →
     若 request.allow_provider_failover=true（默认）：尝试跨厂商故障切换（见 §8），重新走 Key 选择
     若 request.allow_provider_failover=false：直接返回 503 no_available_key

⑥ 调用上游 LLM API
   使用 key_encrypted 解密获取明文 Key
   构造请求：将 model 替换为 provider_model，其余字段透传
   设置超时：120 秒（可配置）

⑦ 处理结果
   成功（2xx）:
     a. DEL gateway:inflight:{request_id}
     b. ZADD gateway:pool:{provider_id} 0 {key_id}（立即可用）
     c. HSET gateway:key:{key_id}:stats consecutive_errors 0
     d. usage      = parseUsage(providerID, rawResponse)        // 适配层：解析各厂商缓存字段
        actual_cost = calculateActualCost(usage, model)         // 分段定价：含缓存命中/写入三段
     e. gRPC → billing.Settle(
          auth_id      = inflight.auth_id,      // 步骤③ PreAuth 返回的 auth_id
          actual_credits = actual_cost,
          service_name = X-Service-Name,        // 透传调用方服务名（如 resume-agent），写入 credit_transactions.service_name
          description  = "LLM 调用 · {model}"  // 人类可读描述
        )
        → 返回 transaction_id，写入 usage_logs.billing_transaction_id
     f. XADD gateway:stream:usage_events（异步，不阻塞响应）
     g. 异步写 usage_logs 到 DB（含 billing_transaction_id）
     h. 返回响应（注入 x_gateway 扩展字段）

   限流（429）:
     a. ZADD gateway:pool:{provider_id} {NOW()+60000} {key_id}（60s 冷却）
     b. gRPC → billing.Rollback(auth_id = inflight.auth_id)    // 释放冻结积分
     c. DEL gateway:inflight:{request_id}
     d. 重试另一个 Key（回到步骤⑤）；无可用 Key → 返回 503

   服务端错误（5xx）:
     a. HINCRBY gateway:key:{key_id}:stats consecutive_errors 1
     b. 若 consecutive_errors >= CIRCUIT_BREAKER_THRESHOLD（默认5）:
          SADD gateway:circuit:open {key_id}
          ZADD score=NOW()+CIRCUIT_BREAKER_TIMEOUT（默认60000ms）
          更新 DB api_keys.circuit_state='open'（异步）
     c. ZADD gateway:pool:{provider_id} {NOW()+30000} {key_id}
     d. 重试另一个 Key 或触发跨厂商切换

   超时（120s）:
     a. 同服务端错误处理
     b. 错误码记为 timeout
```

### 6.2 流式请求（stream=true）

流式请求前置步骤（①~⑥）与非流式相同，区别在结果处理：

```
⑦ 流式结果处理
   建立 SSE 连接，设置响应头:
     Content-Type: text/event-stream
     Cache-Control: no-cache
     X-Accel-Buffering: no（禁止 Nginx 缓冲）

   从上游逐块读取，实时转发给客户端
   最后一个 chunk 到达后（finish_reason != null）:
     从 chunk 的 usage 字段获取实际 Token 数
     若厂商不返回 usage：Gateway 本地计数（累加 completion token 估算）
     注入 x_gateway 字段到最后一个 chunk
     发送 data: [DONE]
     执行 Settlement + 事件发布 + 日志写入（同非流式）

   连接中断（客户端断开）:
     尚未开始流式返回 → Rollback PreAuth，积分全退
     已开始流式 → 使用本地累加的 Token 估算值结算（见下方 fallback 机制）
```

**流式 Token 计数 Fallback**

客户端断开后，厂商通常不会返回 usage 字段。Gateway 需要自己估算已产生的 Token：

```go
// StreamTokenCounter 在流式转发过程中实时累加
type StreamTokenCounter struct {
    completionTokens int
}

// 每收到一个 delta chunk，累加估算
func (c *StreamTokenCounter) AddDelta(content string) {
    // 粗估：中文字符 ÷ 2，ASCII 字符 ÷ 4
    // 精度约 ±10%，足以防止滥用（客户端收到大部分内容后断开 = 免费用）
    chineseCount := countChinese(content)
    asciiCount := len(content) - chineseCount*3  // UTF-8 中文 3 字节
    c.completionTokens += chineseCount/2 + asciiCount/4
}
```

结算优先级：
1. 厂商返回 usage → 用厂商值（最准）
2. 厂商未返回 → 用本地 StreamTokenCounter 累加值
3. 本地值为 0（一个 chunk 都没收到）→ Rollback，不结算

---

### 6.3 优雅关闭（Graceful Shutdown）

```
收到 SIGTERM / SIGINT →
  1. 标记服务为 draining 状态
     - HTTP listener 停止接受新连接（http.Server.Shutdown）
     - gRPC server 调用 GracefulStop()（若有 admin gRPC 接口）

  2. 等待 in-flight 请求完成
     - 最长等待 SHUTDOWN_TIMEOUT（默认 30s，可配置）
     - 流式请求：等待 LLM 响应完成或客户端断开
     - 非流式请求：等待响应返回

  3. 超时强制清理
     - 遍历 gateway:inflight:* 中仍属于本实例的请求
     - 对每个未完成请求：Rollback PreAuth
     - 归还 Key 到 ZSET 池（ZADD score=0）

  4. 关闭连接
     - 关闭 Billing gRPC 连接
     - 关闭 Redis 连接
     - 关闭 DB 连接
     - 退出进程
```

> Go 实现：`signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)` + `http.Server.Shutdown(ctx)`，标准库原生支持。

---

## 7. 熔断器状态机

```
                    consecutive_errors >= threshold
    CLOSED ────────────────────────────────────────► OPEN
      ▲                                                │
      │                                   timeout后（默认60s）
      │                                                ▼
      │             probe_success              HALF_OPEN
      └────────────────────────────────────────────────┘
                                   probe_failure
                              HALF_OPEN ──────────► OPEN（重置计时）
```

| 状态 | 行为 | 转换条件 |
|------|------|---------|
| CLOSED | 正常接受请求 | consecutive_errors >= threshold → OPEN |
| OPEN | 拒绝所有请求（SISMEMBER 检查命中），Key 从调度中跳过 | circuit_opened_at + timeout 后 → HALF_OPEN |
| HALF_OPEN | 允许通过 1 个探测请求 | 成功 → CLOSED（清零 errors）；失败 → OPEN（重置 circuit_opened_at） |

**配置参数**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| CIRCUIT_BREAKER_THRESHOLD | 5 | 连续错误次数触发熔断 |
| CIRCUIT_BREAKER_OPEN_TIMEOUT | 60s | OPEN 状态持续时长后进入 HALF_OPEN |
| CIRCUIT_BREAKER_HALF_OPEN_MAX | 1 | HALF_OPEN 时最大并发探测请求数 |

**DB 同步**：熔断状态变更时，异步写回 `api_keys.circuit_state` 和 `circuit_opened_at`，用于服务重启后恢复状态（Redis 重启后从 DB 重建）。

---

## 8. 跨厂商故障切换

> 调用方通过请求参数 `allow_provider_failover`（默认 `true`）控制本次请求是否允许切换。
> 传 `false` 时主厂商不可用直接 503，适合成本敏感场景（如免费用户请求不希望悄悄切到更贵的模型）。

**触发条件**：当前厂商的所有 Key 均熔断或冷却，重试 10 次后仍无法选到可用 Key，且请求 `allow_provider_failover=true`。

**切换逻辑**

```go
// 按厂商优先级排序（providers.priority ASC）
// 跳过原始厂商，找到第一个 is_active=true 且有健康 Key 的备用厂商
func selectFallbackProvider(requestedModel, originalProviderID string, allowFailover bool) (providerID, providerModel string, err error) {
    if !allowFailover {
        return "", "", ErrNoProviderAvailable  // 调用方禁止切换
    }

    candidates := getActiveProviders()  // 从内存缓存读取，TTL=30s

    for _, provider := range candidates {
        if provider.ID == originalProviderID {
            continue  // 跳过已失败的厂商
        }
        model, ok := getModelForProvider(requestedModel, provider.ID)
        if !ok {
            continue  // 该厂商没有配置此逻辑模型，跳过
        }
        if hasAvailableKey(provider.ID) {
            return provider.ID, model.ProviderModel, nil
        }
    }
    return "", "", ErrNoProviderAvailable
}
```

**前提：model_configs 需配置多厂商映射**

跨厂商切换依赖同一逻辑模型 ID 在多个厂商下都有配置（联合主键 `(id, provider_id)`），否则找不到备用厂商的 provider_model：

```sql
-- 示例：deepseek-chat 主力用 DeepSeek，故障时切到 OpenAI 的近似模型
INSERT INTO model_configs (id, provider_id, provider_model, ...) VALUES
('deepseek-chat', 'deepseek', 'deepseek-chat', ...),   -- 主力
('deepseek-chat', 'openai',   'gpt-4o-mini',   ...);   -- 备用（不配则无法切换）
```

若某逻辑模型只配了一个厂商，即使开关打开，也不会发生切换（找不到备用直接 503），不会有副作用。

**切换后行为**：响应中 `x_gateway.switched_provider=true`，`usage_logs.switched_provider=1`，方便监控告警。

---

## 9. 计费集成（Billing Integration）

> **职责边界说明**：
> LLM Gateway 负责**估算费用 + 发起 gRPC 调用**；
> 积分的冻结/解冻/扣除（DB 事务 + `SELECT FOR UPDATE`、`wallet:balance:{user_id}` Redis 缓存、`credit_transactions` 写入）全部由 **Billing Service** 执行。
> Gateway 不直接操作任何积分相关的 Redis Key 和 DB 表。

### 9.1 费用估算（Gateway 本地计算，用于传给 Billing Pre-Auth）

```go
// Gateway 本地估算，仅用于传给 Billing PreAuth 接口做积分冻结
// 实际扣费依据是 LLM 厂商返回的真实 Token 数，由 Billing Settle 完成
func estimateCost(messages []Message, model ModelConfig, maxTokens int) int {
    // 估算 prompt tokens（近似：字符数/2 for 中文，/4 for 英文）
    // 生产环境建议引入 tiktoken-go 精确计算
    promptTokens := estimateTokens(messages)

    outputTokens := maxTokens
    if outputTokens == 0 {
        outputTokens = model.MaxOutputTokens
    }

    inputCost  := promptTokens * model.InputCreditsPer1k / 1000
    outputCost := outputTokens * model.OutputCreditsPer1k / 1000

    // 安全系数 1.3（高估 30%，防止估算不足）
    return int(math.Ceil(float64(inputCost+outputCost) * 1.3))
}
```

### 9.2 厂商响应适配层（Token 明细统一解析）

各厂商返回的缓存 Token 字段名不同，Gateway 内部有一个适配层将其统一转换：

```go
// TokenUsage 是 Gateway 内部统一的 Token 用量结构
type TokenUsage struct {
    PromptTokens     int  // 总输入 Token（含缓存命中部分）
    CompletionTokens int  // 输出 Token
    CachedTokens     int  // 命中缓存的输入 Token（已含在 PromptTokens 内）
    CacheWriteTokens int  // 写入缓存的 Token（Anthropic，已含在 PromptTokens 内）
}

// parseUsage 从各厂商的原始响应中提取，统一映射到 TokenUsage
func parseUsage(providerID string, raw map[string]any) TokenUsage {
    switch providerID {
    case "openai":
        // raw.usage.prompt_tokens_details.cached_tokens
        return TokenUsage{
            PromptTokens:     raw["usage"]["prompt_tokens"],
            CompletionTokens: raw["usage"]["completion_tokens"],
            CachedTokens:     raw["usage"]["prompt_tokens_details"]["cached_tokens"],
        }
    case "anthropic":
        // raw.usage.cache_read_input_tokens + cache_creation_input_tokens
        return TokenUsage{
            PromptTokens:     raw["usage"]["input_tokens"],
            CompletionTokens: raw["usage"]["output_tokens"],
            CachedTokens:     raw["usage"]["cache_read_input_tokens"],
            CacheWriteTokens: raw["usage"]["cache_creation_input_tokens"],
        }
    case "deepseek":
        // raw.usage.prompt_cache_hit_tokens
        return TokenUsage{
            PromptTokens:     raw["usage"]["prompt_tokens"],
            CompletionTokens: raw["usage"]["completion_tokens"],
            CachedTokens:     raw["usage"]["prompt_cache_hit_tokens"],
        }
    default:
        // 不支持缓存的厂商，CachedTokens = 0
        return TokenUsage{
            PromptTokens:     raw["usage"]["prompt_tokens"],
            CompletionTokens: raw["usage"]["completion_tokens"],
        }
    }
}
```

### 9.3 实际费用计算（分段定价）

```go
// calculateActualCost 按缓存命中/未命中分段计算积分费用
func calculateActualCost(usage TokenUsage, model ModelConfig) int {
    // 未命中缓存的正常输入 Token 数
    normalInputTokens := usage.PromptTokens - usage.CachedTokens - usage.CacheWriteTokens

    cost := 0
    // ① 正常输入（缓存未命中）
    cost += normalInputTokens * model.InputCreditsPer1k / 1000
    // ② 缓存命中（打折价）
    if model.CachedInputCreditsPer1k != nil {
        cost += usage.CachedTokens * (*model.CachedInputCreditsPer1k) / 1000
    } else {
        // 厂商不支持缓存计费，按正常价计算（保守处理，不少收）
        cost += usage.CachedTokens * model.InputCreditsPer1k / 1000
    }
    // ③ 缓存写入（Anthropic 额外收费）
    if model.CacheWriteCreditsPer1k != nil {
        cost += usage.CacheWriteTokens * (*model.CacheWriteCreditsPer1k) / 1000
    }
    // ④ 输出 Token
    cost += usage.CompletionTokens * model.OutputCreditsPer1k / 1000

    return cost
}
```

**各厂商积分定价示例**（seed data）：

```sql
INSERT INTO model_configs
    (id, provider_id, provider_model, display_name,
     context_window, max_output_tokens,
     supports_streaming, supports_tools, supports_vision, supports_prompt_cache,
     input_credits_per_1k, output_credits_per_1k,
     cached_input_credits_per_1k, cache_write_credits_per_1k,
     sort_order)
VALUES
-- OpenAI：缓存命中 5 折，无写入费
('gpt-4o',      'openai',    'gpt-4o',               'GPT-4o',      128000, 4096,  1,1,1,1,  25, 100, 13,   NULL, 1),
('gpt-4o-mini', 'openai',    'gpt-4o-mini',          'GPT-4o Mini', 128000, 4096,  1,1,1,1,  2,  8,   1,    NULL, 2),
-- DeepSeek：缓存命中约 2.6 折，无写入费
('deepseek-chat','deepseek', 'deepseek-chat',         'DeepSeek V3',  64000, 8000,  1,1,0,1,  1,  3,   0,    NULL, 3),
('deepseek-r1',  'deepseek', 'deepseek-reasoner',     'DeepSeek R1',  64000,32000,  1,0,0,1,  5,  20,  1,    NULL, 4),
-- Anthropic：缓存命中 1 折，写入 1.25 倍
('claude-3-5-sonnet','anthropic','claude-3-5-sonnet-20241022','Claude 3.5 Sonnet',200000,8192,1,1,1,1,30,150,3,38,5);
```

### 9.4 gRPC 调用时序

```
── 调用前 ──────────────────────────────────────────────────────────
Gateway:  estimatedCost = estimateCost(messages, model, maxTokens)
          // 估算时无法预知缓存命中率，统一按正常价估算（Pre-Auth 保守高估）
Gateway → Billing gRPC: PreAuth(
            user_id          = X-User-Id,
            estimated_credits = estimatedCost,
            service_name     = X-Service-Name,  // 调用方业务服务名，如 resume-agent
            request_id       = X-Request-Id     // 全链路幂等键
          )
Billing → Gateway: { auth_id: "01HXXX" } 或 FAILED_PRECONDITION(insufficient_credits)

── 调用后（成功）────────────────────────────────────────────────────
Gateway:  usage      = parseUsage(providerID, rawResponse)  // 适配层解析
          actualCost = calculateActualCost(usage, model)    // 分段计算
Gateway → Billing gRPC: Settle(
            auth_id        = preAuthResponse.auth_id,
            actual_credits = actualCost,
            service_name   = X-Service-Name,   // 写入 credit_transactions.service_name，账单页显示来源业务
            description    = "LLM 调用 · {model}"
          )
Billing → Gateway: { transaction_id: "01HYYY" }
Gateway:  usage_logs.billing_transaction_id = transaction_id  // 对账锚点

── 调用后（失败/取消）──────────────────────────────────────────────
Gateway → Billing gRPC: Rollback(auth_id = preAuthResponse.auth_id)
Billing:  [释放全部冻结积分，不写 credit_transactions]
```

> **service_name 透传规则**：Gateway 将 `X-Service-Name` 请求头原样传入 PreAuth 和 Settle，不替换为 `"llm-gateway"`。这样 `credit_transactions.service_name` 记录的是业务来源（如 `resume-agent`），与 Resume Agent 自身 usage_logs 的 service_name 一致，对账时可直接聚合比对。

### 9.5 多轮 Agent 调用的计费处理

Resume Agent 采用 ReAct 模式，一次用户消息可触发多轮 LLM 调用（主调用 + 最多 4 轮工具回调）。这些轮次都使用同一个 `X-Request-Id`。

**PreAuth 幂等保证单次冻结：**

```
Round 1：PreAuth(request_id="abc-xyz")
  → Billing: INSERT pre_auths(request_id="abc-xyz"), frozen=1500 credits → OK, auth_id="01H..."
  → Gateway: 存 inflight record, inflight.auth_id = "01H..."

Round 2（工具回调后继续）：PreAuth(request_id="abc-xyz")
  → Billing: UNIQUE KEY request_id 检查 → ALREADY_EXISTS
  → Billing: 直接返回已有 auth_id="01H..."，不重复冻结
  → Gateway: 重用 inflight.auth_id，不新建 inflight

Round 3, 4, 5：同 Round 2

最终 Settle：Settle(auth_id="01H...", actual_credits=实际总消耗)
  → Billing: 按实际总用量结算，释放多余冻结
```

**关键设计点：**

- Resume Agent 在第一轮调用前，用 `estimate_tokens(full_context) * price_per_token * 1.5` 估算总积分（含多轮 buffer），通过 `max_tokens` 参数告知 Gateway 上限，Gateway 据此 PreAuth
- 后续轮次携带相同 `X-Request-Id`，Gateway 检测到 `ALREADY_EXISTS` 后复用 auth_id，**不重复冻结**
- 所有轮次的实际 Token 累加，最终一次性 Settle

**`modify_template` 工具独立计费：**

`modify_template` 工具触发的 LLM 调用使用独立 sub-request-id（`{original_request_id}-tpl`），与主对话完全分开：

```
主对话：X-Request-Id = abc-xyz          → PreAuth + 多轮 Settle
模板修改：X-Request-Id = abc-xyz-tpl    → 独立 PreAuth + 独立 Settle
```

两套 request_id 各自走完整的 PreAuth → Token 计量 → Settle 流程。

---

### 9.6 异常场景处理

| 场景 | 处理方式 |
|------|---------|
| Pre-Auth 成功，LLM 调用超时 | Settlement 按已接收 Token 数结算；流式中断按实际传输量算 |
| Pre-Auth 成功，Gateway 进程崩溃 | 对账 Job 每 5 分钟扫描 gateway:inflight:*，超时 key 触发 ReleasePreAuth |
| Settlement gRPC 调用失败 | Settle 重试机制（见 §9.7） |
| 厂商返回 usage 不准确 | 以厂商返回为准；Gateway 记录 usage_logs，供人工对账 |

### 9.7 Settle 重试机制

Settle 是扣费关键路径，gRPC 调用失败（Billing 短暂不可用、网络抖动）时需要重试补偿。

**设计选择**：本地 Redis 重试队列，而非 Redis Streams 持久队列。理由：Settle 失败是极低频场景（Billing 长时间不可用才会触发），重试队列 + usage_logs 对账已足够覆盖，无需引入 Streams consumer 的额外复杂度。

```
Redis Key:    gateway:settle:retry
Type:         Sorted Set
Member:       JSON 序列化的 SettleRequest（auth_id, actual_credits, service_name, description, request_id）
Score:        下次重试时间（Unix 毫秒）

重试策略：
  初始延迟：2s
  退避系数：2x（2s → 4s → 8s → 16s → 32s）
  最大重试：5 次
  重试标记：HASH gateway:settle:retry:count:{auth_id} → 当前重试次数，TTL=10min

流程：
  Settle gRPC 失败 →
    ZADD gateway:settle:retry {NOW()+2000} {settle_json}
    HSET gateway:settle:retry:count:{auth_id} 1

  后台 goroutine（每 5s 执行一次）：
    ZRANGEBYSCORE gateway:settle:retry -inf {NOW()} LIMIT 0 10
    对每条记录：
      count = HINCRBY gateway:settle:retry:count:{auth_id} 1
      if count > 5:
        ZREM gateway:settle:retry {member}
        DEL gateway:settle:retry:count:{auth_id}
        记录告警日志：auth_id, request_id, actual_credits
        // usage_logs.billing_transaction_id 保持 NULL，对账时人工排查
        continue
      gRPC Settle(...)
      成功 → ZREM + DEL count + 更新 usage_logs.billing_transaction_id
      失败 → ZADD 新 score = NOW() + 2^count * 1000（指数退避）
```

**Gateway 重启后的重试项恢复**：Redis ZSET 持久化，重启后后台 goroutine 自动继续消费，不会丢失。仅当 Redis 也重启（数据丢失）时才依赖对账机制兜底。

---

## 10. 错误码规范

### 10.1 对外接口错误（业务服务调用）

| HTTP 状态 | error.code | 说明 | 处理建议 |
|----------|-----------|------|---------|
| 400 | `invalid_model` | 请求的 model 不存在或已下架 | 检查模型 ID |
| 400 | `invalid_request` | 请求体字段格式错误 | 检查请求格式 |
| 400 | `model_not_support_tools` | 该模型不支持 Function Calling | 换模型或去掉 tools |
| 401 | `unauthorized` | INTERNAL_SERVICE_TOKEN 无效 | 检查服务配置 |
| 402 | `insufficient_credits` | 用户积分不足 | 前端提示充值 |
| 503 | `no_available_key` | 无可用 API Key（全部熔断/冷却） | 等待自动恢复，触发告警 |
| 503 | `wallet_not_initialized` | Billing 账户未就绪 | 重试 |
| 504 | `upstream_timeout` | 上游 LLM 超时 | 重试或降级 |
| 500 | `internal_error` | Gateway 内部错误（不暴露细节） | 查日志 |

### 10.2 Admin 接口错误码

| code 值 | 含义 |
|---------|------|
| 0 | 成功 |
| 40001 | 资源不存在 |
| 40002 | 参数校验失败 |
| 40003 | 资源冲突（如 provider id 重复） |
| 40004 | 操作不允许（如删除有活跃 Key 的厂商） |
| 50001 | 内部错误 |

---

## 11. 环境变量配置

```env
# ── 服务基础 ──────────────────────────────────────────────
GATEWAY_PORT=8083
INTERNAL_SERVICE_TOKEN=your-internal-secret-token-min-32-chars
GATEWAY_ADMIN_TOKEN=your-admin-token-min-32-chars

# ── 数据库 ────────────────────────────────────────────────
GATEWAY_DB_DSN=gateway_user:password@tcp(mysql:3306)/tadpoles_gateway?charset=utf8mb4&parseTime=True&loc=UTC

# ── Redis ─────────────────────────────────────────────────
REDIS_URL=redis://redis:6379/0

# ── Billing Service（gRPC）────────────────────────────────
BILLING_GRPC_ADDR=billing-service:9094
BILLING_ENABLED=true
# BILLING_ENABLED=false → 使用 FreeBillingClient（积分无限，跳过所有检查）

# ── LLM Provider 模式 ─────────────────────────────────────
LLM_PROVIDER=real
# LLM_PROVIDER=fake → 使用 FakeLLMClient，不调用真实厂商

# Fake LLM 配置（LLM_PROVIDER=fake 时生效）
FAKE_LLM_MIN_LATENCY_MS=100
FAKE_LLM_MAX_LATENCY_MS=2000
FAKE_LLM_ERROR_RATE=0.05           # 5% 随机错误
FAKE_LLM_ERROR_TYPE=rate_limit     # rate_limit / server_error / timeout / network
FAKE_LLM_MIN_OUTPUT_TOKENS=50
FAKE_LLM_MAX_OUTPUT_TOKENS=500
FAKE_KEY_POOL_SCENARIO=concurrent_stress
# Fake Key Pool 场景:
#   concurrent_stress  → 3个Key：快(0)、慢(1500ms)、偶尔失败(10%错误率)
#   circuit_breaker    → 3个Key：健康、always-500、always-429
#   all_failing        → 所有Key均不可用，测试503降级路径

# ── 熔断器 ────────────────────────────────────────────────
CIRCUIT_BREAKER_THRESHOLD=5        # 触发熔断的连续错误次数
CIRCUIT_BREAKER_OPEN_TIMEOUT=60s   # OPEN 状态超时时间

# ── 加密 ──────────────────────────────────────────────────
ENCRYPTION_KEY=32-bytes-hex-string-for-aes256-gcm

# ── 可观测性（可选） ──────────────────────────────────────
OTEL_ENABLED=false
OTEL_EXPORTER=prometheus            # prometheus / otlp
PROMETHEUS_PORT=9090
OTEL_SERVICE_NAME=llm-gateway

# ── 超时配置 ──────────────────────────────────────────────
LLM_REQUEST_TIMEOUT=120s           # 上游 LLM 请求超时
KEY_SELECT_MAX_RETRY=10            # Key 选择最大重试次数
```

---

## 12. 测试接口

### 12.1 Fake LLM Client

当 `LLM_PROVIDER=fake` 时，Gateway 使用内置 FakeLLMClient 替代真实厂商调用：

```go
type FakeLLMClient struct {
    MinLatency    time.Duration  // FAKE_LLM_MIN_LATENCY_MS
    MaxLatency    time.Duration  // FAKE_LLM_MAX_LATENCY_MS
    ErrorRate     float64        // FAKE_LLM_ERROR_RATE (0.0~1.0)
    ErrorType     string         // rate_limit/server_error/timeout/network
    MinOutputTokens int          // FAKE_LLM_MIN_OUTPUT_TOKENS
    MaxOutputTokens int          // FAKE_LLM_MAX_OUTPUT_TOKENS
}
```

- **流式模式**：Fake Client 按 MinLatency~MaxLatency 随机延迟，分批发送 token chunk，模拟真实流式行为
- **错误注入**：按 ErrorRate 概率触发 ErrorType 类型错误，用于测试熔断器和错误处理

### 12.2 健康检查接口

```
GET /health
```

```json
{
  "status": "ok",
  "db": "ok",
  "redis": "ok",
  "billing_grpc": "ok",
  "key_pool": {
    "openai": { "total": 3, "available": 2, "circuit_open": 1 },
    "deepseek": { "total": 2, "available": 2, "circuit_open": 0 }
  }
}
```

### 12.3 验收测试清单

| 测试场景 | 验证点 |
|---------|--------|
| 正常非流式调用 | 响应含 usage、x_gateway 字段；usage_logs 写入；积分结算正确 |
| 正常流式调用 | SSE chunk 逐步返回；最后一 chunk 含 usage；data: [DONE] |
| 积分不足（balance=0） | 返回 402，积分未扣除，usage_logs 不写入 |
| Key 429 限流 | 该 Key 冷却 60s，请求切换到其他 Key 成功返回 |
| Key 5xx 错误×5次 | 第5次报错后熔断，circuit_state=open，后续请求跳过该 Key |
| 熔断后自动恢复 | 60s 后 HALF_OPEN，探测成功→CLOSED |
| 全部 Key 熔断 | 返回 503 no_available_key |
| 跨厂商切换 | 主厂商熔断后，响应 x_gateway.switched_provider=true |
| Gateway 重启 | Redis 中的熔断状态保留，Key 调度不中断 |
| Tool Calls 流式 | tool_calls delta 正确拼接，finish_reason=tool_calls |

---

## 13. 水平扩展与架构演进（未实现，面试讨论用）

### 13.1 Gateway 多实例扩展

**当前设计已天然支持多实例部署**，核心原因：

1. **ZSET 原子调度**：`ZPOPMIN` 是原子操作，100 个 Gateway 实例同时从同一个 `gateway:pool:{provider_id}` 取 Key，每个实例拿到不同 Key，天然无锁
2. **熔断器共享**：`gateway:circuit:open` 是 Redis SET，所有实例共享同一份熔断状态
3. **无本地状态**：除了 model_configs 内存缓存（5min TTL），Gateway 不持有任何本地状态

```
                      Load Balancer
                     ┌──────┼──────┐
                     ▼      ▼      ▼
              Gateway-1  Gateway-2  Gateway-3
                     │      │      │
                     └──────┼──────┘
                            ▼
                    Redis（ZSET + Circuit）
                            │
                    ┌───────┼───────┐
                    ▼       ▼       ▼
                DeepSeek  OpenAI  Anthropic
```

**多实例时的注意事项：**

| 问题 | 解决方案 |
|------|---------|
| in-flight 请求归属 | `gateway:inflight:{request_id}` 中增加 `instance_id` 字段，对账 Job 只清理自己实例的过期项 |
| model_configs 缓存一致性 | 各实例独立刷新（5min TTL），Admin 更新后最多 5min 延迟生效，可接受 |
| Settle 重试队列 | Redis ZSET 已持久化，任一实例的重试 worker 都可以消费（需加分布式锁防重复消费） |
| 日志聚合 | 各实例日志打 instance_id 标签，ELK/Loki 统一收集 |

### 13.2 演进路线

| 阶段 | 方案 | 适用场景 |
|------|------|---------|
| 当前 | 单实例 | 日均 < 1万次 LLM 调用 |
| 阶段 1 | **多实例 + K8s HPA** — 按 CPU/内存自动扩缩容 | 1万~10万次/天 |
| 阶段 2 | **按厂商分组部署** — OpenAI 专用实例组、DeepSeek 专用实例组，独立扩缩容和熔断 | 10万+/天 |
| 阶段 3 | **异步队列模式** — 非流式请求走消息队列削峰（流式仍走直连），解耦请求接收和 LLM 调用 | 突发流量场景 |

### 13.3 usage_logs 分表策略

`usage_logs` 是增长最快的表（每次 LLM 调用写一条），千万级时考虑：

- **按月分表**：`usage_logs_2026_04`、`usage_logs_2026_05`...
- 查询只涉及时间范围 + user_id，分表 key 用 `(created_at 月份)`
- 历史数据按月归档到冷存储（S3/OSS），热表只保留近 3 个月
- GORM 通过 `TableName()` 动态路由，上层无感知

### 13.4 面试谈话要点

1. **Gateway 扩展的关键是 Redis ZSET** — 这不是事后想到的，是设计时就选择 ZSET 的原因之一。`ZPOPMIN` 天然支持多消费者竞争获取，相当于一个无锁的分布式任务队列
2. **熔断器是共享的还是实例级的？** — 共享（Redis SET）。一个实例发现 Key 故障后触发熔断，所有实例立即感知，避免其他实例继续向坏 Key 发请求
3. **为什么不用 Kafka 做请求队列？** — LLM 调用 90% 是流式的，流式响应需要长连接直达客户端，消息队列引入了不必要的中间跳数和延迟。非流式场景量很小，不值得引入 Kafka 的运维成本
