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

// OpenAIToGeminiAdaptor handles OpenAI-format clients sending requests to Gemini upstream.
type OpenAIToGeminiAdaptor struct{}

func (a *OpenAIToGeminiAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	upstreamModel := info.UpstreamModel
	if upstreamModel == "" {
		upstreamModel = info.OriginModel
	}
	// Gemini generateContent endpoint
	return fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", info.BaseURL, upstreamModel), nil
}

func (a *OpenAIToGeminiAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("x-goog-api-key", info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *OpenAIToGeminiAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	var openaiReq dto.OpenAIChatRequest
	if err := json.Unmarshal(requestBody, &openaiReq); err != nil {
		return nil, err
	}

	model := openaiReq.Model
	if model == "" {
		model = "unknown"
	}
	info.OriginModel = model
	upstreamModel := channel.ResolveModelAlias(model)
	info.UpstreamModel = upstreamModel

	if openaiReq.Stream != nil && *openaiReq.Stream {
		info.IsStream = true
	}

	geminiReq, err := CovertOpenAI2Gemini(&openaiReq)
	if err != nil {
		return nil, err
	}
	return geminiReq, nil
}

func (a *OpenAIToGeminiAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
	url, _ := a.GetRequestURL(info)
	req, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, err
	}
	_ = a.SetupRequestHeader(&req.Header, info)

	client := proxy.GetClient()
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (a *OpenAIToGeminiAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var geminiResp map[string]any
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	return ResponseGeminiChat2OpenAI(geminiResp, info.OriginModel), nil
}

//nolint:errcheck
func (a *OpenAIToGeminiAdaptor) streamGeminiToOpenAI(c *gin.Context, resp *http.Response, info *RelayInfo) error {
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
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inputTokens := 0
	outputTokens := 0

	for scanner.Scan() {
		line := scanner.Bytes()

		// Handle UTF-8 remainder
		if len(utf8Remainder) > 0 {
			line = append(utf8Remainder, line...)
			utf8Remainder = nil
		}
		if len(line) > 0 {
			r, size := utf8.DecodeLastRune(line)
			if r == utf8.RuneError && size > 0 && size < len(line) {
				utf8Remainder = line[len(line)-size:]
				line = line[:len(line)-size]
			}
		}

		lineStr := string(line)
		if !strings.HasPrefix(lineStr, "data: ") {
			continue
		}

		data := strings.TrimPrefix(lineStr, "data: ")
		if data == "" {
			continue
		}

		var geminiResp map[string]any
		if err := json.Unmarshal([]byte(data), &geminiResp); err != nil {
			continue
		}

		result, isStop := StreamResponseGeminiChat2OpenAI(geminiResp)

		// Extract usage from Gemini response
		if usage, ok := geminiResp["usageMetadata"].(map[string]any); ok {
			if pt, ok := usage["promptTokenCount"].(float64); ok {
				inputTokens = int(pt)
			}
			if ct, ok := usage["candidatesTokenCount"].(float64); ok {
				outputTokens = int(ct)
			}
		}

		// Add id and model
		result["id"] = "chatcmpl-gemini"
		result["model"] = "gemini"

		chunkData, _ := json.Marshal(result)
		c.Writer.WriteString("data: " + string(chunkData) + "\n\n")
		flusher.Flush()

		if isStop {
			c.Writer.WriteString("data: [DONE]\n\n")
			flusher.Flush()
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[relay] gemini stream read error: %v", err)
		return err
	}

	// Send [DONE] if not already sent
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	flusher.Flush()
	info.InputTokens = inputTokens
	info.OutputTokens = outputTokens
	return nil
}

func (a *OpenAIToGeminiAdaptor) GetModelList() []string {
	return []string{}
}

func (a *OpenAIToGeminiAdaptor) GetChannelName() string {
	return "openai_to_gemini"
}
