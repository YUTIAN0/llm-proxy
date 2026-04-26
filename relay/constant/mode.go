package constant

import "strings"

const (
	RelayModeUnknown = iota
	RelayModeChatCompletions
	RelayModeCompletions
	RelayModeEmbeddings
	RelayModeModerations
	RelayModeImagesGenerations
	RelayModeImagesEdits
	RelayModeEdits
	RelayModeAudioSpeech
	RelayModeAudioTranscription
	RelayModeAudioTranslation
	RelayModeRerank
	RelayModeResponses
	RelayModeRealtime
	RelayModeGemini
	RelayModeClaude
)

func Path2RelayMode(path string) int {
	path = strings.TrimSuffix(path, "/")
	if strings.HasPrefix(path, "/v1/chat/completions") || strings.HasPrefix(path, "/pg/chat/completions") {
		return RelayModeChatCompletions
	} else if strings.HasPrefix(path, "/v1/completions") {
		return RelayModeCompletions
	} else if strings.HasPrefix(path, "/v1/embeddings") || strings.HasSuffix(path, "embeddings") {
		return RelayModeEmbeddings
	} else if strings.HasPrefix(path, "/v1/moderations") {
		return RelayModeModerations
	} else if strings.HasPrefix(path, "/v1/images/generations") {
		return RelayModeImagesGenerations
	} else if strings.HasPrefix(path, "/v1/images/edits") {
		return RelayModeImagesEdits
	} else if strings.HasPrefix(path, "/v1/edits") {
		return RelayModeEdits
	} else if strings.HasPrefix(path, "/v1/responses") {
		return RelayModeResponses
	} else if strings.HasPrefix(path, "/v1/audio/speech") {
		return RelayModeAudioSpeech
	} else if strings.HasPrefix(path, "/v1/audio/transcriptions") {
		return RelayModeAudioTranscription
	} else if strings.HasPrefix(path, "/v1/audio/translations") {
		return RelayModeAudioTranslation
	} else if strings.HasPrefix(path, "/v1/rerank") {
		return RelayModeRerank
	} else if strings.HasPrefix(path, "/v1/realtime") {
		return RelayModeRealtime
	} else if strings.HasPrefix(path, "/v1beta/models") || strings.HasPrefix(path, "/v1/models") {
		return RelayModeGemini
	}
	return RelayModeUnknown
}