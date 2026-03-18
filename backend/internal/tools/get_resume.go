package tools

import (
	"context"
	"encoding/json"

	"github.com/Leelaobai/ai-resume/internal/resume"
)

type GetResumeTool struct {
	resumeStore *resume.Store
}

func NewGetResumeTool(resumeStore *resume.Store) *GetResumeTool {
	return &GetResumeTool{resumeStore: resumeStore}
}

func (t *GetResumeTool) Name() string { return "get_current_resume" }

func (t *GetResumeTool) Description() string {
	return "获取当前简历的完整内容，用于查看已有信息，避免重复询问用户"
}

func (t *GetResumeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *GetResumeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	sessionID := SessionIDFromContext(ctx)
	r, err := t.resumeStore.GetOrCreate(ctx, sessionID)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(r.Data)
	return string(data), nil
}
