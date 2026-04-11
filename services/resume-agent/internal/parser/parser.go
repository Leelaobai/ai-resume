package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/resume"
)

type Parser struct {
	llmClient  *llm.Client
	scriptPath string
}

func NewParser(llmClient *llm.Client) *Parser {
	scriptPath := findScriptPath()
	return &Parser{llmClient: llmClient, scriptPath: scriptPath}
}

// findScriptPath 从当前工作目录向上查找 go.mod，定位 backend 根目录，
// 然后拼出 ../scripts/parse_resume.py（scripts 在 backend 的上一级）。
// 找不到时回退到相对路径。
func findScriptPath() string {
	dir, err := os.Getwd()
	if err != nil {
		return filepath.Join("..", "scripts", "parse_resume.py")
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// dir 就是 backend/，scripts 在上一级
			return filepath.Join(dir, "..", "scripts", "parse_resume.py")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join("..", "scripts", "parse_resume.py")
}

// ParseFile 解析上传的简历文件，返回结构化数据
func (p *Parser) ParseFile(ctx context.Context, filePath string) (*resume.ResumeData, error) {
	// 1. 调用Python脚本提取文本
	text, err := p.extractText(filePath)
	if err != nil {
		return nil, fmt.Errorf("extract text: %w", err)
	}

	if text == "" {
		return nil, fmt.Errorf("no text extracted from file")
	}

	// 2. 调用LLM结构化
	data, err := p.structurize(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("structurize: %w", err)
	}

	return data, nil
}

func (p *Parser) extractText(filePath string) (string, error) {
	cmd := exec.Command("python3", p.scriptPath, filePath)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("python script error: %s", string(exitErr.Stderr))
		}
		return "", err
	}
	return string(output), nil
}

// resumeDataSchema 是 save_parsed_resume 工具的参数 JSON Schema
var resumeDataSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"basic_info": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":     map[string]string{"type": "string"},
				"title":    map[string]string{"type": "string"},
				"email":    map[string]string{"type": "string"},
				"phone":    map[string]string{"type": "string"},
				"location": map[string]string{"type": "string"},
				"website":  map[string]string{"type": "string"},
				"github":   map[string]string{"type": "string"},
			},
			"required": []string{"name", "title"},
		},
		"summary": map[string]string{"type": "string"},
		"experience": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"company":     map[string]string{"type": "string"},
					"title":       map[string]string{"type": "string"},
					"start_date":  map[string]string{"type": "string"},
					"end_date":    map[string]string{"type": "string"},
					"description": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				},
			},
		},
		"education": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"school":     map[string]string{"type": "string"},
					"degree":     map[string]string{"type": "string"},
					"major":      map[string]string{"type": "string"},
					"start_date": map[string]string{"type": "string"},
					"end_date":   map[string]string{"type": "string"},
				},
			},
		},
		"skills": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"category": map[string]string{"type": "string"},
					"items":    map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				},
			},
		},
		"projects": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]string{"type": "string"},
					"role":        map[string]string{"type": "string"},
					"description": map[string]string{"type": "string"},
					"highlights":  map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
					"tech_stack":  map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				},
			},
		},
		"certifications": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
		"languages":      map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
	},
	"required": []string{"basic_info", "summary", "experience", "education", "skills", "projects"},
}

func (p *Parser) structurize(ctx context.Context, text string) (*resume.ResumeData, error) {
	tools := []llm.Tool{{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "save_parsed_resume",
			Description: "保存解析出的简历结构化数据",
			Parameters:  resumeDataSchema,
		},
	}}

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "你是简历解析助手。从用户提供的简历文本中提取结构化信息，调用 save_parsed_resume 工具保存。"},
			{Role: "user", Content: text},
		},
		Tools:      tools,
		ToolChoice: "auto",
	}

	start := time.Now()
	resp, err := p.llmClient.Chat(ctx, req)
	log.Printf("[Parser] LLM structurize took %v", time.Since(start))
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("模型未调用工具")
	}

	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	var data resume.ResumeData
	if err := json.Unmarshal([]byte(args), &data); err != nil {
		return nil, fmt.Errorf("parse tool call args: %w", err)
	}
	return &data, nil
}

// SaveUploadedFile 保存上传文件到临时目录
func SaveUploadedFile(data []byte, filename string) (string, error) {
	tmpDir := os.TempDir()
	filePath := filepath.Join(tmpDir, "resume_upload_"+filename)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", err
	}
	return filePath, nil
}

