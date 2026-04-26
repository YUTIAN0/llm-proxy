package channel

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/llm-proxy/config"
)

type Manager struct {
	channels       map[string]*config.ChannelConfig
	defaultChannel string
	apiKeys        map[string]*config.APIKeyConfig
	cfg            *config.Config
	mu             sync.RWMutex
}

var manager *Manager

func Init(cfg *config.Config) {
	manager = &Manager{
		channels:       make(map[string]*config.ChannelConfig),
		defaultChannel: cfg.DefaultChannel,
		apiKeys:        make(map[string]*config.APIKeyConfig),
		cfg:            cfg,
	}
	for i := range cfg.Channels {
		ch := &cfg.Channels[i]
		manager.channels[ch.Name] = ch
	}
	for i := range cfg.APIKeys {
		k := &cfg.APIKeys[i]
		manager.apiKeys[k.Key] = k
	}
}

// ResolveAPIKey returns the channel config for a given API key and model
// Supports model-based routing: different models can go to different channels
// If no API key config matches, falls back to channel lookup or default
func ResolveAPIKey(apiKey, model string) (*config.ChannelConfig, *config.APIKeyConfig, string) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	// Check if this is a configured API key
	if ak, ok := manager.apiKeys[apiKey]; ok {
		chName := resolveChannelForModel(ak, model)
		ch := manager.channels[chName]
		if ch == nil {
			ch = manager.channels[manager.defaultChannel]
		}
		return ch, ak, ak.Name
	}

	// Check if it matches a channel's API key (pass-through mode)
	for _, ch := range manager.channels {
		if ch.APIKey == apiKey {
			return ch, nil, ch.Name
		}
	}

	// No match - return default channel, no API key config
	return manager.channels[manager.defaultChannel], nil, ""
}

// resolveChannelForModel determines which channel to use based on model
// If no model->channel mapping matches, uses default channel
func resolveChannelForModel(ak *config.APIKeyConfig, model string) string {
	// First check explicit model->channel mappings
	if len(ak.Channels) > 0 {
		// Sort keys by length (descending) to match longest prefix first
		keys := make([]string, 0, len(ak.Channels))
		for k := range ak.Channels {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			return len(keys[i]) > len(keys[j])
		})
		for _, pattern := range keys {
			if modelMatch(pattern, model) {
				channelNames := ak.Channels[pattern]
				if len(channelNames) > 0 {
					return channelNames[0]
				}
			}
		}
	}

	// No model-based routing - return empty to let caller use default
	return ""
}

// modelMatch checks if a pattern matches a model name
// Supports wildcard with * suffix, e.g. "claude-*" matches "claude-haiku-4-5"
func modelMatch(pattern, model string) bool {
	if pattern == model {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(model, prefix)
	}
	return false
}

// GetAPIKeyConfig returns the API key config for a given API key
func GetAPIKeyConfig(apiKey string) *config.APIKeyConfig {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.apiKeys[apiKey]
}

// CheckAPIKeyModelPermission returns error if the model is not allowed for this API key
func CheckAPIKeyModelPermission(ak *config.APIKeyConfig, model string) error {
	if len(ak.Denied) > 0 {
		for _, denied := range ak.Denied {
			if denied == model {
				return fmt.Errorf("model %s is not allowed for API key %s", model, ak.Name)
			}
		}
	}
	if len(ak.Allowed) > 0 {
		for _, allowed := range ak.Allowed {
			if allowed == model {
				return nil
			}
		}
		return fmt.Errorf("model %s is not in the allowed list for API key %s", model, ak.Name)
	}
	return nil
}

func GetByAPIKey(apiKey string) *config.ChannelConfig {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	for _, ch := range manager.channels {
		if ch.APIKey == apiKey {
			return ch
		}
	}
	return manager.channels[manager.defaultChannel]
}

func GetByName(name string) *config.ChannelConfig {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.channels[name]
}

func GetByFormat(format string) *config.ChannelConfig {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	for _, ch := range manager.channels {
		if ch.Format == format {
			return ch
		}
	}
	return nil
}

func GetDefault() *config.ChannelConfig {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.channels[manager.defaultChannel]
}

func GetAllChannels() map[string]*config.ChannelConfig {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make(map[string]*config.ChannelConfig)
	for k, v := range manager.channels {
		result[k] = v
	}
	return result
}

// ResolveModelAlias resolves a model name using global aliases
func ResolveModelAlias(model string) string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.cfg == nil || manager.cfg.ModelAliases == nil {
		return model
	}
	if alias, ok := manager.cfg.ModelAliases[model]; ok && alias != "" {
		return alias
	}
	return model
}