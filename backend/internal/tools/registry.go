package tools

import "github.com/Leelaobai/ai-resume/internal/llm"

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions 返回所有工具定义，传给LLM
func (r *Registry) Definitions() []llm.Tool {
	defs := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, ToLLMTool(t))
	}
	return defs
}
