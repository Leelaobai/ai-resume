package session

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateSession 创建新会话
func (s *Store) CreateSession(ctx context.Context, title string) (*Session, error) {
	session := &Session{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO sessions (title) VALUES ($1) RETURNING id, title, created_at, updated_at`,
		title,
	).Scan(&session.ID, &session.Title, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

// GetSession 获取单个会话
func (s *Store) GetSession(ctx context.Context, id uuid.UUID) (*Session, error) {
	session := &Session{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, title, created_at, updated_at FROM sessions WHERE id = $1`,
		id,
	).Scan(&session.ID, &session.Title, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

// ListSessions 列出所有会话
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, title, created_at, updated_at FROM sessions ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Title, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// DeleteSession 删除会话
func (s *Store) DeleteSession(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// AddMessage 添加消息
func (s *Store) AddMessage(ctx context.Context, msg *Message) error {
	err := s.pool.QueryRow(ctx,
		`INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id)                                                                                                                                                                                  
                 VALUES ($1, $2, $3, $4, $5)                                                                                                                                                                                                                                 
                 RETURNING id, created_at`,
		msg.SessionID, msg.Role, msg.Content, msg.ToolCalls, msg.ToolCallID,
	).Scan(&msg.ID, &msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("add message: %w", err)
	}
	return nil
}

// GetMessages 获取会话消息（分页）
func (s *Store) GetMessages(ctx context.Context, sessionID uuid.UUID, limit, offset int) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, session_id, role, content, tool_calls, tool_call_id, created_at                                                                                                                                                                                            
		 FROM (                                                                                                                                                                                                                                                                
			 SELECT id, session_id, role, content, tool_calls, tool_call_id, created_at                                                                                                                                                                                        
			 FROM messages WHERE session_id = $1                                                                                                                                                                                                                               
			 ORDER BY created_at DESC                                                                                                                                                                                                                                          
			 LIMIT $2 OFFSET $3                               
		 ) sub ORDER BY created_at ASC`,
		sessionID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCalls, &msg.ToolCallID, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// GetMessagesAfter 取某条消息之后的所有消息
func (s *Store) GetMessagesAfter(ctx context.Context, sessionID uuid.UUID, afterMsgID uuid.UUID) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, session_id, role, content, tool_calls, tool_call_id, created_at
			 FROM messages WHERE session_id = $1
			 AND created_at > (SELECT created_at FROM messages WHERE id = $2)
			 ORDER BY created_at ASC`,
		sessionID, afterMsgID,
	)

	if err != nil {
		return nil, fmt.Errorf("get messages after: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.ToolCalls, &msg.ToolCallID, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// EstimateTokens 估算session所有消息的token数
func (s *Store) EstimateTokens(ctx context.Context, sessionID uuid.UUID) (int, error) {
	var charCount int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(LENGTH(content)), 0) FROM messages WHERE session_id = $1`,
		sessionID,
	).Scan(&charCount)
	return charCount / 2, err // 粗估：2字符≈1token
}

// EstimateTokensAfter 估算某条消息之后的token数
func (s *Store) EstimateTokensAfter(ctx context.Context, sessionID uuid.UUID, afterMsgID uuid.UUID) (int, error) {
	var charCount int
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(LENGTH(content)), 0) FROM messages
			 WHERE session_id = $1
			 AND created_at > (SELECT created_at FROM messages WHERE id = $2)`,
		sessionID, afterMsgID,
	).Scan(&charCount)
	return charCount / 2, err
}

func (s *Store) UpdateTitle(ctx context.Context, id uuid.UUID, title string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET title = $1, updated_at = NOW() WHERE id = $2`,
		title, id,
	)
	return err
}
