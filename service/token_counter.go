package service

import (
	"fmt"
	"strings"
	"sync"

	"github.com/llm-proxy/dto"
	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

var defaultEncoder tokenizer.Codec
var encoderMap = make(map[string]tokenizer.Codec)
var encoderMu sync.RWMutex

func InitTokenEncoders() {
	defaultEncoder = codec.NewCl100kBase()
}

func getEncoder(model string) tokenizer.Codec {
	encoderMu.RLock()
	if enc, ok := encoderMap[model]; ok {
		encoderMu.RUnlock()
		return enc
	}
	encoderMu.RUnlock()

	encoderMu.Lock()
	defer encoderMu.Unlock()

	if enc, ok := encoderMap[model]; ok {
		return enc
	}

	m, err := tokenizer.ForModel(tokenizer.Model(model))
	if err != nil {
		encoderMap[model] = defaultEncoder
		return defaultEncoder
	}

	encoderMap[model] = m
	return m
}

// CountTextToken counts tokens in plain text using tiktoken cl100k_base.
func CountTextToken(text string) int {
	if text == "" {
		return 0
	}
	n, _ := defaultEncoder.Count(text)
	return n
}

// CountRequestTokens counts input tokens for a converted OpenAI request.
// Uses the same formula as tiktoken for GPT-3.5/4: ~3 tokens per message + content tokens.
func CountRequestTokens(req *dto.OpenAIChatRequest) int {
	tokens := 0

	for _, msg := range req.Messages {
		tokens += 3 // per message
		tokens += countMessageTokens(msg)
	}
	tokens += 3 // response format

	// Tools: ~8 tokens per tool definition
	if len(req.Tools) > 0 {
		tokens += len(req.Tools) * 8
		for _, tool := range req.Tools {
			if tool.Function != nil {
				tokens += CountTextToken(tool.Function.Name)
				tokens += CountTextToken(fmt.Sprintf("%v", tool.Function.Parameters))
			}
		}
	}

	return tokens
}

func countMessageTokens(msg dto.OpenAIMessage) int {
	tokens := 0

	if msg.Name != "" {
		tokens += 1
		tokens += CountTextToken(msg.Name)
	}

	switch c := msg.Content.(type) {
	case string:
		tokens += CountTextToken(c)
	case []dto.MediaContent:
		for _, mc := range c {
			switch mc.Type {
			case "text":
				tokens += CountTextToken(mc.Text)
			case "image_url":
				// Images counted separately, skip here
			}
		}
	}

	return tokens
}

// FormatMessagesForCount converts messages to a single string for token counting.
func FormatMessagesForCount(messages []dto.OpenAIMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		name := ""
		if msg.Name != "" {
			name = " " + msg.Name
		}
		content := ""
		switch c := msg.Content.(type) {
		case string:
			content = c
		case []dto.MediaContent:
			for _, mc := range c {
				if mc.Type == "text" {
					content += mc.Text
				}
			}
		}
		b.WriteString(fmt.Sprintf("\n%s%s: %s", msg.Role, name, content))
	}
	b.WriteString("\n")
	return b.String()
}
