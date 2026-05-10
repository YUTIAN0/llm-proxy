package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llm-proxy/channel"
	"github.com/llm-proxy/dto"
	"github.com/llm-proxy/proxy"
)

// ResponsesCompactAdaptor handles /v1/responses/compact requests.
// Compact is always passthrough — no format conversion or auto-detect needed.
type ResponsesCompactAdaptor struct{}

func (a *ResponsesCompactAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	baseURL := info.BaseURL
	if strings.HasSuffix(baseURL, "/v1") {
		return fmt.Sprintf("%s/responses/compact", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/responses/compact", baseURL), nil
}

func (a *ResponsesCompactAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *ResponsesCompactAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	var req dto.OpenAIResponsesCompactRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return nil, fmt.Errorf("failed to parse responses/compact request: %w", err)
	}
	info.OriginModel = req.Model
	info.UpstreamModel = channel.ResolveModelAlias(req.Model)
	req.Model = info.UpstreamModel
	return req, nil
}

func (a *ResponsesCompactAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
	url, _ := a.GetRequestURL(info)
	httpReq, err := http.NewRequest("POST", url, requestBody)
	if err != nil {
		return nil, err
	}
	_ = a.SetupRequestHeader(&httpReq.Header, info)
	client := proxy.GetClientWithTimeout(getRequestTimeout())
	return client.Do(httpReq)
}

func (a *ResponsesCompactAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Parse compact response for usage extraction
	var compactResp dto.OpenAIResponsesCompactResponse
	if err := json.Unmarshal(body, &compactResp); err != nil {
		// Return raw body if parsing fails
		var raw any
		if json.Unmarshal(body, &raw) == nil {
			return raw, nil
		}
		return nil, fmt.Errorf("failed to parse compact response: %w", err)
	}

	if compactResp.Error != nil {
		errMsg := fmt.Sprintf("%v", compactResp.Error)
		return map[string]any{"error": map[string]any{"message": errMsg, "type": "upstream_error"}}, nil
	}

	// Extract usage
	if compactResp.Usage != nil {
		info.InputTokens = compactResp.Usage.InputTokens
		info.OutputTokens = compactResp.Usage.OutputTokens
	}

	// Return as-is (passthrough)
	var result any
	if json.Unmarshal(body, &result) != nil {
		return string(body), nil
	}
	return result, nil
}

func (a *ResponsesCompactAdaptor) GetModelList() []string {
	return []string{}
}

func (a *ResponsesCompactAdaptor) GetChannelName() string {
	return "openai-responses-compact"
}
