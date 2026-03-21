package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	LLMBaseURL       string
	LLMApiKey        string
	LLMModel         string
	LocalLLMBaseURL  string
	LocalLLMModel    string
	LocalLLMApiKey   string
	ServerPort   string
	ReActMaxLoop int // ReAct循环最大轮数，默认10
	// 摘要配置
	SummarizeThreshold int // 触发摘要的token阈值，默认200000
	KeepRecentTokens   int // 摘要后保留的最近token数，默认50000
}

func Load() *Config {
	return &Config{
		DBHost:             getEnv("DB_HOST", "localhost"),
		DBPort:             getEnv("DB_PORT", "5433"),
		DBUser:             getEnv("DB_USER", "resume"),
		DBPassword:         getEnv("DB_PASSWORD", "resume123"),
		DBName:             getEnv("DB_NAME", "ai_resume"),
		LLMBaseURL:         getEnv("LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMApiKey:          getEnv("LLM_API_KEY", ""),
		LLMModel:           getEnv("LLM_MODEL", "anthropic/claude-sonnet-4.6"),
		LocalLLMBaseURL:    getEnv("LOCAL_LLM_BASE_URL", "http://localhost:8000/v1"),
		LocalLLMModel:      getEnv("LOCAL_LLM_MODEL", ""),
		LocalLLMApiKey:     getEnv("LOCAL_LLM_API_KEY", "no-key"),
		ServerPort:         getEnv("SERVER_PORT", "8090"),
		ReActMaxLoop:       getEnvInt("REACT_MAX_LOOP", 10),
		SummarizeThreshold: getEnvInt("SUMMARIZE_THRESHOLD", 200000),
		KeepRecentTokens:   getEnvInt("KEEP_RECENT_TOKENS", 50000),
	}
}

func (c *Config) DBDSN() string {
	return "postgres://" + c.DBUser + ":" + c.DBPassword + "@" + c.DBHost + ":" + c.DBPort + "/" + c.DBName + "?sslmode=disable"
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return strings.TrimSpace(val)
	}

	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	val, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return defaultVal
	}
	return val
}
