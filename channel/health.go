package channel

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/llm-proxy/config"
)

type HealthStatus string

const (
	Healthy   HealthStatus = "healthy"
	Unhealthy HealthStatus = "unhealthy"
	Unknown   HealthStatus = "unknown"
)

type ChannelHealth struct {
	Name            string
	Status          HealthStatus
	ConsecutiveFail int
	ConsecutiveOk   int
	LastCheck       time.Time
	LastError       string
	ResponseTime    time.Duration
	mu              sync.RWMutex
}

type HealthChecker struct {
	mu                   sync.RWMutex
	channels             map[string]*ChannelHealth
	interval             time.Duration
	timeout              time.Duration
	unhealthyThreshold   int
	healthyThreshold     int
	stopCh               chan struct{}
}

var healthChecker *HealthChecker

func InitHealthChecker(cfg config.HealthCheckConfig, channels map[string]*config.ChannelConfig) {
	if !cfg.Enabled {
		return
	}

	hc := &HealthChecker{
		channels:           make(map[string]*ChannelHealth),
		unhealthyThreshold: cfg.UnhealthyThreshold,
		healthyThreshold:   cfg.HealthyThreshold,
		stopCh:             make(chan struct{}),
	}

	if cfg.Interval == "" {
		cfg.Interval = "30s"
	}
	d, err := time.ParseDuration(cfg.Interval)
	if err == nil {
		hc.interval = d
	} else {
		hc.interval = 30 * time.Second
	}

	if cfg.Timeout == "" {
		cfg.Timeout = "5s"
	}
	to, err := time.ParseDuration(cfg.Timeout)
	if err == nil {
		hc.timeout = to
	} else {
		hc.timeout = 5 * time.Second
	}

	healthChecker = hc

	// Initialize health status for all channels
	for name, ch := range channels {
		hc.channels[name] = &ChannelHealth{
			Name:    ch.Name,
			Status:  Unknown,
			mu:      sync.RWMutex{},
		}
	}

	if hc.interval > 0 {
		go hc.runTicker()
	}
}

func (hc *HealthChecker) runTicker() {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hc.checkAll()
		case <-hc.stopCh:
			return
		}
	}
}

func (hc *HealthChecker) checkAll() {
	hc.mu.RLock()
	names := make([]string, 0, len(hc.channels))
	for name := range hc.channels {
		names = append(names, name)
	}
	hc.mu.RUnlock()

	for _, name := range names {
		hc.checkChannel(name)
	}
}

func (hc *HealthChecker) checkChannel(name string) {
	hc.mu.RLock()
	chHealth, ok := hc.channels[name]
	hc.mu.RUnlock()

	if !ok {
		return
	}

	chHealth.mu.Lock()
	defer chHealth.mu.Unlock()

	// Use the channel's base_url to make a lightweight health check
	ch := GetByName(name)
	if ch == nil {
		chHealth.Status = Unhealthy
		chHealth.LastError = "channel not found"
		chHealth.LastCheck = time.Now()
		return
	}

	client := &http.Client{Timeout: hc.timeout}
	body := []byte(`{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`)
	req, err := http.NewRequest("POST", ch.BaseURL+"/v1/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		chHealth.Status = Unhealthy
		chHealth.LastError = fmt.Sprintf("failed to create request: %v", err)
		chHealth.LastCheck = time.Now()
		return
	}

	for k, v := range ch.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ch.APIKey)

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		chHealth.ConsecutiveFail++
		chHealth.ConsecutiveOk = 0
		chHealth.Status = Unhealthy
		chHealth.LastError = err.Error()
		chHealth.LastCheck = time.Now()
		chHealth.ResponseTime = elapsed
		log.Printf("[health] channel=%s status=unhealthy error=%s duration=%v", name, err, elapsed)
		return
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		chHealth.ConsecutiveOk++
		chHealth.ConsecutiveFail = 0
		if chHealth.ConsecutiveOk >= hc.healthyThreshold {
			chHealth.Status = Healthy
		}
		chHealth.LastError = ""
	} else {
		chHealth.ConsecutiveFail++
		chHealth.ConsecutiveOk = 0
		chHealth.Status = Unhealthy
		chHealth.LastError = fmt.Sprintf("status code %d", resp.StatusCode)
	}

	chHealth.LastCheck = time.Now()
	chHealth.ResponseTime = elapsed
	log.Printf("[health] channel=%s status=%s consecutive_ok=%d consecutive_fail=%d duration=%v",
		name, chHealth.Status, chHealth.ConsecutiveOk, chHealth.ConsecutiveFail, elapsed)
}

// IsHealthy returns true if the channel is healthy or unknown (not yet checked)
func IsHealthy(name string) bool {
	if healthChecker == nil {
		return true
	}

	hc := healthChecker
	hc.mu.RLock()
	ch, ok := hc.channels[name]
	hc.mu.RUnlock()

	if !ok {
		return true
	}

	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.Status != Unhealthy
}

// GetHealthStatus returns the health status of a channel
func GetHealthStatus(name string) HealthStatus {
	if healthChecker == nil {
		return Unknown
	}

	hc := healthChecker
	hc.mu.RLock()
	ch, ok := hc.channels[name]
	hc.mu.RUnlock()

	if !ok {
		return Unknown
	}

	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.Status
}

// GetAllHealth returns health status of all channels
func GetAllHealth() []map[string]any {
	if healthChecker == nil {
		return nil
	}

	hc := healthChecker
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	result := make([]map[string]any, 0, len(hc.channels))
	for _, ch := range hc.channels {
		ch.mu.RLock()
		result = append(result, map[string]any{
			"name":              ch.Name,
			"status":            ch.Status,
			"consecutive_fail":  ch.ConsecutiveFail,
			"consecutive_ok":    ch.ConsecutiveOk,
			"last_check":        ch.LastCheck,
			"last_error":        ch.LastError,
			"response_time_ms":  ch.ResponseTime.Milliseconds(),
		})
		ch.mu.RUnlock()
	}

	return result
}
