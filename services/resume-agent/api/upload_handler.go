package api

import (
	"io"
	"net/http"

	"github.com/Leelaobai/ai-resume/internal/parser"
	"github.com/gin-gonic/gin"
)

func (s *Server) UploadResume(c *gin.Context) {

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read file failed"})
		return
	}

	// 保存到临时文件
	filePath, err := parser.SaveUploadedFile(data, header.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save file failed"})
		return
	}

	// 解析
	resumeData, err := s.parser.ParseFile(c.Request.Context(), filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "parse failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "简历解析成功",
		"data":    resumeData,
	})

}
