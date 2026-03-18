package resume

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

type Renderer struct {
	templates map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	r := &Renderer{
		templates: make(map[string]*template.Template),
	}

	funcMap := template.FuncMap{
		"joinItems": func(items []string, sep string) string {
			return strings.Join(items, sep)
		},
	}

	// 加载所有模板
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, fmt.Errorf("read templates dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".html")
		tmpl, err := template.New(entry.Name()).Funcs(funcMap).ParseFS(templateFS, "templates/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		r.templates[name] = tmpl
	}

	return r, nil
}

// RenderHTML 渲染简历为HTML字符串
func (r *Renderer) RenderHTML(data ResumeData, templateID string) (string, error) {
	tmpl, ok := r.templates[templateID]
	if !ok {
		// 回退到classic
		tmpl, ok = r.templates["classic"]
		if !ok {
			return "", fmt.Errorf("template %s not found", templateID)
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}

	return buf.String(), nil
}

// ListTemplates 列出可用模板
func (r *Renderer) ListTemplates() []string {
	names := make([]string, 0, len(r.templates))
	for name := range r.templates {
		names = append(names, name)
	}
	return names
}
