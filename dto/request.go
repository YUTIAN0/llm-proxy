package dto

import "encoding/json"

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

// OpenAIResponsesRequest represents an OpenAI Responses API request.
type OpenAIResponsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input,omitempty"`
	Instructions       json.RawMessage `json:"instructions,omitempty"`
	Stream             *bool           `json:"stream,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	Text               json.RawMessage `json:"text,omitempty"`
	Reasoning          *ResponsesReasoning `json:"reasoning,omitempty"`
	ParallelToolCalls  json.RawMessage `json:"parallel_tool_calls,omitempty"`
	Store              json.RawMessage `json:"store,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	User               json.RawMessage `json:"user,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
}

// ResponsesReasoning controls reasoning for Responses API.
type ResponsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// OpenAIResponsesResponse represents an OpenAI Responses API response.
type OpenAIResponsesResponse struct {
	ID               string               `json:"id"`
	Object           string               `json:"object"`
	CreatedAt        int                  `json:"created_at"`
	Status           string               `json:"status"`
	Model            string               `json:"model"`
	Output           []ResponsesOutput    `json:"output"`
	Usage            *ResponsesUsage      `json:"usage"`
	Error            any                  `json:"error,omitempty"`
}

// ResponsesOutput represents an output item in Responses API.
type ResponsesOutput struct {
	Type      string                   `json:"type"`
	ID        string                   `json:"id"`
	Status    string                   `json:"status"`
	Role      string                   `json:"role"`
	Content   []ResponsesOutputContent `json:"content"`
	CallID    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments json.RawMessage          `json:"arguments,omitempty"`
}

// ArgumentsString returns function call arguments as string.
func (r *ResponsesOutput) ArgumentsString() string {
	if r == nil || len(r.Arguments) == 0 {
		return ""
	}
	return string(r.Arguments)
}

// ResponsesOutputContent represents content in a Responses output item.
type ResponsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ResponsesUsage represents usage in Responses API.
type ResponsesUsage struct {
	InputTokens            int `json:"input_tokens"`
	OutputTokens           int `json:"output_tokens"`
	TotalTokens            int `json:"total_tokens"`
	InputTokensDetails     *ResponsesTokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails    *ResponsesTokenDetails `json:"output_tokens_details,omitempty"`
}

// ResponsesTokenDetails represents detailed token counts.
type ResponsesTokenDetails struct {
	CachedTokens  int `json:"cached_tokens,omitempty"`
	TextTokens    int `json:"text_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ResponsesStreamEvent represents an SSE event from the Responses API stream.
type ResponsesStreamEvent struct {
	Type         string                  `json:"type"`
	Response     *OpenAIResponsesResponse `json:"response,omitempty"`
	Delta        string                  `json:"delta,omitempty"`
	Item         *ResponsesOutput        `json:"item,omitempty"`
	OutputIndex  *int                    `json:"output_index,omitempty"`
	ContentIndex *int                    `json:"content_index,omitempty"`
	ItemID       string                  `json:"item_id,omitempty"`
}
