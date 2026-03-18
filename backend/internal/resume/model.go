package resume

import (
	"time"

	"github.com/google/uuid"
)

// Resume 完整简历结构
type Resume struct {
	ID         uuid.UUID  `json:"id"`
	SessionID  uuid.UUID  `json:"session_id"`
	Data       ResumeData `json:"data"`
	TemplateID string     `json:"template_id"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ResumeData 简历内容（存为JSONB）
type ResumeData struct {
	BasicInfo      BasicInfo    `json:"basic_info"`
	Summary        string       `json:"summary"`
	Experience     []Experience `json:"experience"`
	Education      []Education  `json:"education"`
	Skills         []SkillGroup `json:"skills"`
	Projects       []Project    `json:"projects"`
	Certifications []string     `json:"certifications,omitempty"`
	Languages      []string     `json:"languages,omitempty"`
}

type BasicInfo struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Location string `json:"location,omitempty"`
	Website  string `json:"website,omitempty"`
	GitHub   string `json:"github,omitempty"`
}

type Experience struct {
	Company     string   `json:"company"`
	Title       string   `json:"title"`
	StartDate   string   `json:"start_date"`
	EndDate     string   `json:"end_date"`
	Description []string `json:"description"`
}

type Education struct {
	School    string `json:"school"`
	Degree    string `json:"degree"`
	Major     string `json:"major"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type SkillGroup struct {
	Category string   `json:"category"`
	Items    []string `json:"items"`
}

type Project struct {
	Name        string   `json:"name"`
	Role        string   `json:"role,omitempty"`
	Description string   `json:"description"`
	Highlights  []string `json:"highlights,omitempty"`
	TechStack   []string `json:"tech_stack,omitempty"`
}
