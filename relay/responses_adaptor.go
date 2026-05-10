package relay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llm-proxy/channel"
	"github.com/llm-proxy/dto"
	"github.com/llm-proxy/proxy"
)

// ResponsesAdaptor handles requests from clients speaking the OpenAI Responses API,
// converting them to Chat Completions format for upstream, and converting responses back.
type ResponsesAdaptor struct{}

func (a *ResponsesAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	baseURL := info.BaseURL
	if strings.HasSuffix(baseURL, "/v1") {
		return fmt.Sprintf("%s/chat/completions", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/chat/completions", baseURL), nil
}

func (a *ResponsesAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *ResponsesAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	// Parse client's Responses-format request
	var responsesReq dto.OpenAIResponsesRequest
	if err := json.Unmarshal(requestBody, &responsesReq); err != nil {
		return nil, fmt.Errorf("failed to parse responses request: %w", err)
	}

	info.OriginModel = responsesReq.Model
	info.UpstreamModel = channel.ResolveModelAlias(responsesReq.Model)

	// Convert Responses → Chat Completions
	chatReq, err := ResponsesRequestToChatRequest(&responsesReq)
	if err != nil {
		return nil, fmt.Errorf("failed to convert responses to chat: %w", err)
	}
	chatReq.Model = info.UpstreamModel

	// Pre-count input tokens
	info.PreCountTokens = countChatRequestTokens(chatReq)

	return chatReq, nil
}

func (a *ResponsesAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
	url, _ := a.GetRequestURL(info)
	httpReq, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, err
	}
	_ = a.SetupRequestHeader(&httpReq.Header, info)

	client := proxy.GetClientWithTimeout(getRequestTimeout())
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (a *ResponsesAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if info.IsStream {
		return string(body), nil
	}

	// Parse upstream Chat Completions response
	var chatResp map[string]any
	if err := json.Unmarshal(body, &chatResp); err != nil {
		var raw any
		if json.Unmarshal(body, &raw) == nil {
			return raw, nil
		}
		return nil, fmt.Errorf("failed to parse upstream response: %w", err)
	}

	// Check for upstream error
	if errObj, ok := chatResp["error"]; ok {
		errMsg := fmt.Sprintf("%v", errObj)
		return map[string]any{"error": map[string]any{"message": errMsg, "type": "upstream_error"}}, nil
	}

	// Convert Chat Completions response → Responses format
	responsesResp, err := ChatResponseToResponsesResponse(chatResp, info.OriginModel)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat to responses: %w", err)
	}

	// Extract usage for stats
	if responsesResp.Usage != nil {
		info.InputTokens = responsesResp.Usage.InputTokens
		info.OutputTokens = responsesResp.Usage.OutputTokens
	}

	return responsesResp, nil
}

// streamChatToResponses reads upstream Chat Completions SSE events and writes
// OpenAI Responses API SSE events back to the client.
func (a *ResponsesAdaptor) streamChatToResponses(c *gin.Context, httpResp *http.Response, info *RelayInfo) error {
	defer httpResp.Body.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	outputItemID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	var outputText strings.Builder
	var usageText strings.Builder
	sentCreated := false
	sentOutputItemAdded := false
	sentStop := false
	sawToolCall := false
	var model string
	createdAt := int(time.Now().Unix())

	// Tool call tracking
	toolCallIndexByID := make(map[string]int)
	toolCallNameByID := make(map[string]string)
	toolCallArgsByID := make(map[string]string)
	toolCallItemIDByID := make(map[string]string)

	sendEvent := func(eventType string, data map[string]any) bool {
		if data == nil {
			data = map[string]any{}
		}
		data["type"] = eventType
		b, err := json.Marshal(data)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", b); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}

	sendResponseCreated := func() bool {
		if sentCreated {
			return true
		}
		sentCreated = true
		return sendEvent("response.created", map[string]any{
			"response": map[string]any{
				"id":         responseID,
				"object":     "response",
				"created_at": createdAt,
				"status":     "in_progress",
				"model":      info.OriginModel,
				"output":     []any{},
			},
		})
	}

	sendOutputItemAdded := func() bool {
		if sentOutputItemAdded {
			return true
		}
		if !sendResponseCreated() {
			return false
		}
		sentOutputItemAdded = true
		return sendEvent("response.output_item.added", map[string]any{
			"output_index": 0,
			"item": map[string]any{
				"type":   "message",
				"id":     outputItemID,
				"status": "in_progress",
				"role":   "assistant",
				"content": []any{},
			},
		})
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Extract model from chunk
		if m, ok := chunk["model"].(string); ok && m != "" {
			model = m
			info.UpstreamModel = m
		}

		// Process choices
		choices, ok := chunk["choices"].([]any)
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			continue
		}

		delta, _ := choice["delta"].(map[string]any)
		finishReason, _ := choice["finish_reason"].(string)

		if delta != nil {
			// Content delta
			if content, ok := delta["content"].(string); ok && content != "" {
				if !sendOutputItemAdded() {
					return nil
				}
				outputText.WriteString(content)
				usageText.WriteString(content)
				sendEvent("response.output_text.delta", map[string]any{
					"output_index":  0,
					"content_index": 0,
					"item_id":       outputItemID,
					"delta":         content,
				})
			}

			// Reasoning content delta
			if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
				if !sendResponseCreated() {
					return nil
				}
				usageText.WriteString(rc)
				sendEvent("response.reasoning_summary_text.delta", map[string]any{
					"delta": rc,
				})
			}

			// Tool calls delta
			if toolCallsRaw, ok := delta["tool_calls"].([]any); ok {
				for _, tc := range toolCallsRaw {
					tcMap, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					callID, _ := tcMap["id"].(string)
					idx, _ := tcMap["index"].(float64)
					fn, _ := tcMap["function"].(map[string]any)

					if callID == "" {
						// Try to find by index
						for cid, storedIdx := range toolCallIndexByID {
							if storedIdx == int(idx) {
								callID = cid
								break
							}
						}
					}

					if callID != "" {
						if _, exists := toolCallIndexByID[callID]; !exists {
							toolCallIndexByID[callID] = int(idx)
							itemID := fmt.Sprintf("fc_%s_%d", callID, time.Now().UnixNano())
							toolCallItemIDByID[callID] = itemID
						}
					}

					if fn != nil {
						if name, ok := fn["name"].(string); ok && name != "" && callID != "" {
							toolCallNameByID[callID] = name
						}
						if args, ok := fn["arguments"].(string); ok && args != "" && callID != "" {
							toolCallArgsByID[callID] += args
						}
					}

					if callID != "" {
						if !sendResponseCreated() {
							return nil
						}
						itemID := toolCallItemIDByID[callID]
						name := toolCallNameByID[callID]
						argsDelta := ""
						if fn != nil {
							if a, ok := fn["arguments"].(string); ok {
								argsDelta = a
							}
						}
						// Send output_item.added for new tool calls
						if name != "" && !sawToolCall {
							sawToolCall = true
							if !sentOutputItemAdded {
								// Send the text message output item first, then close it
								sendOutputItemAdded()
								// Send output_item.done for the text message
								sendEvent("response.output_item.done", map[string]any{
									"output_index": 0,
									"item": map[string]any{
										"type":   "message",
										"id":     outputItemID,
										"status": "completed",
										"role":   "assistant",
										"content": []any{
											map[string]any{
												"type": "output_text",
												"text": outputText.String(),
											},
										},
									},
								})
							}
							sendEvent("response.output_item.added", map[string]any{
								"output_index": len(toolCallIndexByID),
								"item": map[string]any{
									"type":      "function_call",
									"id":        itemID,
									"call_id":   callID,
									"name":      name,
									"arguments": "",
									"status":    "in_progress",
								},
							})
						}
						if argsDelta != "" {
							sendEvent("response.function_call_arguments.delta", map[string]any{
								"output_index":  len(toolCallIndexByID),
								"content_index": 0,
								"item_id":       itemID,
								"delta":         argsDelta,
							})
						}
					}
				}
			}
		}

		// Handle finish_reason
		if finishReason != "" {
			// Extract usage from chunk if available
			if usageRaw, ok := chunk["usage"].(map[string]any); ok {
				if pt, ok := usageRaw["prompt_tokens"].(float64); ok {
					info.InputTokens = int(pt)
				}
				if ct, ok := usageRaw["completion_tokens"].(float64); ok {
					info.OutputTokens = int(ct)
				}
			}
		}
	}

	// Send output_text.done for the text content
	if !sawToolCall && sentOutputItemAdded {
		sendEvent("response.output_text.done", map[string]any{
			"output_index":  0,
			"content_index": 0,
			"item_id":       outputItemID,
			"text":          outputText.String(),
		})
	}

	// Close tool call items
	for callID, itemID := range toolCallItemIDByID {
		name := toolCallNameByID[callID]
		args := toolCallArgsByID[callID]
		sendEvent("response.output_item.done", map[string]any{
			"output_index": toolCallIndexByID[callID] + 1,
			"item": map[string]any{
				"type":      "function_call",
				"id":        itemID,
				"call_id":   callID,
				"name":      name,
				"arguments": args,
				"status":    "completed",
			},
		})
	}

	// Send output_item.done for text message if not yet sent
	if !sawToolCall && !sentStop {
		if sentOutputItemAdded {
			sendEvent("response.output_item.done", map[string]any{
				"output_index": 0,
				"item": map[string]any{
					"type":   "message",
					"id":     outputItemID,
					"status": "completed",
					"role":   "assistant",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": outputText.String(),
						},
					},
				},
			})
		}
	}

	// Fallback: send response.created if we never sent it
	if !sentCreated {
		sendResponseCreated()
	}

	// Estimate usage
	if info.InputTokens == 0 && info.PreCountTokens > 0 {
		info.InputTokens = info.PreCountTokens
	}
	if info.OutputTokens == 0 && usageText.Len() > 0 {
		info.OutputTokens = len(usageText.String()) / 4
	}

	// Send response.completed
	totalTokens := info.InputTokens + info.OutputTokens
	sendEvent("response.completed", map[string]any{
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": createdAt,
			"status":     "completed",
			"model": func() string {
				if model != "" {
					return model
				}
				return info.OriginModel
			}(),
			"output": func() []any {
				if sawToolCall {
					var items []any
					for callID, itemID := range toolCallItemIDByID {
						items = append(items, map[string]any{
							"type":      "function_call",
							"id":        itemID,
							"call_id":   callID,
							"name":      toolCallNameByID[callID],
							"arguments": toolCallArgsByID[callID],
						})
					}
					return items
				}
				return []any{
					map[string]any{
						"type":   "message",
						"id":     outputItemID,
						"status": "completed",
						"role":   "assistant",
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": outputText.String(),
							},
						},
					},
				}
			}(),
			"usage": map[string]any{
				"input_tokens":  info.InputTokens,
				"output_tokens": info.OutputTokens,
				"total_tokens":  totalTokens,
			},
		},
	})

	sentStop = true

	// Final [DONE]
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()

	return nil
}

func (a *ResponsesAdaptor) GetModelList() []string {
	return []string{}
}

func (a *ResponsesAdaptor) GetChannelName() string {
	return "openai-responses"
}

// countChatRequestTokens provides a rough token count for a chat request.
func countChatRequestTokens(req *dto.OpenAIChatRequest) int {
	count := 0
	for _, msg := range req.Messages {
		count += 4 // role overhead
		if s, ok := msg.Content.(string); ok {
			count += len(s) / 4
		}
	}
	return count
}

// ResponsesPassthroughAdaptor forwards Responses API requests directly to upstream
// without any protocol conversion. Used when upstream supports Responses natively.
type ResponsesPassthroughAdaptor struct{}

func (a *ResponsesPassthroughAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	baseURL := info.BaseURL
	if strings.HasSuffix(baseURL, "/v1") {
		return fmt.Sprintf("%s/responses", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/responses", baseURL), nil
}

func (a *ResponsesPassthroughAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *ResponsesPassthroughAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	// Parse Responses request and only replace model with alias-resolved value
	var req dto.OpenAIResponsesRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return nil, fmt.Errorf("failed to parse responses request: %w", err)
	}
	info.OriginModel = req.Model
	info.UpstreamModel = channel.ResolveModelAlias(req.Model)
	req.Model = info.UpstreamModel
	return req, nil
}

func (a *ResponsesPassthroughAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
	url, _ := a.GetRequestURL(info)
	httpReq, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, err
	}
	_ = a.SetupRequestHeader(&httpReq.Header, info)
	client := proxy.GetClientWithTimeout(getRequestTimeout())
	return client.Do(httpReq)
}

func (a *ResponsesPassthroughAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if info.IsStream {
		return string(body), nil
	}

	// Passthrough: parse and return the Responses response as-is
	var result any
	if json.Unmarshal(body, &result) != nil {
		return string(body), nil
	}
	return result, nil
}

func (a *ResponsesPassthroughAdaptor) GetModelList() []string {
	return []string{}
}

func (a *ResponsesPassthroughAdaptor) GetChannelName() string {
	return "openai-responses-passthrough"
}

// streamPassthrough forwards upstream Responses SSE events directly to the client.
func (a *ResponsesPassthroughAdaptor) streamPassthrough(c *gin.Context, httpResp *http.Response, info *RelayInfo) error {
	defer httpResp.Body.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if _, err := fmt.Fprintf(c.Writer, "%s\n", line); err != nil {
			return err
		}
		if line == "" {
			c.Writer.Flush()
		}

		// Extract usage from response.completed events
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data != "[DONE]" {
				var event dto.ResponsesStreamEvent
				if json.Unmarshal([]byte(data), &event) == nil {
					if event.Type == "response.completed" && event.Response != nil {
						if event.Response.Usage != nil {
							info.InputTokens = event.Response.Usage.InputTokens
							info.OutputTokens = event.Response.Usage.OutputTokens
						}
					}
				}
			}
		}
	}
	c.Writer.Flush()
	return nil
}
