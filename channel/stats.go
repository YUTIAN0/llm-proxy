package channel

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/llm-proxy/config"
)

type KeyStat struct {
	Name         string
	InputTokens  int64
	OutputTokens int64
	Requests     int64
}

type StatsManager struct {
	mu            sync.Mutex
	keyStats      map[string]*KeyStat
	totalRequests int64
	interval      time.Duration
	countTrigger  int
	stopCh        chan struct{}
}

var statsManager *StatsManager

func InitStats(cfg config.StatsConfig) {
	if !cfg.Enabled {
		return
	}

	sm := &StatsManager{
		keyStats:     make(map[string]*KeyStat),
		countTrigger: cfg.RequestCount,
		stopCh:       make(chan struct{}),
	}

	if cfg.Interval != "" {
		d, err := time.ParseDuration(cfg.Interval)
		if err == nil {
			sm.interval = d
		}
	}

	statsManager = sm

	if sm.interval > 0 {
		go sm.runTicker()
	}
}

func RecordStats(keyName string, inputTokens, outputTokens int) {
	if statsManager == nil {
		return
	}

	sm := statsManager

	if keyName == "" {
		keyName = "(anonymous)"
	}

	sm.mu.Lock()
	stat, ok := sm.keyStats[keyName]
	if !ok {
		stat = &KeyStat{Name: keyName}
		sm.keyStats[keyName] = stat
	}
	stat.InputTokens += int64(inputTokens)
	stat.OutputTokens += int64(outputTokens)
	stat.Requests++
	sm.mu.Unlock()

	newTotal := atomic.AddInt64(&sm.totalRequests, 1)

	if sm.countTrigger > 0 && newTotal%int64(sm.countTrigger) == 0 {
		sm.printAndReset()
	}
}

func (sm *StatsManager) runTicker() {
	ticker := time.NewTicker(sm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.printAndReset()
		case <-sm.stopCh:
			return
		}
	}
}

func (sm *StatsManager) printAndReset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.keyStats) == 0 {
		return
	}

	var triggers []string
	if sm.interval > 0 {
		triggers = append(triggers, fmt.Sprintf("interval:%s", sm.interval))
	}
	if sm.countTrigger > 0 {
		triggers = append(triggers, fmt.Sprintf("request_count:%d", sm.countTrigger))
	}

	trigger := "manual"
	if len(triggers) > 0 {
		trigger = triggers[0]
	}

	var totalInput, totalOutput, totalReqs int64

	sortKeys := make([]string, 0, len(sm.keyStats))
	for name := range sm.keyStats {
		sortKeys = append(sortKeys, name)
	}
	sort.Strings(sortKeys)

	log.Printf("[stats] === %s ===", trigger)

	for _, name := range sortKeys {
		s := sm.keyStats[name]
		log.Printf("[stats]   api_key:%-20s | requests=%6d | input_tokens=%10d | output_tokens=%10d",
			s.Name, s.Requests, s.InputTokens, s.OutputTokens)
		totalInput += s.InputTokens
		totalOutput += s.OutputTokens
		totalReqs += s.Requests
	}

	log.Printf("[stats]   %-23s | requests=%6d | input_tokens=%10d | output_tokens=%10d",
		"(total)", totalReqs, totalInput, totalOutput)

	// Reset
	sm.keyStats = make(map[string]*KeyStat)
}
