package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Leelaobai/ai-resume/internal/resume"
)

type MatchJDTool struct {
	resumeStore *resume.Store
}

func NewMatchJDTool(resumeStore *resume.Store) *MatchJDTool {
	return &MatchJDTool{resumeStore: resumeStore}
}

func (t *MatchJDTool) Name() string { return "match_jd" }

func (t *MatchJDTool) Description() string {
	return `对比目标岗位JD和当前简历，找出简历中缺少的关键词、技能、经验描述，并给出具体的调整建议。用户提供JD文本时应调用此工具。`
}

func (t *MatchJDTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
                "type": "object",
                "properties": {
                        "jd_text": {
                                "type": "string",
                                "description": "目标岗位的职位描述(JD)全文"
                        }
                },
                "required": ["jd_text"]
        }`)
}

func (t *MatchJDTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		JDText string `json:"jd_text"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	sessionID := SessionIDFromContext(ctx)
	r, err := t.resumeStore.GetOrCreate(ctx, sessionID)
	if err != nil {
		return "", err
	}

	resumeJSON, _ := json.Marshal(r.Data)

	return fmt.Sprintf(`请对比以下JD和简历，分析：
  1. 简历中已匹配JD的关键词和技能
  2. JD要求但简历中缺少或描述不足的部分
  3. 给出具体的简历调整建议（修改哪些描述、补充哪些关键词）

  目标岗位JD:
  %s

  当前简历:
  %s`, params.JDText, string(resumeJSON)), nil
}
