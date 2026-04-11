package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/session"
	"github.com/google/uuid"
)

type Manager struct {
	store      *Store
	sessionMgr *session.Manager
}

func NewManager(store *Store, sessionMgr *session.Manager) *Manager {
	return &Manager{store: store, sessionMgr: sessionMgr}
}

// GetConversationContext 组装短期记忆：最近N条消息
func (m *Manager) GetConversationContext(ctx context.Context, sessionID uuid.UUID, maxMessages int) ([]llm.Message, error) {
	msgs, err := m.sessionMgr.GetMessages(ctx, sessionID, maxMessages, 0)
	if err != nil {
		return nil, err
	}

	var result []llm.Message
	for _, msg := range msgs {
		result = append(result, llm.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return result, nil
}

// GetLongTermContext 生成长期记忆摘要文本，注入到system prompt
func (m *Manager) GetLongTermContext(ctx context.Context) (string, error) {
	facts, err := m.store.GetAllFacts(ctx)
	if err != nil {
		return "", err
	}
	if len(facts) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("以下是从历史对话中积累的用户信息：\n")

	currentCategory := ""
	for _, f := range facts {
		if f.Category != currentCategory {
			currentCategory = f.Category
			sb.WriteString(fmt.Sprintf("\n[%s]\n", currentCategory))
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", f.Key, f.Value))
	}

	return sb.String(), nil
}

// SaveFact 保存长期记忆
func (m *Manager) SaveFact(ctx context.Context, sessionID uuid.UUID, category, key, value string) error {
	fact := &Fact{
		SessionID:  sessionID,
		Category:   category,
		Key:        key,
		Value:      value,
		Confidence: 1.0,
	}
	return m.store.SaveFact(ctx, fact)
}

func (m *Manager) GetLatestSummary(ctx context.Context, sessionID uuid.UUID) (string, uuid.UUID, error) {
	return m.store.GetLatestSummary(ctx, sessionID)
}
