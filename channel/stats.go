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

type ModelStat struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	Requests     int64
}

type KeyStat struct {
	Name         string
	InputTokens  int64
	OutputTokens int64
	Requests     int64
	ModelStats   map[string]*ModelStat
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

func RecordStats(keyName, model string, inputTokens, outputTokens int) {
	if statsManager == nil {
		return
	}

	sm := statsManager

	if keyName == "" {
		keyName = "(anonymous)"
	}
	if model == "" {
		model = "(unknown)"
	}

	sm.mu.Lock()
	stat, ok := sm.keyStats[keyName]
	if !ok {
		stat = &KeyStat{Name: keyName, ModelStats: make(map[string]*ModelStat)}
		sm.keyStats[keyName] = stat
	}
	stat.InputTokens += int64(inputTokens)
	stat.OutputTokens += int64(outputTokens)
	stat.Requests++

	ms, ok := stat.ModelStats[model]
	if !ok {
		ms = &ModelStat{Model: model}
		stat.ModelStats[model] = ms
	}
	ms.InputTokens += int64(inputTokens)
	ms.OutputTokens += int64(outputTokens)
	ms.Requests++
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

		if len(s.ModelStats) > 0 {
			modelKeys := make([]string, 0, len(s.ModelStats))
			for m := range s.ModelStats {
				modelKeys = append(modelKeys, m)
			}
			sort.Strings(modelKeys)

			for _, m := range modelKeys {
				ms := s.ModelStats[m]
				log.Printf("[stats]     model:%-25s | requests=%6d | input_tokens=%10d | output_tokens=%10d",
					ms.Model, ms.Requests, ms.InputTokens, ms.OutputTokens)
			}
		}

		totalInput += s.InputTokens
		totalOutput += s.OutputTokens
		totalReqs += s.Requests
	}

	log.Printf("[stats]   %-23s | requests=%6d | input_tokens=%10d | output_tokens=%10d",
		"(total)", totalReqs, totalInput, totalOutput)

	// Reset
	sm.keyStats = make(map[string]*KeyStat)
}
