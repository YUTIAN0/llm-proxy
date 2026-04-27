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

// IsKeyRecognized returns true if the API key matches either a configured
// api_keys entry or any channel's upstream API key (pass-through mode).
// Empty keys are not recognized.
func IsKeyRecognized(apiKey string) bool {
	if apiKey == "" {
		return false
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if _, ok := manager.apiKeys[apiKey]; ok {
		return true
	}
	for _, ch := range manager.channels {
		if ch.APIKey == apiKey {
			return true
		}
	}
	return false
}

// ValidateAPIKey returns true if the API key is configured in api_keys section.
func ValidateAPIKey(apiKey string) (*config.APIKeyConfig, bool) {
	if apiKey == "" {
		return nil, false
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	ak, ok := manager.apiKeys[apiKey]
	if !ok {
		return nil, false
	}
	return ak, true
}

// GetAllowedModels returns the list of model names an API key is allowed to use.
// If allowed list is empty (full-access), returns all alias + channel models.
// Also includes aliases that resolve to allowed channel models.
func GetAllowedModels(ak *config.APIKeyConfig) []string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	// If no allowed list, return all known models
	if len(ak.Allowed) == 0 {
		all := make(map[string]bool)
		// Add all alias keys
		if manager.cfg.ModelAliases != nil {
			for k := range manager.cfg.ModelAliases {
				all[k] = true
			}
		}
		// Add all channel models
		for _, ch := range manager.channels {
			for _, m := range ch.Models {
				all[m] = true
			}
		}
		result := make([]string, 0, len(all))
		for k := range all {
			result = append(result, k)
		}
		return result
	}

	// Return explicitly allowed models
	return ak.Allowed
}

// GetAllowedModelsWithAliases returns allowed models plus any aliases that resolve to upstream models in allowed channels.
func GetAllowedModelsWithAliases(ak *config.APIKeyConfig) []string {
	allowed := GetAllowedModels(ak)
	if len(allowed) == 0 {
		return allowed
	}

	allowedSet := make(map[string]bool)
	for _, m := range allowed {
		allowedSet[m] = true
	}

	manager.mu.RLock()
	defer manager.mu.RUnlock()

	// Add any alias that resolves to an allowed model
	if manager.cfg.ModelAliases != nil {
		for aliasKey, aliasVal := range manager.cfg.ModelAliases {
			if aliasVal == "" {
				continue
			}
			// If the alias name itself is not in allowed but resolves to a channel model that IS in allowed
			if !allowedSet[aliasKey] {
				// Check if the resolved value is in allowed set or matches a channel model
				if allowedSet[aliasVal] {
					allowedSet[aliasKey] = true
				}
			}
		}
	}

	result := make([]string, 0, len(allowedSet))
	for k := range allowedSet {
		result = append(result, k)
	}
	return result
}

// GetAllChannelModels returns all models across all channels.
func GetAllChannelModels() []string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	all := make(map[string]bool)
	for _, ch := range manager.channels {
		for _, m := range ch.Models {
			all[m] = true
		}
	}
	// Add aliases
	if manager.cfg.ModelAliases != nil {
		for k := range manager.cfg.ModelAliases {
			all[k] = true
		}
	}
	result := make([]string, 0, len(all))
	for k := range all {
		result = append(result, k)
	}
	return result
}

// GetAllAPIKeys returns all configured API key strings.
func GetAllAPIKeys() []string {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	keys := make([]string, 0, len(manager.apiKeys))
	for k := range manager.apiKeys {
		keys = append(keys, k)
	}
	return keys
}