package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Leelaobai/ai-resume/config"
	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/session"
	"github.com/google/uuid"
)

const summarizeTimeout = 3 * time.Minute

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

// MaybeSummarize 检查是否需要摘要压缩，如果需要则异步执行
func (s *Summarizer) MaybeSummarize(ctx context.Context, sessionID uuid.UUID) error {
	// 1. 查最新已完成的摘要
	existingSummary, lastMsgID, err := s.store.GetLatestSummary(ctx, sessionID)
	hasSummary := err == nil && existingSummary != ""

	// 2. 计算 token 数
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

	// 3. 取需要摘要的消息
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

	// 4. 计算保留多少条最近消息
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

	toSummarize := allMsgs[:keepFrom]
	if len(toSummarize) == 0 {
		return nil
	}

	lastSummarizedMsg := toSummarize[len(toSummarize)-1]

	// 5. 原子操作：检查 pending + 创建 pending（事务 + FOR UPDATE 防并发）
	summaryID, skipped, err := s.store.TryAcquireSummary(ctx, sessionID, lastSummarizedMsg.ID, summarizeTimeout)
	if err != nil {
		return fmt.Errorf("acquire summary: %w", err)
	}
	if skipped {
		log.Printf("Session %s: summary already in progress, skipping", sessionID)
		return nil
	}

	// 6. 异步生成摘要，带超时控制
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), summarizeTimeout)
		defer cancel()

		summary, err := s.generateSummary(bgCtx, existingSummary, toSummarize)
		if err != nil {
			log.Printf("Session %s: generate summary failed: %v", sessionID, err)
			s.store.DeletePendingSummary(context.Background(), summaryID)
			return
		}

		if err := s.store.CompleteSummary(bgCtx, summaryID, summary); err != nil {
			log.Printf("Session %s: complete summary failed: %v", sessionID, err)
			s.store.DeletePendingSummary(context.Background(), summaryID)
			return
		}

		log.Printf("Session %s: summarized %d messages, keeping %d",
			sessionID, len(toSummarize), len(allMsgs)-keepFrom)
	}()

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

	start := time.Now()
	resp, err := s.llmClient.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "你是一个对话摘要助手，负责压缩对话历史。输出纯文本摘要，不要markdown格式。"},
			{Role: "user", Content: prompt},
		},
	})
	log.Printf("[Summarizer] LLM summarize took %v", time.Since(start))
	if err != nil {
		return "", err
	}

	return resp.Choices[0].Message.Content, nil
}
