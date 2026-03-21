package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"

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
	// 从源码位置往上找到 backend/.env，不依赖工作目录
	_, thisFile, _, _ := runtime.Caller(0)
	envPath := filepath.Join(filepath.Dir(thisFile), "..", "..", ".env")
	godotenv.Overload(envPath)
	if wd, err := os.Getwd(); err == nil {
		log.Printf("Working directory: %s", wd)
	}
	log.Printf("ENV raw: SUMMARIZE_THRESHOLD=%s", os.Getenv("SUMMARIZE_THRESHOLD"))
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

	remoteClient := llm.NewClient(cfg.LLMBaseURL, cfg.LLMApiKey, cfg.LLMModel)

	// 如果配置了本地模型，只让 Summarizer 走本地（异步、简单文本压缩）
	// Parser 和 Agent 始终走远程模型（工具调用更可靠）
	var localClient *llm.Client
	if cfg.LocalLLMModel != "" {
		localClient = llm.NewClient(cfg.LocalLLMBaseURL, cfg.LocalLLMApiKey, cfg.LocalLLMModel)
		log.Printf("Local LLM enabled for Summarizer: %s at %s", cfg.LocalLLMModel, cfg.LocalLLMBaseURL)
	} else {
		localClient = remoteClient
	}

	log.Printf("Config: SummarizeThreshold=%d, KeepRecentTokens=%d", cfg.SummarizeThreshold, cfg.KeepRecentTokens)
	summarizer := memory.NewSummarizer(localClient, sessionMgr, memoryStore, cfg)

	// 注册工具
	toolRegistry := tools.NewRegistry()
	// get_current_resume 不再注册：简历数据已预注入 system prompt，每轮从 DB 刷新
	// toolRegistry.Register(tools.NewGetResumeTool(resumeStore))
	toolRegistry.Register(tools.NewUpdateSectionTool(resumeStore))
	toolRegistry.Register(tools.NewExtractInfoTool(memoryMgr))
	toolRegistry.Register(tools.NewMatchJDTool(resumeStore))
	toolRegistry.Register(tools.NewGetTemplateTool(resumeStore, resumeRenderer))
	toolRegistry.Register(tools.NewUpdateStyleTool(resumeStore, resumeRenderer))

	// Agent
	ag := agent.NewAgent(remoteClient, toolRegistry, sessionMgr, memoryMgr, resumeStore, summarizer)

	resumeParser := parser.NewParser(remoteClient)

	// 启动HTTP服务
	server := api.NewServer(ag, sessionMgr, resumeStore, resumeRenderer, resumeExporter, resumeParser)
	log.Printf("Server starting on :%s", cfg.ServerPort)
	if err := server.Run(":" + cfg.ServerPort); err != nil {
		log.Fatalf("server error: %v", err)
		os.Exit(1)
	}
}
