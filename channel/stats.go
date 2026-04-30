package channel

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
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
	Name       string
	InputTokens  int64
	OutputTokens int64
	Requests   int64
	ModelStats map[string]*ModelStat
}

type StatsManager struct {
	mu           sync.Mutex
	keyStats     map[string]*KeyStat
	windowReqs   int64
	interval     time.Duration
	countTrigger int
	stopCh       chan struct{}
	startTime    time.Time
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
		startTime:    time.Now(),
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
	sm.windowReqs++
	currentReqs := sm.windowReqs
	sm.mu.Unlock()

	if sm.countTrigger > 0 && currentReqs >= int64(sm.countTrigger) {
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

	upTime := time.Since(sm.startTime).Truncate(time.Second)
	trigger := fmt.Sprintf("uptime:%s", upTime)

	// Collect rows
	type row struct {
		key    string
		model  string
		reqs   int64
		inTok  int64
		outTok int64
	}
	var rows []row
	var totalInput, totalOutput, totalReqs int64

	sortKeys := make([]string, 0, len(sm.keyStats))
	for name := range sm.keyStats {
		sortKeys = append(sortKeys, name)
	}
	sort.Strings(sortKeys)

	for _, name := range sortKeys {
		s := sm.keyStats[name]
		rows = append(rows, row{key: s.Name, reqs: s.Requests, inTok: s.InputTokens, outTok: s.OutputTokens})

		if len(s.ModelStats) > 0 {
			modelKeys := make([]string, 0, len(s.ModelStats))
			for m := range s.ModelStats {
				modelKeys = append(modelKeys, m)
			}
			sort.Strings(modelKeys)

			for _, m := range modelKeys {
				ms := s.ModelStats[m]
				rows = append(rows, row{model: ms.Model, reqs: ms.Requests, inTok: ms.InputTokens, outTok: ms.OutputTokens})
			}
		}

		totalInput += s.InputTokens
		totalOutput += s.OutputTokens
		totalReqs += s.Requests
	}

	// Calculate column widths
	maxKey := 20
	maxModel := 25
	for _, r := range rows {
		if r.key != "" {
			l := len(fmt.Sprintf("api_key:%s", r.key))
			if l > maxKey {
				maxKey = l
			}
		}
		if r.model != "" {
			l := len(fmt.Sprintf("model:%s", r.model))
			if l > maxModel {
				maxModel = l
			}
		}
	}

	// Build table
	b := &strings.Builder{}
	b.WriteString(fmt.Sprintf("\n[stats] === %s ===\n", trigger))

	border := fmt.Sprintf("[stats] +-%s+-%s+--------+------------+-------------+\n",
		strings.Repeat("-", maxKey), strings.Repeat("-", maxModel))
	b.WriteString(border)
	b.WriteString(fmt.Sprintf("[stats] |-%s|-%s| %6s | %10s | %11s |\n",
		strings.Repeat("-", maxKey), strings.Repeat("-", maxModel), "reqs", "in", "out"))
	b.WriteString(border)

	for _, r := range rows {
		if r.key != "" {
			b.WriteString(fmt.Sprintf("[stats] |%-*s| %-*s | %6d | %10d | %11d |\n",
				maxKey, "api_key:"+r.key, maxModel, "", r.reqs, r.inTok, r.outTok))
		} else {
			b.WriteString(fmt.Sprintf("[stats] | %-*s| %-*s | %6d | %10d | %11d |\n",
				maxKey, "", maxModel, "model:"+r.model, r.reqs, r.inTok, r.outTok))
		}
	}

	b.WriteString(border)
	b.WriteString(fmt.Sprintf("[stats] | %-*s| %-*s | %6d | %10d | %11d |\n",
		maxKey, "(total)", maxModel, "", totalReqs, totalInput, totalOutput))
	b.WriteString(border)
	b.WriteString("\n")

	log.Print(b.String())

	// Reset
	sm.keyStats = make(map[string]*KeyStat)
	sm.windowReqs = 0
}
