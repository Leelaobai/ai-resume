package tools

import (
	"context"
	"encoding/json"

	"github.com/Leelaobai/ai-resume/internal/llm"
)

// Tool 每个工具必须实现的接口
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage // JSON Schema
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// ToLLMTool 将Tool转为LLM API的Tool格式
func ToLLMTool(t Tool) llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}
