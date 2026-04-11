package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/memory"
	"github.com/Leelaobai/ai-resume/internal/resume"
	"github.com/Leelaobai/ai-resume/internal/session"
	"github.com/Leelaobai/ai-resume/internal/tools"
	"github.com/google/uuid"
)

type Agent struct {
	llmClient   *llm.Client
	tools       *tools.Registry
	sessionMgr  *session.Manager
	memoryMgr   *memory.Manager
	resumeStore *resume.Store
	summarizer  *memory.Summarizer
}

func NewAgent(llmClient *llm.Client, toolRegistry *tools.Registry, sessionMgr *session.Manager, memoryMgr *memory.Manager, resumeStore *resume.Store, summarizer *memory.Summarizer) *Agent {
	return &Agent{
		llmClient:   llmClient,
		tools:       toolRegistry,
		sessionMgr:  sessionMgr,
		memoryMgr:   memoryMgr,
		resumeStore: resumeStore,
		summarizer:  summarizer,
	}
}

// Run 处理用户消息，通过channel流式返回事件
func (a *Agent) Run(ctx context.Context, sessionID uuid.UUID, userMsg string, out chan<- StreamEvent) {
	defer func() {
		out <- StreamEvent{Type: "done", Data: nil}
		close(out)
	}()

	// 1. 保存用户消息
	err := a.sessionMgr.AddMessage(ctx, &session.Message{
		SessionID: sessionID,
		Role:      "user",
		Content:   userMsg,
	})
	if err != nil {
		out <- StreamEvent{Type: "error", Data: ErrorData{Message: err.Error()}}
		return
	}

	// 2. 加载历史消息，组装LLM请求
	messages, err := a.buildMessages(ctx, sessionID)
	if err != nil {
		out <- StreamEvent{Type: "error", Data: ErrorData{Message: err.Error()}}
		return
	}

	// 3. 设置sessionID到context，供Tool使用
	ctx = tools.WithSessionID(ctx, sessionID)

	// 4. ReAct循环（最多10轮工具调用，防止死循环）
	for i := 0; i < 10; i++ {
		resp, err := a.llmClient.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    a.tools.Definitions(),
		})
		if err != nil {
			out <- StreamEvent{Type: "error", Data: ErrorData{Message: err.Error()}}
			return
		}

		choice := resp.Choices[0]

		// 如果LLM返回了文本内容，立即推送（不管有没有工具调用）
		if choice.Message.Content != "" {
			out <- StreamEvent{Type: "token", Data: TokenData{Content: choice.Message.Content}}
		}

		// 如果没有工具调用，流程结束
		if len(choice.Message.ToolCalls) == 0 {
			// 保存助手回复
			a.sessionMgr.AddMessage(ctx, &session.Message{
				SessionID: sessionID,
				Role:      "assistant",
				Content:   choice.Message.Content,
			})

			// 检查是否需要摘要（内部异步执行，有 pending 机制防重复）
			if err := a.summarizer.MaybeSummarize(ctx, sessionID); err != nil {
				log.Printf("summarize error: %v", err)
			}

			return
		}

		// LLM请求调用工具
		// 先把助手的tool_calls消息加到上下文
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   choice.Message.Content,
			ToolCalls: choice.Message.ToolCalls,
		})

		// 执行每个工具调用
		for _, tc := range choice.Message.ToolCalls {
			out <- StreamEvent{Type: "tool_call", Data: ToolCallData{
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}}

			tool, ok := a.tools.Get(tc.Function.Name)
			if !ok {
				errMsg := fmt.Sprintf("未知工具: %s", tc.Function.Name)
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    errMsg,
					ToolCallID: tc.ID,
				})
				continue
			}

			result, err := tool.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				log.Printf("tool %s error: %v", tc.Function.Name, err)
				result = "工具执行失败: " + err.Error()
			}

			out <- StreamEvent{Type: "tool_result", Data: ToolResultData{
				Name:   tc.Function.Name,
				Result: result,
			}}

			// 工具结果加到上下文
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})

			// 如果是简历更新工具，通知前端刷新预览
			if tc.Function.Name == "update_resume_section" || tc.Function.Name == "update_resume_style" {
				out <- StreamEvent{Type: "resume_update", Data: nil}
			}
		}

		// 继续循环，带着工具结果再次调用LLM
	}

	out <- StreamEvent{Type: "error", Data: ErrorData{Message: "工具调用次数超过上限"}}
}

// buildMessages 从数据库加载历史消息，组装LLM消息列表
func (a *Agent) buildMessages(ctx context.Context, sessionID uuid.UUID) ([]llm.Message, error) {
	systemContent := SystemPrompt

	longTermCtx, err := a.memoryMgr.GetLongTermContext(ctx)
	if err == nil && longTermCtx != "" {
		systemContent += "\n\n## 已知的用户信息（长期记忆）\n" + longTermCtx
	}

	// 预加载当前简历数据，省去每次 get_current_resume 工具调用
	if r, err := a.resumeStore.GetOrCreate(ctx, sessionID); err == nil {
		if data, err := json.Marshal(r.Data); err == nil {
			systemContent += "\n\n## 当前简历数据\n" + string(data)
		}
	}

	messages := []llm.Message{
		{Role: "system", Content: systemContent},
	}

	// 尝试取最新摘要
	summary, lastMsgID, err := a.memoryMgr.GetLatestSummary(ctx, sessionID)
	if err == nil && summary != "" {
		// 有摘要：注入摘要 + 取摘要之后的消息
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: "以下是之前对话的摘要：\n" + summary,
		})
		messages = append(messages, llm.Message{
			Role:    "assistant",
			Content: "好的，我已了解之前的对话内容，请继续。",
		})
		recentMsgs, err := a.sessionMgr.GetMessagesAfter(ctx, sessionID, lastMsgID)
		if err != nil {
			return nil, err
		}
		for _, msg := range recentMsgs {
			messages = append(messages, llm.Message{Role: msg.Role, Content: msg.Content})
		}
	} else {
		// 无摘要：取最近20条
		shortTermMsgs, err := a.memoryMgr.GetConversationContext(ctx, sessionID, 20)
		if err != nil {
			return nil, err
		}
		messages = append(messages, shortTermMsgs...)
	}

	return messages, nil
}
