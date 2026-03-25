package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTPublisher handles MQTT connection and HA autodiscovery.
type MQTTPublisher struct {
	client          mqtt.Client
	location        string
	heartbeatExpire int
}

// NewMQTTPublisher connects to the MQTT broker.
func NewMQTTPublisher(cfg *Config) (*MQTTPublisher, error) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.MQTTHost, cfg.MQTTPort))
	opts.SetClientID("openwrt-monitor")
	opts.SetUsername(cfg.MQTTUser)
	opts.SetPassword(cfg.MQTTPass)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(10 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetWill("openwrt-monitor/status", "offline", 1, true)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("[mqtt] connected to broker")
		token := c.Publish("openwrt-monitor/status", 1, true, "online")
		token.Wait()
	})
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("[mqtt] connection lost: %v", err)
	})

	client := mqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt connect: %w", err)
	}

	heartbeatExpire := cfg.Interval * 3
	if heartbeatExpire < 90 {
		heartbeatExpire = 90
	}

	return &MQTTPublisher{
		client:          client,
		location:        cfg.Location,
		heartbeatExpire: heartbeatExpire,
	}, nil
}

// PublishDeviceTrackers publishes HA autodiscovery + state for all clients.
func (m *MQTTPublisher) PublishDeviceTrackers(clients map[string]*Client) {
	for mac, c := range clients {
		nodeID := macToNodeID(mac)

		// Autodiscovery config (retained)
		configTopic := fmt.Sprintf("homeassistant/device_tracker/openwrt_monitor/%s/config", nodeID)

		// Use hostname as display name; fall back to MAC if unknown
		displayName := c.Hostname
		if displayName == "" || displayName == mac {
			displayName = mac
		}

		configPayload := map[string]interface{}{
			"name":                  displayName,
			"unique_id":             fmt.Sprintf("openwrt_monitor_%s", nodeID),
			"state_topic":           fmt.Sprintf("openwrt-monitor/device_tracker/%s/state", nodeID),
			"json_attributes_topic": fmt.Sprintf("openwrt-monitor/device_tracker/%s/attributes", nodeID),
			"source_type":           "router",
			"payload_home":          "home",
			"payload_not_home":      "not_home",
			"device": map[string]interface{}{
				"identifiers":  []string{fmt.Sprintf("openwrt_monitor_%s", nodeID)},
				"name":         displayName,
				"manufacturer": "WiFi",
				"model":        "Client",
				"via_device":   "openwrt_monitor",
			},
		}
		m.publishJSON(configTopic, configPayload, true)

		// State
		stateTopic := fmt.Sprintf("openwrt-monitor/device_tracker/%s/state", nodeID)
		m.publish(stateTopic, c.Location, false)

		// Attributes
		attrTopic := fmt.Sprintf("openwrt-monitor/device_tracker/%s/attributes", nodeID)
		attrs := map[string]interface{}{
			"source_type":    "router",
			"mac":            mac,
			"hostname":       c.Hostname,
			"ip":             c.IP,
			"ap":             c.AP,
			"band":           c.Band,
			"signal":         c.Signal,
			"signal_avg":     c.SignalAvg,
			"connected_time": c.ConnectedTime,
			"last_ap":        c.LastAP,
			"rx_rate_mbps":   float64(c.RxRate) / 1e6,
			"tx_rate_mbps":   float64(c.TxRate) / 1e6,
		}
		if !c.ConnectedSince.IsZero() {
			attrs["connected_since"] = c.ConnectedSince.Format(time.RFC3339)
		}
		if !c.LastSeen.IsZero() {
			attrs["last_seen"] = c.LastSeen.Format(time.RFC3339)
		}
		m.publishJSON(attrTopic, attrs, false)
	}
}

// PublishAPSensors publishes HA autodiscovery + state for AP sensors.
func (m *MQTTPublisher) PublishAPSensors(aps map[string]*APState) {
	for _, ap := range aps {
		nodeID := targetNodeID(ap.Name)

		sensors := []struct {
			name       string
			suffix     string
			value      interface{}
			unit       string
			devClass   string
			stateClass string
			icon       string
		}{
			{"Clients 5GHz", "clients_5g", ap.Clients5G, "clients", "", "measurement", "mdi:wifi"},
			{"Clients 2.4GHz", "clients_2g", ap.Clients2G, "clients", "", "measurement", "mdi:wifi"},
			{"WiFi 5GHz RX Total", "rx_bytes_5g", fmt.Sprintf("%.2f", float64(ap.RxBytes5G)/1e9), "GB", "data_size", "total_increasing", "mdi:download-network"},
			{"WiFi 5GHz TX Total", "tx_bytes_5g", fmt.Sprintf("%.2f", float64(ap.TxBytes5G)/1e9), "GB", "data_size", "total_increasing", "mdi:upload-network"},
			{"WiFi 2.4GHz RX Total", "rx_bytes_2g", fmt.Sprintf("%.2f", float64(ap.RxBytes2G)/1e9), "GB", "data_size", "total_increasing", "mdi:download-network"},
			{"WiFi 2.4GHz TX Total", "tx_bytes_2g", fmt.Sprintf("%.2f", float64(ap.TxBytes2G)/1e9), "GB", "data_size", "total_increasing", "mdi:upload-network"},
			{"WiFi 5GHz Download", "rx_rate_5g", fmt.Sprintf("%.2f", ap.RxRate5G), "Mbit/s", "data_rate", "measurement", "mdi:download"},
			{"WiFi 5GHz Upload", "tx_rate_5g", fmt.Sprintf("%.2f", ap.TxRate5G), "Mbit/s", "data_rate", "measurement", "mdi:upload"},
			{"WiFi 2.4GHz Download", "rx_rate_2g", fmt.Sprintf("%.2f", ap.RxRate2G), "Mbit/s", "data_rate", "measurement", "mdi:download"},
			{"WiFi 2.4GHz Upload", "tx_rate_2g", fmt.Sprintf("%.2f", ap.TxRate2G), "Mbit/s", "data_rate", "measurement", "mdi:upload"},
			{"Load", "load", fmt.Sprintf("%.2f", ap.Load), "", "", "measurement", "mdi:gauge"},
			{"Memory Available", "mem_available", fmt.Sprintf("%.1f", float64(ap.MemAvailableKB)/1024.0), "MB", "", "measurement", "mdi:memory"},
			{"Uptime", "uptime", fmt.Sprintf("%.2f", float64(ap.Uptime)/3600.0), "h", "", "measurement", "mdi:timer"},
			{"Up Since", "up_since", uptimeToRFC3339(ap.Uptime), "", "timestamp", "", "mdi:clock-start"},
			{"Channel 5GHz", "channel_5g", ap.Channel5G, "", "", "measurement", "mdi:access-point"},
			{"Channel 2.4GHz", "channel_2g", ap.Channel2G, "", "", "measurement", "mdi:access-point"},
			{"Noise 5GHz", "noise_5g", ap.Noise5G, "dBm", "signal_strength", "measurement", "mdi:signal-variant"},
			{"Noise 2.4GHz", "noise_2g", ap.Noise2G, "dBm", "signal_strength", "measurement", "mdi:signal-variant"},
			{"Airtime 5GHz", "airtime_5g", fmt.Sprintf("%.1f", ap.Airtime5G), "%", "", "measurement", "mdi:chart-arc"},
			{"Airtime 2.4GHz", "airtime_2g", fmt.Sprintf("%.1f", ap.Airtime2G), "%", "", "measurement", "mdi:chart-arc"},
		}

		for _, s := range sensors {
			uniqueID := fmt.Sprintf("openwrt_monitor_%s_%s", nodeID, s.suffix)
			configTopic := fmt.Sprintf("homeassistant/sensor/openwrt_monitor/%s_%s/config", nodeID, s.suffix)
			stateTopic := fmt.Sprintf("openwrt-monitor/sensor/%s/%s", nodeID, s.suffix)

			config := map[string]interface{}{
				"name":        s.name,
				"unique_id":   uniqueID,
				"state_topic": stateTopic,
				"device": map[string]interface{}{
					"identifiers":  []string{fmt.Sprintf("openwrt_monitor_%s", nodeID)},
					"name":         ap.Name,
					"manufacturer": "OpenWrt",
					"model":        "Access Point",
					"via_device":   "openwrt_monitor",
				},
			}
			if s.unit != "" {
				config["unit_of_measurement"] = s.unit
			}
			if s.devClass != "" {
				config["device_class"] = s.devClass
			}
			if s.stateClass != "" {
				config["state_class"] = s.stateClass
			}
			if s.icon != "" {
				config["icon"] = s.icon
			}

			m.publishJSON(configTopic, config, true)
			m.publish(stateTopic, fmt.Sprintf("%v", s.value), false)
		}

		for _, ssid := range ap.SSIDs {
			ssidID := ssidNodeID(ssid.SSID)
			bandID := strings.ToLower(strings.ReplaceAll(ssid.Band, ".", ""))

			ssidSensors := []struct {
				name       string
				suffix     string
				value      interface{}
				unit       string
				devClass   string
				stateClass string
				icon       string
			}{
				{fmt.Sprintf("SSID %s %s Clients", ssid.SSID, ssid.Band), "clients", ssid.Clients, "clients", "", "measurement", "mdi:wifi"},
				{fmt.Sprintf("SSID %s %s RX Total", ssid.SSID, ssid.Band), "rx_bytes", fmt.Sprintf("%.2f", float64(ssid.RxBytes)/1e9), "GB", "data_size", "total_increasing", "mdi:download-network"},
				{fmt.Sprintf("SSID %s %s TX Total", ssid.SSID, ssid.Band), "tx_bytes", fmt.Sprintf("%.2f", float64(ssid.TxBytes)/1e9), "GB", "data_size", "total_increasing", "mdi:upload-network"},
				{fmt.Sprintf("SSID %s %s Download", ssid.SSID, ssid.Band), "rx_rate", fmt.Sprintf("%.2f", ssid.RxRateMbps), "Mbit/s", "data_rate", "measurement", "mdi:download"},
				{fmt.Sprintf("SSID %s %s Upload", ssid.SSID, ssid.Band), "tx_rate", fmt.Sprintf("%.2f", ssid.TxRateMbps), "Mbit/s", "data_rate", "measurement", "mdi:upload"},
				{fmt.Sprintf("SSID %s %s Channel", ssid.SSID, ssid.Band), "channel", ssid.Channel, "", "", "measurement", "mdi:access-point"},
				{fmt.Sprintf("SSID %s %s Noise", ssid.SSID, ssid.Band), "noise", ssid.Noise, "dBm", "signal_strength", "measurement", "mdi:signal-variant"},
			}

			for _, s := range ssidSensors {
				uniqueID := fmt.Sprintf("openwrt_monitor_%s_ssid_%s_%s_%s", nodeID, ssidID, bandID, s.suffix)
				configTopic := fmt.Sprintf("homeassistant/sensor/openwrt_monitor/%s_ssid_%s_%s_%s/config", nodeID, ssidID, bandID, s.suffix)
				stateTopic := fmt.Sprintf("openwrt-monitor/sensor/%s/ssid/%s/%s/%s", nodeID, ssidID, bandID, s.suffix)

				config := map[string]interface{}{
					"name":        s.name,
					"unique_id":   uniqueID,
					"state_topic": stateTopic,
					"device": map[string]interface{}{
						"identifiers":  []string{fmt.Sprintf("openwrt_monitor_%s", nodeID)},
						"name":         ap.Name,
						"manufacturer": "OpenWrt",
						"model":        "Access Point",
						"via_device":   "openwrt_monitor",
					},
				}
				if s.unit != "" {
					config["unit_of_measurement"] = s.unit
				}
				if s.devClass != "" {
					config["device_class"] = s.devClass
				}
				if s.stateClass != "" {
					config["state_class"] = s.stateClass
				}
				if s.icon != "" {
					config["icon"] = s.icon
				}

				m.publishJSON(configTopic, config, true)
				m.publish(stateTopic, fmt.Sprintf("%v", s.value), false)
			}
		}
	}
}

// PublishMonitorStatus publishes monitor online/offline + heartbeat timestamp.
func (m *MQTTPublisher) PublishMonitorStatus(lastPoll time.Time) {
	statusConfigTopic := "homeassistant/binary_sensor/openwrt_monitor/monitor_status/config"
	statusStateTopic := "openwrt-monitor/status"
	statusConfig := map[string]interface{}{
		"name":        "OpenWrt Monitor Status",
		"unique_id":   "openwrt_monitor_status",
		"state_topic": statusStateTopic,
		"payload_on":  "online",
		"payload_off": "offline",
		"icon":        "mdi:lan-check",
		"device": map[string]interface{}{
			"identifiers":  []string{"openwrt_monitor"},
			"name":         "OpenWrt Monitor",
			"manufacturer": "OpenWrt",
			"model":        "HA Add-on",
		},
	}
	m.publishJSON(statusConfigTopic, statusConfig, true)

	heartbeatConfigTopic := "homeassistant/sensor/openwrt_monitor/last_update/config"
	heartbeatStateTopic := "openwrt-monitor/heartbeat/last_update"
	heartbeatConfig := map[string]interface{}{
		"name":         "OpenWrt Monitor Last Update",
		"unique_id":    "openwrt_monitor_last_update",
		"state_topic":  heartbeatStateTopic,
		"device_class": "timestamp",
		"expire_after": m.heartbeatExpire,
		"icon":         "mdi:clock-outline",
		"device": map[string]interface{}{
			"identifiers":  []string{"openwrt_monitor"},
			"name":         "OpenWrt Monitor",
			"manufacturer": "OpenWrt",
			"model":        "HA Add-on",
		},
	}
	m.publishJSON(heartbeatConfigTopic, heartbeatConfig, true)

	timestamp := time.Now().UTC().Format(time.RFC3339)
	if !lastPoll.IsZero() {
		timestamp = lastPoll.UTC().Format(time.RFC3339)
	}
	m.publish(heartbeatStateTopic, timestamp, false)
}

// PublishOfflineStatus explicitly marks the monitor as offline.
func (m *MQTTPublisher) PublishOfflineStatus() {
	m.publish("openwrt-monitor/status", "offline", true)
}

// PublishWANSensors publishes HA autodiscovery + state for WAN sensors.
func (m *MQTTPublisher) PublishWANSensors(wan WANState) {
	nodeID := "wan"

	sensors := []struct {
		name       string
		suffix     string
		value      interface{}
		unit       string
		devClass   string
		stateClass string
		icon       string
	}{
		{"WAN Status", "status", boolToOnOff(wan.Up), "", "", "", "mdi:wan"},
		{"WAN Uptime", "uptime", fmt.Sprintf("%.2f", float64(wan.Uptime)/3600.0), "h", "", "measurement", "mdi:timer-outline"},
		{"WAN Up Since", "up_since", uptimeToRFC3339(wan.Uptime), "", "timestamp", "", "mdi:clock-start"},
		{"WAN Public IP", "public_ip", wan.PublicIP, "", "", "", "mdi:ip-network"},
		{"WAN RX Total", "rx_bytes", fmt.Sprintf("%.2f", float64(wan.RxBytes)/1e9), "GB", "data_size", "total_increasing", "mdi:download-network"},
		{"WAN TX Total", "tx_bytes", fmt.Sprintf("%.2f", float64(wan.TxBytes)/1e9), "GB", "data_size", "total_increasing", "mdi:upload-network"},
		{"WAN Download", "rx_rate", fmt.Sprintf("%.2f", wan.RxRateMbps), "Mbit/s", "data_rate", "measurement", "mdi:download"},
		{"WAN Upload", "tx_rate", fmt.Sprintf("%.2f", wan.TxRateMbps), "Mbit/s", "data_rate", "measurement", "mdi:upload"},
	}

	for _, s := range sensors {
		uniqueID := fmt.Sprintf("openwrt_monitor_%s_%s", nodeID, s.suffix)
		configTopic := fmt.Sprintf("homeassistant/sensor/openwrt_monitor/%s_%s/config", nodeID, s.suffix)
		stateTopic := fmt.Sprintf("openwrt-monitor/sensor/%s/%s", nodeID, s.suffix)

		config := map[string]interface{}{
			"name":        s.name,
			"unique_id":   uniqueID,
			"state_topic": stateTopic,
			"device": map[string]interface{}{
				"identifiers":  []string{"openwrt_monitor_wan"},
				"name":         "WAN Internet",
				"manufacturer": "OpenWrt",
				"model":        "WAN Interface",
				"via_device":   "openwrt_monitor",
			},
		}
		if s.unit != "" {
			config["unit_of_measurement"] = s.unit
		}
		if s.devClass != "" {
			config["device_class"] = s.devClass
		}
		if s.stateClass != "" {
			config["state_class"] = s.stateClass
		}
		if s.icon != "" {
			config["icon"] = s.icon
		}

		m.publishJSON(configTopic, config, true)
		m.publish(stateTopic, fmt.Sprintf("%v", s.value), false)
	}
}

func boolToOnOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// PublishRouterSensors publishes HA autodiscovery + state for router system sensors.
func (m *MQTTPublisher) PublishRouterSensors(router RouterState) {
	if router.Name == "" {
		return
	}

	nodeID := targetNodeID(router.Name)

	sensors := []struct {
		name       string
		suffix     string
		value      interface{}
		unit       string
		devClass   string
		stateClass string
		icon       string
	}{
		{"Router Status", "status", strings.ToUpper(router.Status), "", "", "", "mdi:router-network"},
		{"Router Uptime", "uptime", fmt.Sprintf("%.2f", float64(router.Uptime)/3600.0), "h", "", "measurement", "mdi:timer-outline"},
		{"Router Up Since", "up_since", uptimeToRFC3339(router.Uptime), "", "timestamp", "", "mdi:clock-start"},
		{"Router Load", "load", fmt.Sprintf("%.2f", router.Load), "", "", "measurement", "mdi:gauge"},
		{"Router Memory Available", "mem_available", fmt.Sprintf("%.1f", float64(router.MemAvailableKB)/1024.0), "MB", "", "measurement", "mdi:memory"},
	}

	for _, s := range sensors {
		uniqueID := fmt.Sprintf("openwrt_monitor_%s_%s", nodeID, s.suffix)
		configTopic := fmt.Sprintf("homeassistant/sensor/openwrt_monitor/%s_%s/config", nodeID, s.suffix)
		stateTopic := fmt.Sprintf("openwrt-monitor/sensor/%s/%s", nodeID, s.suffix)

		config := map[string]interface{}{
			"name":        s.name,
			"unique_id":   uniqueID,
			"state_topic": stateTopic,
			"device": map[string]interface{}{
				"identifiers":  []string{fmt.Sprintf("openwrt_monitor_%s", nodeID)},
				"name":         router.Name,
				"manufacturer": "OpenWrt",
				"model":        "Router",
				"via_device":   "openwrt_monitor",
			},
		}
		if s.unit != "" {
			config["unit_of_measurement"] = s.unit
		}
		if s.devClass != "" {
			config["device_class"] = s.devClass
		}
		if s.stateClass != "" {
			config["state_class"] = s.stateClass
		}
		if s.icon != "" {
			config["icon"] = s.icon
		}

		m.publishJSON(configTopic, config, true)
		m.publish(stateTopic, fmt.Sprintf("%v", s.value), false)
	}
}

// PublishMonitoredDevices publishes HA autodiscovery + state for monitored devices as binary_sensors.
func (m *MQTTPublisher) PublishMonitoredDevices(devices map[string]*MonitoredDevice) {
	for _, dev := range devices {
		nodeID := macToNodeID(dev.MAC)

		// Autodiscovery config (retained)
		configTopic := fmt.Sprintf("homeassistant/binary_sensor/openwrt_monitor/dev_%s/config", nodeID)
		stateTopic := fmt.Sprintf("openwrt-monitor/monitored/%s/state", nodeID)
		attrTopic := fmt.Sprintf("openwrt-monitor/monitored/%s/attributes", nodeID)

		config := map[string]interface{}{
			"name":                  dev.Name,
			"unique_id":             fmt.Sprintf("openwrt_monitor_dev_%s", nodeID),
			"state_topic":           stateTopic,
			"json_attributes_topic": attrTopic,
			"payload_on":            "ON",
			"payload_off":           "OFF",
			"device_class":          "connectivity",
			"icon":                  "mdi:lan-connect",
			"device": map[string]interface{}{
				"identifiers":  []string{fmt.Sprintf("openwrt_monitor_dev_%s", nodeID)},
				"name":         dev.Name,
				"manufacturer": "Network",
				"model":        "Monitored Device",
				"via_device":   "openwrt_monitor",
			},
		}
		m.publishJSON(configTopic, config, true)

		// State
		m.publish(stateTopic, boolToOnOff(dev.Online), false)

		// Attributes
		attrs := map[string]interface{}{
			"mac": dev.MAC,
			"ip":  dev.IP,
		}
		if !dev.OnlineSince.IsZero() {
			attrs["online_since"] = dev.OnlineSince.Format(time.RFC3339)
			if dev.Online {
				uptime := time.Since(dev.OnlineSince).Round(time.Second)
				attrs["uptime"] = uptime.String()
				attrs["uptime_seconds"] = int(uptime.Seconds())
			}
		}
		if !dev.LastSeen.IsZero() {
			attrs["last_seen"] = dev.LastSeen.Format(time.RFC3339)
		}
		if !dev.OfflineSince.IsZero() {
			attrs["offline_since"] = dev.OfflineSince.Format(time.RFC3339)
			if !dev.Online {
				downtime := time.Since(dev.OfflineSince).Round(time.Second)
				attrs["downtime"] = downtime.String()
				attrs["downtime_seconds"] = int(downtime.Seconds())
			}
		}
		m.publishJSON(attrTopic, attrs, false)
	}
}

// Close disconnects from the MQTT broker.
func (m *MQTTPublisher) Close() {
	m.client.Disconnect(1000)
}

func (m *MQTTPublisher) publish(topic, payload string, retained bool) {
	token := m.client.Publish(topic, 0, retained, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		log.Printf("[mqtt] publish %s: %v", topic, err)
	}
}

func (m *MQTTPublisher) publishJSON(topic string, payload interface{}, retained bool) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[mqtt] marshal %s: %v", topic, err)
		return
	}
	token := m.client.Publish(topic, 0, retained, data)
	token.Wait()
	if err := token.Error(); err != nil {
		log.Printf("[mqtt] publish %s: %v", topic, err)
	}
}

// macToNodeID converts "6c:2f:80:d6:2f:f4" to "6c2f80d62ff4".
func macToNodeID(mac string) string {
	return strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", "")
}

// targetNodeID normalizes target names from config for MQTT/HA IDs.
func targetNodeID(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	id = strings.ReplaceAll(id, "-", "_")
	id = strings.ReplaceAll(id, " ", "_")
	return id
}

func ssidNodeID(ssid string) string {
	id := strings.ToLower(strings.TrimSpace(ssid))
	if id == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		".", "_",
		"-", "_",
		":", "_",
	)
	id = replacer.Replace(id)
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "unknown"
	}
	return out
}

// uptimeToRFC3339 converts uptime in seconds to an RFC3339 timestamp.
func uptimeToRFC3339(uptimeSec int) string {
	if uptimeSec <= 0 {
		return ""
	}
	// Round to whole minutes to avoid HA history jitter from poll timing.
	return time.Now().Add(-time.Duration(uptimeSec) * time.Second).UTC().Truncate(time.Minute).Format(time.RFC3339)
}
