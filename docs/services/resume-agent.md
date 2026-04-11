# Resume Agent 设计文档

> 版本：v2.2
> 服务端口：8082
> 数据库：resume_db（MySQL）

---

## 目录

1. [服务定位与职责](#1-服务定位与职责)
2. [外部 HTTP API](#2-外部-http-api)
3. [Agent 架构设计](#3-agent-架构设计)
4. [Agent 工具（Tools）定义](#4-agent-工具定义)
5. [Agent 记忆系统](#5-agent-记忆系统)
6. [简历评估方法](#6-简历评估方法)
7. [核心业务流程](#7-核心业务流程)
8. [数据库 Schema](#8-数据库-schema)
9. [Redis Key 设计](#9-redis-key-设计)
10. [文件存储（阿里云 OSS）](#10-文件存储阿里云-oss)
11. [PDF 导出流程](#11-pdf-导出流程)
12. [服务间交互全景](#12-服务间交互全景)
13. [Billing 统计接口（可插拔契约）](#13-billing-统计接口)
14. [环境变量](#14-环境变量)
15. [错误码约定](#15-错误码约定)

---

## 1. 服务定位与职责

Resume Agent 是 TadpolesAI 的核心业务服务，以 **AI Agent** 模式驱动简历优化。区别于简单的问答 Chat，Resume Agent 的 AI 拥有可操作简历数据的工具集，能够直接读取、修改、评估简历，而不仅仅是给出文字建议。

**核心能力：**

- 多份简历管理（草稿/完成状态，模板选择）
- 简历文件解析（上传 PDF/DOCX → AI 结构化提取）
- **AI Agent 对话**：工具调用（Function Calling）驱动，AI 可直接修改简历内容
- JD 定向优化：粘贴职位描述，AI 分析匹配度并针对性优化
- 简历多维评分（0–100 分，含 JD 匹配度分析）
- **对话式模板定制**：AI 可直接修改模板 HTML/CSS，支持任意结构改造（时间轴、双栏、配色等）
- PDF 导出（前端 html2pdf.js 客户端渲染，所见即所得）

**边界说明：**

| 职责 | 归属 |
|------|------|
| 简历数据、AI Agent 逻辑 | Resume Agent |
| 积分扣费、钱包 | Billing Service（由 LLM Gateway 代理，Resume Agent 不直接调） |
| LLM 实际调用、计费 PreAuth/Settle | LLM Gateway |
| 用户身份认证 | APISIX JWT（注入 X-User-Id / X-User-Group） |
| 文件存储 | 阿里云 OSS |
| PDF 导出 | 前端（html2pdf.js），后端不参与 |
| 模板渲染 | 前端（Handlebars.js），后端只存模板 HTML 字符串 |

---

## 2. 外部 HTTP API

Base path: `/v1`，所有请求须携带 JWT（APISIX 注入 `X-User-Id`、`X-User-Group`、`X-Request-Id`）

### 2.1 简历列表

```
GET /v1/resumes
```

**响应：** `200 OK`

```json
{
  "resumes": [
    {
      "id": "01J...",
      "name": "产品经理简历 2026",
      "status": "draft",
      "score": 82,
      "template_id": "tpl_product_classic",
      "updated_at": "2026-04-10T09:00:00Z"
    }
  ]
}
```

---

### 2.2 新建简历

```
POST /v1/resumes
```

**请求：**

```json
{
  "name": "前端工程师简历 2026",
  "template_id": "tpl_frontend_modern",
  "start_type": "from_scratch"
}
```

`start_type`: `from_scratch`（空白骨架）或 `upload`（上传文件后由 §2.7 解析填充）

**响应：** `201 Created`，返回含空骨架 content 的简历对象

---

### 2.3 获取简历详情

```
GET /v1/resumes/{resume_id}
```

返回完整简历对象，包含 `content`（结构化 JSON）、`jd_context`、`score`。

---

### 2.4 更新简历基础信息

```
PUT /v1/resumes/{resume_id}
```

字段（均可选）：`name`、`status`、`template_id`

---

### 2.5 更新简历内容（前端直接编辑）

```
PUT /v1/resumes/{resume_id}/content
```

前端简历编辑器实时编辑时调用（防抖 500ms）。请求体为完整 content JSON。

更新成功后：`DEL resume:score:{resume_id}`（内容变更，评分失效）、`DEL resume:export:{resume_id}`

---

### 2.6 删除简历

```
DELETE /v1/resumes/{resume_id}
```

软删除。**响应：** `204 No Content`

---

### 2.7 上传简历文件

```
POST /v1/resumes/{resume_id}/upload
Content-Type: multipart/form-data
```

字段：`file`（PDF / DOCX，≤10MB）

**流程：** 校验 → 上传 OSS → 提取文本 → 调 AI 解析为结构化 content → 更新 resumes.content

计费：消耗 `CREDITS_AI_PARSE`（约 30 credits），通过 LLM Gateway 走 Billing。

---

### 2.8 设置 JD（职位描述）

```
PUT /v1/resumes/{resume_id}/jd
```

```json
{ "jd_text": "职位：字节跳动抖音产品经理\n岗位职责：..." }
```

存入 `resumes.jd_context`，后续所有 AI 对话自动携带 JD 上下文。
同时触发异步 JD 分析（调 AI 提取关键词/技能要求），结果存 `resumes.jd_analysis`。

---

### 2.9 AI 对话（流式 SSE）

```
POST /v1/resumes/{resume_id}/chat
```

```json
{ "message": "帮我优化字节跳动那段工作经历，突出数据指标" }
```

**响应：** `text/event-stream`

```
data: {"type":"text","delta":"好的，我来优化这段经历，重点量化业务指标...\n\n"}
data: {"type":"tool_call","name":"replace_entry","args":{"section":"work_experience","index":0,"entry":{"company":"字节跳动","title":"产品经理","start_date":"2023-06","bullets":["主导抖音社交模块 DAU 增长 35%，用户 MAU 从 200 万提升至 270 万","推动 A/B 测试体系建设，覆盖 20+ 功能模块，迭代周期缩短 30%","协调研发、设计、运营 3 个团队，按时完成 Q3 全部里程碑交付"]}}}
data: {"type":"tool_result","name":"replace_entry","summary":"已更新字节跳动工作经历，bullets 已重写并量化"}
data: {"type":"text","delta":"改动集中在 3 条 bullet：用具体数据替换了模糊描述，突出增长幅度和协作规模。"}
data: {"type":"done","message_id":"01J...","credits_used":28,"billing_tx_id":"01J..."}
```

**SSE 事件类型：**

| 类型 | 说明 |
|------|------|
| `text` | AI 文字流式输出 delta |
| `tool_call` | AI 调用工具（前端可展示"正在操作简历..."） |
| `tool_result` | 工具执行结果摘要（前端刷新简历预览） |
| `done` | 流结束，附带本轮实际积分消耗 |
| `error` | 错误终止（余额不足 / 限流 / LLM 超时），流随后关闭 |

**`error` 事件格式：**

```
data: {"type":"error","code":"insufficient_credits","message":"积分不足，请充值后重试"}
data: {"type":"error","code":"rate_limited","message":"请求过于频繁，请稍后再试"}
data: {"type":"error","code":"upstream_timeout","message":"AI 响应超时，请重试"}
data: {"type":"error","code":"internal_error","message":"服务内部错误"}
```

> `error` 事件发出后 SSE 连接立即关闭。前端应根据 `code` 区分是否需要提示充值。已消耗的 token 仍会被结算（LLM Gateway 按实际用量 Settle）。客户端断开连接后，服务端检测到写失败立即停止 Agent 执行循环并结算已用 token。

---

### 2.10 获取对话历史

```
GET /v1/resumes/{resume_id}/messages?limit=30&before=01J...
```

游标分页，按 `created_at DESC`，返回消息列表。

---

### 2.11 简历评分

```
POST /v1/resumes/{resume_id}/score
```

触发 AI 多维评分。消耗约 20 credits。

**响应：** `200 OK`

```json
{
  "score": 82,
  "dimensions": {
    "completeness":    { "score": 22, "max": 25, "comment": "缺少项目经历模块" },
    "professionalism": { "score": 23, "max": 25, "comment": "动词表达专业，建议减少被动句" },
    "quantification":  { "score": 20, "max": 25, "comment": "60% bullet 含量化数据，建议提升至 80%" },
    "structure":       { "score": 17, "max": 25, "comment": "教育经历模块建议移至工作经历之后" }
  },
  "jd_match": {
    "score": 74,
    "matched_keywords": ["产品规划", "数据分析", "抖音"],
    "missing_keywords": ["A/B测试", "增长黑客", "用户留存"]
  },
  "summary": "简历整体质量良好，建议补充量化数据和项目经历，针对 JD 补充 A/B 测试相关经验描述。",
  "updated_at": "2026-04-10T10:05:00Z"
}
```

> `jd_match` 仅在 `resumes.jd_context` 非空时返回。

---

### 2.12 导出 PDF（前端实现，后端无接口）

PDF 导出完全由**前端**完成，后端不提供该接口。

**前端实现：**

```javascript
import html2pdf from 'html2pdf.js'

function exportPDF(resumeName) {
  const element = document.querySelector('.resume-preview') // 右侧预览 DOM 节点
  html2pdf().set({
    margin: 0,
    filename: `${resumeName}.pdf`,
    image: { type: 'jpeg', quality: 0.98 },
    html2canvas: { scale: 2, useCORS: true },
    jsPDF: { unit: 'mm', format: 'a4', orientation: 'portrait' }
  }).from(element).save()
}
```

**为什么选前端导出：**

- 预览本身已是 Handlebars 渲染后的完整 HTML，字体/样式/布局与预览 100% 一致（所见即所得）
- 用户对话式修改模板后立即可导出，无需等服务端重新渲染
- 免去服务端 Chromedp、OSS 存储 PDF 的成本与复杂度

PDF 导出**不消耗积分**。

---

### 2.13 模板列表

```
GET /v1/templates?category=product
```

`category` 可选：`all` / `frontend` / `backend` / `ops` / `product` / `design` / `general`

---

## 3. Agent 架构设计

### 3.1 整体架构

Resume Agent 的 AI 采用 **ReAct（Reasoning + Acting）** 模式，底层通过 LLM 的 **Function Calling** 能力驱动工具执行。

```
用户消息
    │
    ▼
┌─────────────────────────────────────┐
│           Agent Executor            │
│                                     │
│  1. 组装 Context（记忆系统）         │
│  2. 携带 Tools Schema 调 LLM        │
│  3. 解析 LLM 响应                   │
│     ├── 纯文字 → 流式转发给用户      │
│     └── tool_call → 执行工具        │
│         └── tool_result → 继续调 LLM│
│  4. 写 DB（消息历史、简历内容）      │
│  5. Billing 结算                    │
└─────────────────────────────────────┘
```

### 3.2 工具调用循环（多轮 Tool Call）

```
┌──────────────────────────────────────────────────────────┐
│  单次用户消息的 Agent 执行循环                            │
│                                                          │
│  组装 Context                                            │
│      │                                                   │
│      ▼                                                   │
│  调 LLM Gateway（携带 tools 定义）                       │
│      │                                                   │
│      ├── 返回 tool_calls ──► 执行工具 ──► 追加结果到    │
│      │                                    messages[]    │
│      │                         │                        │
│      │                         └──────────► 再次调 LLM  │
│      │                                     （最多 5 轮） │
│      │                                                   │
│      └── 返回 text（无 tool_call）──► 流式转发用户       │
│                                         写 DB / 结算     │
└──────────────────────────────────────────────────────────┘
```

**最大工具调用轮次：5 轮**（防止无限循环），超过后强制终止并告知用户。

### 3.3 计费策略（多轮工具调用）

一次用户消息可能触发多轮 LLM 调用（主调用 + 若干工具结果回调）。计费方案：

1. **第一次调 LLM Gateway 前**：PreAuth，预估积分 = `estimate_tokens(full_context) * price_per_token * 1.5`（留 50% buffer 覆盖多轮）
2. **每轮 LLM 调用**：LLM Gateway 记录实际 token 用量，累计到同一个 `auth_id`
3. **全部轮次结束后**：Resume Agent 调 LLM Gateway 的 Settle，传入实际总 token 数

> 实现简化：Resume Agent 在第一轮 LLM 调用时传 `X-Request-Id`（作为 Billing PreAuth 幂等键）；后续工具回调轮次使用同一个 `X-Request-Id` + 标记 `is_continuation=true`，LLM Gateway 累计 token 不重复 PreAuth。

**`modify_template` 工具的独立计费：**

`modify_template` 触发的 LLM 调用不在主对话 streaming 流中，使用独立的 sub-request-id：

```
主对话 X-Request-Id:  {original_request_id}           ← 主 LLM 调用（含多轮工具回调）
模板修改 X-Request-Id: {original_request_id}-tpl       ← modify_template 独立 LLM 调用
```

两个 request-id 各自在 LLM Gateway 完成独立的 PreAuth → Token 计量 → Settle，互不干扰。Resume Agent 在 `usage_logs` 中分别记录两条日志（`action=ai_chat` 和 `action=ai_modify_template`）。

### 3.4 流式输出策略

工具调用阶段暂停文字流，向客户端发送 `tool_call` / `tool_result` 事件；工具执行完成后恢复文字流。客户端收到 `tool_result` 时刷新简历预览面板。

```
client ◄── text delta ◄── LLM 开始输出
client ◄── tool_call event（AI 决定调用工具，文字流暂停）
           Agent 本地执行工具（DB 操作，< 100ms）
client ◄── tool_result event（前端刷新预览）
client ◄── text delta ◄── LLM 继续输出（携带工具结果）
client ◄── done event
```

---

## 4. Agent 工具定义

工具在 Function Calling 的 `tools` 参数中传给 LLM。工具执行逻辑在 Resume Agent 本地运行（操作 DB），**不经过 LLM Gateway**。

> **设计原则：** 简历的完整内容（`resumes.content`）和 JD 分析（`jd_analysis`）在**每次对话轮次开始时从 DB 实时读取**，整体注入 System Prompt。AI 已知道所有简历数据，无需"读取"工具——只需"写入"工具。模板 HTML 因体积较大，通过 `modify_template` 工具触发的独立 LLM 调用处理，不注入主对话 context。

### 工具 LLM 调用情况

| 工具 | 是否调用 LLM | 调用方式 |
|------|------------|----------|
| `replace_section` | ❌ 纯 DB 操作 | 直接覆写简历 JSON 指定章节 |
| `replace_entry` | ❌ 纯 DB 操作 | 直接覆写章节中指定条目 |
| `modify_template` | ✅ 独立 LLM 调用 | 单独 LLM call，不在主 streaming 流中；使用 sub-request-id 独立计费 |
| `analyze_jd` | ✅ 单次 LLM 调用 | 结果写入 DB，24h 内缓存复用 |
| `evaluate_resume` | ✅ 部分 LLM | 规则（完整性 + 量化）+ LLM（专业性 + 结构 + JD 语义） |

---

### 4.1 replace_section — 替换简历章节

```json
{
  "name": "replace_section",
  "description": "替换简历指定章节的完整内容。适用于对某一章节进行整体重写或大范围修改。单条目精准修改请用 replace_entry。",
  "parameters": {
    "section": {
      "type": "string",
      "enum": ["personal_info", "work_experience", "education", "skills", "projects"],
      "description": "要替换的章节名称"
    },
    "content": {
      "description": "新的章节内容。personal_info 为 object；work_experience / education / projects 为 array；skills 为 string[]。必须符合 ResumeContent 中该章节的类型定义。"
    }
  }
}
```

**执行逻辑：** 将 `resumes.content[section]` 整体替换为新值，写 DB，返回写入确认。触发 `DEL resume:score:{resume_id}`。

---

### 4.2 replace_entry — 替换章节中单条条目

```json
{
  "name": "replace_entry",
  "description": "替换简历中某章节的单条条目（如单条工作经历、单条教育经历、单个项目）。比 replace_section 更精准，适合只修改特定条目。",
  "parameters": {
    "section": {
      "type": "string",
      "enum": ["work_experience", "education", "projects"],
      "description": "条目所在章节"
    },
    "index": {
      "type": "integer",
      "description": "条目序号（0-based）"
    },
    "entry": {
      "type": "object",
      "description": "新的条目内容，字段结构与该章节条目类型一致（WorkExperience / Education / Project）"
    }
  }
}
```

**执行逻辑：** 更新 `resumes.content[section][index]`，写 DB，返回更新后的条目。触发 `DEL resume:score:{resume_id}`。

---

### 4.3 modify_template — 对话式修改简历模板

```json
{
  "name": "modify_template",
  "description": "根据用户的自然语言描述修改当前简历的模板样式和布局。支持任意程度的定制：颜色、字体、布局结构、新增版块（时间轴、双栏、技能侧边栏）等。修改后前端立即重新渲染预览。",
  "parameters": {
    "description": {
      "type": "string",
      "description": "用自然语言描述想要的模板变化，例如："把工作经历改成时间轴样式"、"主色改成深蓝色"、"改成双栏布局，技能栏放左侧"。"
    }
  }
}
```

**执行逻辑（独立 LLM 调用，不在主 streaming 流中）：**

```
1. 读取当前模板 HTML：
   先查 resume:tpl:{resume_id}（Redis 30min 缓存）
   未命中 → 查 resume_template_overrides（用户已有定制版）
   不存在 → 查 templates（原始模板）
   命中后写入缓存

2. 发起独立 LLM 调用（非流式）：
   X-Request-Id = {original_request_id}-tpl  ← sub-request-id，独立 PreAuth/Settle
   Prompt:
     你是一个 HTML/CSS 专家。以下是当前简历的 Handlebars 模板：
     <template>{html}</template>
     请按用户要求修改：{description}
     规则：
     - 必须保留所有 {{...}} 数据占位符，不得丢失或改变任何数据绑定
     - 只修改结构/样式，不修改数据绑定逻辑
     - 返回完整的修改后 HTML，不得省略或截断
   返回: 完整的新 HTML 字符串

3. 校验返回的 HTML：
   - 基本结构完整性（</html> 结尾等）
   - 关键占位符存在（{{personal_info.name}}、{{#each work_experience}} 等）

4. UPSERT resume_template_overrides（按 resume_id UNIQUE KEY）

5. DEL resume:tpl:{resume_id}（清除模板缓存）
   DEL resume:score:{resume_id}

6. 通过 SSE tool_result 通知前端：{ "action": "refresh_template" }
   前端重新 fetch 该简历模板 HTML，用 Handlebars 重新渲染预览
```

**计费：** 使用 sub-request-id `{original_request_id}-tpl`，LLM Gateway 对其单独做 PreAuth/Settle，与主对话 token 独立统计，不合并。

**失败处理（⚠️ 必须实现，否则积分会被永久冻结）：**

```
LLM 调用失败（超时/Gateway 错误）:
  → 调 LLM Gateway 触发 Billing.Rollback(auth_id for -tpl)
  → 工具返回错误给主对话 LLM：{"error": "模板修改失败，请重试"}
  → 主对话继续（不终止整个 Agent 执行）

LLM 调用成功但模板 HTML 校验失败（占位符缺失等）:
  → 调 LLM Gateway 触发 Billing.Rollback(auth_id for -tpl)
  → 工具返回错误：{"error": "生成的模板结构不完整，请更具体地描述需求"}

模板 UPSERT 写 DB 失败:
  → 调 LLM Gateway 触发 Billing.Rollback(auth_id for -tpl)
  → 工具返回错误：{"error": "模板保存失败，请重试"}

所有失败路径都必须保证 Rollback 被调用，避免积分泄漏。
```

**Handlebars 数据绑定规范（LLM 调用时必须遵守）：**

```handlebars
{{personal_info.name}}
{{personal_info.email}}
{{personal_info.phone}}
{{personal_info.location}}
{{personal_info.summary}}
{{personal_info.photo_url}}

{{#each work_experience}}
  {{company}} {{title}} {{start_date}} {{end_date}} {{is_current}}
  {{#each bullets}}{{this}}{{/each}}
{{/each}}

{{#each education}}
  {{school}} {{degree}} {{major}} {{start_date}} {{end_date}}
{{/each}}

{{#each skills}}{{this}}{{/each}}

{{#each projects}}
  {{name}} {{description}} {{url}}
  {{#each highlights}}{{this}}{{/each}}
  {{#each tech_stack}}{{this}}{{/each}}
{{/each}}
```

**支持的定制范围示例：**

- "把工作经历改成时间轴样式" → LLM 输出新的时间轴 HTML 结构
- "主色改成深蓝色" → LLM 全局替换颜色 CSS 变量
- "改成双栏布局，左侧放技能和联系方式" → LLM 改 flex/grid，新增侧边栏 section
- "工作经历条目之间加分隔线" → LLM 在 `{{#each work_experience}}` 循环内添加 `<hr>`

---

### 4.4 analyze_jd — 分析职位描述

```json
{
  "name": "analyze_jd",
  "description": "分析当前简历关联的 JD，提取岗位要求、核心技能、优先级关键词，用于定向优化",
  "parameters": {}
}
```

**执行逻辑：**

1. 读取 `resumes.jd_context`，若为空则返回错误提示"请先通过 PUT /jd 接口设置目标职位描述"
2. 若 `resumes.jd_analysis` 已有缓存且 `jd_analyzed_at` 在 24h 内 → 直接返回缓存
3. 否则发起 LLM 调用（单次，非流式，约 300 tokens），结构化提取 JD 信息
4. 写入 `resumes.jd_analysis`、`resumes.jd_analyzed_at`

**返回结构：**

```json
{
  "job_title": "产品经理",
  "company": "字节跳动",
  "required_skills": ["产品规划", "数据分析", "用户研究", "A/B测试"],
  "preferred_skills": ["增长黑客", "短视频行业经验"],
  "experience_years": 3,
  "education_requirement": "本科及以上",
  "key_responsibilities": ["负责抖音社交模块产品规划", "推动跨团队协作"],
  "keywords": ["抖音", "社交", "DAU", "增长"]
}
```

---

### 4.5 evaluate_resume — 多维评估简历

```json
{
  "name": "evaluate_resume",
  "description": "对当前简历进行多维度评分（0–100），返回分项得分、改进建议和 JD 匹配度分析",
  "parameters": {
    "include_jd_match": {
      "type": "boolean",
      "description": "是否包含 JD 匹配度分析（需要 jd_context 非空）",
      "default": true
    }
  }
}
```

**执行逻辑：** 见 §6 简历评估方法（规则 + 部分 LLM 混合）。结果缓存至 `resume:score:{resume_id}`（TTL 1h），内容变更时自动失效。

---

### 工具权限边界

所有工具执行前校验 `resume.user_id == X-User-Id`，否则返回 `PERMISSION_DENIED`。工具只能操作当前对话关联的简历，不能跨简历操作。

---

## 5. Agent 记忆系统

### 5.1 记忆层次

> **自动注入原则：** 每次对话轮次（包括工具回调轮次）开始前，Resume Agent 都会从 DB 新鲜读取 `resumes.content` 和 `jd_analysis`，整体注入 System Prompt。AI 无需调用任何"读取"工具——所有简历数据已在上下文中。

```
┌─────────────────────────────────────────────────┐
│  工作记忆（Working Memory）                      │
│  每次 LLM 调用的 messages[] 数组                 │
│  ┌─────────────────────────────────────────┐    │
│  │  System Prompt（每轮从 DB 实时构建）     │    │
│  │  ├─ 角色设定与工作规则                  │    │
│  │  ├─ 简历完整内容（resumes.content JSON） │    │ ← 每轮从 DB 读取，自动注入
│  │  ├─ JD 分析结果（jd_analysis，如有）    │    │ ← 每轮从 DB 读取，自动注入
│  │  └─ 可用工具列表（5 个工具的摘要）      │    │
│  │  History Messages（最近 N 轮）          │    │ ← 从 DB 分页读取
│  │  Current User Message                  │    │
│  └─────────────────────────────────────────┘    │
│                                                  │
│  注：模板 HTML 体积较大，不注入主对话 context，  │
│      由 modify_template 工具触发独立 LLM 调用处理│
│                                                  │
│  情节记忆（Episodic Memory）                     │
│  resume_messages 表（全量历史，可分页检索）       │ ← MySQL
│                                                  │
│  语义记忆（Semantic Memory）                     │
│  v1 不实现（未来可引入向量检索相关简历/经历）     │
└─────────────────────────────────────────────────┘
```

### 5.2 上下文窗口管理

**token 预算分配（以 deepseek-chat 64K 为基准）：**

| 组成部分 | 预算 | 说明 |
|----------|------|------|
| System Prompt（角色 + 工具摘要） | ~800 tokens | 固定开销 |
| 简历 content JSON | ~2000 tokens | 完整简历约 500–3000 tokens |
| JD 分析结果 | ~500 tokens | 可选 |
| 历史对话（最近 N 轮） | ~4000 tokens | 动态截断 |
| 当前用户消息 | ~200 tokens | |
| 工具调用结果（累计） | ~1000 tokens | 每轮工具结果追加 |
| 预留输出 | ~2000 tokens | |
| **总计** | **~10500 tokens** | 远低于 64K 上限，安全 |

**历史截断策略（`CHAT_MAX_HISTORY_MESSAGES` 条，默认 20）：**

1. 从 `resume_messages` 按 `created_at DESC` 取最近 20 条
2. 计算这 20 条的 token 估算值
3. 若超过 4000 tokens，从最旧的开始删减，直到满足预算
4. 保证 System Prompt + 用户消息 + 工具结果始终完整

### 5.3 System Prompt 完整模板

> 每次对话轮次开始前，`{resume_content_json}` 和 `{jd_analysis_json}` 由 Resume Agent 从 DB 实时读取并填充。

```
你是 TadpolesAI 的 AI 简历顾问助手，代号「蝌蚪」。你的目标是帮助用户打造一份出色的求职简历。

【工作规则】
- 基于用户的真实经历进行优化，不虚构、不夸大
- 优化 bullet 时优先添加量化数据（百分比、金额、时间、规模）
- 使用强动词开头（主导、推动、设计、构建、优化、达成），避免"负责"、"参与"等弱表达
- 保持简洁专业的中文，每条 bullet 建议不超过 50 字
- 当你需要修改简历内容时，直接调用工具，不要只给文字建议
- 每次修改后，主动告诉用户改了哪里、为什么这样改

【当前简历完整内容】（已自动加载，无需调用工具读取）
{resume_content_json}

【目标职位信息】（如有）
{jd_analysis_json}

【可用工具】
你拥有以下 5 个工具可以直接操作简历：
- replace_section：替换简历某章节的全部内容（personal_info / work_experience / education / skills / projects）
- replace_entry：替换某章节中单条条目（工作经历、教育经历、项目，按 index 定位）
- modify_template：根据自然语言描述修改简历模板的样式和布局（颜色、字体、双栏、时间轴等）
- analyze_jd：深度分析目标职位 JD，提取关键技能和要求
- evaluate_resume：对简历进行多维度评分（完整性、量化、专业性、结构、JD 匹配）

注意：简历内容已在上方完整展示，你直接基于已知内容进行修改，无需先"读取"再操作。
```

### 5.4 AI 长期记忆的实现边界

UI 宣传的「AI 长期记忆」在 v1 的具体实现：

- **简历内容即记忆**：每次对话都携带完整简历内容，AI "记住"用户的教育、工作经历
- **对话历史**：同一份简历的历史对话通过 `resume_messages` 持久化，下次打开继续
- **用户信息**：通过 `X-User-Id` 关联，未来可扩展用户偏好存储

v1 **不实现**：跨简历的记忆（如"上次你说想换行业"这类跨简历上下文）、语义搜索历史记忆。

---

## 6. 简历评估方法

### 6.1 多维度评分算法（满分 100）

评分由 Resume Agent **本地算法 + AI 判断**混合实现：

#### 维度 1：内容完整性（25 分）

纯规则计算，不消耗 LLM tokens：

| 检查项 | 分值 |
|--------|------|
| 个人信息（姓名 + 邮箱 + 电话）齐全 | 5 |
| 有工作经历 ≥ 1 条 | 5 |
| 每条工作经历 bullets ≥ 2 条 | 5 |
| 有教育经历 ≥ 1 条 | 5 |
| 技能列表 ≥ 3 项 | 3 |
| 有项目经历 ≥ 1 条 | 2 |

```go
func scoreCompleteness(content ResumeContent) int {
    score := 0
    if content.PersonalInfo.Name != "" && content.PersonalInfo.Email != "" && content.PersonalInfo.Phone != "" {
        score += 5
    }
    if len(content.WorkExperience) >= 1 { score += 5 }
    allBulletsOk := true
    for _, w := range content.WorkExperience {
        if len(w.Bullets) < 2 { allBulletsOk = false; break }
    }
    if allBulletsOk && len(content.WorkExperience) > 0 { score += 5 }
    if len(content.Education) >= 1 { score += 5 }
    if len(content.Skills) >= 3 { score += 3 }
    if len(content.Projects) >= 1 { score += 2 }
    return score
}
```

#### 维度 2：量化程度（25 分）

纯规则计算，正则匹配 bullet 中的量化指标：

```go
// 量化特征：数字 + 单位（%、万、亿、倍、+）
var quantPattern = regexp.MustCompile(`\d+(\.\d+)?[%万亿倍千百十+]|\d{4,}`)

func scoreQuantification(content ResumeContent) int {
    total, quantified := 0, 0
    for _, w := range content.WorkExperience {
        for _, b := range w.Bullets {
            total++
            if quantPattern.MatchString(b) { quantified++ }
        }
    }
    for _, p := range content.Projects {
        for _, h := range p.Highlights {
            total++
            if quantPattern.MatchString(h) { quantified++ }
        }
    }
    if total == 0 { return 0 }
    rate := float64(quantified) / float64(total)
    // 60% → 满分；线性映射
    return int(math.Min(rate/0.6, 1.0) * 25)
}
```

#### 维度 3：表达专业性（25 分）— AI 评分

调 LLM 给出 0–25 的分数，评估：
- 动词强度（主导/推动 vs 负责/参与）
- 句式清晰度（避免长难句、模糊表达）
- 是否有错字/语法问题

AI Prompt（单独调用，约 500 tokens）：

```
请对以下简历的工作经历 bullets 进行表达专业性评分（0-25分）：

评分标准：
- 使用强动词（主导/设计/推动/构建/优化）：+8分
- 表达简洁清晰，无模糊用语：+9分
- 无明显语法错误或错别字：+8分

请以 JSON 回复：{"score": 20, "reason": "..."}

【Bullets 内容】
{work_bullets_text}
```

#### 维度 4：结构清晰度（25 分）— 规则 + AI 混合

规则部分（15 分）：
- 工作经历按时间倒序排列：5 分
- 各章节非空：5 分
- 个人信息无异常格式（如邮箱格式有效）：5 分

AI 部分（10 分）：AI 判断整体简历结构逻辑是否合理、段落长度是否适当。

#### 6.2 最终评分合并

```go
func CalculateScore(content ResumeContent, aiProfessionalism int, aiStructure int) int {
    s1 := scoreCompleteness(content)      // 0-25，规则
    s2 := scoreQuantification(content)    // 0-25，规则
    s3 := aiProfessionalism               // 0-25，AI
    s4 := 15 + aiStructure               // 15（规则）+ 0-10（AI）
    return s1 + s2 + s3 + s4
}
```

### 6.3 JD 匹配度分析

**触发条件：** `resumes.jd_context` 非空时，评分同时输出 `jd_match`

**算法步骤：**

```
1. 调 analyze_jd 工具（或读缓存）→ 获得 required_skills[], keywords[]

2. 关键词匹配（规则）：
   resume_text = 所有 bullets + skills[] 拼接
   matched = [kw for kw in (required_skills + keywords) if kw in resume_text]
   match_rate = len(matched) / len(required_skills + keywords)

3. 经验年限匹配（规则）：
   user_experience_years = sum of work experience durations
   if user_experience_years >= jd.experience_years → +10分 bonus
   
4. 语义相关性（AI 判断）：
   AI 给出 0-100 的匹配分，综合考虑经历与岗位的相关性

5. 最终 jd_match.score = match_rate * 60 + ai_relevance * 0.4
```

**missing_keywords 生成：**

```go
missingKeywords = filter(requiredSkills + keywords, func(kw) {
    return !strings.Contains(resumeText, kw)
})
// 取前 5 个，按重要性排序（required_skills 优先于 keywords）
```

### 6.4 评分缓存策略

```
Redis: resume:score:{resume_id}  TTL 1h

触发缓存失效的操作：
  - PUT /v1/resumes/{id}/content（内容更新）
  - PUT /v1/resumes/{id}/jd（JD 变更）
  - AI 工具调用修改了 content（replace_section / replace_entry）
```

---

## 7. 核心业务流程

### 7.1 AI 对话完整流程

```
POST /v1/resumes/{resume_id}/chat
    │
    ├─ [鉴权] X-User-Id == resume.user_id → 否则 403
    │
    ├─ [并发锁] SET resume:chat:lock:{resume_id} {X-Request-Id} NX EX 300
    │            → 已锁 → 409
    │   （TTL=300s 覆盖最坏情况：5轮×120s；value=X-Request-Id 用于 CAS 解锁）
    │
    ├─ [读记忆] SELECT resume（content + jd_context + jd_analysis）
    │           SELECT resume_messages LIMIT 20 ORDER BY created_at DESC
    │
    ├─ [组装 Context]
    │   system_prompt = template(resume.content, resume.jd_analysis)
    │   messages = [system] + reverse(history) + [{"role":"user","content":msg}]
    │
    ├─ [PreAuth] 估算 token → 调 LLM Gateway PreAuth
    │            Header: X-Request-Id（作为 Billing 幂等键）
    │
    ├─ [Agent 执行循环] max_rounds=5
    │   │
    │   ├─ POST LLM Gateway /v1/chat/completions
    │   │   Headers: X-Service-Name:resume-agent, X-User-Id, X-Request-Id
    │   │
    │   ├─ 响应含 tool_calls？
    │   │   ├─ YES → 向客户端发 tool_call SSE
    │   │   │        执行工具（DB 操作）
    │   │   │        向客户端发 tool_result SSE
    │   │   │        追加 tool result 到 messages[]
    │   │   │        DEL resume:score:{resume_id}（若工具修改了 content）
    │   │   │        DEL resume:export:{resume_id}
    │   │   │        继续循环
    │   │   │
    │   │   └─ NO  → 流式转发文字给客户端
    │   │            退出循环
    │   │
    │   └─ 超过 max_rounds → 发 error SSE，终止
    │
    ├─ [写 DB]
    │   INSERT resume_messages (role=user, content=用户消息)
    │   INSERT resume_messages (role=assistant, content=AI回复,
    │                           credits_used, billing_tx_id)
    │   INSERT usage_logs (action, credits_used, billing_tx_id)
    │
    ├─ [Settle] 通过 LLM Gateway 完成 Billing Settle（实际 token 总量）
    │
    ├─ 发送 done SSE（credits_used, billing_tx_id）
    │
    └─ [CAS 解锁] Lua: if GET == X-Request-Id then DEL end
    　　（只有当前持锁者才能解锁，防止锁过期后其他请求删掉新持有者的锁）
```

### 7.2 简历文件解析流程

```
POST /v1/resumes/{resume_id}/upload
    │
    ├─ 校验文件类型（PDF / DOCX）和大小（≤10MB）
    │
    ├─ 上传到 OSS：uploads/{user_id}/{resume_id}/original.{ext}
    │   更新 resumes.original_file_url
    │
    ├─ 提取文本：
    │   PDF  → pdfcpu / unipdf 提取纯文本
    │   DOCX → gooxml 提取段落文本
    │
    ├─ 调 LLM Gateway（单次，非流式）
    │   Prompt: 将以下简历文本解析为结构化 JSON...
    │   返回: ResumeContent JSON
    │
    ├─ 校验 AI 返回的 JSON 结构（必填字段非空）
    │
    ├─ UPDATE resumes.content = parsed_content
    │   DEL resume:score:{resume_id}
    │   DEL resume:export:{resume_id}
    │
    └─ 返回 200 + 解析后的 content
```

### 7.3 新建简历流程

```
POST /v1/resumes
    │
    ├─ start_type = "from_scratch"
    │   INSERT resumes (content = 空骨架 JSON，status=draft)
    │   返回 201
    │
    └─ start_type = "upload"
        INSERT resumes (content = null，status=draft)
        返回 201（前端紧接着调 POST /upload）
```

---

## 8. 数据库 Schema

数据库：`resume_db`，使用 GORM + MySQL 8.0

### 8.0 GORM 公共基类

```go
type Base struct {
    ID        string         `gorm:"type:char(36);primaryKey"`
    CreatedAt time.Time
    UpdatedAt time.Time
    DeletedAt gorm.DeletedAt `gorm:"index"`
}

type InsertOnlyBase struct {
    ID        string    `gorm:"type:char(36);primaryKey"`
    CreatedAt time.Time
}
```

ID 使用 ULID，`BeforeCreate` Hook 自动生成。

---

### 8.1 resumes

```sql
CREATE TABLE resumes (
    id               CHAR(36)      NOT NULL,
    user_id          CHAR(36)      NOT NULL,
    name             VARCHAR(100)  NOT NULL,
    status           ENUM('draft','completed') NOT NULL DEFAULT 'draft',
    template_id      VARCHAR(100)  NOT NULL,
    content          JSON              NULL  COMMENT '结构化简历内容（ResumeContent）',
    jd_context       TEXT              NULL  COMMENT '用户粘贴的原始 JD 文本',
    jd_analysis      JSON              NULL  COMMENT 'AI 解析 JD 的结构化结果，缓存避免重复调用',
    jd_analyzed_at   DATETIME(3)       NULL  COMMENT 'jd_analysis 的生成时间，超 24h 需重新分析',
    score            TINYINT UNSIGNED  NULL  COMMENT 'AI 综合评分 0-100',
    original_file_url VARCHAR(512)     NULL  COMMENT '用户上传的原始文件 OSS URL',
    created_at       DATETIME(3)   NOT NULL,
    updated_at       DATETIME(3)   NOT NULL,
    deleted_at       DATETIME(3)       NULL,

    PRIMARY KEY (id),
    KEY idx_resumes_user_del    (user_id, deleted_at),
    KEY idx_resumes_user_update (user_id, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**ResumeContent JSON 结构（Go 类型）：**

```go
type ResumeContent struct {
    PersonalInfo   PersonalInfo     `json:"personal_info"`
    WorkExperience []WorkExperience `json:"work_experience"`
    Education      []Education      `json:"education"`
    Skills         []string         `json:"skills"`
    Projects       []Project        `json:"projects"`
}

type PersonalInfo struct {
    Name     string `json:"name"`
    Phone    string `json:"phone"`
    Email    string `json:"email"`
    Location string `json:"location"`
    PhotoURL string `json:"photo_url,omitempty"`
    Summary  string `json:"summary,omitempty"`  // 个人简介
}

type WorkExperience struct {
    Company   string   `json:"company"`
    Title     string   `json:"title"`
    StartDate string   `json:"start_date"`       // YYYY-MM
    EndDate   *string  `json:"end_date"`          // null = 在职
    IsCurrent bool     `json:"is_current"`
    Bullets   []string `json:"bullets"`
}

type Education struct {
    School    string  `json:"school"`
    Degree    string  `json:"degree"`
    Major     string  `json:"major,omitempty"`
    StartDate string  `json:"start_date"`
    EndDate   string  `json:"end_date"`
    GPA       *string `json:"gpa,omitempty"`
    Honors    []string `json:"honors,omitempty"`
}

type Project struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    TechStack   []string `json:"tech_stack,omitempty"`
    Highlights  []string `json:"highlights"`
    URL         *string  `json:"url,omitempty"`
}
```

**GORM Model：**

```go
type Resume struct {
    Base
    UserID          string         `gorm:"type:char(36);not null"`
    Name            string         `gorm:"size:100;not null"`
    Status          string         `gorm:"type:enum('draft','completed');default:'draft';not null"`
    TemplateID      string         `gorm:"size:100;not null"`
    Content         *ResumeContent `gorm:"serializer:json"`
    JDContext       *string        `gorm:"type:text"`
    JDAnalysis      *JDAnalysis    `gorm:"serializer:json"`
    JDAnalyzedAt    *time.Time
    Score           *uint8
    OriginalFileURL *string        `gorm:"size:512"`
}
```

---

### 8.2 resume_messages

```sql
CREATE TABLE resume_messages (
    id            CHAR(36)     NOT NULL,
    resume_id     CHAR(36)     NOT NULL,
    user_id       CHAR(36)     NOT NULL,
    role          ENUM('user','assistant') NOT NULL,
    content       TEXT         NOT NULL,
    tool_calls    JSON             NULL  COMMENT 'AI 本轮发起的工具调用记录（用于调试和重放）',
    credits_used  INT          NOT NULL DEFAULT 0,
    billing_tx_id CHAR(36)         NULL,
    created_at    DATETIME(3)  NOT NULL,

    PRIMARY KEY (id),
    KEY idx_rm_resume_created (resume_id, created_at),
    KEY idx_rm_user_created   (user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**GORM Model：**

```go
type ResumeMessage struct {
    InsertOnlyBase
    ResumeID     string     `gorm:"type:char(36);not null"`
    UserID       string     `gorm:"type:char(36);not null"`
    Role         string     `gorm:"type:enum('user','assistant');not null"`
    Content      string     `gorm:"type:text;not null"`
    ToolCalls    *[]ToolCallRecord `gorm:"serializer:json"`
    CreditsUsed  int        `gorm:"default:0;not null"`
    BillingTxID  *string    `gorm:"type:char(36)"`
}

type ToolCallRecord struct {
    Name   string          `json:"name"`
    Args   json.RawMessage `json:"args"`
    Result json.RawMessage `json:"result"`
}
```

---

### 8.3 templates

```sql
CREATE TABLE templates (
    id             VARCHAR(100) NOT NULL,
    name           VARCHAR(100) NOT NULL,
    category       VARCHAR(50)  NOT NULL,
    thumbnail_url  VARCHAR(512) NOT NULL,
    html_template  MEDIUMTEXT   NOT NULL  COMMENT 'Handlebars HTML 模板，前端渲染；占位符见 §4.3 规范',
    is_recommended TINYINT(1)   NOT NULL DEFAULT 0,
    sort_order     INT          NOT NULL DEFAULT 0,
    created_at     DATETIME(3)  NOT NULL,
    updated_at     DATETIME(3)  NOT NULL,

    PRIMARY KEY (id),
    KEY idx_tpl_category_sort (category, sort_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

### 8.5 resume_template_overrides

用户通过对话定制模板后，修改结果存这张表，**不覆盖**原始 `templates` 表，做到用户级隔离。

```sql
CREATE TABLE resume_template_overrides (
    id            CHAR(36)    NOT NULL,
    resume_id     CHAR(36)    NOT NULL,
    user_id       CHAR(36)    NOT NULL,
    html_template MEDIUMTEXT  NOT NULL  COMMENT '用户定制后的 Handlebars HTML',
    created_at    DATETIME(3) NOT NULL,
    updated_at    DATETIME(3) NOT NULL,

    PRIMARY KEY (id),
    UNIQUE KEY uq_rto_resume (resume_id)  -- 每份简历只有一条 override
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**GORM Model：**

```go
type ResumeTemplateOverride struct {
    Base
    ResumeID     string `gorm:"type:char(36);not null;uniqueIndex"`
    UserID       string `gorm:"type:char(36);not null"`
    HtmlTemplate string `gorm:"type:mediumtext;not null"`
}
```

**模板读取优先级（前端 `GET /v1/resumes/{id}` 或 `GET /v1/templates/{id}` 时）：**

```
resume_template_overrides（用户定制版）
    ↓ 不存在
templates（原始模板）
```

---

### 8.4 usage_logs

```sql
CREATE TABLE usage_logs (
    id             CHAR(36)    NOT NULL,
    user_id        CHAR(36)    NOT NULL,
    resume_id      CHAR(36)        NULL,
    action         VARCHAR(50) NOT NULL  COMMENT 'ai_chat/ai_parse/ai_score/ai_jd_analyze/ai_modify_template',
    credits_used   INT         NOT NULL,
    billing_tx_id  CHAR(36)        NULL,
    created_at     DATETIME(3) NOT NULL,

    PRIMARY KEY (id),
    KEY idx_ul_user_created   (user_id, created_at),
    KEY idx_ul_action_created (user_id, action, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 9. Redis Key 设计

| Key 模式 | 类型 | TTL | 用途 |
|----------|------|-----|------|
| `resume:chat:lock:{resume_id}` | String | **300s** | 并发 AI 对话互斥锁；value=X-Request-Id，CAS 解锁（Lua 脚本保证只有持锁者能释放） |
| `resume:score:{resume_id}` | String (JSON) | 1h | 评分结果缓存（含 dimensions + jd_match） |
| `resume:tpl:{resume_id}` | String (HTML) | 30min | 模板 HTML 缓存（override 优先，原始模板兜底）；modify_template 工具执行后 DEL |

**锁超时保障：** AI 流结束或出错时主动 `DEL`；60s TTL 是兜底，防止进程崩溃导致死锁。

---

## 10. 文件存储（阿里云 OSS）

| 对象路径 | 权限 | 说明 |
|----------|------|------|
| `uploads/{user_id}/{resume_id}/original.{ext}` | 私有 | 用户上传原始文件，服务端直传 |
| `templates/{template_id}/thumbnail.png` | 公开读 | 模板缩略图，走 CDN |
| `avatars/{user_id}/photo.jpg` | 公开读 | 简历头像，前端 STS 直传 |

> PDF 由前端导出，不经过 OSS，无需服务端存储。

**头像上传（OSS STS 临时凭证）：**

```
GET /v1/oss/sts/avatar-upload
→ Resume Agent 调阿里云 STS 颁发临时凭证（限定 path: avatars/{user_id}/*）
→ 前端拿凭证直接传 OSS，不经过 Resume Agent
→ 前端传完后调 PUT /v1/resumes/{id}/content 更新 photo_url
```

---

## 11. PDF 导出（前端实现）

PDF 导出完全在**浏览器侧**完成，后端无任何参与。

**前端完整流程：**

```
用户点击「导出 PDF」
    │
    ├─ 前端已持有当前简历 content（state）
    │   + 已渲染的模板 HTML（Handlebars 渲染后的 DOM）
    │
    ├─ html2pdf.js 捕获 .resume-preview DOM 节点
    │   → html2canvas 将 DOM 截图为 canvas（scale=2 确保清晰度）
    │   → jsPDF 将 canvas 转为 A4 PDF
    │
    └─ 触发浏览器下载：{resumeName}.pdf
```

**优势对比：**

| | 前端导出 | 后端 Chromedp |
|--|---------|--------------|
| 与预览一致性 | ✅ 100% 所见即所得 | ⚠️ 可能有字体/渲染差异 |
| 模板定制后可导出 | ✅ 即改即导 | ❌ 需服务端重新渲染 |
| 服务端开销 | ✅ 零 | ❌ CPU 密集，需安装 Chrome |
| OSS 存储 | ✅ 不需要 | ❌ 需存储 PDF 文件 |
| 离线可用 | ✅ 本地执行 | ❌ 依赖网络 |

PDF 导出**不消耗积分**。

---

## 12. 服务间交互全景

```
                    ┌──────────────┐
                    │    前端       │
                    └──────┬───────┘
                           │ HTTPS
                    ┌──────▼───────┐
                    │    APISIX    │  JWT 验签，注入
                    │  API Gateway │  X-User-Id / X-User-Group / X-Request-Id
                    └──────┬───────┘
                           │ HTTP
          ┌────────────────▼────────────────┐
          │          Resume Agent            │
          │  (HTTP :8082)                   │
          │                                 │
          │  ┌──────────────────────────┐  │
          │  │    Agent Executor         │  │
          │  │  - Tool Runner            │  │
          │  │  - Context Builder        │  │
          │  │  - Score Calculator       │  │
          │  └──────────┬───────────────┘  │
          └─────────────┼───────────────────┘
                        │
          ┌─────────────┼──────────────────────────┐
          │             │                           │
   ┌──────▼──────┐  ┌──▼──────┐  ┌───────────────┐│
   │ LLM Gateway │  │  MySQL  │  │  阿里云 OSS   ││
   │  HTTP :8080 │  │resume_db│  │ 文件存储       ││
   │             │  └─────────┘  └───────────────┘│
   │  ┌────────┐ │       │                         │
   │  │Billing │ │  ┌────▼────┐                    │
   │  │PreAuth/│ │  │  Redis  │                    │
   │  │Settle  │ │  │  Cache  │                    │
   │  └────────┘ │  └─────────┘                    │
   └─────────────┘                                  │
          │                                          │
   ┌──────▼──────┐                                  │
   │   Billing   │◄─────────────────────────────────┘
   │   Service   │  GET /v1/internal/billing-stats
   │  gRPC :9094 │  （Billing 主动拉 Resume Agent 统计）
   └─────────────┘

   ─────────────────────────────────────────────────
   调用方向说明：
   → Resume Agent → LLM Gateway（AI 推理）
   → LLM Gateway → Billing Service（PreAuth/Settle，gRPC）
   → Billing Service → Resume Agent（billing-stats 查询，HTTP）
   → Resume Agent → OSS（文件读写，SDK）
   → Resume Agent → Redis（缓存/锁）
   → Resume Agent → MySQL（数据读写）
   ─────────────────────────────────────────────────
```

### 12.1 关键 Header 传播链

```
前端 → APISIX → Resume Agent → LLM Gateway → Billing Service

X-User-Id:      {user_id}        ← APISIX 从 JWT sub 注入，全链路透传
X-User-Group:   {group}          ← APISIX 从 JWT group 注入；Resume Agent 用于选模型（MODEL_FOR_FREE/DEFAULT/PREMIUM）；
                                    透传至 LLM Gateway（Gateway 写 usage_logs，不参与路由）
X-Request-Id:   {request_id}     ← 前端生成，APISIX 透传，Resume Agent 透传至 LLM Gateway；
                                    LLM Gateway 用此作为 Billing PreAuth 幂等键；
                                    多轮 Agent 调用共用同一 ID，modify_template 用 {id}-tpl
X-Service-Name: resume-agent     ← Resume Agent 自己加（⚠️ 必填，不能省略）；
                                    LLM Gateway 原样透传至 Billing PreAuth/Settle；
                                    credit_transactions.service_name = "resume-agent"；
                                    账单页展示和对账依赖此字段
```

### 12.2 计费对账链

```
resume_agent.usage_logs.billing_tx_id
    ↕ 一一对应
billing_service.credit_transactions.id
    ↕ 一一对应（通过 X-Request-Id 追溯）
llm_gateway.usage_logs.billing_transaction_id
```

---

## 13. Billing 统计接口

实现 Billing Service §5 定义的可插拔 billing-stats 标准契约：

```
GET /v1/internal/billing-stats?user_id={uid}&start={iso8601}&end={iso8601}
```

**实现：** 查 `usage_logs` 表，GROUP BY action，聚合 SUM(credits_used)

**响应：**

```json
{
  "service_name": "resume-agent",
  "user_id": "01J...",
  "period": { "start": "2026-03-10T00:00:00Z", "end": "2026-04-10T00:00:00Z" },
  "total_credits_used": 480,
  "breakdown": [
    { "action": "ai_chat",            "count": 12, "credits": 360 },
    { "action": "ai_parse",           "count": 3,  "credits": 90  },
    { "action": "ai_score",           "count": 3,  "credits": 20  },
    { "action": "ai_jd_analyze",      "count": 2,  "credits": 10  },
    { "action": "ai_modify_template", "count": 4,  "credits": 24  }
  ]
}
```

---

## 14. 环境变量

```env
# 服务端口
HTTP_PORT=8082

# 数据库
RESUME_DB_DSN=user:pass@tcp(mysql:3306)/resume_db?charset=utf8mb4&parseTime=True

# Redis
REDIS_ADDR=redis:6379
REDIS_PASSWORD=

# LLM Gateway
LLM_GATEWAY_URL=http://llm-gateway:8083
LLM_GATEWAY_INTERNAL_KEY=your-internal-api-key    # LLM Gateway 内部鉴权 key

# 模型路由（按用户分组，对应 LLM Gateway 中的逻辑模型 ID）
MODEL_FOR_FREE=deepseek-chat
MODEL_FOR_DEFAULT=deepseek-chat
MODEL_FOR_PREMIUM=gpt-4o

# 阿里云 OSS
OSS_ENDPOINT=https://oss-cn-hangzhou.aliyuncs.com
OSS_BUCKET=tadpoles-ai
OSS_ACCESS_KEY_ID=your-key-id
OSS_ACCESS_KEY_SECRET=your-key-secret
OSS_CDN_BASE=https://cdn.tadpoles.ai

# OSS STS（前端头像直传临时凭证）
STS_ROLE_ARN=acs:ram::xxxx:role/oss-upload-role
STS_EXPIRY_SECONDS=3600

# Agent 配置
AGENT_MAX_TOOL_ROUNDS=5             # 单次对话最大工具调用轮次
CHAT_MAX_HISTORY_MESSAGES=20        # 上下文携带的最近历史消息数
CHAT_LOCK_TTL_SECONDS=60            # 对话并发锁 TTL

# 评分缓存
SCORE_CACHE_TTL_SECONDS=3600

# 积分单价（credits，仅作配置参考；实际计费由 LLM Gateway token 计算）
CREDITS_AI_PARSE=30                 # 简历文件解析（固定估算）
CREDITS_AI_SCORE_FIXED=5           # 评分中规则部分的固定积分
```

---

## 15. 错误码约定

| HTTP Status | code | 场景 |
|-------------|------|------|
| 400 | `invalid_param` | 参数格式错误（文件类型不支持、名称超长等） |
| 403 | `forbidden` | 简历不属于当前用户 |
| 404 | `not_found` | 简历或模板不存在 |
| 409 | `chat_in_progress` | 该简历已有 AI 对话进行中，请等待完成 |
| 413 | `file_too_large` | 上传文件超过 10MB |
| 422 | `parse_failed` | 文件解析失败（无法提取文本） |
| 402 | `insufficient_credits` | 积分不足（Billing 返回余额不足，与 LLM Gateway 统一） |
| 429 | `rate_limited` | 触发 LLM Gateway 限流 |
| 503 | `gateway_unavailable` | LLM Gateway 不可用 |
