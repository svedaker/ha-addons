package main

import (
	"fmt"
	"log"
	"runtime"
	"runtime/debug"
	"sync"
)

type GoRuntimeStatus struct {
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapInUseBytes uint64 `json:"heap_inuse_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
	StackInUse     uint64 `json:"stack_inuse_bytes"`
	RSSApproxBytes uint64 `json:"rss_approx_bytes"`
	NumGC          uint32 `json:"num_gc"`
	Goroutines     int    `json:"goroutines"`
}

type MemoryLogDecision struct {
	Status              GoRuntimeStatus
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
}

func collectGoRuntimeStatus() GoRuntimeStatus {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	rssApprox := ms.HeapInuse + ms.StackInuse + ms.MSpanInuse + ms.MCacheInuse + ms.GCSys + ms.OtherSys

	return GoRuntimeStatus{
		HeapAllocBytes: ms.HeapAlloc,
		HeapInUseBytes: ms.HeapInuse,
		HeapSysBytes:   ms.HeapSys,
		StackInUse:     ms.StackInuse,
		RSSApproxBytes: rssApprox,
		NumGC:          ms.NumGC,
		Goroutines:     runtime.NumGoroutine(),
	}
}

func evaluateMemoryUsage() MemoryLogDecision {
	status := collectGoRuntimeStatus()
	limitBytes := debug.SetMemoryLimit(-1)
	decision := MemoryLogDecision{
		Status:     status,
		LimitBytes: limitBytes,
	}

	if limitBytes <= 0 {
		return decision
	}

	decision.UsagePct = float64(status.RSSApproxBytes) / float64(limitBytes) * 100
	usageRatio := float64(status.RSSApproxBytes) / float64(limitBytes)

	if usageRatio >= 0.80 {
		decision.Warning = fmt.Sprintf("memory usage is close to GOMEMLIMIT: %.1f%%", decision.UsagePct)
		return decision
	}

	if usageRatio <= 0.35 {
		recommendedMiB := recommendLowerLimitMiB(status.RSSApproxBytes)
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
	status := decision.Status
	base := fmt.Sprintf(
		"memory %s: rss~=%s heap_alloc=%s heap_inuse=%s goroutines=%d gc=%d",
		context,
		formatMiB(status.RSSApproxBytes),
		formatMiB(status.HeapAllocBytes),
		formatMiB(status.HeapInUseBytes),
		status.Goroutines,
		status.NumGC,
	)

	if decision.LimitBytes > 0 {
		base = fmt.Sprintf("%s gomemlimit=%d MiB usage=%.1f%%", base, decision.LimitBytes/(1024*1024), decision.UsagePct)
	}

	log.Println(base)
	logMemoryTransitions(decision)
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