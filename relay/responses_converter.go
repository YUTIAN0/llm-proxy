package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/llm-proxy/dto"
)

// ResponsesRequestToChatRequest converts an OpenAI Responses API request to Chat Completions format.
// This is used when a client sends a Responses-format request but the upstream only supports Chat Completions.
func ResponsesRequestToChatRequest(req *dto.OpenAIResponsesRequest) (*dto.OpenAIChatRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	chatReq := &dto.OpenAIChatRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	// Parse input: can be a string or an array of items
	var inputItems []map[string]any
	if len(req.Input) > 0 {
		// Try as string first
		var inputStr string
		if err := json.Unmarshal(req.Input, &inputStr); err == nil {
			chatReq.Messages = []dto.OpenAIMessage{
				{Role: "user", Content: inputStr},
			}
		} else if err := json.Unmarshal(req.Input, &inputItems); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}
	}

	// Parse instructions as system message
	var instructions string
	if len(req.Instructions) > 0 {
		_ = json.Unmarshal(req.Instructions, &instructions)
	}
	if instructions != "" {
		chatReq.Messages = append([]dto.OpenAIMessage{
			{Role: "system", Content: instructions},
		}, chatReq.Messages...)
	}

	// Convert input items to messages
	for _, item := range inputItems {
		itemType, _ := item["type"].(string)

		// function_call_output → tool message
		if itemType == "function_call_output" {
			callID, _ := item["call_id"].(string)
			output, _ := item["output"].(string)
			chatReq.Messages = append(chatReq.Messages, dto.OpenAIMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    output,
			})
			continue
		}

		// function_call → will be added as tool_calls on the assistant message
		if itemType == "function_call" {
			callID, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			var args string
			if a, ok := item["arguments"].(string); ok {
				args = a
			} else if item["arguments"] != nil {
				b, _ := json.Marshal(item["arguments"])
				args = string(b)
			}
			// Attach to last assistant message or create one
			lastIdx := len(chatReq.Messages) - 1
			if lastIdx >= 0 && chatReq.Messages[lastIdx].Role == "assistant" {
				chatReq.Messages[lastIdx].ToolCalls = append(chatReq.Messages[lastIdx].ToolCalls, dto.ToolCall{
					ID:   callID,
					Type: "function",
					Function: dto.FunctionResponse{
						Name:      name,
						Arguments: args,
					},
				})
			} else {
				chatReq.Messages = append(chatReq.Messages, dto.OpenAIMessage{
					Role: "assistant",
					ToolCalls: []dto.ToolCall{
						{
							ID:   callID,
							Type: "function",
							Function: dto.FunctionResponse{
								Name:      name,
								Arguments: args,
							},
						},
					},
				})
			}
			continue
		}

		// Regular message with role
		role, _ := item["role"].(string)
		if role == "" {
			role = "user"
		}

		// Handle content: string or array of content parts
		var content any
		if contentRaw, ok := item["content"]; ok {
			switch c := contentRaw.(type) {
			case string:
				content = c
			case []any:
				// Check if it's simple text content parts
				var texts []string
				for _, part := range c {
					if m, ok := part.(map[string]any); ok {
						textType, _ := m["type"].(string)
						if textType == "input_text" || textType == "output_text" || textType == "text" {
							if t, ok := m["text"].(string); ok && t != "" {
								texts = append(texts, t)
							}
						} else if textType == "input_image" {
							// Convert to OpenAI image_url format
							if imageURL, ok := m["image_url"].(string); ok {
								texts = append(texts, "") // placeholder
								// We'll handle multimodal separately if needed
								_ = imageURL
							}
						}
					}
				}
				if len(texts) > 0 {
					content = strings.Join(texts, "\n")
				} else {
					content = fmt.Sprintf("%v", contentRaw)
				}
			default:
				content = fmt.Sprintf("%v", contentRaw)
			}
		} else {
			content = ""
		}

		chatReq.Messages = append(chatReq.Messages, dto.OpenAIMessage{
			Role:    role,
			Content: content,
		})
	}

	// If no messages were created, add a default empty user message
	if len(chatReq.Messages) == 0 {
		chatReq.Messages = []dto.OpenAIMessage{
			{Role: "user", Content: ""},
		}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		var tools []map[string]any
		if err := json.Unmarshal(req.Tools, &tools); err == nil {
			for _, tool := range tools {
				toolType, _ := tool["type"].(string)
				if toolType == "" {
					toolType = "function"
				}
				if toolType == "function" {
					name, _ := tool["name"].(string)
					desc, _ := tool["description"].(string)
					ot := dto.OpenAITool{
						Type: "function",
						Function: &dto.FunctionRequest{
							Name:        name,
							Description: desc,
							Parameters:  tool["parameters"],
						},
					}
					chatReq.Tools = append(chatReq.Tools, ot)
				}
			}
		}
	}

	// Convert tool_choice
	if len(req.ToolChoice) > 0 {
		var tc any
		if err := json.Unmarshal(req.ToolChoice, &tc); err == nil {
			switch v := tc.(type) {
			case string:
				chatReq.ToolChoice = v
			case map[string]any:
				// Responses: {"type":"function","name":"..."} → Chat: {"type":"function","function":{"name":"..."}}
				t, _ := v["type"].(string)
				if t == "function" {
					if name, ok := v["name"].(string); ok && name != "" {
						chatReq.ToolChoice = map[string]any{
							"type": "function",
							"function": map[string]any{
								"name": name,
							},
						}
					} else {
						chatReq.ToolChoice = v
					}
				} else {
					chatReq.ToolChoice = v
				}
			default:
				chatReq.ToolChoice = v
			}
		}
	}

	// max_output_tokens → max_tokens
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		chatReq.MaxTokens = req.MaxOutputTokens
	}

	// reasoning
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		chatReq.ReasoningEffort = req.Reasoning.Effort
	}

	return chatReq, nil
}

// ChatResponseToResponsesResponse converts an OpenAI Chat Completions response to Responses API format.
func ChatResponseToResponsesResponse(chatResp map[string]any, model string) (*dto.OpenAIResponsesResponse, error) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        fmt.Sprintf("resp_%d", time.Now().UnixNano()),
		Object:    "response",
		CreatedAt: int(time.Now().Unix()),
		Status:    "completed",
		Model:     model,
	}

	// Extract from chat response
	if id, ok := chatResp["id"].(string); ok {
		resp.ID = id
	}
	if m, ok := chatResp["model"].(string); ok && m != "" {
		resp.Model = m
	}
	if created, ok := chatResp["created"].(float64); ok {
		resp.CreatedAt = int(created)
	}

	// Extract choices
	if choices, ok := chatResp["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			msg, _ := choice["message"].(map[string]any)
			role, _ := msg["role"].(string)
			if role == "" {
				role = "assistant"
			}

			content, _ := msg["content"].(string)
			output := dto.ResponsesOutput{
				Type: "message",
				ID:   fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				Role: role,
				Status: "completed",
			}

			// Check for tool_calls
			if toolCallsRaw, ok := msg["tool_calls"].([]any); ok && len(toolCallsRaw) > 0 {
				for _, tc := range toolCallsRaw {
					if tcMap, ok := tc.(map[string]any); ok {
						callID, _ := tcMap["id"].(string)
						if callID == "" {
							callID = fmt.Sprintf("call_%d", time.Now().UnixNano())
						}
						fn, _ := tcMap["function"].(map[string]any)
						name, _ := fn["name"].(string)
						args, _ := fn["arguments"].(string)

						var argsRaw json.RawMessage
						if args != "" {
							argsRaw = json.RawMessage(args)
						}
						resp.Output = append(resp.Output, dto.ResponsesOutput{
							Type:      "function_call",
							ID:        fmt.Sprintf("fc_%d", time.Now().UnixNano()),
							CallID:    callID,
							Name:      name,
							Arguments: argsRaw,
						})
					}
				}
			} else if content != "" {
				output.Content = []dto.ResponsesOutputContent{
					{Type: "output_text", Text: content},
				}
				resp.Output = append(resp.Output, output)
			} else {
				resp.Output = append(resp.Output, output)
			}
		}
	}

	// Usage
	if usageRaw, ok := chatResp["usage"].(map[string]any); ok {
		promptTokens, _ := usageRaw["prompt_tokens"].(float64)
		completionTokens, _ := usageRaw["completion_tokens"].(float64)
		totalTokens, _ := usageRaw["total_tokens"].(float64)
		resp.Usage = &dto.ResponsesUsage{
			InputTokens:  int(promptTokens),
			OutputTokens: int(completionTokens),
			TotalTokens:  int(totalTokens),
		}
		if resp.Usage.TotalTokens == 0 {
			resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
		}
	}

	return resp, nil
}

// ChatRequestToResponsesRequest converts an OpenAI Chat Completions request to Responses API format.
func ChatRequestToResponsesRequest(req *dto.OpenAIChatRequest) (*dto.OpenAIResponsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	var instructionsParts []string
	inputItems := make([]map[string]any, 0, len(req.Messages))

	for _, msg := range req.Messages {
		role := msg.Role

		// system/developer → instructions
		if role == "system" || role == "developer" {
			text := extractMsgText(msg.Content)
			if text != "" {
				instructionsParts = append(instructionsParts, text)
			}
			continue
		}

		// tool result → function_call_output
		if role == "tool" {
			output := extractMsgText(msg.Content)
			callID := msg.ToolCallID
			if callID == "" {
				inputItems = append(inputItems, map[string]any{
					"role":    "user",
					"content": output,
				})
				continue
			}
			inputItems = append(inputItems, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			})
			continue
		}

		// Build content
		text := extractMsgText(msg.Content)
		item := map[string]any{"role": role}
		if text != "" {
			item["content"] = text
		}
		inputItems = append(inputItems, item)

		// assistant tool_calls → function_call items
		if role == "assistant" {
			for _, tc := range msg.ToolCalls {
				name := tc.Function.Name
				if name == "" {
					continue
				}
				inputItems = append(inputItems, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      name,
					"arguments": tc.Function.Arguments,
				})
			}
		}
	}

	// Build input JSON
	inputRaw, err := json.Marshal(inputItems)
	if err != nil {
		return nil, err
	}

	// Build instructions JSON
	var instructionsRaw json.RawMessage
	if len(instructionsParts) > 0 {
		instructions := strings.Join(instructionsParts, "\n\n")
		instructionsRaw, _ = json.Marshal(instructions)
	}

	// Build tools JSON
	var toolsRaw json.RawMessage
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if tool.Function == nil {
				continue
			}
			t := map[string]any{
				"type":        "function",
				"name":        tool.Function.Name,
				"description": tool.Function.Description,
				"parameters":  tool.Function.Parameters,
			}
			tools = append(tools, t)
		}
		toolsRaw, _ = json.Marshal(tools)
	}

	// Build tool_choice JSON
	var toolChoiceRaw json.RawMessage
	if req.ToolChoice != nil {
		switch v := req.ToolChoice.(type) {
		case string:
			toolChoiceRaw, _ = json.Marshal(v)
		default:
			b, _ := json.Marshal(v)
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if t, _ := m["type"].(string); t == "function" {
					if fn, ok := m["function"].(map[string]any); ok {
						if name, _ := fn["name"].(string); name != "" {
							toolChoiceRaw, _ = json.Marshal(map[string]any{
								"type": "function",
								"name": name,
							})
						} else {
							toolChoiceRaw = b
						}
					} else {
						toolChoiceRaw = b
					}
				} else {
					toolChoiceRaw = b
				}
			} else {
				toolChoiceRaw = b
			}
		}
	}

	// max_output_tokens
	maxOutputTokens := 0
	if req.MaxTokens != nil && *req.MaxTokens > maxOutputTokens {
		maxOutputTokens = *req.MaxTokens
	}
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > maxOutputTokens {
		maxOutputTokens = *req.MaxCompletionTokens
	}

	out := &dto.OpenAIResponsesRequest{
		Model:           req.Model,
		Input:           inputRaw,
		Instructions:    instructionsRaw,
		Stream:          req.Stream,
		Temperature:     req.Temperature,
		ToolChoice:      toolChoiceRaw,
		Tools:           toolsRaw,
		TopP:            req.TopP,
	}
	if maxOutputTokens > 0 {
		out.MaxOutputTokens = &maxOutputTokens
	}
	if req.ReasoningEffort != "" {
		out.Reasoning = &dto.ResponsesReasoning{
			Effort:  req.ReasoningEffort,
			Summary: "detailed",
		}
	}

	return out, nil
}

// ResponsesResponseToChatResponse converts an OpenAI Responses API response to Chat Completions format.
func ResponsesResponseToChatResponse(resp *dto.OpenAIResponsesResponse, model string) map[string]any {
	text := extractOutputText(resp)

	// Extract tool calls
	var toolCalls []map[string]any
	if text == "" && len(resp.Output) > 0 {
		for _, out := range resp.Output {
			if out.Type != "function_call" {
				continue
			}
			name := strings.TrimSpace(out.Name)
			if name == "" {
				continue
			}
			callID := strings.TrimSpace(out.CallID)
			if callID == "" {
				callID = strings.TrimSpace(out.ID)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": out.ArgumentsString(),
				},
			})
		}
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	msg := map[string]any{
		"role":    "assistant",
		"content": text,
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		msg["content"] = nil
	}

	result := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": resp.CreatedAt,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason,
			},
		},
	}

	// Usage
	if resp.Usage != nil {
		usage := map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		}
		if resp.Usage.TotalTokens == 0 {
			usage["total_tokens"] = resp.Usage.InputTokens + resp.Usage.OutputTokens
		}
		result["usage"] = usage
	}

	return result
}

// extractOutputText extracts text from Responses API output items.
func extractOutputText(resp *dto.OpenAIResponsesResponse) string {
	if resp == nil || len(resp.Output) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, out := range resp.Output {
		if out.Type != "message" {
			continue
		}
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	if sb.Len() > 0 {
		return sb.String()
	}
	// Fallback: collect any text
	for _, out := range resp.Output {
		for _, c := range out.Content {
			if c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
	}
	return sb.String()
}

// extractMsgText extracts text from message content (string or array).
func extractMsgText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var texts []string
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					texts = append(texts, t)
				}
			}
		}
		return strings.Join(texts, "\n")
	default:
		if content != nil {
			return fmt.Sprintf("%v", content)
		}
	}
	return ""
}
