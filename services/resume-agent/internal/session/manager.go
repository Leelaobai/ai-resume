package session

import (
	"context"

	"github.com/google/uuid"
)

type Manager struct {
	store *Store
}

func NewManager(store *Store) *Manager {
	return &Manager{store: store}
}

func (m *Manager) Create(ctx context.Context, title string) (*Session, error) {
	if title == "" {
		title = "New Session"
	}
	return m.store.CreateSession(ctx, title)
}

func (m *Manager) Get(ctx context.Context, id uuid.UUID) (*Session, error) {
	return m.store.GetSession(ctx, id)
}

func (m *Manager) List(ctx context.Context) ([]Session, error) {
	return m.store.ListSessions(ctx)
}

func (m *Manager) Delete(ctx context.Context, id uuid.UUID) error {
	return m.store.DeleteSession(ctx, id)
}

func (m *Manager) AddMessage(ctx context.Context, msg *Message) error {
	return m.store.AddMessage(ctx, msg)
}

func (m *Manager) GetMessages(ctx context.Context, sessionID uuid.UUID, limit, offset int) ([]Message, error) {
	return m.store.GetMessages(ctx, sessionID, limit, offset)
}

func (m *Manager) GetMessagesAfter(ctx context.Context, sessionID uuid.UUID, afterMsgID uuid.UUID) ([]Message, error) {
	return m.store.GetMessagesAfter(ctx, sessionID, afterMsgID)
}

func (m *Manager) EstimateTokens(ctx context.Context, sessionID uuid.UUID) (int, error) {
	return m.store.EstimateTokens(ctx, sessionID)
}

func (m *Manager) EstimateTokensAfter(ctx context.Context, sessionID uuid.UUID, afterMsgID uuid.UUID) (int, error) {
	return m.store.EstimateTokensAfter(ctx, sessionID, afterMsgID)
}

func (m *Manager) UpdateTitle(ctx context.Context, id uuid.UUID, title string) error {
	return m.store.UpdateTitle(ctx, id, title)
}
