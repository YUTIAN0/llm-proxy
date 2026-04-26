package relay

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

var debugLogger = log.New(os.Stderr, "[llm-proxy-debug] ", log.LstdFlags|log.Lmicroseconds)

func isDebugEnabled() bool {
	return os.Getenv("LLM_PROXY_DEBUG") != ""
}

func debugLog(format string, v ...any) {
	if isDebugEnabled() {
		debugLogger.Printf(format, v...)
	}
}

func logClientSSE(event string, data []byte) {
	if isDebugEnabled() {
		debugData := string(data)
		if len(debugData) > 500 {
			debugData = debugData[:500] + "..."
		}
		debugLog("[CLIENT SSE] event=%s, data=%s", event, debugData)
	}
}

func logRelayResponse(info *RelayInfo, adaptor Adaptor, statusCode int, duration time.Duration, err error) {
	var clientFormat, upstreamFormat string
	switch info.Format {
	case "claude":
		clientFormat = "Claude Messages"
		upstreamFormat = "OpenAI Compatible"
	case "gemini", "gemini_to_openai":
		clientFormat = "Google Gemini"
		upstreamFormat = "OpenAI Compatible"
	default:
		clientFormat = "OpenAI Compatible"
		upstreamFormat = "OpenAI Compatible"
	}

	apiKeyTag := ""
	if info.ClientAPIKeyName != "" {
		apiKeyTag = "api_key:" + info.ClientAPIKeyName
	} else if info.ClientAPIKey != "" {
		apiKeyTag = "api_key:" + info.ClientAPIKey
	}

	modelTag := info.OriginModel
	if info.UpstreamModel != "" && info.UpstreamModel != info.OriginModel {
		modelTag = info.OriginModel + "->" + info.UpstreamModel
	}

	streamTag := ""
	if info.IsStream {
		streamTag = "[stream]"
	}

	errTag := ""
	if err != nil {
		errTag = " error:" + err.Error()
	}

	msg := fmt.Sprintf("[relay] %s->%s %s | ch:%s | model:%s | status:%d | tokens:in=%d out=%d | dur:%v",
		clientFormat, upstreamFormat, streamTag, info.ChannelName, modelTag, statusCode,
		info.InputTokens, info.OutputTokens, duration)
	if apiKeyTag != "" {
		msg += " | " + apiKeyTag
	}
	if errTag != "" {
		msg += errTag
	}
	log.Print(msg)
}

// extractModelFromPath extracts model name from Gemini-style URL paths.
func extractModelFromPath(fullPath string) string {
	path := fullPath
	for _, prefix := range []string{"/v1beta/", "/v1/"} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	path = strings.TrimPrefix(path, "models/")
	if idx := strings.LastIndex(path, ":"); idx > 0 {
		return path[:idx]
	}
	return path
}
