package proxy

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/llm-proxy/config"
	"golang.org/x/net/proxy"
)

var (
	proxyClient *http.Client
	proxyMu     sync.RWMutex
	proxyCfg    config.ProxyConfig
)

// Init sets up the proxy client if enabled
func Init(pcfg config.ProxyConfig) {
	proxyMu.Lock()
	defer proxyMu.Unlock()

	proxyCfg = pcfg
	proxyClient = nil

	if !pcfg.Enabled || pcfg.Type != "socks5" || pcfg.Addr == "" {
		return
	}

	dialer, err := proxy.SOCKS5("tcp", pcfg.Addr,
		&proxy.Auth{
			User:     pcfg.Username,
			Password: pcfg.Password,
		},
		proxy.Direct,
	)
	if err != nil {
		return
	}

	proxyClient = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		},
	}
}

// GetClient returns the proxy client if configured, otherwise nil (use default)
func GetClient() *http.Client {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	return proxyClient
}

// IsEnabled returns true if proxy is configured and enabled
func IsEnabled() bool {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	return proxyCfg.Enabled && proxyClient != nil
}
