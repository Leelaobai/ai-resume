package api

import (
	"fmt"
	"log"
	"net/http"

	"github.com/Leelaobai/ai-resume/internal/resume"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// renderWithTemplate 根据 template_id 决定用自定义模板还是内置模板渲染
func (s *Server) renderWithTemplate(r *resume.Resume) (string, error) {
	if r.TemplateID == "custom" && r.CustomTemplate != nil {
		html, err := s.resumeRenderer.RenderHTMLFromCustomTemplate(r.Data, *r.CustomTemplate)
		if err != nil {
			// 自定义模板渲染失败，回退到 classic
			log.Printf("custom template render failed, falling back: %v", err)
			return s.resumeRenderer.RenderHTML(r.Data, "classic")
		}
		return html, nil
	}
	return s.resumeRenderer.RenderHTML(r.Data, r.TemplateID)
}

func (s *Server) GetResume(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	r, err := s.resumeStore.GetOrCreate(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// GetResumeHTML 返回渲染后的简历HTML
func (s *Server) GetResumeHTML(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	r, err := s.resumeStore.GetOrCreate(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	html, err := s.renderWithTemplate(r)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// ExportResumePDF 导出简历为A4 PDF
func (s *Server) ExportResumePDF(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	r, err := s.resumeStore.GetOrCreate(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	html, err := s.renderWithTemplate(r)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	pdfBytes, err := s.resumeExporter.ExportPDF(c.Request.Context(), html)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	filename := fmt.Sprintf("%s_resume.pdf", r.Data.BasicInfo.Name)
	if r.Data.BasicInfo.Name == "" {
		filename = "resume.pdf"
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}

func (s *Server) UpdateTemplate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	var req struct {
		TemplateID string `json:"template_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.TemplateID == "" {
		c.JSON(400, gin.H{"error": "template_id is required"})
		return
	}

	if err := s.resumeStore.UpdateTemplate(c.Request.Context(), id, req.TemplateID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"ok": true})
}

// DeleteCustomTemplate 清除自定义模板，回退到内置模板
func (s *Server) DeleteCustomTemplate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	if err := s.resumeStore.ClearCustomTemplate(c.Request.Context(), id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// 回退到 classic
	if err := s.resumeStore.UpdateTemplate(c.Request.Context(), id, "classic"); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"ok": true})
}
