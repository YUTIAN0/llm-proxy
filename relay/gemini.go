package relay

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llm-proxy/dto"
)

// CovertOpenAI2Gemini converts an OpenAI chat completion request to Gemini format.
// Based on new-api relay/channel/gemini/relay-gemini.go CovertOpenAI2Gemini.
func CovertOpenAI2Gemini(openaiReq *dto.OpenAIChatRequest) (map[string]any, error) {
	geminiReq := map[string]any{
		"contents":         make([]map[string]any, 0),
		"generationConfig": map[string]any{},
	}

	genConfig := geminiReq["generationConfig"].(map[string]any)
	if openaiReq.Temperature != nil {
		genConfig["temperature"] = *openaiReq.Temperature
	}
	if openaiReq.TopP != nil && *openaiReq.TopP > 0 {
		genConfig["topP"] = *openaiReq.TopP
	}
	if openaiReq.MaxTokens != nil && *openaiReq.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = *openaiReq.MaxTokens
	}
	if openaiReq.MaxCompletionTokens != nil && *openaiReq.MaxCompletionTokens > 0 {
		genConfig["maxOutputTokens"] = *openaiReq.MaxCompletionTokens
	}
	if openaiReq.N != nil && *openaiReq.N > 1 {
		genConfig["candidateCount"] = *openaiReq.N
	}

	// Stop sequences
	if openaiReq.Stop != nil {
		stops := parseStopSequences(openaiReq.Stop)
		if len(stops) > 5 {
			stops = stops[:5]
		}
		if len(stops) > 0 {
			genConfig["stopSequences"] = stops
		}
	}

	// Safety settings - set all to BLOCK_NONE for maximum compatibility
	safetySettings := make([]map[string]any, 0)
	categories := []string{
		"HARM_CATEGORY_HARASSMENT",
		"HARM_CATEGORY_HATE_SPEECH",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT",
		"HARM_CATEGORY_DANGEROUS_CONTENT",
	}
	for _, cat := range categories {
		safetySettings = append(safetySettings, map[string]any{
			"category":  cat,
			"threshold": "BLOCK_NONE",
		})
	}
	geminiReq["safetySettings"] = safetySettings

	// Tools
	if len(openaiReq.Tools) > 0 {
		functions := make([]map[string]any, 0)
		for _, tool := range openaiReq.Tools {
			if tool.Function == nil {
				continue
			}
			fn := map[string]any{
				"name": tool.Function.Name,
			}
			if tool.Function.Description != "" {
				fn["description"] = tool.Function.Description
			}
			if tool.Function.Parameters != nil {
				// Clean parameters for Gemini compatibility
				cleaned := cleanFunctionParameters(tool.Function.Parameters)
				fn["parameters"] = cleaned
			}
			functions = append(functions, fn)
		}
		if len(functions) > 0 {
			geminiReq["tools"] = []map[string]any{
				{"functionDeclarations": functions},
			}
		}

		// tool_choice → toolConfig
		if openaiReq.ToolChoice != nil {
			geminiReq["toolConfig"] = convertToolChoiceToGeminiConfig(openaiReq.ToolChoice)
		}
	}

	// Messages → contents + systemInstruction
	var systemParts []string
	contents := make([]map[string]any, 0)

	for _, msg := range openaiReq.Messages {
		switch msg.Role {
		case "system", "developer":
			text := extractTextContent(msg.Content)
			if text != "" {
				systemParts = append(systemParts, text)
			}
		case "assistant":
			parts := convertOpenAIMessageToGeminiParts(msg, "model")
			if len(parts) > 0 {
				contents = append(contents, map[string]any{
					"role":  "model",
					"parts": parts,
				})
			}
		case "tool":
			// Tool result → user message with functionResponse
			name := msg.ToolCallID
			if name == "" {
				name = "unknown_tool"
			}
			name = strings.TrimPrefix(name, "call_")
			respContent := extractTextContent(msg.Content)
			// Try to parse as JSON object
			var respObj map[string]any
			if err := json.Unmarshal([]byte(respContent), &respObj); err != nil {
				respObj = map[string]any{"result": respContent}
			}
			contents = append(contents, map[string]any{
				"role": "user",
				"parts": []map[string]any{
					{
						"functionResponse": map[string]any{
							"name":     name,
							"response": respObj,
						},
					},
				},
			})
		default: // user
			parts := convertOpenAIMessageToGeminiParts(msg, "user")
			if len(parts) > 0 {
				contents = append(contents, map[string]any{
					"role":  "user",
					"parts": parts,
				})
			}
		}
	}

	geminiReq["contents"] = contents

	if len(systemParts) > 0 {
		geminiReq["systemInstruction"] = map[string]any{
			"parts": []map[string]any{
				{"text": strings.Join(systemParts, "\n")},
			},
		}
	}

	return geminiReq, nil
}

// convertOpenAIMessageToGeminiParts converts an OpenAI message to Gemini parts.
func convertOpenAIMessageToGeminiParts(msg dto.OpenAIMessage, role string) []map[string]any {
	parts := make([]map[string]any, 0)

	// Handle tool_calls (assistant message with function calls)
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			args := map[string]any{}
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			parts = append(parts, map[string]any{
				"functionCall": map[string]any{
					"name": tc.Function.Name,
					"args": args,
				},
			})
		}
	}

	// Handle content
	text := extractTextContent(msg.Content)
	if text != "" {
		parts = append(parts, map[string]any{"text": text})
	}

	return parts
}

// extractTextContent extracts text from OpenAI message content (string or array).
func extractTextContent(content any) string {
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

// convertToolChoiceToGeminiConfig converts OpenAI tool_choice to Gemini toolConfig.
func convertToolChoiceToGeminiConfig(toolChoice any) map[string]any {
	config := map[string]any{
		"functionCallingConfig": map[string]any{},
	}
	fcc := config["functionCallingConfig"].(map[string]any)

	switch tc := toolChoice.(type) {
	case string:
		switch tc {
		case "auto":
			fcc["mode"] = "AUTO"
		case "none":
			fcc["mode"] = "NONE"
		case "required":
			fcc["mode"] = "ANY"
		default:
			fcc["mode"] = "AUTO"
		}
	case map[string]any:
		if tcType, ok := tc["type"].(string); ok && tcType == "function" {
			fcc["mode"] = "ANY"
			if fn, ok := tc["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					fcc["allowedFunctionNames"] = []string{name}
				}
			}
		}
	}

	return config
}

// parseStopSequences parses stop parameter (string or array) into a string slice.
func parseStopSequences(stop any) []string {
	if stop == nil {
		return nil
	}
	switch v := stop.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []any:
		seqs := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				seqs = append(seqs, s)
			}
		}
		return seqs
	}
	return nil
}

// cleanFunctionParameters recursively removes unsupported fields from Gemini function parameters.
func cleanFunctionParameters(params any) any {
	if params == nil {
		return nil
	}

	allowedFields := map[string]bool{
		"type": true, "description": true, "properties": true,
		"required": true, "items": true, "enum": true,
		"anyOf": true, "nullable": true, "format": true,
		"pattern": true, "minimum": true, "maximum": true,
		"minLength": true, "maxLength": true,
		"minItems": true, "maxItems": true,
		"minProperties": true, "maxProperties": true,
		"default": true, "title": true,
	}

	switch v := params.(type) {
	case map[string]any:
		cleaned := make(map[string]any)
		for k, val := range v {
			if allowedFields[k] {
				cleaned[k] = cleanFunctionParameters(val)
			}
		}
		// Normalize type to uppercase for Gemini
		if t, ok := cleaned["type"].(string); ok {
			switch strings.ToLower(t) {
			case "object":
				cleaned["type"] = "OBJECT"
			case "array":
				cleaned["type"] = "ARRAY"
			case "string":
				cleaned["type"] = "STRING"
			case "integer":
				cleaned["type"] = "INTEGER"
			case "number":
				cleaned["type"] = "NUMBER"
			case "boolean":
				cleaned["type"] = "BOOLEAN"
			}
		}
		return cleaned
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = cleanFunctionParameters(item)
		}
		return result
	default:
		return params
	}
}

// GeminiFinishReasonToOpenAI maps Gemini finish reasons to OpenAI format.
func GeminiFinishReasonToOpenAI(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "SPII", "BLOCKLIST", "PROHIBITED_CONTENT":
		return "content_filter"
	default:
		return "stop"
	}
}

// GeminiFinishReasonToClaude maps Gemini finish reasons to Claude stop_reason.
func GeminiFinishReasonToClaude(reason string) string {
	switch reason {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY", "RECITATION", "SPII", "BLOCKLIST", "PROHIBITED_CONTENT":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// ResponseGeminiChat2OpenAI converts a Gemini GenerateContentResponse to OpenAI format.
// Based on new-api responseGeminiChat2OpenAI.
func ResponseGeminiChat2OpenAI(geminiResp map[string]any, model string) map[string]any {
	candidates, _ := geminiResp["candidates"].([]any)
	usageMetadata, _ := geminiResp["usageMetadata"].(map[string]any)

	choices := make([]map[string]any, 0, len(candidates))
	for i, c := range candidates {
		candidate, ok := c.(map[string]any)
		if !ok {
			continue
		}

		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)

		text := ""
		var toolCalls []map[string]any
		reasoning := ""

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
					"id":   fmt.Sprintf("call_%d_%d", i, len(toolCalls)),
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
				if t, ok := part["text"].(string); ok {
					reasoning += t
				}
				continue
			}
			// Text part
			if t, ok := part["text"].(string); ok {
				text += t
			}
		}

		finishReason := "stop"
		if fr, ok := candidate["finishReason"].(string); ok {
			finishReason = GeminiFinishReasonToOpenAI(fr)
		}
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}

		choice := map[string]any{
			"index": i,
			"message": map[string]any{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": finishReason,
		}

		if reasoning != "" {
			choice["message"].(map[string]any)["reasoning_content"] = reasoning
		}
		if len(toolCalls) > 0 {
			choice["message"].(map[string]any)["tool_calls"] = toolCalls
			choice["message"].(map[string]any)["content"] = nil
		}

		choices = append(choices, choice)
	}

	result := map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%s", model),
		"object":  "chat.completion",
		"created": 0,
		"model":   model,
		"choices": choices,
	}

	// Usage
	if usageMetadata != nil {
		promptTokens, _ := usageMetadata["promptTokenCount"].(float64)
		totalTokens, _ := usageMetadata["totalTokenCount"].(float64)
		candidatesTokens, _ := usageMetadata["candidatesTokenCount"].(float64)
		thoughtsTokens, _ := usageMetadata["thoughtsTokenCount"].(float64)
		completionTokens := candidatesTokens + thoughtsTokens
		if completionTokens == 0 && totalTokens > 0 {
			completionTokens = totalTokens - promptTokens
		}
		result["usage"] = map[string]any{
			"prompt_tokens":     int(promptTokens),
			"completion_tokens": int(completionTokens),
			"total_tokens":      int(totalTokens),
		}
	}

	return result
}

// ResponseGeminiChat2Claude converts a Gemini GenerateContentResponse to Claude Messages format.
func ResponseGeminiChat2Claude(geminiResp map[string]any, model string) map[string]any {
	candidates, _ := geminiResp["candidates"].([]any)
	usageMetadata, _ := geminiResp["usageMetadata"].(map[string]any)

	content := make([]map[string]any, 0)
	stopReason := "end_turn"

	for _, c := range candidates {
		candidate, ok := c.(map[string]any)
		if !ok {
			continue
		}

		contentMap, _ := candidate["content"].(map[string]any)
		parts, _ := contentMap["parts"].([]any)

		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}

			// Function call
			if fc, ok := part["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args, _ := fc["args"].(map[string]any)
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    fmt.Sprintf("toolu_%s", name),
					"name":  name,
					"input": args,
				})
				stopReason = "tool_use"
				continue
			}

			// Thought part
			if thought, ok := part["thought"].(bool); ok && thought {
				if t, ok := part["text"].(string); ok {
					content = append(content, map[string]any{
						"type":     "thinking",
						"thinking": t,
					})
				}
				continue
			}

			// Text part
			if t, ok := part["text"].(string); ok && t != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": t,
				})
			}
		}

		if fr, ok := candidate["finishReason"].(string); ok {
			stopReason = GeminiFinishReasonToClaude(fr)
		}
	}

	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	// Usage
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if usageMetadata != nil {
		if pt, ok := usageMetadata["promptTokenCount"].(float64); ok {
			usage["input_tokens"] = int(pt)
		}
		totalTokens, _ := usageMetadata["totalTokenCount"].(float64)
		promptTokens, _ := usageMetadata["promptTokenCount"].(float64)
		outputTokens := totalTokens - promptTokens
		if outputTokens < 0 {
			outputTokens = 0
		}
		usage["output_tokens"] = int(outputTokens)
	}

	return map[string]any{
		"id":            fmt.Sprintf("msg_%s", model),
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         usage,
	}
}

// StreamResponseGeminiChat2OpenAI converts a single Gemini SSE chunk to OpenAI stream format.
// Based on new-api streamResponseGeminiChat2OpenAI.
func StreamResponseGeminiChat2OpenAI(geminiResp map[string]any) (map[string]any, bool) {
	candidates, _ := geminiResp["candidates"].([]any)
	isStop := false

	choices := make([]map[string]any, 0, len(candidates))
	for _, c := range candidates {
		candidate, ok := c.(map[string]any)
		if !ok {
			continue
		}

		// Check for finish reason
		var finishReason *string
		if fr, ok := candidate["finishReason"].(string); ok {
			if fr == "STOP" {
				isStop = true
				stop := "stop"
				finishReason = &stop
			} else {
				mapped := GeminiFinishReasonToOpenAI(fr)
				finishReason = &mapped
			}
		}

		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)

		var textParts []string
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
					"id":   fmt.Sprintf("call_%d", len(toolCalls)),
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": string(argsJSON),
					},
				})
				continue
			}

			// Thought
			if thought, ok := part["thought"].(bool); ok && thought {
				isThought = true
				if t, ok := part["text"].(string); ok {
					textParts = append(textParts, t)
				}
				continue
			}

			// Text
			if t, ok := part["text"].(string); ok && t != "\n" {
				textParts = append(textParts, t)
			}
		}

		// Build delta
		delta := map[string]any{}
		if isThought && len(textParts) > 0 {
			delta["reasoning_content"] = strings.Join(textParts, "\n")
		} else if len(textParts) > 0 {
			delta["content"] = strings.Join(textParts, "\n")
		}
		if len(toolCalls) > 0 {
			delta["tool_calls"] = toolCalls
			fr := "tool_calls"
			finishReason = &fr
		}

		choice := map[string]any{
			"index": 0,
			"delta": delta,
		}
		if finishReason != nil {
			choice["finish_reason"] = *finishReason
		}

		choices = append(choices, choice)
	}

	result := map[string]any{
		"object":  "chat.completion.chunk",
		"choices": choices,
	}

	return result, isStop
}
