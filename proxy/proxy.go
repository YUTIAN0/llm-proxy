package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/llm-proxy/config"
	"golang.org/x/net/proxy"
)

var (
	proxyClient *http.Client
	proxyMu     sync.RWMutex
	proxyCfg    config.ProxyConfig
	skipVerify  bool
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

// SetVerifySSL controls whether upstream TLS certificates are verified.
// When verify is false, TLS verification is skipped (InsecureSkipVerify=true).
func SetVerifySSL(verify bool) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	skipVerify = !verify
}

// GetClientWithTimeout returns an HTTP client with the given timeout.
// If proxy is configured, it inherits the proxy transport.
// If verify_ssl is false, TLS certificate verification is skipped.
func GetClientWithTimeout(timeout time.Duration) *http.Client {
	proxyMu.RLock()
	defer proxyMu.RUnlock()

	var transport *http.Transport
	if skipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	if proxyClient != nil {
		if transport == nil {
			transport = &http.Transport{}
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return proxyClient.Transport.(*http.Transport).DialContext(ctx, network, addr)
		}
		c := &http.Client{
			Transport: transport,
			Timeout:   timeout,
		}
		return c
	}
	if transport != nil {
		return &http.Client{Transport: transport, Timeout: timeout}
	}
	return &http.Client{Timeout: timeout}
}

// IsEnabled returns true if proxy is configured and enabled
func IsEnabled() bool {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	return proxyCfg.Enabled && proxyClient != nil
}
