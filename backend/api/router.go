package api

import (
	"github.com/Leelaobai/ai-resume/internal/agent"
	"github.com/Leelaobai/ai-resume/internal/parser"
	"github.com/Leelaobai/ai-resume/internal/resume"
	"github.com/Leelaobai/ai-resume/internal/session"
	"github.com/gin-gonic/gin"
)

type Server struct {
	router         *gin.Engine
	agent          *agent.Agent
	sessionMgr     *session.Manager
	resumeStore    *resume.Store
	resumeRenderer *resume.Renderer
	resumeExporter *resume.Exporter
	parser         *parser.Parser
}

func NewServer(agent *agent.Agent, sessionMgr *session.Manager, resumeStore *resume.Store, renderer *resume.Renderer, exporter *resume.Exporter, parser *parser.Parser) *Server {
	s := &Server{
		router:         gin.Default(),
		agent:          agent,
		sessionMgr:     sessionMgr,
		resumeStore:    resumeStore,
		resumeRenderer: renderer,
		resumeExporter: exporter,
		parser:         parser,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.router.Use(corsMiddleware())

	api := s.router.Group("/api")
	{
		// 会话
		api.GET("/sessions", s.ListSessions)
		api.POST("/sessions", s.CreateSession)
		api.GET("/sessions/:id", s.GetSession)
		api.DELETE("/sessions/:id", s.DeleteSession)
		api.PATCH("/sessions/:id", s.UpdateSession)

		// 聊天
		api.POST("/sessions/:id/chat", s.Chat)
		api.GET("/sessions/:id/messages", s.GetMessages)

		// 简历
		api.GET("/sessions/:id/resume", s.GetResume)
		api.GET("/sessions/:id/resume/html", s.GetResumeHTML)
		api.GET("/sessions/:id/resume/pdf", s.ExportResumePDF)
		api.POST("/sessions/:id/upload", s.UploadResume)
		api.PATCH("/sessions/:id/resume/template", s.UpdateTemplate)
	}
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
