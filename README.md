# AI Resume Agent

An AI-powered resume writing assistant built with a ReAct Agent architecture. Through natural conversation, the AI Agent helps you create, refine, and export professional resumes.

## Architecture

```
User Message
     │
     ▼
┌───────────────────────────────────────────────────────┐
│                    Agent Core                         │
│                                                       │
│  ┌───────────┐   ┌──────────┐   ┌──────────────────┐  │
│  │ Short-term│   │Long-term │   │  System Prompt   │  │
│  │  Memory   │   │  Memory  │   │  + Memory Context│  │
│  │(recent N) │   │ (facts)  │   │                  │  │
│  └────┬──────┘   └────┬─────┘   └────────┬─────────┘  │
│       └───────────────┼──────────────────┘            │
│                       ▼                               │
│              Assemble Messages                        │
│                       │                               │
│                       ▼                               │
│              ┌────────────────┐                       │
│              │   LLM Call     │◄──────────────┐       │
│              │ (OpenRouter)   │               │       │
│              └───────┬────────┘               │       │
│                      │                        │       │
│               ┌──────┴──────┐                 │       │
│               ▼             ▼                 │       │
│          Text Reply    Tool Calls             │       │
│               │             │                 │       │
│               ▼             ▼                 │       │
│           SSE Stream   Execute Tools ─────────┘       │
│           to Client    (ReAct Loop, max n(10) rounds) │
│                                                       │
└───────────────────────────────────────────────────────┘
```

### Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go + Gin |
| Frontend | Vue 3 + TypeScript + Vite |
| Database | PostgreSQL + pgvector |
| LLM | OpenRouter API (Claude Sonnet) + Local model via oMLX (optional) |
| PDF Export | chromedp (headless Chrome) |
| File Parsing | Python (pdfplumber + python-docx) |

## Modules

### 1. Agent Core (`internal/agent/`)

Implements the **ReAct (Reason + Act)** pattern:

```
Think → Select Tool → Execute → Observe Result → Decide Next Step
```

The agent autonomously decides which tools to call based on the conversation. Each turn, the LLM either returns text (conversation ends) or tool calls (loop continues). Max 10 iterations per user message to prevent infinite loops.

Key design: **Resume JSON is the single source of truth.** All modifications patch specific JSON fields, never regenerate the entire resume.

### 2. Tool System (`internal/tools/`)

Interface-based design with a central registry:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}
    Execute(ctx context.Context, args json.RawMessage) (string, error)
}
```

Session ID is passed via `context.Context`, making tools stateless and shareable across sessions.

| Tool | Purpose |
|------|---------|
| `update_resume_section` | Patch any resume section (basic_info, experience, skills, etc.) |
| `extract_user_info` | Extract structured facts from conversation into long-term memory |
| `match_jd` | Compare resume against a job description, identify gaps |
| `get_current_template` | Read current HTML template source |
| `update_resume_style` | Update resume HTML template (CSS/layout) |

> Resume data is pre-injected into the system prompt on every turn (from DB), eliminating the need for a `get_current_resume` tool call.

### 3. Session Management (`internal/session/`)

Multi-turn conversation persistence. Each session holds its own message history and linked resume data.

### 4. Memory System (`internal/memory/`)

Two-layer memory architecture:

- **Short-term**: Recent conversation messages loaded as LLM context
- **Long-term**: Structured facts (category/key/value) extracted by the `extract_user_info` tool, injected into the system prompt

**Conversation Summarizer**: When context tokens exceed a configurable threshold, older messages are compressed into a summary via LLM. Summaries are stored incrementally (each new summary merges with the previous one). Original messages are never deleted — summaries only affect what's sent to the LLM. If a local model is configured, the Summarizer runs on it (async, non-blocking) while Agent and Parser always use the remote model.

```
Token count after last summary > threshold?
  ├─ No  → Use messages directly
  └─ Yes → TryAcquireSummary (atomic: check pending + create pending in one tx)
           ├─ Pending exists & not timed out → Skip
           ├─ Pending exists & timed out (3min) → Delete old, create new pending
           └─ No pending → Create pending
           → Async goroutine generates summary (3min timeout)
           → On success: CompleteSummary (status → done)
           → On failure: DeletePendingSummary (allows retry)
           → Next buildMessages: summary (status=done) + messages after last_message_id
```

### 5. Resume Engine (`internal/resume/`)

- **Data Model**: Structured JSON (BasicInfo, Summary, Experience, Education, Skills, Projects, Certifications, Languages)
- **Renderer**: Go `html/template` with embedded HTML templates
- **Exporter**: chromedp renders HTML to A4 PDF (8.27in × 11.69in)

### 6. Resume Parser (`internal/parser/`)

Upload flow: PDF/DOCX → Python script extracts raw text → Go sends text to LLM for structured JSON extraction → Parsed data sent to Agent for intelligent merging (not direct DB overwrite).

### 7. LLM Client (`internal/llm/`)

Custom HTTP client (~200 lines) for OpenRouter's OpenAI-compatible API. Supports both streaming (SSE via `bufio.Scanner`) and non-streaming calls. No third-party SDK — full control over request/response handling.

## API

```
POST   /api/sessions              # Create session
GET    /api/sessions              # List sessions
DELETE /api/sessions/:id          # Delete session
POST   /api/sessions/:id/chat     # Chat (SSE stream)
GET    /api/sessions/:id/messages # Message history
GET    /api/sessions/:id/resume   # Resume JSON
GET    /api/sessions/:id/resume/pdf  # Export PDF
POST   /api/sessions/:id/upload   # Upload resume file
```

### SSE Event Types

| Event | Description |
|-------|-------------|
| `token` | Incremental text content |
| `tool_call` | Agent is calling a tool (name + args) |
| `tool_result` | Tool execution result |
| `resume_update` | Resume data changed, refresh preview |
| `done` | Stream complete |
| `error` | Error occurred |

## Database Schema

```sql
sessions            -- Conversation sessions
messages            -- Chat messages (user/assistant/tool)
resumes             -- Resume data (JSONB), one per session
memory_facts        -- Long-term memory (category/key/value)
conversation_summaries -- Summaries with last_message_id + status (pending/done)
```

## Getting Started

### Prerequisites

- Go 1.21+
- Node.js 18+
- Docker
- Python 3 (with pdfplumber, python-docx)
- Google Chrome (for PDF export)

### Setup

```bash
# 1. Clone
git clone https://github.com/Leelaobai/ai-resume.git
cd ai-resume

# 2. Start PostgreSQL
make db

# 3. Run database migrations
docker exec ai-resume-db psql -U resume -d ai_resume -f /path/to/001_init.up.sql

# 4. Configure environment
cat > backend/.env << EOF
LLM_API_KEY=your_openrouter_api_key
LLM_MODEL=anthropic/claude-sonnet-4.6
DB_HOST=localhost
DB_PORT=5433
DB_USER=resume
DB_PASSWORD=resume123
DB_NAME=ai_resume
SERVER_PORT=8090
SUMMARIZE_THRESHOLD=200000
KEEP_RECENT_TOKENS=50000
EOF

# 5. Install Python dependencies
pip install pdfplumber python-docx

# 6. Start backend
make backend

# 7. Start frontend (new terminal)
make frontend
```

Open `http://localhost:5173` in your browser.

### Usage

1. Click **"+ New Session"** to create a conversation
2. Describe your experience, skills, and background in the chat
3. The AI Agent will automatically structure your input into a resume (visible in the right panel)
4. Paste a job description to get targeted optimization suggestions
5. Upload an existing resume (PDF/DOCX) for AI-powered merging
6. Click **"Export PDF"** to download

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_API_KEY` | (required) | OpenRouter API key |
| `LLM_MODEL` | `anthropic/claude-sonnet-4.6` | LLM model identifier |
| `LOCAL_LLM_BASE_URL` | `http://localhost:8000/v1` | Local model API endpoint (oMLX) |
| `LOCAL_LLM_MODEL` | (empty) | Local model name. If empty, all LLM calls use remote |
| `LOCAL_LLM_API_KEY` | `no-key` | Local model API key |
| `DB_PORT` | `5433` | PostgreSQL port |
| `SERVER_PORT` | `8090` | Backend HTTP port |
| `SUMMARIZE_THRESHOLD` | `200000` | Token count to trigger summarization |
| `KEEP_RECENT_TOKENS` | `50000` | Tokens to keep after summarization |
| `REACT_MAX_LOOP` | `10` | Max ReAct tool-call iterations per message |

## Project Structure

```
ai-resume/
├── backend/
│   ├── cmd/server/main.go          # Entry point
│   ├── config/config.go            # Configuration
│   ├── api/                        # HTTP handlers
│   │   ├── router.go
│   │   ├── chat_handler.go         # SSE streaming
│   │   ├── session_handler.go
│   │   ├── resume_handler.go
│   │   └── upload_handler.go
│   └── internal/
│       ├── agent/                  # ReAct Agent core
│       ├── tools/                  # Tool interface & implementations
│       ├── llm/                    # LLM HTTP client
│       ├── session/                # Session & message management
│       ├── memory/                 # Short-term + long-term memory
│       ├── resume/                 # Data model, renderer, PDF exporter
│       ├── parser/                 # Resume file parser
│       └── store/                  # Database connection & migrations
├── frontend/
│   └── src/
│       ├── api/client.ts           # API client + SSE consumer
│       ├── stores/chat.ts          # Pinia state management
│       └── components/
│           ├── ChatPanel.vue       # Chat with markdown rendering
│           ├── ResumePreview.vue   # A4 live preview + PDF export
│           ├── SessionList.vue     # Session sidebar
│           └── FileUpload.vue      # Drag-and-drop upload
├── scripts/
│   └── parse_resume.py             # PDF/DOCX text extraction
├── docker-compose.yml              # PostgreSQL + pgvector
└── Makefile
```

## Design Decisions

- **ReAct over fixed planner**: The agent decides each step dynamically, better suited for conversational interaction where users change requirements mid-flow
- **Resume as single JSONB column**: Avoids complex relational tables; tools directly patch JSON paths
- **SSE over WebSocket**: Unidirectional streaming is sufficient, simpler to implement, native browser support
- **Custom LLM client over SDK**: Full control over SSE parsing and tool calling protocol (~200 lines)
- **chromedp for PDF**: Real browser engine rendering guarantees CSS fidelity
- **No authentication**: Local-first single-user tool, keeps complexity low
- **No RAG**: The knowledge base (resume best practices) is small enough to fit in the system prompt; RAG would add complexity without proportional value

## License

MIT
