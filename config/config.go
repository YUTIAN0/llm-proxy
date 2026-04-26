package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server         ServerConfig    `yaml:"server"`
	Channels       []ChannelConfig `yaml:"channels"`
	DefaultChannel string          `yaml:"default_channel"`
	ModelAliases   map[string]string `yaml:"model_aliases"`
	APIKeys        []APIKeyConfig  `yaml:"api_keys"`
	Proxy          ProxyConfig     `yaml:"proxy"`
}

type ServerConfig struct {
	Port int         `yaml:"port"`
	TLS  TLSConfig   `yaml:"tls"`
}

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type ChannelConfig struct {
	Name    string            `yaml:"name"`
	APIKey  string            `yaml:"api_key"`
	BaseURL string            `yaml:"base_url"`
	Format  string            `yaml:"format"` // "openai", "claude", "gemini"
	Models  []string          `yaml:"models"`
	Headers map[string]string `yaml:"headers"` // custom request headers forwarded to upstream
}

type APIKeyConfig struct {
	Key      string              `yaml:"key"`
	Name     string              `yaml:"name"`     // human-readable name for logging
	Allowed  []string            `yaml:"allowed"`  // allowed models (empty = all allowed)
	Denied   []string            `yaml:"denied"`   // denied models
	Channels map[string][]string `yaml:"channels"` // model pattern -> channel name mapping (optional, all channels by default)
}

type ProxyConfig struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`     // "socks5"
	Addr    string `yaml:"addr"`     // "host:port"
	Username string `yaml:"username"` // optional
	Password string `yaml:"password"` // optional
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.ModelAliases == nil {
		cfg.ModelAliases = make(map[string]string)
	}

	return &cfg, nil
}