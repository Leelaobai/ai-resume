package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Leelaobai/ai-resume/internal/resume"
)

type GetTemplateTool struct {
	resumeStore    *resume.Store
	resumeRenderer *resume.Renderer
}

func NewGetTemplateTool(resumeStore *resume.Store, resumeRenderer *resume.Renderer) *GetTemplateTool {
	return &GetTemplateTool{resumeStore: resumeStore, resumeRenderer: resumeRenderer}
}

func (t *GetTemplateTool) Name() string { return "get_current_template" }

func (t *GetTemplateTool) Description() string {
	return "获取当前简历使用的HTML模板源码。如果有自定义模板则返回自定义模板，否则返回当前内置模板的源码。修改样式前必须先调用此工具获取当前模板。"
}

func (t *GetTemplateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

func (t *GetTemplateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	sessionID := SessionIDFromContext(ctx)
	r, err := t.resumeStore.GetOrCreate(ctx, sessionID)
	if err != nil {
		return "", err
	}

	// template_id 为 "custom" 且有自定义模板时返回自定义模板
	if r.TemplateID == "custom" && r.CustomTemplate != nil {
		return fmt.Sprintf("当前使用自定义模板：\n\n%s", *r.CustomTemplate), nil
	}

	// 读取内置模板源码
	templateID := r.TemplateID
	if templateID == "" || templateID == "custom" {
		templateID = "classic"
	}
	source, err := t.resumeRenderer.GetTemplateSource(templateID)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("当前使用内置模板 [%s]：\n\n%s", templateID, source), nil
}
