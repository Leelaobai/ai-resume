package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/resume"
)

type Parser struct {
	llmClient  *llm.Client
	scriptPath string
}

func NewParser(llmClient *llm.Client) *Parser {
	// 找到scripts目录
	scriptPath := filepath.Join(".", "..", "scripts", "parse_resume.py")
	return &Parser{llmClient: llmClient, scriptPath: scriptPath}
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

func (p *Parser) structurize(ctx context.Context, text string) (*resume.ResumeData, error) {
	prompt := fmt.Sprintf(`从以下简历文本中提取结构化信息。只输出纯JSON，不要任何解释、注释或markdown标记。
	重要：JSON字符串值中绝对不能包含双引号，用「」替代。例如：荣获「最佳新人」称号。

	{
	  "basic_info": {"name":"","title":"","email":"","phone":"","location":"","website":"","github":""},
	  "summary": "",
	  "experience": [{"company":"","title":"","start_date":"","end_date":"","description":[""]}],
	  "education": [{"school":"","degree":"","major":"","start_date":"","end_date":""}],
	  "skills": [{"category":"","items":[""]}],
	  "projects": [{"name":"","description":"","highlights":[""],"tech_stack":[""]}]
	}
  
	简历文本:
	%s
  
	只输出JSON，不要其他任何文字。`, text)

	resp, err := p.llmClient.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		return nil, err
	}

	content := resp.Choices[0].Message.Content

	// 提取JSON：找第一个{和最后一个}
	start := -1
	end := -1
	for i := 0; i < len(content); i++ {
		if content[i] == '{' {
			start = i
			break
		}
	}
	for i := len(content) - 1; i >= 0; i-- {
		if content[i] == '}' {
			end = i + 1
			break
		}
	}

	if start == -1 || end == -1 || start >= end {
		return nil, fmt.Errorf("no JSON found in LLM response")
	}

	jsonStr := content[start:end]

	var data resume.ResumeData
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		// 尝试修复后再解析
		fixed := fixJSON(jsonStr)
		if err := json.Unmarshal([]byte(fixed), &data); err != nil {
			return nil, fmt.Errorf("parse LLM response: %w", err)
		}
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

// fixJSON 修复JSON中未转义的双引号
// 处理类似 荣获部门"最佳新人"称号 这种情况
func fixJSON(raw string) string {
	result := make([]byte, 0, len(raw))
	inString := false
	i := 0

	for i < len(raw) {
		ch := raw[i]

		if ch == '\\' && inString && i+1 < len(raw) {
			// 转义字符，原样保留
			result = append(result, ch, raw[i+1])
			i += 2
			continue
		}

		if ch == '"' {
			if !inString {
				// 进入字符串
				inString = true
				result = append(result, ch)
			} else {
				// 判断这个引号是字符串结束符还是值内的引号
				// 看后面的字符：如果是 , ] } : 或空白则是结束符
				next := skipWhitespace(raw, i+1)
				if next >= len(raw) || raw[next] == ',' || raw[next] == ']' || raw[next] == '}' || raw[next] == ':' {
					inString = false
					result = append(result, ch)
				} else {
					// 值内的引号，转义它
					result = append(result, '\\', '"')
				}
			}
		} else {
			result = append(result, ch)
		}

		i++
	}

	return string(result)
}

func skipWhitespace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}
