package main

import (
	"context"
	"log"
	"os"

	"github.com/Leelaobai/ai-resume/api"
	"github.com/Leelaobai/ai-resume/config"
	"github.com/Leelaobai/ai-resume/internal/agent"
	"github.com/Leelaobai/ai-resume/internal/llm"
	"github.com/Leelaobai/ai-resume/internal/memory"
	"github.com/Leelaobai/ai-resume/internal/parser"
	"github.com/Leelaobai/ai-resume/internal/resume"
	"github.com/Leelaobai/ai-resume/internal/session"
	"github.com/Leelaobai/ai-resume/internal/store"
	"github.com/Leelaobai/ai-resume/internal/tools"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	cfg := config.Load()

	if cfg.LLMApiKey == "" {
		log.Fatal("LLM_API_KEY is required. Set it in environment or .env file")
	}

	// 数据库
	ctx := context.Background()
	db, err := store.NewDB(ctx, cfg.DBDSN())
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer db.Close()
	log.Println("Database connected")

	// 各模块初始化
	sessionStore := session.NewStore(db.Pool)
	sessionMgr := session.NewManager(sessionStore)

	resumeStore := resume.NewStore(db.Pool)

	resumeRenderer, err := resume.NewRenderer()
	if err != nil {
		log.Fatalf("init renderer: %v", err)
	}
	resumeExporter := resume.NewExporter()

	memoryStore := memory.NewStore(db.Pool)
	memoryMgr := memory.NewManager(memoryStore, sessionMgr)

	llmClient := llm.NewClient(cfg.LLMApiKey, cfg.LLMModel)

	summarizer := memory.NewSummarizer(llmClient, sessionMgr, memoryStore, cfg)

	// 注册工具
	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.NewGetResumeTool(resumeStore))
	toolRegistry.Register(tools.NewUpdateSectionTool(resumeStore))
	toolRegistry.Register(tools.NewExtractInfoTool(memoryMgr))
	toolRegistry.Register(tools.NewMatchJDTool(resumeStore))

	// Agent
	ag := agent.NewAgent(llmClient, toolRegistry, sessionMgr, memoryMgr, summarizer)

	resumeParser := parser.NewParser(llmClient)

	// 启动HTTP服务
	server := api.NewServer(ag, sessionMgr, resumeStore, resumeRenderer, resumeExporter, resumeParser)
	log.Printf("Server starting on :%s", cfg.ServerPort)
	if err := server.Run(":" + cfg.ServerPort); err != nil {
		log.Fatalf("server error: %v", err)
		os.Exit(1)
	}
}
