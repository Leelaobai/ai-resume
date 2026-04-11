package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/Leelaobai/ai-resume/internal/resume"
)

type UpdateStyleTool struct {
	resumeStore    *resume.Store
	resumeRenderer *resume.Renderer
}

func NewUpdateStyleTool(resumeStore *resume.Store, resumeRenderer *resume.Renderer) *UpdateStyleTool {
	return &UpdateStyleTool{resumeStore: resumeStore, resumeRenderer: resumeRenderer}
}

func (t *UpdateStyleTool) Name() string { return "update_resume_style" }

func (t *UpdateStyleTool) Description() string {
	return `更新简历的HTML/CSS样式模板。传入完整的Go HTML模板代码，系统会验证后保存。

模板必须是合法的Go html/template语法，使用 {{.Field}} 引用数据。可用字段：

数据结构：
- .BasicInfo.Name, .BasicInfo.Title, .BasicInfo.Email, .BasicInfo.Phone, .BasicInfo.Location, .BasicInfo.Website, .BasicInfo.GitHub
- .Summary（字符串）
- .Experience[]  每项有 .Company, .Title, .StartDate, .EndDate, .Description[]
- .Education[]   每项有 .School, .Degree, .Major, .StartDate, .EndDate
- .Skills[]      每项有 .Category, .Items[]
- .Projects[]    每项有 .Name, .Role, .Description, .Highlights[], .TechStack[]
- .Certifications[]（字符串数组）
- .Languages[]（字符串数组）

Go template语法：
- 条件：{{if .Field}}...{{end}}
- 循环：{{range .List}}...{{end}}
- 辅助函数：{{joinItems .Items "、"}} 将字符串数组用分隔符连接

重要：
- 必须保留所有数据绑定（{{.Field}}），只修改HTML结构和CSS样式
- 模板应包含完整的HTML文档（<!DOCTYPE html>到</html>）
- CSS写在<style>标签中
- 建议保留 @page { size: A4; margin: 0; } 和 body { width: 210mm; } 以确保PDF导出正常`
}

func (t *UpdateStyleTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"template": {
				"type": "string",
				"description": "完整的Go HTML模板代码"
			}
		},
		"required": ["template"]
	}`)
}

func (t *UpdateStyleTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Template string `json:"template"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	if strings.TrimSpace(params.Template) == "" {
		return "错误：模板内容不能为空", nil
	}

	// 1. 验证模板语法
	funcMap := template.FuncMap{
		"joinItems": func(items []string, sep string) string {
			return strings.Join(items, sep)
		},
	}

	tmpl, err := template.New("custom").Funcs(funcMap).Parse(params.Template)
	if err != nil {
		return fmt.Sprintf("模板语法错误：%v\n请修正后重试。", err), nil
	}

	// 2. 试渲染确认不报错
	sessionID := SessionIDFromContext(ctx)
	r, err := t.resumeStore.GetOrCreate(ctx, sessionID)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, r.Data); err != nil {
		return fmt.Sprintf("模板渲染失败：%v\n请检查字段名是否正确后重试。", err), nil
	}

	// 3. 验证通过，保存到数据库
	if err := t.resumeStore.UpdateCustomTemplate(ctx, sessionID, params.Template); err != nil {
		return "", err
	}

	return "样式模板已更新成功，简历预览将自动刷新。", nil
}
