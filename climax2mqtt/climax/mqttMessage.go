package climax

import (
	"encoding/json"
	"fmt"
	"math"
)

type MqttMessage struct {
	Topic   string
	Message []byte
	Retain  bool
	Err     error
}

func (ts TemperatureSensor) MqttDiscoveryMessageTemperature() MqttMessage {
	id := ts.Identify()
	topic := fmt.Sprintf("homeassistant/sensor/%s/temperature/config", id)
	payload := map[string]interface{}{
		"unique_id":           fmt.Sprintf("%s_temperature", id),
		"state_topic":         fmt.Sprintf("climax2mqtt/sensors/%s/state", id),
		"name":                fmt.Sprintf("%s Temperature", ts.Name),
		"device_class":        "temperature",
		"state_class":         "measurement",
		"unit_of_measurement": "°C",
		"value_template":      "{{ value_json.temperature }}",
		"device": map[string]interface{}{
			"identifiers":  []string{id},
			"name":         ts.Name,
			"manufacturer": "Climax",
			"model":        "Temperature Sensor",
		},
	}

	jsonData, err := json.MarshalIndent(payload, "", "    ")
	if err != nil {
		return MqttMessage{topic, nil, false, err}
	}
	return MqttMessage{topic, jsonData, true, nil}
}

func (psm PowerSwitchMeter) MqttDiscoveryMessagePower() MqttMessage {
	id := psm.Identify()
	topic := fmt.Sprintf("homeassistant/sensor/%s/power/config", id)
	payload := map[string]interface{}{
		"unique_id":           fmt.Sprintf("%s_power", id),
		"state_topic":         fmt.Sprintf("climax2mqtt/sensors/%s/state", id),
		"name":                fmt.Sprintf("%s Power", psm.Name),
		"device_class":        "power",
		"state_class":         "measurement",
		"unit_of_measurement": "W",
		"value_template":      "{{ value_json.power }}",
		"device": map[string]interface{}{
			"identifiers":  []string{id},
			"name":         psm.Name,
			"manufacturer": "Climax",
			"model":        "Power Switch Meter",
		},
	}

	jsonData, err := json.MarshalIndent(payload, "", "    ")
	if err != nil {
		return MqttMessage{topic, nil, false, err}
	}
	return MqttMessage{topic, jsonData, true, nil}
}

func (psm PowerSwitchMeter) MqttDiscoveryMessageSwitch() MqttMessage {
	id := psm.Identify()
	topic := fmt.Sprintf("homeassistant/switch/%s/power_switch/config", id)
	payload := map[string]interface{}{
		"unique_id":      fmt.Sprintf("%s_power_switch", id),
		"command_topic":  fmt.Sprintf("climax2mqtt/switches/%s/set", id),
		"state_topic":    fmt.Sprintf("climax2mqtt/sensors/%s/state", id),
		"name":           fmt.Sprintf("%s Power Switch", psm.Name),
		"payload_on":     "ON",
		"payload_off":    "OFF",
		"state_on":       "ON",
		"state_off":      "OFF",
		"value_template": "{{ value_json.power_state }}",
		"device": map[string]interface{}{
			"identifiers":  []string{id},
			"name":         psm.Name,
			"manufacturer": "Climax",
			"model":        "Power Switch Meter",
		},
	}

	// Marshaling the payload into JSON format
	jsonData, err := json.MarshalIndent(payload, "", "    ")
	if err != nil {
		return MqttMessage{topic, nil, false, err}
	}
	return MqttMessage{topic, jsonData, true, nil}
}

func (psm PowerSwitchMeter) MqttDiscoveryMessageEnergy() MqttMessage {
	id := psm.Identify()
	topic := fmt.Sprintf("homeassistant/sensor/%s/energy/config", id)
	payload := map[string]interface{}{
		"unique_id":           fmt.Sprintf("%s_energy", id),
		"state_topic":         fmt.Sprintf("climax2mqtt/sensors/%s/state", id),
		"name":                fmt.Sprintf("%s Energy Usage", psm.Name),
		"device_class":        "energy",
		"state_class":         "total_increasing",
		"unit_of_measurement": "kWh",
		"value_template":      "{{ value_json.energy }}",
		"icon":                "mdi:counter",
		"device": map[string]interface{}{
			"identifiers":  []string{id},
			"name":         psm.Name,
			"manufacturer": "Climax",
			"model":        "Power Switch Meter",
		},
	}

	jsonData, err := json.MarshalIndent(payload, "", "    ")
	if err != nil {
		return MqttMessage{"", nil, false, err}
	}
	return MqttMessage{topic, jsonData, true, nil}
}

func (ts TemperatureSensor) MqttUpdateValueMessage() MqttMessage {
	id := ts.Identify()
	topic := fmt.Sprintf("climax2mqtt/sensors/%s/state", id)

	// Payload structure reflecting the current state/value
	payload := map[string]interface{}{
		"temperature": ts.Temperature,
	}

	// Serialize the payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return MqttMessage{topic, nil, false, fmt.Errorf("error serializing temperature update to JSON: %w", err)}
	}

	return MqttMessage{topic, jsonData, false, nil}
}

func (psm PowerSwitchMeter) MqttUpdateValueMessage() MqttMessage {
	id := psm.Identify()
	topic := fmt.Sprintf("climax2mqtt/sensors/%s/state", id)

	onOffState := "OFF"
	if psm.OnOff {
		onOffState = "ON"
	}
	payload := map[string]interface{}{
		"power_state": onOffState,
		"power":       math.Max(psm.Power, 0),
		"energy":      psm.Energy,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return MqttMessage{"", nil, false, fmt.Errorf("error serializing power switch meter state update to JSON: %w", err)}
	}

	return MqttMessage{topic, jsonData, false, nil}
}
