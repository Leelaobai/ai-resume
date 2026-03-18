package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Leelaobai/ai-resume/internal/resume"
)

type UpdateSectionTool struct {
	resumeStore *resume.Store
}

func NewUpdateSectionTool(resumeStore *resume.Store) *UpdateSectionTool {
	return &UpdateSectionTool{resumeStore: resumeStore}
}

func (t *UpdateSectionTool) Name() string { return "update_resume_section" }

func (t *UpdateSectionTool) Description() string {
	return `更新简历的指定部分。data必须严格使用以下JSON字段名：
- basic_info: {"name":"","title":"","email":"","phone":"","location":"","website":"","github":""}
- summary: 纯字符串
- experience: [{"company":"","title":"职位","start_date":"2020.01","end_date":"2025.03","description":["职责描述1","职责描述2"]}]
- education: [{"school":"","degree":"本科/硕士","major":"","start_date":"","end_date":""}]
- skills: [{"category":"编程语言","items":["Go","Python"]}]
- projects: [{"name":"","role":"","description":"","highlights":["亮点1"],"tech_stack":["Go","Redis"]}]
- certifications: ["证书1","证书2"]
- languages: ["中文","英文"]`
}

func (t *UpdateSectionTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"section": {
				"type": "string",
				"description": "要更新的简历部分",
				"enum": ["basic_info","summary","experience","education","skills","projects","certifications","languages"]
			},
			"data": {
				"description": "该部分的完整新内容，必须严格按照工具描述中的字段名"
			}
		},
		"required": ["section", "data"]
	}`)
}

func (t *UpdateSectionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Section string          `json:"section"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	// 获取当前简历
	sessionID := SessionIDFromContext(ctx)
	r, err := t.resumeStore.GetOrCreate(ctx, sessionID)
	if err != nil {
		return "", err
	}

	// 根据section更新对应字段
	switch params.Section {
	case "basic_info":
		json.Unmarshal(params.Data, &r.Data.BasicInfo)
	case "summary":
		var s string
		json.Unmarshal(params.Data, &s)
		r.Data.Summary = s
	case "experience":
		json.Unmarshal(params.Data, &r.Data.Experience)
	case "education":
		json.Unmarshal(params.Data, &r.Data.Education)
	case "skills":
		json.Unmarshal(params.Data, &r.Data.Skills)
	case "projects":
		json.Unmarshal(params.Data, &r.Data.Projects)
	case "certifications":
		json.Unmarshal(params.Data, &r.Data.Certifications)
	case "languages":
		json.Unmarshal(params.Data, &r.Data.Languages)
	default:
		return "", fmt.Errorf("unknown section: %s", params.Section)
	}

	// 保存
	if err := t.resumeStore.UpdateData(ctx, sessionID, r.Data); err != nil {
		return "", err
	}

	return fmt.Sprintf("已更新简历的 %s 部分", params.Section), nil
}
