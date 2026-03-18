package agent

// StreamEvent 推送给前端的SSE事件
type StreamEvent struct {
	Type string      `json:"type"` // token, tool_call, tool_result, resume_update, done, error
	Data interface{} `json:"data"`
}

type TokenData struct {
	Content string `json:"content"`
}

type ToolCallData struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

type ToolResultData struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

type ErrorData struct {
	Message string `json:"message"`
}
