package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// ScannedClient is a WiFi client from one AP scan.
type ScannedClient struct {
	MAC           string
	Band          string
	Signal        int
	SignalAvg     int
	ConnectedTime int
	RxRate        int
	TxRate        int
	RxBytes       int64
	TxBytes       int64
}

// APInfo holds aggregated system/wifi info from one AP.
type APInfo struct {
	Uptime          int
	Load            float64
	MemAvailableKB  int
	Clients5G       int
	Clients2G       int
	RxBytes5G       int64
	TxBytes5G       int64
	RxBytes2G       int64
	TxBytes2G       int64
	Channel5G       int
	Channel2G       int
	Noise5G         int
	Noise2G         int
	AirtimeActive5G int64
	AirtimeBusy5G   int64
	AirtimeActive2G int64
	AirtimeBusy2G   int64
	SSIDs           map[string]SSIDInfo
}

// SSIDInfo holds aggregated stats for one SSID on one band.
type SSIDInfo struct {
	SSID    string
	Band    string
	Clients int
	RxBytes int64
	TxBytes int64
	Channel int
	Noise   int
}

// Collector polls all targets and updates the state.
type Collector struct {
	cfg     *Config
	state   *State
	clients map[string]*UbusClient // keyed by host
}

// NewCollector creates a collector with ubus clients for all targets.
func NewCollector(cfg *Config, state *State) *Collector {
	clients := make(map[string]*UbusClient, len(cfg.Targets))
	for _, t := range cfg.Targets {
		clients[t.Host] = NewUbusClient(t.Host, cfg.UbusScheme, cfg.UbusUser, cfg.UbusPass)
	}
	return &Collector{cfg: cfg, state: state, clients: clients}
}

// Poll runs one full collection cycle.
func (c *Collector) Poll() {
	now := time.Now()
	seenMACs := make(map[string]bool)

	// 1. Fetch DHCP leases + host hints + WAN status from router
	if router := c.cfg.Router(); router != nil {
		c.fetchDHCPLeases(router)
		c.fetchHostHints(router)
		c.fetchRouterSystem(router, now)
		c.fetchWANStatus(router, now)
	}

	// 2. Fetch WiFi clients + AP info from each AP
	for _, ap := range c.cfg.APs() {
		clients, apInfo, err := c.fetchAPData(ap)
		if err != nil {
			log.Printf("[%s] poll error: %v", ap.Name, err)
			c.state.MarkAPOffline(ap.Name, ap.Host)
			continue
		}

		location := c.cfg.Location
		if strings.TrimSpace(ap.Location) != "" {
			location = normalizeLocation(ap.Location)
		}

		// Update AP state
		c.state.UpdateAPState(ap.Name, apInfo, now)

		// Track seen MACs
		for _, cl := range clients {
			mac := normMAC(cl.MAC)
			seenMACs[mac] = true
		}

		// Update client state
		c.state.UpdateClients(ap.Name, location, clients, now)
	}

	// 3. Update monitored devices (uses host hints + DHCP leases already fetched)
	c.state.UpdateMonitoredDevices(now)

	// 4. Mark absent clients
	gracePeriod := time.Duration(c.cfg.GracePeriod) * time.Second
	c.state.MarkAbsent(seenMACs, gracePeriod, now)

	c.state.SetLastPoll(now)
}

// fetchRouterSystem gets router system metrics (uptime, load, memory).
func (c *Collector) fetchRouterSystem(router *Target, now time.Time) {
	uc := c.clients[router.Host]
	data, err := uc.Call("system", "info", nil)
	if err != nil {
		log.Printf("[%s] router system info error: %v", router.Name, err)
		c.state.MarkRouterOffline(router.Name, router.Host)
		return
	}

	var sysInfo struct {
		Uptime int      `json:"uptime"`
		Load   [3]int64 `json:"load"`
		Memory struct {
			Available int `json:"available"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(data, &sysInfo); err != nil {
		log.Printf("[%s] parse router system info: %v", router.Name, err)
		return
	}

	load := 0.0
	if len(sysInfo.Load) > 0 {
		load = float64(sysInfo.Load[0]) / 65536.0
	}
	memAvailableKB := sysInfo.Memory.Available / 1024

	c.state.UpdateRouterSystem(router.Name, router.Host, sysInfo.Uptime, load, memAvailableKB, now)
}

// fetchDHCPLeases gets DHCP leases from the router via luci-rpc.
func (c *Collector) fetchDHCPLeases(router *Target) {
	uc := c.clients[router.Host]
	data, err := uc.Call("luci-rpc", "getDHCPLeases", nil)
	if err != nil {
		log.Printf("[%s] DHCP leases error: %v", router.Name, err)
		return
	}

	// Response is {"dhcp_leases": [...]}
	var result struct {
		Leases []DHCPLease `json:"dhcp_leases"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		// Try direct array
		var leases []DHCPLease
		if err2 := json.Unmarshal(data, &leases); err2 != nil {
			log.Printf("[%s] parse DHCP leases: %v", router.Name, err)
			return
		}
		c.state.UpdateDHCPLeases(leases)
		return
	}

	c.state.UpdateDHCPLeases(result.Leases)
	if shouldLogLeaseCount(router.Name, len(result.Leases)) {
		log.Printf("[%s] DHCP leases: %d", router.Name, len(result.Leases))
	}
}

// fetchHostHints gets host hints from the router via luci-rpc.
// This covers devices with static IPs that don't appear in DHCP leases.
func (c *Collector) fetchHostHints(router *Target) {
	uc := c.clients[router.Host]
	data, err := uc.Call("luci-rpc", "getHostHints", nil)
	if err != nil {
		log.Printf("[%s] host hints error: %v", router.Name, err)
		return
	}

	// Response is a map of MAC → {name, ipaddrs, ip6addrs}
	var hints map[string]HostHint
	if err := json.Unmarshal(data, &hints); err != nil {
		log.Printf("[%s] parse host hints: %v", router.Name, err)
		return
	}

	c.state.UpdateHostHints(hints)
	if shouldLogHostHintCount(router.Name, len(hints)) {
		log.Printf("[%s] host hints: %d", router.Name, len(hints))
	}
}

// fetchWANStatus gets WAN interface status + traffic counters from the router.
func (c *Collector) fetchWANStatus(router *Target, now time.Time) {
	uc := c.clients[router.Host]

	// 1. Get WAN interface status (uptime, IP, up/down)
	wanData, err := uc.Call("network.interface.wan", "status", nil)
	if err != nil {
		log.Printf("[%s] WAN interface error: %v", router.Name, err)
		return
	}

	var wanStatus struct {
		Up       bool   `json:"up"`
		Uptime   int    `json:"uptime"`
		Device   string `json:"device"`
		IPv4Addr []struct {
			Address string `json:"address"`
			Mask    int    `json:"mask"`
		} `json:"ipv4-address"`
	}
	if err := json.Unmarshal(wanData, &wanStatus); err != nil {
		log.Printf("[%s] parse WAN status: %v", router.Name, err)
		return
	}

	publicIP := ""
	if len(wanStatus.IPv4Addr) > 0 {
		publicIP = wanStatus.IPv4Addr[0].Address
	}

	// 2. Get traffic counters from network.device (uses the WAN device name)
	wanDev := wanStatus.Device
	if wanDev == "" {
		wanDev = "wan"
	}

	var rxBytes, txBytes int64
	devData, err := uc.Call("network.device", "status", map[string]interface{}{"name": wanDev})
	if err == nil {
		var devStatus struct {
			Statistics struct {
				RxBytes int64 `json:"rx_bytes"`
				TxBytes int64 `json:"tx_bytes"`
			} `json:"statistics"`
		}
		if json.Unmarshal(devData, &devStatus) == nil {
			rxBytes = devStatus.Statistics.RxBytes
			txBytes = devStatus.Statistics.TxBytes
		}
	}

	c.state.UpdateWAN(wanStatus.Up, wanStatus.Uptime, wanDev, publicIP, rxBytes, txBytes, now)
	if shouldLogWANStatus(router.Name, wanStatus.Up, wanDev, publicIP) {
		log.Printf("[%s] WAN: up=%v uptime=%ds ip=%s rx=%.1fGB tx=%.1fGB",
			router.Name, wanStatus.Up, wanStatus.Uptime, publicIP,
			float64(rxBytes)/1e9, float64(txBytes)/1e9)
	}
}

// fetchAPData gets WiFi clients and system info from one AP.
func (c *Collector) fetchAPData(ap Target) ([]ScannedClient, APInfo, error) {
	uc := c.clients[ap.Host]
	info := APInfo{SSIDs: map[string]SSIDInfo{}}
	var allClients []ScannedClient
	seenBandRadioStats := map[string]bool{}

	// Get WiFi interface list
	devices, err := c.getWifiDevices(uc, ap.Name)
	if err != nil {
		return nil, info, fmt.Errorf("get devices: %w", err)
	}

	// Get assoclist + info + survey per interface
	for _, dev := range devices {
		band := guessBand(dev)
		ssid := dev

		// iwinfo.info — ssid, channel, noise
		infoData, err := uc.Call("iwinfo", "info", map[string]interface{}{"device": dev})
		if err == nil {
			var iwInfo struct {
				SSID    string `json:"ssid"`
				Channel int    `json:"channel"`
				Noise   int    `json:"noise"`
			}
			if json.Unmarshal(infoData, &iwInfo) == nil {
				if iwInfo.SSID != "" {
					ssid = iwInfo.SSID
				}
				if band == "5GHz" {
					if info.Channel5G == 0 {
						info.Channel5G = iwInfo.Channel
					}
					if info.Noise5G == 0 {
						info.Noise5G = iwInfo.Noise
					}
				} else {
					if info.Channel2G == 0 {
						info.Channel2G = iwInfo.Channel
					}
					if info.Noise2G == 0 {
						info.Noise2G = iwInfo.Noise
					}
				}
			}
		}

		ssidKey := ssidBandKey(ssid, band)
		ssidInfo := info.SSIDs[ssidKey]
		ssidInfo.SSID = ssid
		ssidInfo.Band = band

		// assoclist
		clients, err := c.getAssocList(uc, dev)
		if err != nil {
			log.Printf("[%s] assoclist %s: %v", ap.Name, dev, err)
			continue
		}
		for i := range clients {
			clients[i].Band = band
		}
		allClients = append(allClients, clients...)
		ssidInfo.Clients += len(clients)

		if band == "5GHz" {
			info.Clients5G += len(clients)
		} else {
			info.Clients2G += len(clients)
		}

		// network.device.status — per-band RX/TX counters on AP interfaces
		devData, err := uc.Call("network.device", "status", map[string]interface{}{"name": dev})
		if err == nil {
			var devStatus struct {
				Statistics struct {
					RxBytes int64 `json:"rx_bytes"`
					TxBytes int64 `json:"tx_bytes"`
				} `json:"statistics"`
			}
			if json.Unmarshal(devData, &devStatus) == nil {
				ssidInfo.RxBytes += devStatus.Statistics.RxBytes
				ssidInfo.TxBytes += devStatus.Statistics.TxBytes
				if band == "5GHz" {
					info.RxBytes5G += devStatus.Statistics.RxBytes
					info.TxBytes5G += devStatus.Statistics.TxBytes
				} else {
					info.RxBytes2G += devStatus.Statistics.RxBytes
					info.TxBytes2G += devStatus.Statistics.TxBytes
				}
			}
		}

		if band == "5GHz" {
			ssidInfo.Channel = info.Channel5G
			ssidInfo.Noise = info.Noise5G
		} else {
			ssidInfo.Channel = info.Channel2G
			ssidInfo.Noise = info.Noise2G
		}
		info.SSIDs[ssidKey] = ssidInfo

		// iwinfo.survey is radio-level data and usually identical for all SSIDs on the same band.
		if !seenBandRadioStats[band] {
			surveyData, err := uc.Call("iwinfo", "survey", map[string]interface{}{"device": dev})
			if err == nil {
				totalActive, totalBusy := c.parseSurvey(surveyData)
				if band == "5GHz" {
					info.AirtimeActive5G = totalActive
					info.AirtimeBusy5G = totalBusy
				} else {
					info.AirtimeActive2G = totalActive
					info.AirtimeBusy2G = totalBusy
				}
				seenBandRadioStats[band] = true
			}
		}
	}

	// system.info — uptime, load, memory
	sysData, err := uc.Call("system", "info", nil)
	if err == nil {
		var sysInfo struct {
			Uptime int      `json:"uptime"`
			Load   [3]int64 `json:"load"`
			Memory struct {
				Total     int `json:"total"`
				Free      int `json:"free"`
				Shared    int `json:"shared"`
				Buffered  int `json:"buffered"`
				Available int `json:"available"`
			} `json:"memory"`
		}
		if json.Unmarshal(sysData, &sysInfo) == nil {
			info.Uptime = sysInfo.Uptime
			info.Load = float64(sysInfo.Load[0]) / 65536.0
			info.MemAvailableKB = sysInfo.Memory.Available / 1024
		}
	}

	return allClients, info, nil
}

func ssidBandKey(ssid, band string) string {
	return ssid + "|" + band
}

// getWifiDevices returns the list of WiFi AP interfaces (e.g. phy0-ap0, phy1-ap0).
func (c *Collector) getWifiDevices(uc *UbusClient, apName string) ([]string, error) {
	data, err := uc.Call("iwinfo", "devices", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Devices []string `json:"devices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse devices: %w", err)
	}

	// Filter to only AP interfaces (phy*-ap*)
	var apDevices []string
	for _, d := range result.Devices {
		if strings.Contains(d, "-ap") {
			apDevices = append(apDevices, d)
		}
	}

	if len(apDevices) == 0 {
		log.Printf("[%s] warning: no AP interfaces found in %v", apName, result.Devices)
	}

	return apDevices, nil
}

// getAssocList gets the associated client list for one WiFi interface.
func (c *Collector) getAssocList(uc *UbusClient, device string) ([]ScannedClient, error) {
	data, err := uc.Call("iwinfo", "assoclist", map[string]interface{}{"device": device})
	if err != nil {
		return nil, err
	}

	var result struct {
		Results []struct {
			MAC           string `json:"mac"`
			Signal        int    `json:"signal"`
			SignalAvg     int    `json:"signal_avg"`
			ConnectedTime int    `json:"connected_time"`
			RX            struct {
				Rate  int   `json:"rate"`
				Bytes int64 `json:"bytes"`
			} `json:"rx"`
			TX struct {
				Rate  int   `json:"rate"`
				Bytes int64 `json:"bytes"`
			} `json:"tx"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse assoclist: %w", err)
	}

	clients := make([]ScannedClient, 0, len(result.Results))
	for _, r := range result.Results {
		clients = append(clients, ScannedClient{
			MAC:           r.MAC,
			Signal:        r.Signal,
			SignalAvg:     r.SignalAvg,
			ConnectedTime: r.ConnectedTime,
			RxRate:        r.RX.Rate,
			TxRate:        r.TX.Rate,
			RxBytes:       r.RX.Bytes,
			TxBytes:       r.TX.Bytes,
		})
	}

	return clients, nil
}

// parseSurvey extracts airtime counters from iwinfo.survey result.
func (c *Collector) parseSurvey(data json.RawMessage) (int64, int64) {
	var result struct {
		Results []struct {
			Frequency    int   `json:"frequency"`
			ActiveTime   int64 `json:"active_time"`
			BusyTime     int64 `json:"busy_time"`
			ReceiveTime  int64 `json:"receive_time"`
			TransmitTime int64 `json:"transmit_time"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, 0
	}

	// Sum all entries (usually one per active channel)
	var totalActive, totalBusy int64
	for _, s := range result.Results {
		if s.ActiveTime > 0 {
			totalActive += s.ActiveTime
			totalBusy += s.BusyTime
		}
	}

	return totalActive, totalBusy
}

// guessBand determines band from interface name.
// phy0-ap0 = 2.4GHz, phy1-ap0 = 5GHz (MT7981B convention).
func guessBand(device string) string {
	if strings.HasPrefix(device, "phy1") {
		return "5GHz"
	}
	return "2.4GHz"
}
