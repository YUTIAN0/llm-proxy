package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llm-proxy/channel"
	"github.com/llm-proxy/config"
	"github.com/llm-proxy/relay/constant"
)

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

	var ch *config.ChannelConfig
	var akConfig *config.APIKeyConfig
	if strings.HasSuffix(path, "/messages") && !strings.Contains(path, "/v1/chat/") {
		ch = channel.GetDefault()
		info.Format = "claude"
	} else if strings.Contains(path, "/models/") && (strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")) {
		ch = channel.GetDefault()
		info.Format = "gemini_to_openai"
		info.OriginModel = extractModelFromPath(path)
	} else {
		targetFormat := c.GetHeader("X-Target-Format")
		channelName := c.GetHeader("X-Channel")
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
	}

	// Resolve API key config and model-based routing
	if ch != nil {
		resolvedCh, akCfg, akName := channel.ResolveAPIKey(clientAPIKey, bodyModel)
		if resolvedCh != nil {
			ch = resolvedCh
		}
		if akCfg != nil {
			akConfig = akCfg
			info.ClientAPIKeyName = akName
		}
	}

	if ch != nil {
		info.APIKey = ch.APIKey
		info.BaseURL = ch.BaseURL
		info.ChannelName = ch.Name
		info.CustomHeaders = ch.Headers
		if ch.Format != "" {
			info.Format = ch.Format
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no channel configured"})
		return
	}

	// DEBUG: log routing decisions
	debugLog("[ROUTING] channel=%s, format=%s, model=%s, is_stream=%v, origin_model=%s, upstream_model=%s",
		info.ChannelName, info.Format, bodyModel, info.IsStream, info.OriginModel, info.UpstreamModel)

	// Validate API key - if an API key was provided, it must be recognized
	// (either a configured api_keys entry or a channel's upstream key)
	if clientAPIKey != "" && !channel.IsKeyRecognized(clientAPIKey) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}

	adaptor := GetAdaptorByFormat(info.Format, mode)

	convertedReq, err := adaptor.ConvertRequest(c, info, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if akConfig != nil && info.OriginModel != "" {
		if err := channel.CheckAPIKeyModelPermission(akConfig, info.OriginModel); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}

	reqJSON, _ := json.Marshal(convertedReq)

	// DEBUG: log converted request (truncated to 1000 bytes)
	debugUpstreamBody := string(reqJSON)
	if len(debugUpstreamBody) > 1000 {
		debugUpstreamBody = debugUpstreamBody[:1000] + "..."
	}
	debugLog("[UPSTREAM REQUEST] url=%s, body=%s", info.BaseURL+"/v1/chat/completions", debugBody)
	reader := bytes.NewReader(reqJSON)

	resp, err := adaptor.DoRequest(c, info, reader)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	httpResp := resp.(*http.Response)

	if info.IsStream && info.Format == "claude" {
		if streamErr := adaptor.(*ClaudeToOpenAIAdaptor).streamClaudeResponse(c, httpResp); streamErr != nil {
			logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), streamErr)
			c.JSON(http.StatusBadGateway, gin.H{"error": streamErr.Error()})
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		return
	}

	if info.IsStream && (info.Format == "gemini" || info.Format == "gemini_to_openai") {
		if streamErr := adaptor.(*GeminiToOpenAIAdaptor).streamGeminiResponse(c, httpResp); streamErr != nil {
			logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), streamErr)
			c.JSON(http.StatusBadGateway, gin.H{"error": streamErr.Error()})
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		return
	}

	if info.IsStream && info.Format == "openai_to_gemini" {
		if streamErr := adaptor.(*OpenAIToGeminiAdaptor).streamGeminiToOpenAI(c, httpResp); streamErr != nil {
			logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), streamErr)
			c.JSON(http.StatusBadGateway, gin.H{"error": streamErr.Error()})
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		return
	}

	result, err := adaptor.DoResponse(c, httpResp, info)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	if info.IsStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		if s, ok := result.(string); ok {
			c.Writer.WriteString(s)
		}
		logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
		return
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

	logRelayResponse(info, adaptor, httpResp.StatusCode, time.Since(startTime), nil)
	c.JSON(httpResp.StatusCode, result)
}
