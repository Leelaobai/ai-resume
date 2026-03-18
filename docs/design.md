# AI Resume Agent - 实施计划

## Context

用户是Go后端开发者，近期失业转AI方向求职。需要2-3天内完成一个AI简历生成Agent项目：(1)自用写简历 (2)开源展示Agent开发能力。架构需涵盖Agent核心模块（tools、session、memory、RAG等），面试可谈。

## 技术栈

- **后端（主力）**: Go + Gin —— Agent核心、API、所有业务逻辑
- **辅助脚本**: Python —— 简历文件解析（pdfplumber/python-docx）、RAG种子数据处理
- **前端**: Vue 3 + Vite + TypeScript
- **数据库**: PostgreSQL + pgvector
- **LLM**: OpenRouter API（Claude Opus等）
- **PDF导出**: chromedp（headless Chrome）
- **Go核心库**: pgx v5, chromedp, golang-migrate
- **Python库**: pdfplumber, python-docx（仅用于解析脚本）

## 架构概览

```
用户消息 → [短期记忆] + [长期记忆] + [RAG检索] → 组装Prompt → [LLM调用]
                                                                    ↓
                                               文本回复 ←── 或 ──→ Tool调用
                                                 ↓                    ↓
                                              SSE推流            执行工具 → 更新简历/记忆
                                                                    ↓
                                                              追加结果，重新调LLM
```

## 7大模块


| 模块                    | 目录                              | 核心职责                                      |
| --------------------- | ------------------------------- | ----------------------------------------- |
| 1. Agent Core         | `internal/agent/`               | ReAct循环、Prompt组装、流式响应                     |
| 2. Tools System       | `internal/tools/`               | 工具注册/发现/执行（extract_info, update_section等） |
| 3. Session Management | `internal/session/`             | 多轮对话持久化、会话列表                              |
| 4. Memory System      | `internal/memory/`              | 短期（滑动窗口）+ 长期（用户画像/偏好）                     |
| 5. RAG Module         | `internal/rag/`                 | Embedding + pgvector相似检索，简历最佳实践           |
| 6. Resume Engine      | `internal/resume/`              | 结构化数据模型 + HTML模板渲染 + PDF导出                |
| 7. Resume Parser      | `internal/parser/` + `scripts/` | 上传PDF/DOCX → Python脚本提取文本 → Go调LLM结构化     |


## 项目结构

```
ai-resume/
├── docker-compose.yml           # PostgreSQL + pgvector
├── Makefile                     # dev, build, migrate, seed
├── backend/
│   ├── cmd/server/main.go
│   ├── config/config.go
│   ├── internal/
│   │   ├── agent/               # agent.go, prompt.go, stream.go
│   │   ├── tools/               # registry.go, types.go, 各工具实现
│   │   ├── session/             # manager.go, store.go, types.go
│   │   ├── memory/              # short_term.go, long_term.go, summarizer.go
│   │   ├── rag/                 # embedder.go, indexer.go, retriever.go
│   │   ├── resume/              # model.go, renderer.go, exporter.go, templates/
│   │   ├── parser/              # parser.go (调用Python脚本提取文本，再LLM结构化)
│   │   ├── llm/                 # client.go, types.go, stream.go
│   │   └── store/               # postgres.go, migrations/
│   └── api/                     # router.go, handler各文件
├── scripts/                     # Python辅助脚本
│   ├── parse_resume.py          # PDF/DOCX文本提取（pdfplumber + python-docx）
│   ├── seed_rag.py              # RAG种子数据Embedding+入库
│   └── requirements.txt         # Python依赖（pdfplumber, python-docx, psycopg2）
├── frontend/
│   └── src/
│       ├── components/          # ChatPanel, ResumePreview, FileUpload等
│       ├── composables/         # useChat.ts, useResume.ts
│       └── stores/              # Pinia状态管理
└── docs/                        # 架构文档（面试用）
```

## 数据库设计

- **sessions** - 会话表（id, title, timestamps）
- **messages** - 消息表（session_id, role, content, tool_calls JSONB）
- **resumes** - 简历数据表（session_id, data JSONB, template_id）—— 单JSONB列存完整简历
- **memory_facts** - 长期记忆表（category, key, value, confidence）
- **rag_documents** - RAG文档表（content, embedding VECTOR(1536), metadata）
- **conversation_summaries** - 对话摘要表（用于短期记忆压缩）

## API设计

```
POST   /api/sessions/:id/chat        # 发消息，返回SSE流
GET    /api/sessions/:id/messages     # 消息历史
CRUD   /api/sessions                  # 会话管理
GET    /api/sessions/:id/resume       # 获取简历数据
GET    /api/sessions/:id/resume/html  # 渲染后HTML
GET    /api/sessions/:id/resume/pdf   # 导出PDF
POST   /api/sessions/:id/upload       # 上传简历解析
GET    /api/templates                 # 模板列表
```

SSE事件类型：`token`(文本流) | `tool_call`(工具调用) | `resume_update`(刷新预览) | `done` | `error`

## Agent Tools


| 工具                                  | 功能             |
| ----------------------------------- | -------------- |
| `extract_user_info`                 | 从对话提取结构化信息存入记忆 |
| `update_resume_section`             | 更新简历指定部分       |
| `get_current_resume`                | 获取当前简历状态       |
| `suggest_improvements`              | 基于RAG建议改进      |
| `search_templates` / `set_template` | 模板管理           |


## 实施节奏

### Day 1：基础 + Agent核心（能跑通的聊天）

**目标**：聊天UI能和LLM对话，agent能调用工具更新简历，右侧实时预览

1. 项目脚手架：go mod、Gin骨架、Vue+Vite、docker-compose
2. 数据库迁移脚本 + pgvector
3. `internal/llm/` —— OpenRouter HTTP客户端 + SSE流式解析
4. `internal/session/` —— 会话+消息CRUD
5. `internal/tools/` —— Tool接口、Registry、实现`update_resume_section`和`get_current_resume`
6. `internal/agent/` —— ReAct循环（组装prompt → 调LLM → 处理tool_calls → 循环）
7. `api/` —— Gin路由、CORS、SSE流式chat端点、会话CRUD端点
8. `internal/resume/model.go` —— Resume结构体
9. 前端：分栏布局、ChatPanel + SSE消费、基础ResumePreview

### Day 2：简历渲染 + 解析 + 记忆

**目标**：完整简历HTML渲染、模板切换、文件上传解析、记忆系统

1. `internal/resume/renderer.go` —— HTML模板渲染（2-3个模板）
2. `internal/resume/exporter.go` —— chromedp导出PDF（A4）
3. `internal/parser/` + `scripts/parse_resume.py` —— Go调用Python脚本提取PDF/DOCX文本 → LLM结构化
4. `internal/memory/` —— 短期滑动窗口 + 长期事实存储
5. 前端：iframe预览、模板选择器、文件上传组件
6. `suggest_improvements` 工具（先用硬编码最佳实践）

### Day 3：RAG + 打磨 + 文档

**目标**：RAG工作、全模块联通、README和架构文档

1. `internal/rag/` —— Embedding + pgvector检索
2. `scripts/seed_rag.py` —— 种子数据：10-15篇简历最佳实践文档，批量Embedding入库
3. `memory/summarizer.go` —— 长对话摘要压缩
4. 前端打磨：Markdown渲染、打字指示器、工具调用可视化
5. System prompt精调
6. README + 架构文档（面试准备用）

## 核心设计原则

**Resume JSON 是唯一数据源（Source of Truth）**

- 所有修改都是patch JSON字段，再重新渲染HTML
- 用户说"帮我修改B公司的描述" → Agent更新JSON中对应section → 重新渲染
- 绝不重新生成整份简历，避免丢失已有内容

**ReAct循环（而非显式Planner）**

- Agent每一步自己决定：思考 → 选择工具 → 执行 → 观察结果 → 决定下一步
- 比固定Plan更灵活，适合对话式交互（用户随时改需求）
- 面试时可以讲ReAct模式，这是主流Agent架构

## 用户交互流程模拟（面试可讲）

```
=== 场景：用户有3段工作经历，从零开始到导出PDF ===

第1轮：用户开启对话
  用户: "我想写一份Go后端工程师的简历"
  Agent思考: 需要收集信息 → 回复引导问题
  Agent: "好的！请告诉我你的基本信息和工作经历..."
  [短期记忆: 记录对话]

第2轮：用户输入经历
  用户: "我叫张三，5年Go开发。A公司做电商订单系统，B公司做支付，C公司做用户中心"
  Agent思考: 有足够信息了 → 调用extract_user_info工具 → 调用update_resume_section工具
  [工具调用: extract_user_info → 提取结构化数据]
  [工具调用: update_resume_section → 写入Resume JSON]
  [长期记忆: 存储技能facts、经历facts]
  [SSE事件: resume_update → 前端右侧实时刷新简历预览]
  Agent: "已生成简历初稿，你可以在右侧预览。要修改哪里？"

第3轮：用户修改
  用户: "B公司的支付系统我还做了Redis高并发优化"
  Agent思考: 需要更新特定section → 调用update_resume_section
  [工具调用: update_resume_section("experience[1]", 追加描述)]
  [Resume JSON被patch，HTML重新渲染]
  Agent: "已更新B公司的描述，加入了Redis高并发优化"

第4轮：用户要求优化
  用户: "帮我把简历控制在一页A4"
  Agent思考: 需要精简内容 → 调用suggest_improvements(RAG检索最佳实践)
  [工具调用: suggest_improvements → RAG检索"简历精简技巧"]
  [工具调用: update_resume_section → 精简各section]
  Agent: "已精简到一页，主要压缩了项目描述的冗余部分"

第5轮：导出
  用户: "可以了，导出PDF"
  [API调用: GET /api/sessions/:id/resume/pdf]
  [chromedp渲染HTML → A4 PDF]
  Agent: "PDF已生成，点击下载"
```

## 关键技术决策

- **自研LLM客户端**（~200行）而非用第三方SDK → 完全控制SSE和tool calling
- **Resume存为单JSONB列** → 避免复杂关系表，LLM工具直接patch JSON路径
- **pgx直接写SQL** → 对pgvector操作最友好，避免ORM抽象
- **SSE而非WebSocket** → 单向推流足够，更简单，浏览器原生支持
- **chromedp导出PDF** → 真实浏览器引擎渲染，CSS保真度最高
- **不做认证** → 单用户本地优先，降低复杂度

## 验证方式

1. `docker-compose up` 启动PostgreSQL
2. `make migrate` 执行数据库迁移
3. `make dev` 同时启动Go后端和Vue前端
4. 浏览器打开，新建会话，输入"我是张三，5年Go开发经验"
5. 验证：agent调用`update_resume_section`，右侧简历预览实时更新
6. 上传一份PDF简历，验证自动解析提取
7. 点击导出PDF，验证生成A4格式文件

