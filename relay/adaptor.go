package relay

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Adaptor defines the interface for protocol conversion between different LLM API formats.
type Adaptor interface {
	GetRequestURL(info *RelayInfo) (string, error)
	SetupRequestHeader(req *http.Header, info *RelayInfo) error
	ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error)
	DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error)
	DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error)
	GetModelList() []string
	GetChannelName() string
}

// RelayInfo carries context for a single relay request.
type RelayInfo struct {
	Mode             int
	OriginModel      string
	UpstreamModel    string
	ChannelID        int
	ChannelName      string
	APIKey           string
	ClientAPIKey     string
	ClientAPIKeyName string
	BaseURL          string
	Format           string
	IsStream         bool
	CustomHeaders    map[string]string
	InputTokens      int
	OutputTokens     int
}

// SSE event structs to maintain correct field order in JSON output.
type ContentBlockStartEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type ContentBlockDeltaEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type ContentBlockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type MessageStartEvent struct {
	Type    string      `json:"type"`
	Message MessageData `json:"message"`
}

type MessageData struct {
	Type    string       `json:"type"`
	Model   string       `json:"model"`
	Usage   MessageUsage `json:"usage"`
	Role    string       `json:"role"`
	ID      string       `json:"id"`
	Content []any        `json:"content"`
}

type MessageUsage struct {
	InputTokens                 int `json:"input_tokens"`
	CacheCreationInputTokens    int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens        int `json:"cache_read_input_tokens"`
	OutputTokens                int `json:"output_tokens"`
	ClaudeCacheCreation5MTokens int `json:"claude_cache_creation_5_m_tokens"`
	ClaudeCacheCreation1HTokens int `json:"claude_cache_creation_1_h_tokens"`
}

type MessageDeltaEvent struct {
	Type  string             `json:"type"`
	Usage MessageDeltaUsage  `json:"usage"`
	Delta MessageDeltaContent `json:"delta"`
}

type MessageDeltaUsage struct {
	InputTokens                 int `json:"input_tokens"`
	CacheCreationInputTokens    int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens        int `json:"cache_read_input_tokens"`
	OutputTokens                int `json:"output_tokens"`
	ClaudeCacheCreation5MTokens int `json:"claude_cache_creation_5_m_tokens"`
	ClaudeCacheCreation1HTokens int `json:"claude_cache_creation_1_h_tokens"`
}

type ThinkingContentBlock struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type TextContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolUseContentBlock struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input any    `json:"input"`
}

type ThinkingDelta struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type TextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type InputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type MessageDeltaContent struct {
	StopReason string `json:"stop_reason"`
}

// LastMessageType tracks which content block type is currently open.
type LastMessageType int

const (
	LastMessageTypeNone LastMessageType = iota
	LastMessageTypeText
	LastMessageTypeThinking
	LastMessageTypeTools
)

// ClaudeStreamState tracks state across SSE events for Claude streaming.
type ClaudeStreamState struct {
	Index             int
	LastMessageType   LastMessageType
	ToolCallBaseIndex int
	ToolCallMaxOffset int
	Done              bool
	MessageID         string
	Model             string
	InputTokens       int
	OutputTokens      int
	SentMessageStart  bool
	SentThinkingStart bool
	SentTextStart     bool
	SentMessageStop   bool
	InThinkTag        bool
	ToolBlocks        map[int]*ToolBlockState
}

type ToolBlockState struct {
	Index     int
	ID        string
	Name      string
	Started   bool
	Arguments string
}

// GeminiStreamState tracks state across Gemini SSE events.
type GeminiStreamState struct {
	Index             int
	LastMessageType   LastMessageType
	ToolCallBaseIndex int
	ToolCallMaxOffset int
	MessageID         string
	Model             string
	InputTokens       int
	OutputTokens      int
	SentMessageStart  bool
	SentMessageStop   bool
	HasToolUse        bool
	IsStop            bool
	FinishReason      string
	AccumulatedText   string
	AccumulatedThought string
	ToolBlocks        map[int]*ToolBlockState
}

// GetAdaptorByFormat returns the appropriate adaptor for the given format.
func GetAdaptorByFormat(format string, mode int) Adaptor {
	switch format {
	case "claude", "claude_to_openai":
		return &ClaudeToOpenAIAdaptor{}
	case "gemini", "gemini_to_openai":
		return &GeminiToOpenAIAdaptor{}
	case "openai_to_gemini":
		return &OpenAIToGeminiAdaptor{}
	default:
		return &OpenAIAdaptor{}
	}
}

func mapStopReasonFromFinish(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func mapStopReason(openaiResp map[string]any) string {
	if choices, ok := openaiResp["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if fr, ok := choice["finish_reason"].(string); ok {
				return mapStopReasonFromFinish(fr)
			}
		}
	}
	return "end_turn"
}
