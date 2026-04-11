package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Leelaobai/ai-resume/internal/memory"
)

type ExtractInfoTool struct {
	memoryMgr *memory.Manager
}

func NewExtractInfoTool(memoryMgr *memory.Manager) *ExtractInfoTool {
	return &ExtractInfoTool{memoryMgr: memoryMgr}
}

func (t *ExtractInfoTool) Name() string { return "extract_user_info" }

func (t *ExtractInfoTool) Description() string {
	return `从对话中提取用户信息保存为长期记忆。每次提取一条事实。
  category可选: skill(技能), experience(经历), education(教育), preference(偏好), personal(个人信息)
  示例: category="skill", key="Go", value="5年Go后端开发经验，熟悉Gin、gRPC"`
}

func (t *ExtractInfoTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
                "type": "object",
                "properties": {
                        "category": {
                                "type": "string",
                                "enum": ["skill", "experience", "education", "preference", "personal"]
                        },
                        "key": {
                                "type": "string",
                                "description": "信息的关键字，如技能名、公司名"
                        },
                        "value": {
                                "type": "string",
                                "description": "具体内容描述"
                        }
                },
                "required": ["category", "key", "value"]
        }`)
}

func (t *ExtractInfoTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Category string `json:"category"`
		Key      string `json:"key"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	sessionID := SessionIDFromContext(ctx)
	if err := t.memoryMgr.SaveFact(ctx, sessionID, params.Category, params.Key, params.Value); err != nil {
		return "", err
	}

	return fmt.Sprintf("已记忆: [%s] %s = %s", params.Category, params.Key, params.Value), nil
}
