package main

import (
	"fmt"
	"log"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

const (
	memorySnapshotInterval      = 15 * time.Minute
	memorySnapshotUsageDeltaPct = 5.0
)

type MemoryLogDecision struct {
	RSSApproxBytes      uint64
	HeapAllocBytes      uint64
	HeapInUseBytes      uint64
	NumGC               uint32
	Goroutines          int
	LimitBytes          int64
	UsagePct            float64
	Warning             string
	Recommendation      string
	RecommendedLimitMiB int64
	HasRecommendation   bool
}

var memoryLogState struct {
	mu                 sync.Mutex
	lastWarning        string
	lastRecommendation string
	lastSnapshotAt     time.Time
	lastSnapshotUsage  float64
	hasSnapshot        bool
}

func evaluateMemoryUsage() MemoryLogDecision {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	rssApprox := ms.HeapInuse + ms.StackInuse + ms.MSpanInuse + ms.MCacheInuse + ms.GCSys + ms.OtherSys
	limitBytes := debug.SetMemoryLimit(-1)
	decision := MemoryLogDecision{
		RSSApproxBytes: rssApprox,
		HeapAllocBytes: ms.HeapAlloc,
		HeapInUseBytes: ms.HeapInuse,
		NumGC:          ms.NumGC,
		Goroutines:     runtime.NumGoroutine(),
		LimitBytes:     limitBytes,
	}

	if limitBytes <= 0 {
		return decision
	}

	decision.UsagePct = float64(rssApprox) / float64(limitBytes) * 100
	usageRatio := float64(rssApprox) / float64(limitBytes)

	if usageRatio >= 0.80 {
		decision.Warning = fmt.Sprintf("memory usage is close to GOMEMLIMIT: %.1f%%", decision.UsagePct)
		return decision
	}

	if usageRatio <= 0.35 {
		recommendedMiB := recommendLowerLimitMiB(rssApprox)
		currentMiB := limitBytes / (1024 * 1024)
		if recommendedMiB > 0 && recommendedMiB < currentMiB {
			decision.HasRecommendation = true
			decision.RecommendedLimitMiB = recommendedMiB
			decision.Recommendation = fmt.Sprintf("memory usage is consistently low relative to GOMEMLIMIT; %d MiB would likely be enough", recommendedMiB)
		}
	}

	return decision
}

func recommendLowerLimitMiB(rssApproxBytes uint64) int64 {
	if rssApproxBytes == 0 {
		return 0
	}

	const mib = uint64(1024 * 1024)
	recommendedBytes := rssApproxBytes * 3 / 2
	recommendedMiB := int64((recommendedBytes + mib - 1) / mib)

	if recommendedMiB < 32 {
		recommendedMiB = 32
	}

	step := int64(16)
	if remainder := recommendedMiB % step; remainder != 0 {
		recommendedMiB += step - remainder
	}

	return recommendedMiB
}

func logMemoryUsage(context string) {
	decision := evaluateMemoryUsage()
	if shouldLogMemorySnapshot(decision) {
		base := fmt.Sprintf(
			"memory %s: rss~=%s heap_alloc=%s heap_inuse=%s goroutines=%d gc=%d",
			context,
			formatMiB(decision.RSSApproxBytes),
			formatMiB(decision.HeapAllocBytes),
			formatMiB(decision.HeapInUseBytes),
			decision.Goroutines,
			decision.NumGC,
		)

		if decision.LimitBytes > 0 {
			base = fmt.Sprintf("%s gomemlimit=%d MiB usage=%.1f%%", base, decision.LimitBytes/(1024*1024), decision.UsagePct)
		}

		log.Println(base)
	}

	logMemoryTransitions(decision)
}

func shouldLogMemorySnapshot(decision MemoryLogDecision) bool {
	memoryLogState.mu.Lock()
	defer memoryLogState.mu.Unlock()

	now := time.Now()
	if !memoryLogState.hasSnapshot {
		memoryLogState.lastSnapshotAt = now
		memoryLogState.lastSnapshotUsage = decision.UsagePct
		memoryLogState.hasSnapshot = true
		return true
	}

	shouldLog := false
	if decision.Warning != "" {
		shouldLog = true
	} else if now.Sub(memoryLogState.lastSnapshotAt) >= memorySnapshotInterval {
		shouldLog = true
	} else {
		delta := decision.UsagePct - memoryLogState.lastSnapshotUsage
		if delta < 0 {
			delta = -delta
		}
		if delta >= memorySnapshotUsageDeltaPct {
			shouldLog = true
		}
	}

	if shouldLog {
		memoryLogState.lastSnapshotAt = now
		memoryLogState.lastSnapshotUsage = decision.UsagePct
	}

	return shouldLog
}

func logMemoryTransitions(decision MemoryLogDecision) {
	memoryLogState.mu.Lock()
	defer memoryLogState.mu.Unlock()

	if decision.Warning != memoryLogState.lastWarning {
		if decision.Warning != "" {
			log.Printf("memory warning: %s", decision.Warning)
		}
		memoryLogState.lastWarning = decision.Warning
	}

	currentRecommendation := ""
	if decision.HasRecommendation {
		currentRecommendation = decision.Recommendation
	}
	if currentRecommendation != memoryLogState.lastRecommendation {
		if currentRecommendation != "" {
			log.Printf("memory recommendation: %s", currentRecommendation)
		}
		memoryLogState.lastRecommendation = currentRecommendation
	}
}

func formatMiB(bytes uint64) string {
	return fmt.Sprintf("%.1f MiB", float64(bytes)/(1024*1024))
}
