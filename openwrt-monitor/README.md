# OpenWrt Monitor — HA Add-on

Centralized WiFi client tracking, AP monitoring, WAN monitoring, and device
availability tracking for OpenWrt networks. Runs as a single Go binary
(Home Assistant add-on) and publishes everything to HA via MQTT autodiscovery.

## Features

- **Device tracking** — all WiFi clients from all APs, with home/not_home state
- **Hostname resolution** — DHCP leases + host hints (covers static IP devices)
- **AP sensors** — clients, load, memory, uptime, channel, noise per AP
- **Per-SSID sensors** — clients, totals, rates, channel/noise per SSID and band
- **Airtime utilization** — % channel busy time per band (2.4 GHz / 5 GHz)
- **Roaming detection** — tracks which AP and band each client is on
- **WAN monitoring** — status, uptime, public IP, total RX/TX, download/upload speed
- **Monitored devices** — configurable list of MACs tracked as binary_sensor (online/offline with uptime/downtime)
- **Monitor heartbeat** — status binary_sensor + `last_update` timestamp sensor with expiry
- **HTTP status endpoint** — JSON dump of entire state at `/status`

## Compatibility

Works with any device running **OpenWrt 23.x–25.x** that has uhttpd and rpcd.
The only difference is the package manager:

| OpenWrt version | Package manager | Install command |
|-----------------|-----------------|-----------------|
| **≥ 25.x** | apk | `apk add <package>` |
| **≤ 24.x** | opkg | `opkg install <package>` |

All examples below use `apk`. Replace with `opkg install` if running ≤ 24.x.

## Architecture

```
┌──────────────────────────────────────────┐
│         openwrt-monitor (Go)             │
│         HA Add-on                        │
│                                          │
│  Poll interval (default 30s):            │
│  1. DHCP leases from router (ubus)       │
│  2. Host hints from router (ubus)        │
│  3. WAN status + traffic from router     │
│  4. WiFi clients from APs (ubus)        │
│  5. System info + survey from APs        │
│  6. Update monitored device states       │
│  7. Publish to MQTT (autodiscovery)      │
│  8. Update /status endpoint              │
└─────┬──────────────────────┬─────────────┘
      │ ubus HTTPS           │ MQTT
      ▼                      ▼
 ┌──────────┐          ┌──────────┐
 │ OpenWrt  │          │ MQTT     │
 │ APs +    │          │ broker   │
 │ Router   │          │ → HA     │
 └──────────┘          └──────────┘
```

## Prerequisites — OpenWrt device setup

The add-on connects to OpenWrt devices via **ubus JSON-RPC over HTTPS**.
Each device needs a dedicated `monitor` user with read-only access.

### 1. HTTPS certificates

Install `px5g-mbedtls` on each device to enable self-signed HTTPS:

```bash
apk add px5g-mbedtls        # OpenWrt ≥ 25.x
# opkg install px5g-mbedtls  # OpenWrt ≤ 24.x

/etc/init.d/uhttpd restart

# Verify HTTPS is listening
netstat -tlnp | grep :443
```

uhttpd auto-generates an EC P-256 self-signed cert on restart.
The Go client uses `InsecureSkipVerify: true` (self-signed OK on LAN).

### 2. Create monitor user

```bash
echo "monitor:x:1000:1000:monitor:/dev/null:/bin/false" >> /etc/passwd
echo -e 'YOUR_PASSWORD\nYOUR_PASSWORD' | passwd monitor
```

### 3. rpcd ACL — Access Points

Create `/usr/share/rpcd/acl.d/openwrt-monitor.json` on each **AP**:

```json
{
    "openwrt-monitor": {
        "description": "Read-only access for openwrt-monitor",
        "read": {
            "ubus": {
                "iwinfo": ["assoclist", "devices", "info", "survey"],
                "system": ["info", "board"],
                "network.device": ["status"]
            }
        }
    }
}
```

### 4. rpcd ACL — Router

Create `/usr/share/rpcd/acl.d/openwrt-monitor.json` on the **router**:

```json
{
    "openwrt-monitor": {
        "description": "Read-only access for openwrt-monitor",
        "read": {
            "ubus": {
                "luci-rpc": ["getDHCPLeases", "getHostHints"],
                "system": ["info", "board"],
                "network.device": ["status"],
                "network.interface.wan": ["status"]
            }
        }
    }
}
```

> **Note:** `luci-rpc` requires the `luci-mod-rpc` package:
> `apk add luci-mod-rpc` (or `opkg install luci-mod-rpc` on ≤ 24.x)

### 5. rpcd ACL — Combined (role: both)

If a single device is both router and AP (`role: both`), use this combined ACL:

```json
{
    "openwrt-monitor": {
        "description": "Read-only access for openwrt-monitor",
        "read": {
            "ubus": {
                "iwinfo": ["assoclist", "devices", "info", "survey"],
                "luci-rpc": ["getDHCPLeases", "getHostHints"],
                "system": ["info", "board"],
                "network.device": ["status"],
                "network.interface.wan": ["status"]
            }
        }
    }
}
```

### 6. rpcd login block

Add monitor login to rpcd on **all devices**:

```bash
uci add rpcd login
uci set rpcd.@login[-1].username='monitor'
uci set rpcd.@login[-1].password='$p$monitor'
uci add_list rpcd.@login[-1].read='openwrt-monitor'
uci commit rpcd
/etc/init.d/rpcd restart
```

The `$p$monitor` syntax means "use the Linux shadow password for user `monitor`".

### 7. Verify

```bash
# From any machine, test login:
curl -sk https://DEVICE_IP/ubus -d '{
  "jsonrpc":"2.0","id":1,"method":"call",
  "params":["00000000000000000000000000000000","session","login",
    {"username":"monitor","password":"YOUR_PASSWORD"}]
}'
# Should return a session token

# Test assoclist on an AP (use session token from login):
curl -sk https://AP_IP/ubus -d '{
  "jsonrpc":"2.0","id":2,"method":"call",
  "params":["SESSION","iwinfo","assoclist",{"device":"phy1-ap0"}]
}'

# Test WAN status on the router:
curl -sk https://ROUTER_IP/ubus -d '{
  "jsonrpc":"2.0","id":3,"method":"call",
  "params":["SESSION","network.interface.wan","status",{}]
}'
```

## Installation

1. **Settings → Add-ons → Add-on Store → ⋮ → Repositories**
2. Add: `https://github.com/svedaker/ha-addons`
3. Find **OpenWrt Monitor** → Install
4. Go to **Configuration** tab, fill in:
   - MQTT credentials
   - ubus monitor credentials
   - Targets (APs + router with host/name/role)
5. **Start**

## Configuration

| Option | Default | Description |
|--------|---------|-------------|
| `interval` | 30 | Poll interval in seconds |
| `grace_period` | 120 | Seconds before marking device as `not_home` |
| `location` | "home" | Location name for home state |
| `status_port` | 8099 | HTTP port for `/status` endpoint |
| `mqtt_host` | — | MQTT broker IP |
| `mqtt_port` | 1883 | MQTT broker port |
| `mqtt_user` | — | MQTT username |
| `mqtt_password` | — | MQTT password |
| `ubus_user` | "monitor" | ubus login username (all devices) |
| `ubus_password` | — | ubus login password |
| `ubus_scheme` | "https" | "http" or "https" |
| `targets` | — | Array of OpenWrt devices (see below) |
| `monitored_devices` | [] | Array of MACs to track online/offline (see below) |

### Targets

Each target is an OpenWrt device with a role:

```yaml
targets:
  - host: "192.168.1.1"
    name: "router"
    role: "router"      # Polls: DHCP leases, host hints, WAN status
  - host: "192.168.1.10"
    name: "ap-living-room"
    role: "ap"           # Polls: iwinfo, system info, airtime survey
  - host: "192.168.1.11"
    name: "ap-office"
    role: "ap"
```

- **router** — provides DHCP leases, host hints, WAN data
- **ap** — provides WiFi client data, airtime, system metrics
- **both** — single device acting as router + AP (gets all polls)

Example with a single all-in-one OpenWrt device:

```yaml
targets:
  - host: "192.168.1.1"
    name: "my-router"
    role: "both"
```

When using `role: both`, the device needs the combined ACL (see prerequisites).

### Partial setups

Not all roles are required. The add-on adapts to whatever targets are configured:

| Setup | What works | What's missing |
|-------|-----------|----------------|
| APs only (non-OpenWrt router) | WiFi device tracking, AP sensors, airtime | Hostnames, IPs, WAN sensors, monitored devices |
| Router only (no OpenWrt APs) | DHCP leases, WAN sensors, monitored devices | WiFi device tracking, AP sensors |
| APs + router | Everything | — |
| Single device (`role: both`) | Everything | — |

### Monitored devices

Optional list of network devices to track as `binary_sensor` entities (online/offline).
Devices are detected from active WiFi associations first, then DHCP leases and host hints.

```yaml
monitored_devices:
  - mac: "AA:BB:CC:DD:EE:01"
    name: "NAS"
  - mac: "AA:BB:CC:DD:EE:02"
    name: "Smart TV"
  - mac: "AA:BB:CC:DD:EE:03"
    name: "Access Point"
```

## Entities created in HA

### Per WiFi client — `device_tracker`

| Attribute | Example | Description |
|-----------|---------|-------------|
| `state` | home / not_home | Based on WiFi association |
| `ap` | ap-office | Current AP name |
| `band` | 5GHz | 2.4GHz or 5GHz |
| `signal` | -63 | Signal strength (dBm) |
| `signal_avg` | -61 | Average signal (dBm) |
| `connected_since` | 2026-03-22T14:30:00Z | Session start |
| `connected_time` | 471 | Kernel seconds |
| `last_seen` | 2026-03-22T15:10:30Z | Last poll seen |
| `last_ap` | ap-office | Last known AP (kept at not_home) |
| `ip` | 192.168.1.42 | From DHCP lease or host hints |
| `mac` | 34:31:8f:a7:e5:4e | MAC address |
| `rx_rate_mbps` | 866.7 | Receive rate |
| `tx_rate_mbps` | 58.6 | Transmit rate |

### Per AP — `sensor`

| Sensor | Unit | Description |
|--------|------|-------------|
| Clients 5GHz | clients | Connected 5GHz clients |
| Clients 2.4GHz | clients | Connected 2.4GHz clients |
| WiFi 5GHz RX Total | GB | Total received traffic on 5GHz interface |
| WiFi 5GHz TX Total | GB | Total transmitted traffic on 5GHz interface |
| WiFi 2.4GHz RX Total | GB | Total received traffic on 2.4GHz interface |
| WiFi 2.4GHz TX Total | GB | Total transmitted traffic on 2.4GHz interface |
| WiFi 5GHz Download | Mbit/s | Current 5GHz download rate |
| WiFi 5GHz Upload | Mbit/s | Current 5GHz upload rate |
| WiFi 2.4GHz Download | Mbit/s | Current 2.4GHz download rate |
| WiFi 2.4GHz Upload | Mbit/s | Current 2.4GHz upload rate |
| Airtime 5GHz | % | Channel utilization 5GHz |
| Airtime 2.4GHz | % | Channel utilization 2.4GHz |
| Channel 5GHz | — | Active channel number |
| Channel 2.4GHz | — | Active channel number |
| Noise 5GHz | dBm | Noise floor |
| Noise 2.4GHz | dBm | Noise floor |
| Load | — | System load average |
| Memory Available | kB | Available memory |
| Uptime | s | System uptime |

### WAN — `sensor`

| Sensor | Unit | Description |
|--------|------|-------------|
| WAN Status | ON/OFF | WAN interface up/down |
| WAN Uptime | s | Seconds since WAN came up |
| WAN Public IP | — | Public IPv4 address |
| WAN RX Total | GB | Total received bytes |
| WAN TX Total | GB | Total transmitted bytes |
| WAN Download | Mbit/s | Current download rate |
| WAN Upload | Mbit/s | Current upload rate |

### Monitored device — `binary_sensor`

| Attribute | Example | Description |
|-----------|---------|-------------|
| `state` | ON / OFF | Device visible on network |
| `mac` | aa:bb:cc:dd:ee:01 | MAC address |
| `ip` | 192.168.1.50 | Last known IP |
| `online_since` | 2026-03-22T10:00:00Z | When device came online |
| `uptime` | 5h30m0s | Time online (when ON) |
| `uptime_seconds` | 19800 | Uptime in seconds |
| `last_seen` | 2026-03-22T15:30:00Z | Last seen timestamp |
| `offline_since` | 2026-03-22T15:32:00Z | When device went offline |
| `downtime` | 2m0s | Time offline (when OFF) |
| `downtime_seconds` | 120 | Downtime in seconds |

## Status endpoint

```bash
curl http://homeassistant.local:8099/status | jq
```

Returns full JSON with all WiFi clients, AP states, DHCP leases, host hints,
WAN status, and monitored device states.

## Local development

```bash
# Build
cd openwrt-monitor/
CGO_ENABLED=0 go build -o openwrt-monitor .

# Run with test config
./openwrt-monitor --config test-options.json

# Or via go run
go run . --config test-options.json
```

## Tech stack

- **Go** with one external dependency: `paho.mqtt.golang`
- **ubus JSON-RPC** over HTTPS (self-signed certs)
- **MQTT autodiscovery** for zero-config HA integration
- **HA add-on** format — built by Supervisor, config via UI
