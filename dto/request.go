package dto

// OpenAIMessage represents a message in OpenAI format
type OpenAIMessage struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"`
	Name             string          `json:"name,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool call in OpenAI format
type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type"`
	Function FunctionResponse `json:"function"`
}

// FunctionResponse represents function call details
type FunctionResponse struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAIChatRequest represents an OpenAI chat completion request
type OpenAIChatRequest struct {
	Model            string          `json:"model"`
	Messages         []OpenAIMessage `json:"messages"`
	Temperature      *float64        `json:"temperature,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int         `json:"max_completion_tokens,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	TopK             *int            `json:"top_k,omitempty"`
	N                *int            `json:"n,omitempty"`
	Stream           *bool           `json:"stream,omitempty"`
	Stop             interface{}     `json:"stop,omitempty"`
	Tools            []OpenAITool    `json:"tools,omitempty"`
	ToolChoice       interface{}     `json:"tool_choice,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
}

// OpenAITool represents a tool in OpenAI format
type OpenAITool struct {
	Type         string           `json:"type"`
	Function     *FunctionRequest `json:"function,omitempty"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// FunctionRequest represents function definition
type FunctionRequest struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
	Parameters  any    `json:"parameters,omitempty"`
}

// CacheControl represents cache control settings
type CacheControl struct {
	Type string `json:"type"`
}

// MediaContent represents multi-modal content
type MediaContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	ImageURL     *ImageURL     `json:"image_url,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageURL represents an image URL
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// OpenAIResponse represents an OpenAI response
type OpenAIResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a choice in OpenAI response
type Choice struct {
	Index        int            `json:"index"`
	Message      OpenAIMessage  `json:"message"`
	FinishReason string         `json:"finish_reason"`
	Delta        *OpenAIMessage `json:"delta,omitempty"`
}

// Usage represents token usage
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ClaudeMessage represents a Claude message content block
type ClaudeMessage struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Input      any    `json:"input,omitempty"`
	ToolUseID  string `json:"tool_use_id,omitempty"`
	Content    any    `json:"content,omitempty"`
	Source     *Source `json:"source,omitempty"`
}

// Source represents image/document source
type Source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}
