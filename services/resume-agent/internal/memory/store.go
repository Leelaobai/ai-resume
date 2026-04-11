package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// GetLatestSummary 取最新一条已完成的摘要
func (s *Store) GetLatestSummary(ctx context.Context, sessionID uuid.UUID) (string, uuid.UUID, error) {
	var summary string
	var lastMsgID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT summary, last_message_id FROM conversation_summaries
			 WHERE session_id = $1 AND status = 'done'
			 ORDER BY created_at DESC LIMIT 1`,
		sessionID,
	).Scan(&summary, &lastMsgID)
	return summary, lastMsgID, err
}

// TryAcquireSummary 原子操作：检查并创建 pending 摘要
// 返回值：
//   - summaryID, false, nil → 成功创建了新的 pending 记录
//   - uuid.Nil, true, nil  → 已有未超时的 pending，应跳过
//   - 超时的 pending 会被删除，然后创建新的
func (s *Store) TryAcquireSummary(ctx context.Context, sessionID uuid.UUID, lastMsgID uuid.UUID, timeout time.Duration) (uuid.UUID, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 加行级锁查 pending（FOR UPDATE 防并发）
	var pendingID uuid.UUID
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, created_at FROM conversation_summaries
			WHERE session_id = $1 AND status = 'pending'
			ORDER BY created_at DESC LIMIT 1
			FOR UPDATE`,
		sessionID,
	).Scan(&pendingID, &createdAt)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("query pending: %w", err)
	}

	if err == nil {
		// 有 pending 记录
		if time.Since(createdAt) < timeout {
			// 未超时，跳过
			return uuid.Nil, true, nil
		}
		// 超时，删掉旧的
		_, _ = tx.Exec(ctx,
			`DELETE FROM conversation_summaries WHERE id = $1`,
			pendingID,
		)
	}

	// 创建新的 pending
	var newID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO conversation_summaries (session_id, summary, last_message_id, status)
			VALUES ($1, '', $2, 'pending') RETURNING id`,
		sessionID, lastMsgID,
	).Scan(&newID)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("insert pending: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("commit: %w", err)
	}
	return newID, false, nil
}

// CompleteSummary 填入摘要内容，标记为 done
func (s *Store) CompleteSummary(ctx context.Context, summaryID uuid.UUID, summary string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE conversation_summaries SET summary = $1, status = 'done' WHERE id = $2`,
		summary, summaryID,
	)
	return err
}

// DeletePendingSummary 删除失败的 pending 记录
func (s *Store) DeletePendingSummary(ctx context.Context, summaryID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM conversation_summaries WHERE id = $1 AND status = 'pending'`,
		summaryID,
	)
	return err
}
