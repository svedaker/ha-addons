package main

import (
	"strings"
	"sync"
	"time"
)

// Client represents a tracked WiFi client device.
type Client struct {
	MAC            string    `json:"mac"`
	Hostname       string    `json:"hostname"`
	IP             string    `json:"ip"`
	AP             string    `json:"ap"`
	Band           string    `json:"band"`
	Signal         int       `json:"signal"`
	SignalAvg      int       `json:"signal_avg"`
	ConnectedTime  int       `json:"connected_time"`
	ConnectedSince time.Time `json:"connected_since"`
	LastSeen       time.Time `json:"last_seen"`
	LastAP         string    `json:"last_ap"`
	Location       string    `json:"location"`
	RxRate         int       `json:"rx_rate"`
	TxRate         int       `json:"tx_rate"`
	RxBytes        int64     `json:"rx_bytes"`
	TxBytes        int64     `json:"tx_bytes"`
}

// APState represents the current state of an access point.
type APState struct {
	Host              string    `json:"host"`
	Name              string    `json:"name"`
	Status            string    `json:"status"` // "online" or "offline"
	Uptime            int       `json:"uptime"`
	Load              float64   `json:"load"`
	MemAvailableKB    int       `json:"memory_available_kb"`
	Clients5G         int       `json:"clients_5g"`
	Clients2G         int       `json:"clients_2g"`
	RxBytes5G         int64     `json:"rx_bytes_5g"`
	TxBytes5G         int64     `json:"tx_bytes_5g"`
	RxBytes2G         int64     `json:"rx_bytes_2g"`
	TxBytes2G         int64     `json:"tx_bytes_2g"`
	RxRate5G          float64   `json:"rx_rate_mbps_5g"`
	TxRate5G          float64   `json:"tx_rate_mbps_5g"`
	RxRate2G          float64   `json:"rx_rate_mbps_2g"`
	TxRate2G          float64   `json:"tx_rate_mbps_2g"`
	Channel5G         int       `json:"channel_5g"`
	Channel2G         int       `json:"channel_2g"`
	Noise5G           int       `json:"noise_5g"`
	Noise2G           int       `json:"noise_2g"`
	Airtime5G         float64   `json:"airtime_5g_pct"`
	Airtime2G         float64   `json:"airtime_2g_pct"`
	LastPoll          time.Time `json:"last_poll"`
	// Previous survey counters for delta calculation
	prevBusy5G   int64
	prevActive5G int64
	prevBusy2G   int64
	prevActive2G int64
	prevRxBytes5G int64
	prevTxBytes5G int64
	prevRxBytes2G int64
	prevTxBytes2G int64
	prevPollTime  time.Time
}

// WANState holds WAN interface status and traffic counters.
type WANState struct {
	Up           bool      `json:"up"`
	Uptime       int       `json:"uptime"`
	Device       string    `json:"device"`
	PublicIP     string    `json:"public_ip"`
	RxBytes      int64     `json:"rx_bytes"`
	TxBytes      int64     `json:"tx_bytes"`
	RxRateMbps   float64   `json:"rx_rate_mbps"`
	TxRateMbps   float64   `json:"tx_rate_mbps"`
	LastPoll     time.Time `json:"last_poll"`
	// previous counters for rate calculation
	prevRxBytes  int64
	prevTxBytes  int64
	prevPollTime time.Time
}

// RouterState holds router system metrics.
type RouterState struct {
	Name           string    `json:"name"`
	Host           string    `json:"host"`
	Status         string    `json:"status"` // "online" or "offline"
	Uptime         int       `json:"uptime"`
	Load           float64   `json:"load"`
	MemAvailableKB int       `json:"memory_available_kb"`
	LastPoll       time.Time `json:"last_poll"`
}

// State holds all tracked clients and AP states.
type State struct {
	mu               sync.RWMutex
	clients          map[string]*Client           // keyed by MAC
	aps              map[string]*APState          // keyed by AP name
	router           *RouterState
	wan              *WANState
	monitoredDevices map[string]*MonitoredDevice  // keyed by MAC (lowercase)
	lastPoll         time.Time
	startTime        time.Time
	dhcpLeases       map[string]DHCPLease // keyed by MAC (lowercase)
	hostHints        map[string]HostHint  // keyed by MAC (lowercase), from getHostHints
}

// DHCPLease holds hostname + IP from the router.
type DHCPLease struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ipaddr"`
	MAC      string `json:"macaddr"`
}

// HostHint holds hostname + IPs from luci-rpc getHostHints.
// Covers devices with static IPs that don't appear in DHCP leases.
type HostHint struct {
	Name    string   `json:"name"`
	IPAddrs []string `json:"ipaddrs"`
}

// MonitoredDevice tracks online/offline state for a configured device.
type MonitoredDevice struct {
	MAC         string    `json:"mac"`
	Name        string    `json:"name"`
	Online      bool      `json:"online"`
	IP          string    `json:"ip"`
	OnlineSince time.Time `json:"online_since"`
	LastSeen    time.Time `json:"last_seen"`
	OfflineSince time.Time `json:"offline_since,omitempty"`
}

// NewState creates an empty state.
func NewState() *State {
	return &State{
		clients:          make(map[string]*Client),
		aps:              make(map[string]*APState),
		router:           &RouterState{Status: "offline"},
		wan:              &WANState{},
		monitoredDevices: make(map[string]*MonitoredDevice),
		dhcpLeases:       make(map[string]DHCPLease),
		hostHints:        make(map[string]HostHint),
		startTime:        time.Now(),
	}
}

// UpdateRouterSystem updates router system metrics.
func (s *State) UpdateRouterSystem(name string, host string, uptime int, load float64, memAvailableKB int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.router
	r.Name = name
	r.Host = host
	r.Status = "online"
	r.Uptime = uptime
	r.Load = load
	r.MemAvailableKB = memAvailableKB
	r.LastPoll = now
}

// MarkRouterOffline marks the router as offline.
func (s *State) MarkRouterOffline(name string, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.router
	r.Name = name
	r.Host = host
	r.Status = "offline"
}

// GetRouter returns a copy of the router state.
func (s *State) GetRouter() RouterState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.router
}

// UpdateDHCPLeases replaces the DHCP lease table.
func (s *State) UpdateDHCPLeases(leases []DHCPLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dhcpLeases = make(map[string]DHCPLease, len(leases))
	for _, l := range leases {
		s.dhcpLeases[normMAC(l.MAC)] = l
	}
}

// UpdateHostHints replaces the host hints table.
func (s *State) UpdateHostHints(hints map[string]HostHint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hostHints = make(map[string]HostHint, len(hints))
	for mac, h := range hints {
		s.hostHints[normMAC(mac)] = h
	}
}

// UpdateClients processes a scan result from one AP.
// seenMACs is the set of MACs seen in this cycle (across all APs).
func (s *State) UpdateClients(apName string, clients []ScannedClient, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sc := range clients {
		mac := normMAC(sc.MAC)
		existing, found := s.clients[mac]

		// Resolve hostname/IP from DHCP lease, then host hints fallback
		hostname := mac // fallback: normalized lowercase MAC
		ip := ""
		if lease, ok := s.dhcpLeases[mac]; ok {
			if lease.Hostname != "" {
				hostname = lease.Hostname
			}
			ip = lease.IP
		} else if hint, ok := s.hostHints[mac]; ok {
			// Use host hints for devices with static IPs
			if hint.Name != "" {
				hostname = stripDomain(hint.Name)
			}
			if len(hint.IPAddrs) > 0 {
				ip = hint.IPAddrs[0]
			}
		}

		if !found {
			// New client
			s.clients[mac] = &Client{
				MAC:            mac,
				Hostname:       hostname,
				IP:             ip,
				AP:             apName,
				Band:           sc.Band,
				Signal:         sc.Signal,
				SignalAvg:      sc.SignalAvg,
				ConnectedTime:  sc.ConnectedTime,
				ConnectedSince: now,
				LastSeen:       now,
				LastAP:         apName,
				Location:       "home",
				RxRate:         sc.RxRate,
				TxRate:         sc.TxRate,
				RxBytes:        sc.RxBytes,
				TxBytes:        sc.TxBytes,
			}
		} else {
			// Existing client
			if existing.Location == "not_home" {
				// Coming back — new session
				existing.ConnectedSince = now
			}
			existing.Hostname = hostname
			existing.IP = ip
			existing.AP = apName
			existing.Band = sc.Band
			existing.Signal = sc.Signal
			existing.SignalAvg = sc.SignalAvg
			existing.ConnectedTime = sc.ConnectedTime
			existing.LastSeen = now
			existing.LastAP = apName
			existing.Location = "home"
			existing.RxRate = sc.RxRate
			existing.TxRate = sc.TxRate
			existing.RxBytes = sc.RxBytes
			existing.TxBytes = sc.TxBytes
		}
	}
}

// MarkAbsent marks clients as not_home if they haven't been seen within gracePeriod.
func (s *State) MarkAbsent(seenMACs map[string]bool, gracePeriod time.Duration, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for mac, c := range s.clients {
		if seenMACs[mac] {
			continue
		}
		if c.Location == "home" && now.Sub(c.LastSeen) > gracePeriod {
			c.Location = "not_home"
			c.AP = ""
			c.Band = ""
			c.Signal = 0
			c.SignalAvg = 0
			c.ConnectedTime = 0
			c.RxRate = 0
			c.TxRate = 0
		}
	}
}

// UpdateAPState updates the state for an AP.
func (s *State) UpdateAPState(name string, info APInfo, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ap, found := s.aps[name]
	if !found {
		ap = &APState{Name: name}
		s.aps[name] = ap
	}

	ap.Status = "online"
	ap.Uptime = info.Uptime
	ap.Load = info.Load
	ap.MemAvailableKB = info.MemAvailableKB
	ap.Clients5G = info.Clients5G
	ap.Clients2G = info.Clients2G
	ap.RxBytes5G = info.RxBytes5G
	ap.TxBytes5G = info.TxBytes5G
	ap.RxBytes2G = info.RxBytes2G
	ap.TxBytes2G = info.TxBytes2G
	ap.Channel5G = info.Channel5G
	ap.Channel2G = info.Channel2G
	ap.Noise5G = info.Noise5G
	ap.Noise2G = info.Noise2G
	ap.LastPoll = now

	if !ap.prevPollTime.IsZero() {
		elapsed := now.Sub(ap.prevPollTime).Seconds()
		if elapsed > 0 {
			if info.RxBytes5G >= ap.prevRxBytes5G {
				ap.RxRate5G = float64(info.RxBytes5G-ap.prevRxBytes5G) * 8 / elapsed / 1e6
			}
			if info.TxBytes5G >= ap.prevTxBytes5G {
				ap.TxRate5G = float64(info.TxBytes5G-ap.prevTxBytes5G) * 8 / elapsed / 1e6
			}
			if info.RxBytes2G >= ap.prevRxBytes2G {
				ap.RxRate2G = float64(info.RxBytes2G-ap.prevRxBytes2G) * 8 / elapsed / 1e6
			}
			if info.TxBytes2G >= ap.prevTxBytes2G {
				ap.TxRate2G = float64(info.TxBytes2G-ap.prevTxBytes2G) * 8 / elapsed / 1e6
			}
		}
	}

	// Calculate airtime delta
	if ap.prevActive5G > 0 && info.AirtimeActive5G > ap.prevActive5G {
		activeDelta := info.AirtimeActive5G - ap.prevActive5G
		busyDelta := info.AirtimeBusy5G - ap.prevBusy5G
		if activeDelta > 0 {
			ap.Airtime5G = float64(busyDelta) / float64(activeDelta) * 100
		}
	}
	if ap.prevActive2G > 0 && info.AirtimeActive2G > ap.prevActive2G {
		activeDelta := info.AirtimeActive2G - ap.prevActive2G
		busyDelta := info.AirtimeBusy2G - ap.prevBusy2G
		if activeDelta > 0 {
			ap.Airtime2G = float64(busyDelta) / float64(activeDelta) * 100
		}
	}

	ap.prevActive5G = info.AirtimeActive5G
	ap.prevBusy5G = info.AirtimeBusy5G
	ap.prevActive2G = info.AirtimeActive2G
	ap.prevBusy2G = info.AirtimeBusy2G
	ap.prevRxBytes5G = info.RxBytes5G
	ap.prevTxBytes5G = info.TxBytes5G
	ap.prevRxBytes2G = info.RxBytes2G
	ap.prevTxBytes2G = info.TxBytes2G
	ap.prevPollTime = now
}

// MarkAPOffline marks an AP as offline.
func (s *State) MarkAPOffline(name string, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ap, found := s.aps[name]
	if !found {
		ap = &APState{Name: name, Host: host}
		s.aps[name] = ap
	}
	ap.Status = "offline"
}

// UpdateWAN updates the WAN state with new data and calculates rates.
func (s *State) UpdateWAN(up bool, uptime int, device string, publicIP string, rxBytes int64, txBytes int64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w := s.wan
	w.Up = up
	w.Uptime = uptime
	w.Device = device
	w.PublicIP = publicIP
	w.RxBytes = rxBytes
	w.TxBytes = txBytes

	// Calculate rates (Mbps) from delta
	if !w.prevPollTime.IsZero() && rxBytes >= w.prevRxBytes && txBytes >= w.prevTxBytes {
		elapsed := now.Sub(w.prevPollTime).Seconds()
		if elapsed > 0 {
			w.RxRateMbps = float64(rxBytes-w.prevRxBytes) * 8 / elapsed / 1e6
			w.TxRateMbps = float64(txBytes-w.prevTxBytes) * 8 / elapsed / 1e6
		}
	}

	w.prevRxBytes = rxBytes
	w.prevTxBytes = txBytes
	w.prevPollTime = now
	w.LastPoll = now
}

// GetWAN returns a copy of the WAN state.
func (s *State) GetWAN() WANState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.wan
}

// InitMonitoredDevices sets up the monitored devices from config.
func (s *State) InitMonitoredDevices(devices []MonitoredDevCfg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range devices {
		mac := normMAC(d.MAC)
		s.monitoredDevices[mac] = &MonitoredDevice{
			MAC:  mac,
			Name: d.Name,
		}
	}
}

// UpdateMonitoredDevices checks host hints + DHCP leases for each monitored device.
func (s *State) UpdateMonitoredDevices(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for mac, dev := range s.monitoredDevices {
		// Check if device is visible in host hints or DHCP leases
		ip := ""
		seen := false

		if hint, ok := s.hostHints[mac]; ok && len(hint.IPAddrs) > 0 {
			ip = hint.IPAddrs[0]
			seen = true
		}
		if lease, ok := s.dhcpLeases[mac]; ok && lease.IP != "" {
			ip = lease.IP
			seen = true
		}

		if seen {
			dev.IP = ip
			dev.LastSeen = now
			if !dev.Online {
				// Transition: offline → online
				dev.Online = true
				dev.OnlineSince = now
				dev.OfflineSince = time.Time{}
			}
		} else {
			if dev.Online {
				// Transition: online → offline
				dev.Online = false
				dev.OfflineSince = now
			}
		}
	}
}

// AllMonitoredDevices returns a snapshot of all monitored devices.
func (s *State) AllMonitoredDevices() map[string]*MonitoredDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*MonitoredDevice, len(s.monitoredDevices))
	for k, v := range s.monitoredDevices {
		d := *v
		result[k] = &d
	}
	return result
}

// SetLastPoll records the time of the last poll cycle.
func (s *State) SetLastPoll(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastPoll = t
}

// Snapshot returns a copy of the current state for JSON serialization.
func (s *State) Snapshot() StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clients := make(map[string]*Client, len(s.clients))
	for k, v := range s.clients {
		c := *v
		clients[k] = &c
	}

	aps := make(map[string]*APState, len(s.aps))
	for k, v := range s.aps {
		a := *v
		aps[k] = &a
	}

	totalWiFi := 0
	for _, ap := range s.aps {
		totalWiFi += ap.Clients5G + ap.Clients2G
	}

	wanCopy := *s.wan
	routerCopy := *s.router

	monDevs := make(map[string]*MonitoredDevice, len(s.monitoredDevices))
	for k, v := range s.monitoredDevices {
		d := *v
		monDevs[k] = &d
	}

	return StateSnapshot{
		Uptime:           time.Since(s.startTime).Round(time.Second).String(),
		LastPoll:         s.lastPoll,
		APs:              aps,
		Router:           &routerCopy,
		Clients:          clients,
		DHCPLeases:       len(s.dhcpLeases),
		TotalWiFiClients: totalWiFi,
		WAN:              &wanCopy,
		MonitoredDevices: monDevs,
	}
}

// AllClients returns a snapshot of all clients (for MQTT publishing).
func (s *State) AllClients() map[string]*Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*Client, len(s.clients))
	for k, v := range s.clients {
		c := *v
		result[k] = &c
	}
	return result
}

// AllAPs returns a snapshot of all AP states (for MQTT publishing).
func (s *State) AllAPs() map[string]*APState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*APState, len(s.aps))
	for k, v := range s.aps {
		a := *v
		result[k] = &a
	}
	return result
}

// StateSnapshot is the JSON-serializable status output.
type StateSnapshot struct {
	Uptime           string                       `json:"uptime"`
	LastPoll         time.Time                    `json:"last_poll"`
	APs              map[string]*APState          `json:"aps"`
	Router           *RouterState                 `json:"router"`
	Clients          map[string]*Client           `json:"clients"`
	DHCPLeases       int                          `json:"dhcp_leases"`
	TotalWiFiClients int                          `json:"total_wifi_clients"`
	WAN              *WANState                    `json:"wan"`
	MonitoredDevices map[string]*MonitoredDevice  `json:"monitored_devices"`
}

// stripDomain removes any domain suffix from a hostname (e.g. "freenas.lan" → "freenas").
func stripDomain(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// normMAC normalizes a MAC address to lowercase with colons.
func normMAC(mac string) string {
	result := make([]byte, 0, 17)
	for _, b := range []byte(mac) {
		if b >= 'A' && b <= 'F' {
			b = b + 32 // toLower
		}
		if b != '-' {
			result = append(result, b)
		} else {
			result = append(result, ':')
		}
	}
	return string(result)
}
