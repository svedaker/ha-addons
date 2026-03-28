package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[openwrt-monitor] ")

	// Determine config path
	configPath := "data/options.json" // HA add-on convention
	for i, arg := range os.Args[1:] {
		if arg == "--config" && i+1 < len(os.Args[1:]) {
			configPath = os.Args[i+2]
		}
	}

	log.Printf("loading config from %s", configPath)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("targets: %d APs, router: %v", len(cfg.APs()), cfg.Router() != nil)
	log.Printf("interval: %ds, grace_period: %ds, location: %s", cfg.Interval, cfg.GracePeriod, cfg.Location)

	// Initialize state
	state := NewState()
	state.InitMonitoredDevices(cfg.MonitoredDevices)
	if len(cfg.MonitoredDevices) > 0 {
		log.Printf("monitoring %d devices", len(cfg.MonitoredDevices))
	}

	// Connect MQTT
	log.Printf("connecting to MQTT %s:%d", cfg.MQTTHost, cfg.MQTTPort)
	mqttPub, err := NewMQTTPublisher(cfg)
	if err != nil {
		log.Fatalf("MQTT connection failed: %v", err)
	}
	defer mqttPub.Close()
	log.Println("MQTT connected")

	// Create collector
	collector := NewCollector(cfg, state)

	// Start HTTP status endpoint
	go startStatusServer(cfg.StatusPort, state)

	// Signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Main polling loop
	log.Println("starting polling loop")
	ticker := time.NewTicker(time.Duration(cfg.Interval) * time.Second)
	defer ticker.Stop()

	// Run first poll immediately
	runPollCycle(collector, mqttPub, state)

	for {
		select {
		case <-ticker.C:
			runPollCycle(collector, mqttPub, state)
		case sig := <-sigCh:
			log.Printf("received signal %v, shutting down", sig)
			mqttPub.PublishMonitorStatus(time.Now())
			mqttPub.PublishOfflineStatus()
			return
		}
	}
}

func runPollCycle(collector *Collector, mqttPub *MQTTPublisher, state *State) {
	start := time.Now()
	collector.Poll()
	logMemoryUsage("after poll")

	// Publish to MQTT
	mqttPub.PublishDeviceTrackers(state.AllClients())
	mqttPub.PublishAPSensors(state.AllAPs())
	mqttPub.PublishRouterSensors(state.GetRouter())
	mqttPub.PublishWANSensors(state.GetWAN())
	mqttPub.PublishMonitoredDevices(state.AllMonitoredDevices())

	snap := state.Snapshot()
	mqttPub.PublishMonitorStatus(snap.LastPoll)
	log.Printf("poll done in %v — %d clients, %d APs, %d DHCP leases",
		time.Since(start).Round(time.Millisecond),
		len(snap.Clients),
		len(snap.APs),
		snap.DHCPLeases,
	)
}

func startStatusServer(port int, state *State) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		snap := state.Snapshot()
		payload := struct {
			StateSnapshot
			GoRuntime GoRuntimeStatus `json:"go_runtime"`
		}{
			StateSnapshot: snap,
			GoRuntime:     collectGoRuntimeStatus(),
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(payload); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("status endpoint on http://0.0.0.0%s/status", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("status server: %v", err)
	}
}
