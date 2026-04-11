package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Leelaobai/ai-resume/internal/agent"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (s *Server) Chat(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	// 设置SSE响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	// 启动Agent，通过channel接收事件
	eventCh := make(chan agent.StreamEvent, 10)
	go s.agent.Run(c.Request.Context(), id, req.Message, eventCh)

	// 把Agent事件转为SSE推给前端
	c.Stream(func(w io.Writer) bool {
		event, ok := <-eventCh
		if !ok {
			return false // channel关闭，结束流
		}

		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		c.Writer.Flush()
		return true
	})
}
