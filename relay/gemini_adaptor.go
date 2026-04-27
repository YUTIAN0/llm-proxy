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

type GeminiToOpenAIAdaptor struct{}

func (a *GeminiToOpenAIAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	baseURL := info.BaseURL
	if strings.HasSuffix(baseURL, "/v1") {
		return fmt.Sprintf("%s/chat/completions", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/chat/completions", baseURL), nil
}

func (a *GeminiToOpenAIAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *GeminiToOpenAIAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	var geminiReq map[string]any
	if err := json.Unmarshal(requestBody, &geminiReq); err != nil {
		return nil, err
	}

	model := info.OriginModel
	if model == "" {
		model = "unknown"
	}
	upstreamModel := channel.ResolveModelAlias(model)
	info.UpstreamModel = upstreamModel

	// Convert Gemini contents to OpenAI messages
	messages := make([]dto.OpenAIMessage, 0)

	// System instructions
	if sysInstr, ok := geminiReq["systemInstruction"].(map[string]any); ok {
		if parts, ok := sysInstr["parts"].([]any); ok {
			var texts []string
			for _, p := range parts {
				if partMap, ok := p.(map[string]any); ok {
					if text, ok := partMap["text"].(string); ok && text != "" {
						texts = append(texts, text)
					}
				}
			}
			if len(texts) > 0 {
				messages = append(messages, dto.OpenAIMessage{
					Role:    "system",
					Content: strings.Join(texts, "\n"),
				})
			}
		}
	}

	// Contents → messages
	if contents, ok := geminiReq["contents"].([]any); ok {
		for _, c := range contents {
			contentMap, ok := c.(map[string]any)
			if !ok {
				continue
			}

			role := "user"
			if r, ok := contentMap["role"].(string); ok {
				if r == "model" {
					role = "assistant"
				}
			}

			if parts, ok := contentMap["parts"].([]any); ok {
				msg := dto.OpenAIMessage{Role: role}
				var textParts []string
				var toolCalls []dto.ToolCall
				var toolResultContent string
				var toolResultID string

				for _, p := range parts {
					partMap, ok := p.(map[string]any)
					if !ok {
						continue
					}

					// Text part
					if text, ok := partMap["text"].(string); ok {
						textParts = append(textParts, text)
						continue
					}

					// Function call
					if fc, ok := partMap["functionCall"].(map[string]any); ok {
						name, _ := fc["name"].(string)
						args := fc["args"]
						argsJSON := "{}"
						if args != nil {
							if b, err := json.Marshal(args); err == nil {
								argsJSON = string(b)
							}
						}
						toolCalls = append(toolCalls, dto.ToolCall{
							ID:   fmt.Sprintf("call_%s", name),
							Type: "function",
							Function: dto.FunctionResponse{
								Name:      name,
								Arguments: argsJSON,
							},
						})
						continue
					}

					// Function response
					if fr, ok := partMap["functionResponse"].(map[string]any); ok {
						name, _ := fr["name"].(string)
						toolResultID = fmt.Sprintf("call_%s", name)
						respContent := fr["response"]
						if b, err := json.Marshal(respContent); err == nil {
							toolResultContent = string(b)
						} else {
							toolResultContent = fmt.Sprintf("%v", respContent)
						}
						continue
					}

					// Inline data (image)
					if inline, ok := partMap["inlineData"].(map[string]any); ok {
						mimeType, _ := inline["mimeType"].(string)
						data, _ := inline["data"].(string)
						if strings.HasPrefix(mimeType, "image/") {
							textParts = append(textParts, fmt.Sprintf("![image](data:%s;base64,%s)", mimeType, data))
						}
					}
				}

				// Handle tool results as tool messages
				if toolResultID != "" {
					messages = append(messages, dto.OpenAIMessage{
						Role:       "tool",
						Content:    toolResultContent,
						ToolCallID: toolResultID,
					})
					continue
				}

				// Build the message
				if len(toolCalls) > 0 {
					msg.ToolCalls = toolCalls
					msg.Content = ""
				} else if len(textParts) > 0 {
					msg.Content = strings.Join(textParts, "\n")
				}

				if msg.Content != nil || len(msg.ToolCalls) > 0 {
					messages = append(messages, msg)
				}
			}
		}
	}

	maxTokens := 4096
	if genCfg, ok := geminiReq["generationConfig"].(map[string]any); ok {
		if mt, ok := genCfg["maxOutputTokens"].(float64); ok {
			maxTokens = int(mt)
		}
	}

	result := &dto.OpenAIChatRequest{
		Model:    upstreamModel,
		Messages: messages,
		MaxTokens: &maxTokens,
	}

	// Always set stream based on info.IsStream (detected from URL or query param)
	if info.IsStream {
		stream := true
		result.Stream = &stream
	}

	return result, nil
}

func (a *GeminiToOpenAIAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
	url, _ := a.GetRequestURL(info)
	httpReq, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, err
	}
	_ = a.SetupRequestHeader(&httpReq.Header, info)

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

func (a *GeminiToOpenAIAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if info.IsStream {
		// Non-streaming fallback for stream path - shouldn't normally reach here
		return string(body), nil
	}

	var openaiResp map[string]any
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, err
	}

	return a.convertOpenAIToGemini(openaiResp, info.OriginModel), nil
}

func (a *GeminiToOpenAIAdaptor) convertOpenAIToGemini(openaiResp map[string]any, model string) map[string]any {
	if errMsg, ok := openaiResp["error"].(string); ok && errMsg != "" {
		return map[string]any{
			"error": map[string]any{
				"code":    "upstream_error",
				"message": errMsg,
			},
		}
	}
	if errMap, ok := openaiResp["error"].(map[string]any); ok {
		return map[string]any{"error": errMap}
	}

	candidates := make([]map[string]any, 0)

	if choices, ok := openaiResp["choices"].([]any); ok {
		for i, ch := range choices {
			if choice, ok := ch.(map[string]any); ok {
				msg, _ := choice["message"].(map[string]any)
				content := ""
				if c, ok := msg["content"].(string); ok {
					content = c
				}

				finishReason := "STOP"
				if fr, ok := choice["finish_reason"].(string); ok {
					if fr == "length" {
						finishReason = "MAX_TOKENS"
					}
				}

				candidates = append(candidates, map[string]any{
					"content": map[string]any{
						"parts":  []map[string]any{{"text": content}},
						"role":   "model",
					},
					"finishReason": finishReason,
					"index":        i,
				})
			}
		}
	}

	result := map[string]any{"candidates": candidates}

	if usage, ok := openaiResp["usage"].(map[string]any); ok {
		result["usageMetadata"] = map[string]any{
			"promptTokenCount":     usage["prompt_tokens"],
			"candidatesTokenCount": usage["completion_tokens"],
			"totalTokenCount":      usage["total_tokens"],
		}
	}

	return result
}

// streamGeminiResponse handles Gemini streaming: reads upstream OpenAI SSE, converts to Gemini format.
//nolint:errcheck
func (a *GeminiToOpenAIAdaptor) streamGeminiResponse(c *gin.Context, resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		log.Printf("[relay] upstream gemini stream error: status=%d body=%s", resp.StatusCode, string(errBody))
		c.Status(resp.StatusCode)
		c.Writer.Write(errBody)
		return fmt.Errorf("upstream returned %d", resp.StatusCode)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	var utf8Remainder []byte
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	accumulatedText := ""
	accumulatedThought := ""

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

			debugLog("[UPSTREAM GEMINI SSE] data=%s", truncateStr(dataStr, 500))

			if dataStr == "[DONE]" {
				// Send final response with usage if available
				flusher.Flush()
				break
			}

			var openaiResp map[string]any
			if err := json.Unmarshal([]byte(dataStr), &openaiResp); err != nil {
				continue
			}

			// Convert OpenAI chunk to Gemini format
			geminiChunk := a.convertOpenAIStreamChunkToGemini(openaiResp, &accumulatedText, &accumulatedThought)
			if geminiChunk != nil {
				chunkData, _ := json.Marshal(geminiChunk)
				c.Writer.WriteString("data: " + string(chunkData) + "\n\n")
				flusher.Flush()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[relay] gemini stream read error: %v", err)
		return err
	}

	return nil
}

// convertOpenAIStreamChunkToGemini converts an OpenAI stream chunk to Gemini format.
func (a *GeminiToOpenAIAdaptor) convertOpenAIStreamChunkToGemini(openaiResp map[string]any, accumulatedText, accumulatedThought *string) map[string]any {
	choices, ok := openaiResp["choices"].([]any)
	if !ok || len(choices) == 0 {
		// Check for usage in final chunk
		if usage, ok := openaiResp["usage"].(map[string]any); ok {
			return map[string]any{
				"usageMetadata": map[string]any{
					"promptTokenCount":     usage["prompt_tokens"],
					"candidatesTokenCount": usage["completion_tokens"],
					"totalTokenCount":      usage["total_tokens"],
				},
			}
		}
		return nil
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil
	}

	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return nil
	}

	parts := make([]map[string]any, 0)

	// Handle reasoning content (thinking)
	if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
		*accumulatedThought += reasoning
		parts = append(parts, map[string]any{
			"thought": true,
			"text":    *accumulatedThought,
		})
	}

	// Handle regular content
	if content, ok := delta["content"].(string); ok && content != "" {
		*accumulatedText += content
		parts = append(parts, map[string]any{
			"text": *accumulatedText,
		})
	}

	// Handle tool calls
	if toolCalls, ok := delta["tool_calls"].([]any); ok && len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]any); ok {
				funcMap, _ := tcMap["function"].(map[string]any)
				name, _ := funcMap["name"].(string)
				args, _ := funcMap["arguments"].(string)

				argsMap := map[string]any{}
				if args != "" {
					_ = json.Unmarshal([]byte(args), &argsMap)
				}

				parts = append(parts, map[string]any{
					"functionCall": map[string]any{
						"name": name,
						"args": argsMap,
					},
				})
			}
		}
	}

	if len(parts) == 0 {
		return nil
	}

	// Check finish reason
	finishReason := ""
	if fr, ok := choice["finish_reason"].(string); ok {
		switch fr {
		case "stop":
			finishReason = "STOP"
		case "length":
			finishReason = "MAX_TOKENS"
		case "tool_calls":
			finishReason = "STOP"
		}
	}

	result := map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"parts": parts,
					"role":  "model",
				},
			},
		},
	}

	if finishReason != "" {
		result["candidates"].([]map[string]any)[0]["finishReason"] = finishReason
	}

	return result
}

// sendGeminiStreamEnd sends the final message_delta and message_stop events.
//nolint:errcheck,unused
func (a *GeminiToOpenAIAdaptor) sendGeminiStreamEnd(c *gin.Context, state *GeminiStreamState) {
	if state.SentMessageStop {
		return
	}

	a.stopGeminiOpenBlocks(c, state)

	stopReason := "end_turn"
	if state.HasToolUse {
		stopReason = "tool_use"
	}
	if state.FinishReason != "" {
		stopReason = state.FinishReason
	}

	deltaData, _ := json.Marshal(MessageDeltaEvent{
		Type: "message_delta",
		Usage: MessageDeltaUsage{
			InputTokens:  state.InputTokens,
			OutputTokens: state.OutputTokens,
		},
		Delta: MessageDeltaContent{
			StopReason: stopReason,
		},
	})
	c.Writer.WriteString("event: message_delta\n")
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
	logClientSSE("message_delta", deltaData)

	c.Writer.WriteString("event: message_stop\n")
	c.Writer.WriteString("data: {\"type\":\"message_stop\"}\n\n")
	logClientSSE("message_stop", []byte(`{"type":"message_stop"}`))

	state.SentMessageStop = true
}

// stopGeminiOpenBlocks closes any open content blocks.
//nolint:errcheck,unused
func (a *GeminiToOpenAIAdaptor) stopGeminiOpenBlocks(c *gin.Context, state *GeminiStreamState) {
	switch state.LastMessageType {
	case LastMessageTypeThinking, LastMessageTypeText:
		c.Writer.WriteString("event: content_block_stop\n")
		c.Writer.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", state.Index))
	case LastMessageTypeTools:
		for i := state.ToolCallBaseIndex; i <= state.ToolCallBaseIndex+state.ToolCallMaxOffset; i++ {
			c.Writer.WriteString("event: content_block_stop\n")
			c.Writer.WriteString(fmt.Sprintf("data: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i))
		}
	}
}

// stopGeminiOpenBlocksAndAdvance closes blocks and advances the index.
//nolint:unused
func (a *GeminiToOpenAIAdaptor) stopGeminiOpenBlocksAndAdvance(c *gin.Context, state *GeminiStreamState) {
	if state.LastMessageType == LastMessageTypeNone {
		return
	}

	a.stopGeminiOpenBlocks(c, state)

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

// geminiSseToClaude converts a single Gemini SSE chunk to Claude-format events.
// Based on new-api streamResponseGeminiChat2OpenAI + cc-switch streaming_gemini.rs.
// Gemini's streamGenerateContent delivers cumulative snapshots of content.parts,
// so we need to compute deltas from accumulated state.
//nolint:errcheck,unused
func (a *GeminiToOpenAIAdaptor) geminiSseToClaude(c *gin.Context, geminiResp map[string]any, state *GeminiStreamState) {
	// Extract metadata
	if id, ok := geminiResp["responseId"].(string); ok && state.MessageID == "" {
		state.MessageID = id
	}
	if model, ok := geminiResp["modelVersion"].(string); ok && state.Model == "" {
		state.Model = model
	}

	// Usage metadata
	if usage, ok := geminiResp["usageMetadata"].(map[string]any); ok {
		if pt, ok := usage["promptTokenCount"].(float64); ok {
			state.InputTokens = int(pt)
		}
		if tt, ok := usage["totalTokenCount"].(float64); ok {
			pt := float64(state.InputTokens)
			state.OutputTokens = int(tt - pt)
			if state.OutputTokens < 0 {
				state.OutputTokens = 0
			}
		}
		if ct, ok := usage["candidatesTokenCount"].(float64); ok {
			if thoughts, ok := usage["thoughtsTokenCount"].(float64); ok {
				state.OutputTokens = int(ct + thoughts)
			}
		}
	}

	// Send message_start if not yet sent
	if !state.SentMessageStart {
		state.SentMessageStart = true
		msg, _ := json.Marshal(MessageStartEvent{
			Type: "message_start",
			Message: MessageData{
				Type:  "message",
				Role:  "assistant",
				ID:    state.MessageID,
				Model: state.Model,
				Content: []any{},
				Usage: MessageUsage{
					InputTokens:  state.InputTokens,
					OutputTokens: 0,
				},
			},
		})
		c.Writer.WriteString("event: message_start\n")
		c.Writer.WriteString("data: " + string(msg) + "\n\n")
		logClientSSE("message_start", msg)
	}

	// Check for prompt feedback block
	if pf, ok := geminiResp["promptFeedback"].(map[string]any); ok {
		if br, ok := pf["blockReason"].(string); ok && br != "" {
			a.emitGeminiTextDelta(c, fmt.Sprintf("Request blocked: %s", br), state)
			state.FinishReason = "end_turn"
			return
		}
	}

	// Process candidates
	candidates, ok := geminiResp["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return
	}

	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		return
	}

	// Extract finish reason
	if fr, ok := candidate["finishReason"].(string); ok {
		state.FinishReason = GeminiFinishReasonToClaude(fr)
		if fr == "STOP" {
			state.IsStop = true
		}
	}

	content, _ := candidate["content"].(map[string]any)
	parts, _ := content["parts"].([]any)

	var visibleTexts []string
	var toolCalls []map[string]any
	isThought := false

	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}

		// Function call
		if fc, ok := part["functionCall"].(map[string]any); ok {
			name, _ := fc["name"].(string)
			args, _ := fc["args"].(map[string]any)
			argsJSON, _ := json.Marshal(args)
			toolCalls = append(toolCalls, map[string]any{
				"id":   fmt.Sprintf("call_%s", name),
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": string(argsJSON),
				},
			})
			continue
		}

		// Thought part
		if thought, ok := part["thought"].(bool); ok && thought {
			isThought = true
			if t, ok := part["text"].(string); ok && t != "" {
				visibleTexts = append(visibleTexts, t)
			}
			continue
		}

		// Regular text (skip newlines)
		if t, ok := part["text"].(string); ok && t != "\n" {
			visibleTexts = append(visibleTexts, t)
		}
	}

	// Compute delta from cumulative snapshot
	visibleText := strings.Join(visibleTexts, "")
	if isThought && visibleText != "" {
		// Emit as thinking/reasoning content
		if state.AccumulatedThought == "" || !strings.HasPrefix(visibleText, state.AccumulatedThought) {
			// Not cumulative, emit full text
			a.emitGeminiThinkingDelta(c, visibleText, state)
			state.AccumulatedThought = visibleText
		} else {
			// Cumulative, emit delta
			delta := visibleText[len(state.AccumulatedThought):]
			if delta != "" {
				a.emitGeminiThinkingDelta(c, delta, state)
				state.AccumulatedThought = visibleText
			}
		}
	} else if visibleText != "" {
		// Compute text delta
		if state.AccumulatedText == "" || !strings.HasPrefix(visibleText, state.AccumulatedText) {
			a.emitGeminiTextDelta(c, visibleText, state)
			state.AccumulatedText = visibleText
		} else {
			delta := visibleText[len(state.AccumulatedText):]
			if delta != "" {
				a.emitGeminiTextDelta(c, delta, state)
				state.AccumulatedText = visibleText
			}
		}
	}

	// Handle tool calls
	if len(toolCalls) > 0 {
		a.handleGeminiToolCalls(c, toolCalls, state)
	}
}

// emitGeminiThinkingDelta emits a thinking content block delta.
//nolint:errcheck,unused
func (a *GeminiToOpenAIAdaptor) emitGeminiThinkingDelta(c *gin.Context, content string, state *GeminiStreamState) {
	if state.LastMessageType != LastMessageTypeThinking {
		a.stopGeminiOpenBlocksAndAdvance(c, state)

		blockData, _ := json.Marshal(ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: state.Index,
			ContentBlock: ThinkingContentBlock{
				Type:     "thinking",
				Thinking: "",
			},
		})
		c.Writer.WriteString("event: content_block_start\n")
		c.Writer.WriteString("data: " + string(blockData) + "\n\n")
		logClientSSE("content_block_start", blockData)

		state.LastMessageType = LastMessageTypeThinking
	}

	deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: state.Index,
		Delta: ThinkingDelta{
			Type:     "thinking_delta",
			Thinking: content,
		},
	})
	c.Writer.WriteString("event: content_block_delta\n")
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
	logClientSSE("content_block_delta", deltaData)
}

// emitGeminiTextDelta emits a text content block delta.
//nolint:errcheck,unused
func (a *GeminiToOpenAIAdaptor) emitGeminiTextDelta(c *gin.Context, text string, state *GeminiStreamState) {
	if state.LastMessageType == LastMessageTypeThinking {
		a.stopGeminiOpenBlocksAndAdvance(c, state)
	}

	if state.LastMessageType != LastMessageTypeText {
		blockData, _ := json.Marshal(ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: state.Index,
			ContentBlock: TextContentBlock{
				Type: "text",
				Text: "",
			},
		})
		c.Writer.WriteString("event: content_block_start\n")
		c.Writer.WriteString("data: " + string(blockData) + "\n\n")
		logClientSSE("content_block_start", blockData)

		state.LastMessageType = LastMessageTypeText
	}

	deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: state.Index,
		Delta: TextDelta{
			Type: "text_delta",
			Text: text,
		},
	})
	c.Writer.WriteString("event: content_block_delta\n")
	c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
	logClientSSE("content_block_delta", deltaData)
}

// handleGeminiToolCalls handles function call parts from Gemini stream.
//nolint:errcheck,unused
func (a *GeminiToOpenAIAdaptor) handleGeminiToolCalls(c *gin.Context, toolCalls []map[string]any, state *GeminiStreamState) {
	state.HasToolUse = true

	if state.LastMessageType != LastMessageTypeTools {
		a.stopGeminiOpenBlocksAndAdvance(c, state)
		state.ToolCallBaseIndex = state.Index
		state.ToolCallMaxOffset = 0
		state.LastMessageType = LastMessageTypeTools
	}

	for i, tc := range toolCalls {
		offset := i
		if offset > state.ToolCallMaxOffset {
			state.ToolCallMaxOffset = offset
		}

		blockIndex := state.ToolCallBaseIndex + offset

		toolID, _ := tc["id"].(string)
		funcMap, _ := tc["function"].(map[string]any)
		funcName, _ := funcMap["name"].(string)
		funcArgs, _ := funcMap["arguments"].(string)

		// Check if this tool block was already started
		if _, exists := state.ToolBlocks[blockIndex]; !exists {
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
			c.Writer.WriteString("event: content_block_start\n")
			c.Writer.WriteString("data: " + string(blockData) + "\n\n")
			logClientSSE("content_block_start", blockData)

			state.ToolBlocks[blockIndex] = &ToolBlockState{
				Index:   blockIndex,
				ID:      toolID,
				Name:    funcName,
				Started: true,
			}
		}

		if funcArgs != "" {
			deltaData, _ := json.Marshal(ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: blockIndex,
				Delta: InputJSONDelta{
					Type:        "input_json_delta",
					PartialJSON: funcArgs,
				},
			})
			c.Writer.WriteString("event: content_block_delta\n")
			c.Writer.WriteString("data: " + string(deltaData) + "\n\n")
			logClientSSE("content_block_delta", deltaData)
		}
	}

	state.Index = state.ToolCallBaseIndex + state.ToolCallMaxOffset
}

func (a *GeminiToOpenAIAdaptor) GetModelList() []string {
	return []string{}
}

func (a *GeminiToOpenAIAdaptor) GetChannelName() string {
	return "gemini_to_openai"
}

func truncateStr(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
