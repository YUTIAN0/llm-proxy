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

type OpenAIAdaptor struct{}

func (a *OpenAIAdaptor) GetRequestURL(info *RelayInfo) (string, error) {
	baseURL := info.BaseURL
	if strings.HasSuffix(baseURL, "/v1") {
		return fmt.Sprintf("%s/chat/completions", baseURL), nil
	}
	return fmt.Sprintf("%s/v1/chat/completions", baseURL), nil
}

func (a *OpenAIAdaptor) SetupRequestHeader(req *http.Header, info *RelayInfo) error {
	req.Set("Content-Type", "application/json")
	req.Set("Authorization", "Bearer "+info.APIKey)
	for k, v := range info.CustomHeaders {
		req.Set(k, v)
	}
	return nil
}

func (a *OpenAIAdaptor) ConvertRequest(c *gin.Context, info *RelayInfo, requestBody []byte) (any, error) {
	var req dto.OpenAIChatRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return nil, err
	}
	info.OriginModel = req.Model
	info.UpstreamModel = channel.ResolveModelAlias(req.Model)
	if info.UpstreamModel != info.OriginModel {
		req.Model = info.UpstreamModel
	}
	return req, nil
}

func (a *OpenAIAdaptor) DoRequest(c *gin.Context, info *RelayInfo, requestBody io.Reader) (any, error) {
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

func (a *OpenAIAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *RelayInfo) (any, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result any
	if info.IsStream {
		return string(body), nil
	}
		_ = json.Unmarshal(body, &result)
	return result, nil
}

func (a *OpenAIAdaptor) GetModelList() []string {
	return []string{}
}

func (a *OpenAIAdaptor) GetChannelName() string {
	return "openai"
}
