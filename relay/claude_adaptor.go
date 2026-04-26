package relay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/llm-proxy/channel"
	"github.com/llm-proxy/dto"
	"github.com/llm-proxy/proxy"
)

type ClaudeToOpenAIAdaptor struct{}

func (a *ClaudeToOpenAIAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	baseURL := info.BaseURL
	if strings.HasSuffix(baseURL, "/v1") {
		return fmt.Sprintf("%s/chat/completions", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/chat/completions", baseURL), nil
}

func (a *ClaudeToOpenAIAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *ClaudeToOpenAIAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	var claudeReq map[string]any
	if err := json.Unmarshal(requestBody, &claudeReq); err != nil {
		return nil, err
	}

	model := "unknown"
	if m, ok := claudeReq["model"].(string); ok {
		info.OriginModel = m
		model = channel.ResolveModelAlias(m)
		info.UpstreamModel = model
	}

	messages := make([]dto.OpenAIMessage, 0)

	// Handle system prompt (string or array)
	if system, ok := claudeReq["system"]; ok {
		messages = a.convertSystemMessage(system)
	}

	// Convert messages
	if msgList, ok := claudeReq["messages"].([]any); ok {
		for _, m := range msgList {
			if msgMap, ok := m.(map[string]any); ok {
				converted := a.convertClaudeMessage(msgMap)
				messages = append(messages, converted...)
			}
		}
	}

	maxTokens := 4096
	if mt, ok := claudeReq["max_tokens"].(float64); ok {
		maxTokens = int(mt)
	}

	result := &dto.OpenAIChatRequest{
		Model:    model,
		Messages: messages,
	}

	// Handle max_tokens vs max_completion_tokens for o-series
	if isOpenAiOSeries(model) {
		result.MaxCompletionTokens = &maxTokens
	} else {
		result.MaxTokens = &maxTokens
	}

	if s, ok := claudeReq["stream"].(bool); ok && s {
		info.IsStream = true
		stream := true
		result.Stream = &stream
	}

	if tools, ok := claudeReq["tools"].([]any); ok {
		result.Tools = a.convertTools(tools)
	}

	// Handle tool_choice
	if toolChoice, ok := claudeReq["tool_choice"]; ok {
		result.ToolChoice = a.convertToolChoice(toolChoice)
	}

	if temp, ok := claudeReq["temperature"].(float64); ok {
		result.Temperature = &temp
	}

	if topP, ok := claudeReq["top_p"].(float64); ok {
		result.TopP = &topP
	}

	if topK, ok := claudeReq["top_k"].(float64); ok {
		k := int(topK)
		result.TopK = &k
	}

	// Handle stop_sequences
	if stopSeq, ok := claudeReq["stop_sequences"].([]any); ok {
		if len(stopSeq) == 1 {
			if s, ok := stopSeq[0].(string); ok {
				result.Stop = s
			}
		} else {
			result.Stop = stopSeq
		}
	}

	// Handle thinking/reasoning parameters
	if thinking, ok := claudeReq["thinking"].(map[string]any); ok {
		if effort := a.resolveReasoningEffort(thinking, model); effort != "" {
			result.ReasoningEffort = effort
		}
	}

	return result, nil
}

// isOpenAiOSeries checks if model is OpenAI o-series (o1, o3, o4-mini, etc.)
func isOpenAiOSeries(model string) bool {
	if len(model) > 1 && model[0] == 'o' {
		if b := model[1]; b >= '0' && b <= '9' {
			return true
		}
	}
	return false
}

// convertSystemMessage converts Claude system to OpenAI format
func (a *ClaudeToOpenAIAdaptor) convertSystemMessage(system any) []dto.OpenAIMessage {
	messages := make([]dto.OpenAIMessage, 0)

	switch s := system.(type) {
	case string:
		if s != "" {
			messages = append(messages, dto.OpenAIMessage{Role: "system", Content: s})
		}
	case []any:
		// Multiple system messages - combine into one
		var texts []string
		for _, item := range s {
			if itemMap, ok := item.(map[string]any); ok {
				if text, ok := itemMap["text"].(string); ok && text != "" {
					texts = append(texts, text)
				}
			}
		}
		if len(texts) > 0 {
			combined := strings.Join(texts, "\n")
			messages = append(messages, dto.OpenAIMessage{Role: "system", Content: combined})
		}
	}

	return messages
}

// convertClaudeMessage converts a Claude message to OpenAI format
func (a *ClaudeToOpenAIAdaptor) convertClaudeMessage(msgMap map[string]any) []dto.OpenAIMessage {
	messages := make([]dto.OpenAIMessage, 0)

	role, _ := msgMap["role"].(string)
	if role == "" {
		role = "user"
	}

	content := msgMap["content"]

	// Handle string content
	if contentStr, ok := content.(string); ok {
		messages = append(messages, dto.OpenAIMessage{
			Role:    role,
			Content: contentStr,
		})
		return messages
	}

	// Handle array content
	contentArray, ok := content.([]any)
	if !ok {
		return messages
	}

	// Check for tool_result
	for _, item := range contentArray {
		if itemMap, ok := item.(map[string]any); ok {
			if itemType, _ := itemMap["type"].(string); itemType == "tool_result" {
				// Convert to OpenAI tool message
				toolUseID, _ := itemMap["tool_use_id"].(string)
				toolContent := itemMap["content"]

				contentStr := ""
				switch c := toolContent.(type) {
				case string:
					contentStr = c
				case []any:
					// Extract text from array
					var texts []string
					for _, p := range c {
						if pMap, ok := p.(map[string]any); ok {
							if text, ok := pMap["text"].(string); ok {
								texts = append(texts, text)
							}
						}
					}
					contentStr = strings.Join(texts, "\n")
				}

				messages = append(messages, dto.OpenAIMessage{
					Role:       "tool",
					Content:    contentStr,
					ToolCallID: toolUseID,
				})
				return messages
			}
		}
	}

	// Handle regular content (text, images, tool_use)
	var textParts []string
	var mediaContents []dto.MediaContent
	var toolCalls []dto.ToolCall

	for _, item := range contentArray {
		if itemMap, ok := item.(map[string]any); ok {
			itemType, _ := itemMap["type"].(string)

			switch itemType {
			case "text":
				if text, ok := itemMap["text"].(string); ok && text != "" {
					textParts = append(textParts, text)
					mediaContents = append(mediaContents, dto.MediaContent{
						Type: "text",
						Text: text,
					})
				}

			case "image":
				if source, ok := itemMap["source"].(map[string]any); ok {
					mediaType, _ := source["media_type"].(string)
					data, _ := source["data"].(string)
					if mediaType != "" && data != "" {
						imageURL := fmt.Sprintf("data:%s;base64,%s", mediaType, data)
						mediaContents = append(mediaContents, dto.MediaContent{
							Type: "image_url",
							ImageURL: &dto.ImageURL{
								URL: imageURL,
							},
						})
					}
				}

			case "tool_use":
				toolID, _ := itemMap["id"].(string)
				toolName, _ := itemMap["name"].(string)
				toolInput := itemMap["input"]

				inputJSON := "{}"
				if toolInput != nil {
					if b, err := json.Marshal(toolInput); err == nil {
						inputJSON = string(b)
					}
				}

				toolCalls = append(toolCalls, dto.ToolCall{
					ID:   toolID,
					Type: "function",
					Function: dto.FunctionResponse{
						Name:      toolName,
						Arguments: inputJSON,
					},
				})
			}
		}
	}

	// Build the message
	msg := dto.OpenAIMessage{Role: role}

	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		msg.Content = "" // Content should be empty for assistant messages with tool calls
	} else if len(mediaContents) > 0 {
		msg.Content = mediaContents
	} else if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "\n")
	}

	if msg.Content != nil || len(msg.ToolCalls) > 0 {
		messages = append(messages, msg)
	}

	return messages
}

// convertToolChoice converts Claude tool_choice to OpenAI format
func (a *ClaudeToOpenAIAdaptor) convertToolChoice(toolChoice any) any {
	switch tc := toolChoice.(type) {
	case string:
		switch tc {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "none":
			return "none"
		default:
			return tc
		}
	case map[string]any:
		// Claude format: {"type": "tool", "name": "tool_name"}
		if tcType, ok := tc["type"].(string); ok {
			if tcType == "tool" {
				if name, ok := tc["name"].(string); ok {
					return map[string]any{
						"type": "function",
						"function": map[string]any{
							"name": name,
						},
					}
				}
			}
		}
		return tc
	default:
		return toolChoice
	}
}

// resolveReasoningEffort maps Claude thinking to OpenAI reasoning_effort
// Note: Only use universally supported values: none, low, medium, high
// (xhigh is not supported by vLLM and other OpenAI-compatible servers)
func (a *ClaudeToOpenAIAdaptor) resolveReasoningEffort(thinking map[string]any, model string) string {
	// Check for output_config.effort first (Claude 4.7)
	if outputConfig, ok := thinking["output_config"].(map[string]any); ok {
		if effort, ok := outputConfig["effort"].(string); ok {
			switch effort {
			case "low", "medium", "high":
				return effort
			case "max":
				return "high"
			}
		}
	}

	// Map thinking.type to reasoning_effort
	thinkingType, _ := thinking["type"].(string)
	switch thinkingType {
	case "adaptive":
		return "high"
	case "enabled":
		budgetTokens := 0
		if bt, ok := thinking["budget_tokens"].(float64); ok {
			budgetTokens = int(bt)
		}
		switch {
		case budgetTokens < 4000:
			return "low"
		case budgetTokens < 16000:
			return "medium"
		default:
			return "high"
		}
	}

	return ""
}

func (a *ClaudeToOpenAIAdaptor) convertTools(claudeTools []any) []dto.OpenAITool {
	tools := make([]dto.OpenAITool, 0)
	for _, t := range claudeTools {
		if toolMap, ok := t.(map[string]any); ok {
			// Skip BatchTool type
			if toolType, ok := toolMap["type"].(string); ok && toolType == "BatchTool" {
				continue
			}

			// Check if already in OpenAI format (has "function" field)
			if fn, ok := toolMap["function"].(map[string]any); ok {
				tool := dto.OpenAITool{Type: "function"}
				tool.Function = &dto.FunctionRequest{}
				if name, ok := fn["name"].(string); ok {
					tool.Function.Name = name
				}
				if desc, ok := fn["description"].(string); ok {
					tool.Function.Description = desc
				}
				if params, ok := fn["parameters"].(map[string]any); ok {
					tool.Function.Parameters = params
				}
				// Preserve cache_control
				if cc, ok := toolMap["cache_control"].(map[string]any); ok {
					tool.CacheControl = &dto.CacheControl{Type: "ephemeral"}
					if ccType, ok := cc["type"].(string); ok {
						tool.CacheControl.Type = ccType
					}
				}
				tools = append(tools, tool)
				continue
			}

			// Native Claude format: {"name": "...", "description": "...", "input_schema": {...}}
			if name, ok := toolMap["name"].(string); ok {
				tool := dto.OpenAITool{Type: "function"}
				tool.Function = &dto.FunctionRequest{Name: name}
				if desc, ok := toolMap["description"].(string); ok {
					tool.Function.Description = desc
				}
				if schema, ok := toolMap["input_schema"].(map[string]any); ok {
					tool.Function.Parameters = schema
				}
				// Preserve cache_control
				if cc, ok := toolMap["cache_control"].(map[string]any); ok {
					tool.CacheControl = &dto.CacheControl{Type: "ephemeral"}
					if ccType, ok := cc["type"].(string); ok {
						tool.CacheControl.Type = ccType
					}
				}
				tools = append(tools, tool)
			}
		}
	}
	return tools
}

func (a *ClaudeToOpenAIAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
	url, _ := a.GetRequestURL(info)
	httpReq, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, err
	}
	a.SetupRequestHeader(&httpReq.Header, info)

	client := proxy.GetClient()
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (a *ClaudeToOpenAIAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	claudeResp := a.convertOpenAIToClaude(body, info.OriginModel)
	return claudeResp, nil
}

func (a *ClaudeToOpenAIAdaptor) streamClaudeResponse(c *gin.Context, resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		log.Printf("[relay] upstream stream error: status=%d body=%s", resp.StatusCode, string(errBody))
		var errResp map[string]any
		if json.Unmarshal(errBody, &errResp) == nil {
			c.Status(resp.StatusCode)
			c.JSON(resp.StatusCode, a.convertErrorToClaude(errResp))
			return fmt.Errorf("upstream returned %d", resp.StatusCode)
		}
		c.Status(resp.StatusCode)
		c.Writer.Write(errBody)
		return fmt.Errorf("upstream returned %d", resp.StatusCode)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	debugLog("[CLIENT RESPONSE] setting SSE headers for client")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	state := &ClaudeStreamState{
		ToolBlocks: make(map[int]*ToolBlockState),
	}

	var utf8Remainder []byte
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			dataBytes := append(utf8Remainder, []byte(strings.TrimPrefix(line, "data: "))...)
			utf8Remainder = nil

			dataStr := string(dataBytes)
			if !utf8.ValidString(dataStr) {
				for i := 0; i < len(dataStr); {
					r, size := utf8.DecodeRuneInString(dataStr[i:])
					if r == utf8.RuneError {
						utf8Remainder = []byte(dataStr[i:])
						dataStr = dataStr[:i]
						break
					}
					i += size
				}
			}

			// DEBUG: log first few upstream SSE events
			if dataStr != "[DONE]" {
				debugUpstream := dataStr
				if len(debugUpstream) > 500 {
					debugUpstream = debugUpstream[:500] + "..."
				}
				debugLog("[UPSTREAM SSE] data=%s", debugUpstream)
			} else {
				debugLog("[UPSTREAM SSE] data=[DONE]")
			}

			if dataStr == "[DONE]" {
				a.sendStreamEnd(c, state)
				flusher.Flush()
				break
			}

			var sseEvent map[string]any
			if err := json.Unmarshal([]byte(dataStr), &sseEvent); err != nil {
				continue
			}

			a.openaiSseToClaudeWithState(c, sseEvent, state)
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[relay] stream read error: %v", err)
		return err
	}

	return nil
}

func (a *ClaudeToOpenAIAdaptor) sendStreamEnd(c *gin.Context, state *ClaudeStreamState) {
	if state.SentMessageStop {
		return
	}

	a.stopOpenBlocks(c, state)

	c.Writer.WriteString("event: message_delta\n")
	deltaData, _ := json.Marshal(MessageDeltaEvent{
		Type: "message_delta",
		Usage: MessageDeltaUsage{
			InputTokens:               state.InputTokens,
			OutputTokens:              state.OutputTokens,
			CacheCreationInputTokens:  0,
			CacheReadInputTokens:      0,
			ClaudeCacheCreation5MTokens: 0,
			ClaudeCacheCreation1HTokens: 0,
		},
		Delta: MessageDeltaContent{
			StopReason: "end_turn",
		},
	})
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
	logClientSSE("message_delta", deltaData)

	c.Writer.WriteString("event: message_stop\n")
	c.Writer.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	logClientSSE("message_stop", []byte(`{"type":"message_stop"}`))

	state.SentMessageStop = true
}

func (a *ClaudeToOpenAIAdaptor) stopOpenBlocks(c *gin.Context, state *ClaudeStreamState) {
	switch state.LastMessageType {
	case LastMessageTypeThinking:
		c.Writer.WriteString("event: content_block_stop\n")
		c.Writer.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", state.Index))
	case LastMessageTypeText:
		c.Writer.WriteString("event: content_block_stop\n")
		c.Writer.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", state.Index))
	case LastMessageTypeTools:
		for i := state.ToolCallBaseIndex; i <= state.ToolCallBaseIndex+state.ToolCallMaxOffset; i++ {
			c.Writer.WriteString("event: content_block_stop\n")
			c.Writer.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i))
		}
	}
}

func (a *ClaudeToOpenAIAdaptor) stopOpenBlocksAndAdvance(c *gin.Context, state *ClaudeStreamState) {
	if state.LastMessageType == LastMessageTypeNone {
		return
	}

	a.stopOpenBlocks(c, state)

	switch state.LastMessageType {
	case LastMessageTypeTools:
		state.Index = state.ToolCallBaseIndex + state.ToolCallMaxOffset + 1
		state.ToolCallBaseIndex = 0
		state.ToolCallMaxOffset = 0
	default:
		state.Index++
	}

	state.LastMessageType = LastMessageTypeNone
}

func (a *ClaudeToOpenAIAdaptor) openaiSseToClaudeWithState(c *gin.Context, sseEvent map[string]any, state *ClaudeStreamState) {
	if state.MessageID == "" {
		if id, ok := sseEvent["id"].(string); ok {
			state.MessageID = id
		}
	}
	if state.Model == "" {
		if m, ok := sseEvent["model"].(string); ok {
			state.Model = m
		}
	}

	if usage, ok := sseEvent["usage"].(map[string]any); ok {
		if pt, ok := usage["prompt_tokens"].(float64); ok {
			state.InputTokens = int(pt)
		}
		if ct, ok := usage["completion_tokens"].(float64); ok {
			state.OutputTokens = int(ct)
		}
	}

	choices, ok := sseEvent["choices"].([]any)
	if !ok || len(choices) == 0 {
		if usage, ok := sseEvent["usage"].(map[string]any); ok && state.SentMessageStart {
			if pt, ok := usage["prompt_tokens"].(float64); ok {
				state.InputTokens = int(pt)
			}
			if ct, ok := usage["completion_tokens"].(float64); ok {
				state.OutputTokens = int(ct)
			}
		}
		return
	}

	choice := choices[0].(map[string]any)
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return
	}
	finishReason, _ := choice["finish_reason"].(string)

	if !state.SentMessageStart {
		state.SentMessageStart = true

		c.Writer.WriteString("event: message_start\n")
		msg, _ := json.Marshal(MessageStartEvent{
			Type: "message_start",
			Message: MessageData{
				Type:  "message",
				Role:  "assistant",
				ID:    state.MessageID,
				Model: state.Model,
				Content: []any{},
				Usage: MessageUsage{
					InputTokens:               state.InputTokens,
					CacheCreationInputTokens:  0,
					CacheReadInputTokens:      0,
					OutputTokens:              0,
					ClaudeCacheCreation5MTokens: 0,
					ClaudeCacheCreation1HTokens: 0,
				},
			},
		})
		c.Writer.WriteString("data: " + string(msg) + "\n\n")
		logClientSSE("message_start", msg)
	}

	if toolCalls, ok := delta["tool_calls"].([]any); ok && len(toolCalls) > 0 {
		a.handleToolCalls(c, toolCalls, state)
		return
	}

	if reasoning, ok := delta["reasoning"].(string); ok && reasoning != "" {
		a.handleThinkingContent(c, reasoning, state)
		return
	}

	if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
		a.handleThinkingContent(c, reasoning, state)
		return
	}

	if text, ok := delta["content"].(string); ok && text != "" {
		a.handleTextContent(c, text, state)
		return
	}

	if finishReason != "" && !state.SentMessageStop {
		state.SentMessageStop = true
		stopReason := mapStopReasonFromFinish(finishReason)

		a.stopOpenBlocks(c, state)

		c.Writer.WriteString("event: message_delta\n")
		deltaData, _ := json.Marshal(MessageDeltaEvent{
			Type: "message_delta",
			Usage: MessageDeltaUsage{
				InputTokens:               state.InputTokens,
				OutputTokens:              state.OutputTokens,
				CacheCreationInputTokens:  0,
				CacheReadInputTokens:      0,
				ClaudeCacheCreation5MTokens: 0,
				ClaudeCacheCreation1HTokens: 0,
			},
			Delta: MessageDeltaContent{
				StopReason: stopReason,
			},
		})
		c.Writer.WriteString("data: " + string(deltaData) + "\n\n")

		c.Writer.WriteString("event: message_stop\n")
		c.Writer.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	}
}

func (a *ClaudeToOpenAIAdaptor) handleToolCalls(c *gin.Context, toolCalls []any, state *ClaudeStreamState) {
	if state.LastMessageType != LastMessageTypeTools {
		a.stopOpenBlocksAndAdvance(c, state)
		state.ToolCallBaseIndex = state.Index
		state.ToolCallMaxOffset = 0
		state.LastMessageType = LastMessageTypeTools
	}

	for i, tc := range toolCalls {
		toolCall, ok := tc.(map[string]any)
		if !ok {
			continue
		}

		offset := i
		if idx, ok := toolCall["index"].(float64); ok {
			offset = int(idx)
		}

		if offset > state.ToolCallMaxOffset {
			state.ToolCallMaxOffset = offset
		}

		blockIndex := state.ToolCallBaseIndex + offset

		toolID, _ := toolCall["id"].(string)
		funcMap, _ := toolCall["function"].(map[string]any)
		funcName, _ := funcMap["name"].(string)
		funcArgs, _ := funcMap["arguments"].(string)

		if toolID != "" || funcName != "" {
			c.Writer.WriteString("event: content_block_start\n")
			blockData, _ := json.Marshal(ContentBlockStartEvent{
				Type:  "content_block_start",
				Index: blockIndex,
				ContentBlock: ToolUseContentBlock{
					Type:  "tool_use",
					ID:    toolID,
					Name:  funcName,
					Input: map[string]any{},
				},
			})
			c.Writer.WriteString("data: " + string(blockData) + "\n\n")

			state.ToolBlocks[blockIndex] = &ToolBlockState{
				Index:     blockIndex,
				ID:        toolID,
				Name:      funcName,
				Started:   true,
				Arguments: "",
			}
		}

		if funcArgs != "" {
			c.Writer.WriteString("event: content_block_delta\n")
			deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: blockIndex,
				Delta: InputJSONDelta{
					Type:        "input_json_delta",
					PartialJSON: funcArgs,
				},
			})
			c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
		}
	}

	state.Index = state.ToolCallBaseIndex + state.ToolCallMaxOffset
}

func (a *ClaudeToOpenAIAdaptor) handleThinkingContent(c *gin.Context, content string, state *ClaudeStreamState) {
	if state.LastMessageType != LastMessageTypeThinking {
		a.stopOpenBlocksAndAdvance(c, state)

		c.Writer.WriteString("event: content_block_start\n")
		blockData, _ := json.Marshal(ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: state.Index,
			ContentBlock: ThinkingContentBlock{
				Type:     "thinking",
				Thinking: "",
			},
		})
		c.Writer.WriteString("data: " + string(blockData) + "\n\n")

		state.LastMessageType = LastMessageTypeThinking
		state.SentThinkingStart = true
	}

	c.Writer.WriteString("event: content_block_delta\n")
	deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: state.Index,
		Delta: ThinkingDelta{
			Type:     "thinking_delta",
			Thinking: content,
		},
	})
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
}

func (a *ClaudeToOpenAIAdaptor) handleTextContent(c *gin.Context, text string, state *ClaudeStreamState) {
	// If we're already inside a  tag from a previous chunk,
	// treat ALL content as thinking until we see
	if state.InThinkTag {
		endIdx := strings.Index(text, "")
		if endIdx >= 0 {
			// Emit the thinking part before
			thinkingPart := text[:endIdx]
			if thinkingPart != "" {
				a.emitThinkingDelta(c, thinkingPart, state)
			}
			// Close the thinking block
			state.InThinkTag = false
			a.stopOpenBlocksAndAdvance(c, state)

			// Remaining text after  goes to text
			remaining := text[endIdx+len(""):]
			remaining = strings.TrimLeft(remaining, "\n\r ")
			if remaining != "" {
				a.emitTextDelta(c, remaining, state)
			}
		} else {
			// Still inside thinking, emit entire text as thinking
			a.emitThinkingDelta(c, text, state)
		}
		return
	}

	// Not in a think tag - check if this chunk starts one
	thinkingContent, remainingText := a.extractThinkingTags(text, state)

	if thinkingContent != "" {
		// We found  tag at the start
		if remainingText == "" && !strings.Contains(text, "") {
			// Only ...  with no closing tag - enter thinking mode
			state.InThinkTag = true
			a.emitThinkingDelta(c, thinkingContent, state)
		} else {
			// ... in the same chunk
			a.emitThinkingDelta(c, thinkingContent, state)
			// Close thinking, start text
			a.stopOpenBlocksAndAdvance(c, state)
			if remainingText != "" {
				a.emitTextDelta(c, remainingText, state)
			}
		}
		return
	}

	// No thinking tag - emit as text
	if remainingText != "" {
		a.emitTextDelta(c, remainingText, state)
	}
}

func (a *ClaudeToOpenAIAdaptor) emitThinkingDelta(c *gin.Context, content string, state *ClaudeStreamState) {
	if state.LastMessageType != LastMessageTypeThinking {
		a.stopOpenBlocksAndAdvance(c, state)

		c.Writer.WriteString("event: content_block_start\n")
		blockData, _ := json.Marshal(ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: state.Index,
			ContentBlock: ThinkingContentBlock{
				Type:     "thinking",
				Thinking: "",
			},
		})
		c.Writer.WriteString("data: " + string(blockData) + "\n\n")
		logClientSSE("content_block_start", blockData)

		state.LastMessageType = LastMessageTypeThinking
		state.SentThinkingStart = true
	}

	c.Writer.WriteString("event: content_block_delta\n")
	deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: state.Index,
		Delta: ThinkingDelta{
			Type:     "thinking_delta",
			Thinking: content,
		},
	})
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
	logClientSSE("content_block_delta", deltaData)
}

func (a *ClaudeToOpenAIAdaptor) emitTextDelta(c *gin.Context, text string, state *ClaudeStreamState) {
	if state.LastMessageType == LastMessageTypeThinking {
		a.stopOpenBlocksAndAdvance(c, state)
	}

	if state.LastMessageType != LastMessageTypeText {
		c.Writer.WriteString("event: content_block_start\n")
		blockData, _ := json.Marshal(ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: state.Index,
			ContentBlock: TextContentBlock{
				Type: "text",
				Text: "",
			},
		})
		c.Writer.WriteString("data: " + string(blockData) + "\n\n")
		logClientSSE("content_block_start", blockData)

		state.LastMessageType = LastMessageTypeText
		state.SentTextStart = true
	}

	c.Writer.WriteString("event: content_block_delta\n")
	deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: state.Index,
		Delta: TextDelta{
			Type:      "text_delta",
			Text: text,
		},
	})
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
	logClientSSE("content_block_delta", deltaData)
}

func (a *ClaudeToOpenAIAdaptor) extractThinkingTags(text string, state *ClaudeStreamState) (thinking string, remaining string) {
	// Check for  tags (used by MiniMax and other models)
	if strings.HasPrefix(text, "") {
		endTag := ""
		if endIdx := strings.Index(text, endTag); endIdx > 0 {
			thinking = text[len("") : endIdx]
			remaining = text[endIdx+len(endTag):]
			return thinking, remaining
		}
		thinking = text[len(""):]
		remaining = ""
		return thinking, remaining
	}

	// Check for <thinking> tags
	if strings.HasPrefix(text, "<thinking>") {
		endTag := "</thinking>"
		if endIdx := strings.Index(text, endTag); endIdx > 0 {
			thinking = text[len("<thinking>") : endIdx]
			remaining = text[endIdx+len(endTag):]
			return thinking, remaining
		}
		thinking = text[len("<thinking>"):]
		remaining = ""
		return thinking, remaining
	}

	// Check for \n or \r\n prefix (some models add newline)
	trimmed := strings.TrimLeftFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	if strings.HasPrefix(trimmed, "") {
		prefix := text[:len(text)-len(trimmed)]
		endTag := ""
		rest := trimmed[len(""):]
		if endIdx := strings.Index(rest, endTag); endIdx > 0 {
			thinking = rest[:endIdx]
			remaining = prefix + rest[endIdx+len(endTag):]
			return thinking, remaining
		}
		thinking = rest
		remaining = prefix
		return thinking, remaining
	}

	remaining = text
	return "", remaining
}

func (a *ClaudeToOpenAIAdaptor) convertOpenAIToClaude(openaiBody []byte, model string) map[string]any {
	var openaiResp map[string]any
	if err := json.Unmarshal(openaiBody, &openaiResp); err != nil {
		return map[string]any{
			"id":          "error",
			"content":     []any{},
			"model":       model,
			"stop_reason": "error",
			"type":        "message",
			"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
		}
	}

	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if u, ok := openaiResp["usage"].(map[string]any); ok {
		if pt, ok := u["prompt_tokens"].(float64); ok {
			usage["input_tokens"] = int(pt)
		}
		if ct, ok := u["completion_tokens"].(float64); ok {
			usage["output_tokens"] = int(ct)
		}
	}

	choices, ok := openaiResp["choices"].([]any)
	if !ok || len(choices) == 0 {
		return map[string]any{
			"id":            openaiResp["id"],
			"type":          "message",
			"role":          "assistant",
			"content":       []map[string]any{{"type": "text", "text": ""}},
			"model":         model,
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         usage,
		}
	}

	choice := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)

	text := ""
	if c, ok := msg["content"].(string); ok {
		text = c
	}

	content := make([]map[string]any, 0)

	if r, ok := msg["reasoning"].(string); ok && r != "" {
		content = append(content, map[string]any{
			"type":          "thinking",
			"thinking":      r,
			"cache_control": map[string]string{"type": "ephemeral"},
		})
	}

	if toolCalls, ok := msg["tool_calls"].([]any); ok && len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]any); ok {
				toolUse := map[string]any{"type": "tool_use"}
				if id, ok := tcMap["id"].(string); ok {
					toolUse["id"] = id
				}
				if name, ok := tcMap["name"].(string); ok {
					toolUse["name"] = name
				}
				if args, ok := tcMap["function"].(map[string]any); ok {
					if arguments, ok := args["arguments"].(string); ok {
						toolUse["input"] = arguments
					}
				}
				content = append(content, toolUse)
			}
		}
	}

	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}

	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	return map[string]any{
		"id":            openaiResp["id"],
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   mapStopReason(openaiResp),
		"stop_sequence": nil,
		"usage":         usage,
	}
}

func (a *ClaudeToOpenAIAdaptor) convertErrorToClaude(errResp map[string]any) map[string]any {
	errType := "api_error"
	errMsg := "unknown error"

	if e, ok := errResp["error"].(map[string]any); ok {
		if t, ok := e["type"].(string); ok {
			errType = t
		}
		if m, ok := e["message"].(string); ok {
			errMsg = m
		}
	}

	return map[string]any{
		"type":  "error",
		"error": map[string]any{
			"type":    errType,
			"message": errMsg,
		},
	}
}

func (a *ClaudeToOpenAIAdaptor) GetModelList() []string {
	return []string{}
}

func (a *ClaudeToOpenAIAdaptor) GetChannelName() string {
	return "claude"
}
