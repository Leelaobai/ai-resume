package resume

import (
	"context"
	"encoding/json"
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

// GetOrCreate 获取会话的简历，不存在则创建空简历
func (s *Store) GetOrCreate(ctx context.Context, sessionID uuid.UUID) (*Resume, error) {
	r := &Resume{}
	var dataBytes []byte

	err := s.pool.QueryRow(ctx,
		`SELECT id, session_id, data, template_id, created_at, updated_at                                                                                                                                                                                            
			 FROM resumes WHERE session_id = $1`, sessionID,
	).Scan(&r.ID, &r.SessionID, &dataBytes, &r.TemplateID, &r.CreatedAt, &r.UpdatedAt)

	if err != nil {
		// 不存在，创建空简历
		dataBytes, _ = json.Marshal(ResumeData{})
		err = s.pool.QueryRow(ctx,
			`INSERT INTO resumes (session_id, data) VALUES ($1, $2)
					 RETURNING id, session_id, data, template_id, created_at, updated_at`,
			sessionID, dataBytes,
		).Scan(&r.ID, &r.SessionID, &dataBytes, &r.TemplateID, &r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("create resume: %w", err)
		}
	}

	if err := json.Unmarshal(dataBytes, &r.Data); err != nil {
		return nil, fmt.Errorf("unmarshal resume data: %w", err)
	}
	return r, nil
}

// UpdateData 更新简历数据
func (s *Store) UpdateData(ctx context.Context, sessionID uuid.UUID, data ResumeData) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal resume data: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`UPDATE resumes SET data = $1, updated_at = NOW() WHERE session_id = $2`,
		dataBytes, sessionID,
	)
	if err != nil {
		return fmt.Errorf("update resume: %w", err)
	}
	return nil
}

// UpdateTemplate 切换模板
func (s *Store) UpdateTemplate(ctx context.Context, sessionID uuid.UUID, templateID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE resumes SET template_id = $1, updated_at = NOW() WHERE session_id = $2`,
		templateID, sessionID,
	)
	if err != nil {
		return fmt.Errorf("update template: %w", err)
	}
	return nil
}
