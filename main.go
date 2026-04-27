package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

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

	// GET /v1/models and GET /v1/models/:model
	r.GET("/v1/*path", func(c *gin.Context) {
		fullPath := c.Param("path")
		clientAPIKey := extractAPIKey(c)
		if clientAPIKey != "" && !channel.IsKeyRecognized(clientAPIKey) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		// GET /v1/models - list models
		if fullPath == "/models" || fullPath == "/models/" {
			models := channel.GetAllChannelModels()
			if clientAPIKey != "" {
				ak, ok := channel.ValidateAPIKey(clientAPIKey)
				if ok {
					models = channel.GetAllowedModelsWithAliases(ak)
				}
			}
			data := make([]map[string]any, 0, len(models))
			for _, m := range models {
				data = append(data, map[string]any{"id": m, "object": "model"})
			}
			c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
			return
		}

		// GET /v1/models/:model - get single model info
		if strings.HasPrefix(fullPath, "/models/") {
			modelName := strings.TrimPrefix(fullPath, "/models/")
			if modelName != "" {
				resolved := channel.ResolveModelAlias(modelName)
				result := gin.H{
					"id":       modelName,
					"object":   "model",
					"owned_by": "llm-proxy",
				}
				if resolved != modelName {
					result["alias_for"] = resolved
				}
				c.JSON(http.StatusOK, result)
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// GET /v1beta/models - Gemini-style model list
	r.GET("/v1beta/*path", func(c *gin.Context) {
		fullPath := c.Param("path")
		clientAPIKey := extractAPIKey(c)
		if clientAPIKey != "" && !channel.IsKeyRecognized(clientAPIKey) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		if fullPath == "/models" || fullPath == "/models/" {
			models := channel.GetAllChannelModels()
			if clientAPIKey != "" {
				ak, ok := channel.ValidateAPIKey(clientAPIKey)
				if ok {
					models = channel.GetAllowedModelsWithAliases(ak)
				}
			}
			modelsList := make([]map[string]any, 0, len(models))
			for _, m := range models {
				modelsList = append(modelsList, map[string]any{
					"name":         fmt.Sprintf("models/%s", m),
					"display_name": m,
				})
			}
			c.JSON(http.StatusOK, gin.H{"models": modelsList})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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

func extractAPIKey(c *gin.Context) string {
	key := c.GetHeader("X-API-Key")
	if key == "" {
		key = c.GetHeader("x-api-key")
	}
	if key == "" {
		key = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	if key == "" {
		key = c.GetHeader("X-Goog-Api-Key")
	}
	if key == "" {
		key = c.GetHeader("x-goog-api-key")
	}
	return key
}
