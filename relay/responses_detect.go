package relay

import "sync"

// modelResponsesSupport records whether a channel+model combination supports
// native Responses API (true = passthrough works, false = needs conversion).
// Key format: "channelName:modelName"
var modelResponsesSupport sync.Map

// supportsResponses checks if the given channel+model supports native Responses API.
// Returns (supported, known): if known is false, the model hasn't been tested yet.
func supportsResponses(channelName, model string) (supported, known bool) {
	key := channelName + ":" + model
	v, ok := modelResponsesSupport.Load(key)
	if !ok {
		return false, false
	}
	return v.(bool), true
}

// markResponsesSupport records whether a channel+model supports native Responses API.
func markResponsesSupport(channelName, model string, supported bool) {
	key := channelName + ":" + model
	modelResponsesSupport.Store(key, supported)
}

// isResponsesUnsupportedError returns true if the HTTP status code indicates
// the upstream doesn't support the Responses API endpoint.
// Includes 500 because some upstreams (e.g. new-api/vllm) return 500 when
// they don't properly handle the Responses API format.
func isResponsesUnsupportedError(statusCode int) bool {
	return statusCode == 400 || statusCode == 404 || statusCode == 405 || statusCode == 500
}

// modelCompactSupport records whether a channel+model combination supports
// the /responses/compact endpoint (true = passthrough works, false = unsupported).
// Key format: "channelName:modelName"
var modelCompactSupport sync.Map

// supportsCompact checks if the given channel+model supports the compact endpoint.
func supportsCompact(channelName, model string) (supported, known bool) {
	key := channelName + ":" + model
	v, ok := modelCompactSupport.Load(key)
	if !ok {
		return false, false
	}
	return v.(bool), true
}

// markCompactSupport records whether a channel+model supports the compact endpoint.
func markCompactSupport(channelName, model string, supported bool) {
	key := channelName + ":" + model
	modelCompactSupport.Store(key, supported)
}
