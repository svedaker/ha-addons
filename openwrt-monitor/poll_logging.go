package main

import (
	"sync"
	"time"
)

const slowPollThreshold = 3 * time.Second

type wanLogSnapshot struct {
	Up       bool
	Device   string
	PublicIP string
}

type pollSummarySnapshot struct {
	Clients int
	APs     int
	Leases  int
	Slow    bool
}

var pollLogState struct {
	mu          sync.Mutex
	dhcpLeases  map[string]int
	hostHints   map[string]int
	wan         map[string]wanLogSnapshot
	pollSummary pollSummarySnapshot
	hasSummary  bool
}

func init() {
	pollLogState.dhcpLeases = make(map[string]int)
	pollLogState.hostHints = make(map[string]int)
	pollLogState.wan = make(map[string]wanLogSnapshot)
}

func shouldLogLeaseCount(routerName string, count int) bool {
	pollLogState.mu.Lock()
	defer pollLogState.mu.Unlock()

	lastCount, ok := pollLogState.dhcpLeases[routerName]
	pollLogState.dhcpLeases[routerName] = count
	return !ok || lastCount != count
}

func shouldLogHostHintCount(routerName string, count int) bool {
	pollLogState.mu.Lock()
	defer pollLogState.mu.Unlock()

	lastCount, ok := pollLogState.hostHints[routerName]
	pollLogState.hostHints[routerName] = count
	return !ok || lastCount != count
}

func shouldLogWANStatus(routerName string, up bool, device string, publicIP string) bool {
	pollLogState.mu.Lock()
	defer pollLogState.mu.Unlock()

	current := wanLogSnapshot{
		Up:       up,
		Device:   device,
		PublicIP: publicIP,
	}
	last, ok := pollLogState.wan[routerName]
	pollLogState.wan[routerName] = current
	if !ok {
		return false
	}
	return last != current
}

func shouldLogPollSummary(duration time.Duration, clients int, aps int, leases int) bool {
	pollLogState.mu.Lock()
	defer pollLogState.mu.Unlock()

	current := pollSummarySnapshot{
		Clients: clients,
		APs:     aps,
		Leases:  leases,
		Slow:    duration >= slowPollThreshold,
	}

	if !pollLogState.hasSummary {
		pollLogState.pollSummary = current
		pollLogState.hasSummary = true
		return true
	}

	last := pollLogState.pollSummary
	pollLogState.pollSummary = current

	if current.Clients != last.Clients || current.APs != last.APs || current.Leases != last.Leases {
		return true
	}

	return current.Slow != last.Slow
}
