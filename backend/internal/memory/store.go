package memory

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

// SaveFact 保存一条长期记忆
func (s *Store) SaveFact(ctx context.Context, fact *Fact) error {
	err := s.pool.QueryRow(ctx,
		`INSERT INTO memory_facts (session_id, category, key, value, confidence)
                 VALUES ($1, $2, $3, $4, $5)
                 RETURNING id, created_at`,
		fact.SessionID, fact.Category, fact.Key, fact.Value, fact.Confidence,
	).Scan(&fact.ID, &fact.CreatedAt)
	if err != nil {
		return fmt.Errorf("save fact: %w", err)
	}
	return nil
}

// GetFacts 按类别获取记忆
func (s *Store) GetFacts(ctx context.Context, category string) ([]Fact, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, session_id, category, key, value, confidence, created_at
                 FROM memory_facts WHERE category = $1
                 ORDER BY confidence DESC, created_at DESC`,
		category,
	)
	if err != nil {
		return nil, fmt.Errorf("get facts: %w", err)
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.SessionID, &f.Category, &f.Key, &f.Value, &f.Confidence, &f.CreatedAt); err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// GetAllFacts 获取所有记忆
func (s *Store) GetAllFacts(ctx context.Context) ([]Fact, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, session_id, category, key, value, confidence, created_at
                 FROM memory_facts ORDER BY category, created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("get all facts: %w", err)
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.SessionID, &f.Category, &f.Key, &f.Value, &f.Confidence, &f.CreatedAt); err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// GetLatestSummary 取最新一条摘要
func (s *Store) GetLatestSummary(ctx context.Context, sessionID uuid.UUID) (string, uuid.UUID, error) {
	var summary string
	var lastMsgID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT summary, last_message_id FROM conversation_summaries
			 WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`,
		sessionID,
	).Scan(&summary, &lastMsgID)
	return summary, lastMsgID, err
}

// SaveSummary INSERT一条新摘要
func (s *Store) SaveSummary(ctx context.Context, sessionID uuid.UUID, summary string, lastMsgID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO conversation_summaries (session_id, summary, last_message_id)
			 VALUES ($1, $2, $3)`,
		sessionID, summary, lastMsgID,
	)
	return err
}
