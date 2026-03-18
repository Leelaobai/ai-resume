package memory

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Leelaobai/ai-resume/config"
	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/session"
	"github.com/google/uuid"
)

type Summarizer struct {
	llmClient  *llm.Client
	sessionMgr *session.Manager
	store      *Store
	config     *config.Config
}

func NewSummarizer(llmClient *llm.Client, sessionMgr *session.Manager, store *Store, config *config.Config) *Summarizer {
	return &Summarizer{
		llmClient:  llmClient,
		sessionMgr: sessionMgr,
		store:      store,
		config:     config,
	}
}

// MaybeSummarize 检查是否需要摘要压缩，如果需要则执行
func (s *Summarizer) MaybeSummarize(ctx context.Context, sessionID uuid.UUID) error {
	// 1. 查最新摘要
	existingSummary, lastMsgID, err := s.store.GetLatestSummary(ctx, sessionID)
	hasSummary := err == nil && existingSummary != ""

	// 2. 计算需要发给LLM的token数
	var tokenCount int
	if hasSummary {
		tokenCount, err = s.sessionMgr.EstimateTokensAfter(ctx, sessionID, lastMsgID)
	} else {
		tokenCount, err = s.sessionMgr.EstimateTokens(ctx, sessionID)
	}
	if err != nil {
		return err
	}

	if tokenCount <= s.config.SummarizeThreshold {
		return nil
	}

	log.Printf("Session %s: %d tokens after summary, threshold %d, summarizing...",
		sessionID, tokenCount, s.config.SummarizeThreshold)

	// 3. 取摘要之后的所有消息（或全部消息）
	var allMsgs []session.Message
	if hasSummary {
		allMsgs, err = s.sessionMgr.GetMessagesAfter(ctx, sessionID, lastMsgID)
	} else {
		allMsgs, err = s.sessionMgr.GetMessages(ctx, sessionID, 9999, 0)
	}
	if err != nil {
		return err
	}
	if len(allMsgs) == 0 {
		return nil
	}

	// 4. 计算保留多少条最近消息（从后往前累计token直到KeepRecentTokens）
	keepFrom := len(allMsgs)
	keepTokens := 0
	for i := len(allMsgs) - 1; i >= 0; i-- {
		msgTokens := len(allMsgs[i].Content) / 2
		if keepTokens+msgTokens > s.config.KeepRecentTokens {
			break
		}
		keepTokens += msgTokens
		keepFrom = i
	}

	// 需要被摘要的消息
	toSummarize := allMsgs[:keepFrom]
	if len(toSummarize) == 0 {
		return nil
	}

	// 5. 生成摘要
	summary, err := s.generateSummary(ctx, existingSummary, toSummarize)
	if err != nil {
		return fmt.Errorf("generate summary: %w", err)
	}

	// 6. 保存摘要，last_message_id 指向被摘要的最后一条消息
	lastSummarizedMsg := toSummarize[len(toSummarize)-1]
	if err := s.store.SaveSummary(ctx, sessionID, summary, lastSummarizedMsg.ID); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}

	log.Printf("Session %s: summarized %d messages, keeping %d",
		sessionID, len(toSummarize), len(allMsgs)-keepFrom)
	return nil
}

func (s *Summarizer) generateSummary(ctx context.Context, existingSummary string, msgs []session.Message) (string, error) {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}

	prompt := "请将以下对话内容压缩为简洁的摘要，保留关键信息（用户的个人信息、工作经历、技能、偏好、已做的简历修改等）。摘要应能让后续对话理解之前的上下文。\n\n"
	if existingSummary != "" {
		prompt += "已有摘要：\n" + existingSummary + "\n\n请将以下新对话合并到摘要中：\n\n"
	}
	prompt += sb.String()

	resp, err := s.llmClient.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "你是一个对话摘要助手，负责压缩对话历史。输出纯文本摘要，不要markdown格式。"},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}
