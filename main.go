package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/llm-proxy/channel"
	"github.com/llm-proxy/config"
	"github.com/llm-proxy/proxy"
	"github.com/llm-proxy/relay"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	channel.Init(cfg)

	// Initialize proxy if configured
	proxy.Init(cfg.Proxy)
	if proxy.IsEnabled() {
		log.Printf("proxy enabled: socks5://%s", cfg.Proxy.Addr)
	}

	r := gin.Default()

	// OpenAI compatible and Claude/Gemini relay handlers
	r.POST("/v1/*path", relay.RelayHandler)
	r.POST("/v1beta/*path", relay.RelayHandler)
	r.GET("/v1/*path", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.GET("/v1beta/*path", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	addr := ":8080"
	if cfg.Server.Port > 0 {
		addr = fmt.Sprintf(":%d", cfg.Server.Port)
	}

	log.Printf("starting server on %s", addr)
	if cfg.Server.TLS.Cert != "" && cfg.Server.TLS.Key != "" {
		log.Fatal(r.RunTLS(addr, cfg.Server.TLS.Cert, cfg.Server.TLS.Key))
	} else {
		log.Fatal(r.Run(addr))
	}
}