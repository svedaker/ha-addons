package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config represents the full HA add-on options.json structure.
type Config struct {
	Interval         int                `json:"interval"`
	GracePeriod      int                `json:"grace_period"`
	Location         string             `json:"location"`
	StatusPort       int                `json:"status_port"`
	MQTTHost         string             `json:"mqtt_host"`
	MQTTPort         int                `json:"mqtt_port"`
	MQTTUser         string             `json:"mqtt_user"`
	MQTTPass         string             `json:"mqtt_password"`
	UbusUser         string             `json:"ubus_user"`
	UbusPass         string             `json:"ubus_password"`
	UbusScheme       string             `json:"ubus_scheme"`
	Targets          []Target           `json:"targets"`
	MonitoredDevices []MonitoredDevCfg  `json:"monitored_devices"`
}

// Target represents a single OpenWrt device to monitor.
type Target struct {
	Host string `json:"host"`
	Name string `json:"name"`
	Role string `json:"role"` // "ap", "router", or "both"
}

// MonitoredDevCfg represents a device to track online/offline status.
type MonitoredDevCfg struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

// LoadConfig reads the HA add-on options.json file.
// Default path is /data/options.json (HA convention).
// For local testing, pass a custom path via --config flag.
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer file.Close()

	var cfg Config
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	// Defaults
	if cfg.Interval <= 0 {
		cfg.Interval = 30
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = 120
	}
	if cfg.Location == "" {
		cfg.Location = "home"
	}
	if cfg.StatusPort <= 0 {
		cfg.StatusPort = 8099
	}
	if cfg.UbusScheme == "" {
		cfg.UbusScheme = "https"
	}

	return &cfg, nil
}

// APs returns targets with role "ap" or "both".
func (c *Config) APs() []Target {
	var aps []Target
	for _, t := range c.Targets {
		if t.Role == "ap" || t.Role == "both" {
			aps = append(aps, t)
		}
	}
	return aps
}

// Router returns the first target with role "router" or "both", or nil.
func (c *Config) Router() *Target {
	for _, t := range c.Targets {
		if t.Role == "router" || t.Role == "both" {
			return &t
		}
	}
	return nil
}
