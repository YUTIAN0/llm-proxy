package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llm-proxy/channel"
	"github.com/llm-proxy/config"
	"github.com/llm-proxy/dto"
	"github.com/llm-proxy/relay/constant"
	"github.com/llm-proxy/service"
)

var globalCfg *config.Config

func SetConfig(cfg *config.Config) {
	globalCfg = cfg
}

func RelayHandler(c *gin.Context) {
	startTime := time.Now()

	path := c.Request.URL.Path
	mode := constant.Path2RelayMode(path)
	info := &RelayInfo{
		Mode:     mode,
		IsStream: c.GetHeader("Accept") == "text/event-stream" || c.GetHeader("Accept") == "text/event-stream, */*",
	}

	body, _ := io.ReadAll(c.Request.Body)

	// DEBUG: log client request headers
	debugBody := string(body)
	if len(debugBody) > 1000 {
		debugBody = debugBody[:1000] + "..."
	}
	debugLog("[CLIENT REQUEST] path=%s, method=%s, body=%s", path, c.Request.Method, debugBody)
	for k, vv := range c.Request.Header {
		if k == "Authorization" || k == "X-Api-Key" || k == "x-api-key" {
			debugLog("[CLIENT REQUEST] header %s=[REDACTED]", k)
		} else {
			debugLog("[CLIENT REQUEST] header %s=%s", k, strings.Join(vv, ", "))
		}
	}

	var bodyMap map[string]any
	if json.Unmarshal(body, &bodyMap) == nil {
		if s, ok := bodyMap["stream"].(bool); ok && s {
			info.IsStream = true
		}
	}

	// Gemini streaming is indicated by endpoint name or alt=sse query param
	if strings.Contains(path, ":streamGenerateContent") {
		info.IsStream = true
	}
	if c.Query("alt") == "sse" {
		info.IsStream = true
	}

	clientAPIKey := c.GetHeader("X-API-Key")
	if clientAPIKey == "" {
		clientAPIKey = c.GetHeader("x-api-key")
	}
	if clientAPIKey == "" {
		clientAPIKey = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	// Gemini API uses x-goog-api-key header
	if clientAPIKey == "" {
		clientAPIKey = c.GetHeader("X-Goog-Api-Key")
	}
	if clientAPIKey == "" {
		clientAPIKey = c.GetHeader("x-goog-api-key")
	}
	info.ClientAPIKey = clientAPIKey

	var bodyModel string
	if json.Unmarshal(body, &bodyMap) == nil {
		if m, ok := bodyMap["model"].(string); ok {
			bodyModel = m
		}
	}

	// Determine initial channel and format
	ch, akConfig, fmt_ := resolveChannel(path, bodyModel, c, clientAPIKey)
	if ch == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no channel configured"})
		return
	}
	info.Format = fmt_

	// Resolve API key config and model-based routing
	resolvedCh, akCfg, akName := channel.ResolveAPIKey(clientAPIKey, bodyModel)
	if resolvedCh != nil {
		ch = resolvedCh
	}
	if akCfg != nil {
		akConfig = akCfg
		info.ClientAPIKeyName = akName
	}

	// Validate API key - if an API key was provided, it must be recognized
	if clientAPIKey != "" && !channel.IsKeyRecognized(clientAPIKey) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}

	if akConfig != nil && bodyModel != "" {
		if err := channel.CheckAPIKeyModelPermission(akConfig, bodyModel); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}

	// Build ordered channel list for retry
	var retryChannels []*config.ChannelConfig
	if globalCfg.Retry.Enabled {
		retryChannels = channel.GetOrderedChannels(true)
		// Deduplicate - remove already tried channel, keep order
		seen := make(map[string]bool)
		seen[ch.Name] = true
		filtered := make([]*config.ChannelConfig, 0, len(retryChannels))
		for _, rc := range retryChannels {
			if !seen[rc.Name] {
				filtered = append(filtered, rc)
				seen[rc.Name] = true
			}
		}
		retryChannels = append([]*config.ChannelConfig{ch}, filtered...)
	} else {
		retryChannels = []*config.ChannelConfig{ch}
	}

	maxAttempts := globalCfg.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if maxAttempts > len(retryChannels) {
		maxAttempts = len(retryChannels)
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			log.Printf("[relay] retry attempt %d/%d, channel=%s", attempt+1, maxAttempts, retryChannels[attempt].Name)
		}

		err := sendAndHandleRequest(c, path, mode, info, body, retryChannels[attempt], bodyModel, startTime)
		if err == nil {
			return
		}

		// Check if retry is configured and should continue
		if !globalCfg.Retry.Enabled || attempt >= maxAttempts-1 {
			lastErr = err
			break
		}
		lastErr = err
	}

	// All attempts failed
	c.JSON(http.StatusBadGateway, gin.H{"error": "all channels failed: " + lastErr.Error()})
}

func resolveChannel(path string, bodyModel string, c *gin.Context, clientAPIKey string) (*config.ChannelConfig, *config.APIKeyConfig, string) {
	format := ""

	if strings.HasSuffix(path, "/messages") && !strings.Contains(path, "/v1/chat/") {
		ch := channel.GetDefault()
		format = "claude"
		return ch, nil, format
	} else if strings.Contains(path, "/models/") && (strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")) {
		ch := channel.GetDefault()
		format = "gemini_to_openai"
		return ch, nil, format
	} else {
		targetFormat := c.GetHeader("X-Target-Format")
		channelName := c.GetHeader("X-Channel")
		var ch *config.ChannelConfig
		if channelName != "" {
			ch = channel.GetByName(channelName)
		} else if targetFormat != "" {
			ch = channel.GetByFormat(targetFormat)
		} else {
			ch = channel.GetByAPIKey(clientAPIKey)
			if ch == nil {
				ch = channel.GetDefault()
			}
		}
		return ch, nil, format
	}
}

func sendAndHandleRequest(c *gin.Context, path string, mode int, info *RelayInfo, body []byte, ch *config.ChannelConfig, bodyModel string, startTime time.Time) error {
	// Setup channel info
	info.APIKey = ch.APIKey
	info.BaseURL = ch.BaseURL
	info.ChannelName = ch.Name
	info.CustomHeaders = ch.Headers
	if ch.Format != "" {
		info.Format = ch.Format
	}

	// DEBUG: log routing decisions
	debugLog("[ROUTING] channel=%s, format=%s, model=%s, is_stream=%v, origin_model=%s, upstream_model=%s",
		info.ChannelName, info.Format, bodyModel, info.IsStream, info.OriginModel, info.UpstreamModel)

	adaptor := GetAdaptorByFormat(info.Format, mode)

	convertedReq, err := adaptor.ConvertRequest(c, info, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return err
	}

	// Pre-count input tokens using tiktoken
	if openaiReq, ok := convertedReq.(*dto.OpenAIChatRequest); ok {
		info.PreCountTokens = service.CountRequestTokens(openaiReq)
	}

	reqJSON, _ := json.Marshal(convertedReq)

	// Apply parameter override
	if len(globalCfg.ParamOverride) > 0 {
		reqBody := make(map[string]any)
		if json.Unmarshal(reqJSON, &reqBody) == nil {
			reqBody = ApplyParamOverride(reqBody, globalCfg.ParamOverride)
			reqJSON, _ = json.Marshal(reqBody)
		}
	}

	// DEBUG: log converted request (truncated to 1000 bytes)
	debugUpstreamBody := string(reqJSON)
	if len(debugUpstreamBody) > 1000 {
		debugUpstreamBody = debugUpstreamBody[:1000] + "..."
	}
	debugLog("[UPSTREAM REQUEST] url=%s, body=%s", info.BaseURL+"/v1/chat/completions", debugUpstreamBody)
	reader := bytes.NewReader(reqJSON)

	resp, err := adaptor.DoRequest(c, info, reader)
	if err != nil {
		return err
	}

	httpResp := resp.(*http.Response)

	// Check if this response should trigger a retry
	if shouldRetry(httpResp) {
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		log.Printf("[relay] retry trigger: status=%d body=%s", httpResp.StatusCode, truncateStr(string(body), 200))
		return fmt.Errorf("upstream returned %d", httpResp.StatusCode)
	}

	if info.IsStream && info.Format == "claude" {
		if streamErr := adaptor.(*ClaudeToOpenAIAdaptor).streamClaudeResponse(c, httpResp, info); streamErr != nil {
			logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), streamErr)
			return streamErr
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		channel.RecordStats(info.ClientAPIKeyName, info.OriginModel, info.InputTokens, info.OutputTokens)
		return nil
	}

	if info.IsStream && (info.Format == "gemini" || info.Format == "gemini_to_openai") {
		if streamErr := adaptor.(*GeminiToOpenAIAdaptor).streamGeminiResponse(c, httpResp, info); streamErr != nil {
			logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), streamErr)
			return streamErr
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		channel.RecordStats(info.ClientAPIKeyName, info.OriginModel, info.InputTokens, info.OutputTokens)
		return nil
	}

	if info.IsStream && info.Format == "openai_to_gemini" {
		if streamErr := adaptor.(*OpenAIToGeminiAdaptor).streamGeminiToOpenAI(c, httpResp, info); streamErr != nil {
			logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), streamErr)
			return streamErr
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		channel.RecordStats(info.ClientAPIKeyName, info.OriginModel, info.InputTokens, info.OutputTokens)
		return nil
	}

	result, err := adaptor.DoResponse(c, httpResp, info)
	if err != nil {
		return err
	}

	if info.IsStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		if s, ok := result.(string); ok {
			_, _ = c.Writer.WriteString(s)
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		channel.RecordStats(info.ClientAPIKeyName, info.OriginModel, info.InputTokens, info.OutputTokens)
		return nil
	}

	if r, ok := result.(map[string]any); ok {
		if usage, ok := r["usage"].(map[string]any); ok {
			for _, field := range []string{"prompt_tokens", "input_tokens"} {
				if v, ok := usage[field]; ok {
					switch t := v.(type) {
					case float64:
						info.InputTokens = int(t)
					case int:
						info.InputTokens = t
					}
				}
			}
			for _, field := range []string{"completion_tokens", "output_tokens"} {
				if v, ok := usage[field]; ok {
					switch t := v.(type) {
					case float64:
						info.OutputTokens = int(t)
					case int:
						info.OutputTokens = t
					}
				}
			}
		}
	}

	// Fallback to pre-counted tokens if upstream didn't return usage
	if info.InputTokens == 0 {
		info.InputTokens = info.PreCountTokens
	}

	logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
	channel.RecordStats(info.ClientAPIKeyName, info.OriginModel, info.InputTokens, info.OutputTokens)
	c.JSON(httpResp.StatusCode, result)
	return nil
}

func shouldRetry(resp *http.Response) bool {
	if !globalCfg.Retry.Enabled {
		return false
	}

	statusCodes := globalCfg.Retry.StatusCodes
	if len(statusCodes) == 0 {
		statusCodes = []int{429, 500, 502, 503, 504}
	}

	for _, sc := range statusCodes {
		if resp.StatusCode == sc {
			return true
		}
	}
	return false
}
