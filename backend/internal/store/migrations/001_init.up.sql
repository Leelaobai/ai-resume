CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 会话表
CREATE TABLE sessions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title       VARCHAR(255) NOT NULL DEFAULT 'New Session',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 聊天消息
CREATE TABLE messages (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id   UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role         VARCHAR(20) NOT NULL,
    content      TEXT NOT NULL,
    tool_calls   JSONB,
    tool_call_id VARCHAR(255),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_messages_session ON messages(session_id, created_at);

-- 简历数据（JSON为唯一数据源）
CREATE TABLE resumes (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    data        JSONB NOT NULL DEFAULT '{}',
    template_id VARCHAR(50) NOT NULL DEFAULT 'classic',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(session_id)
);

-- 长期记忆
CREATE TABLE memory_facts (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id  UUID REFERENCES sessions(id) ON DELETE SET NULL,
    category    VARCHAR(50) NOT NULL,
    key         VARCHAR(255) NOT NULL,
    value       TEXT NOT NULL,
    confidence  REAL NOT NULL DEFAULT 1.0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_memory_category ON memory_facts(category);

-- RAG文档（向量检索，预留）
CREATE TABLE rag_documents (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    doc_type    VARCHAR(50) NOT NULL,
    title       VARCHAR(255) NOT NULL,
    content     TEXT NOT NULL,
    embedding   VECTOR(1536),
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rag_embedding ON rag_documents
    USING hnsw (embedding vector_cosine_ops);

-- 对话摘要（短期记忆压缩）
CREATE TABLE conversation_summaries (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    summary         TEXT NOT NULL,
    last_message_id UUID REFERENCES messages(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
