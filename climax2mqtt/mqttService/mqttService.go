package mqttService

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MqttConfig struct {
	Server   string `yaml:"mqtt_server" env:"mqtt_server" json:"mqtt_server"`
	Port     int    `yaml:"mqtt_port" env:"mqtt_port" json:"mqtt_port" env-default:"1883"`
	Username string `yaml:"username" env:"mqtt_username" json:"mqtt_username" env-default:"mqtt"`
	Password string `yaml:"password" env:"mqtt_password" json:"mqtt_password" env-default:"mqtt"`
}

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	log.Printf("mqtt: Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	log.Println("mqtt: Connected")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	log.Printf("mqtt: Connect lost: %v", err)
}

func Connect(cfg MqttConfig) mqtt.Client {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.Server, cfg.Port))
	opts.SetClientID("climax2mqtt")
	opts.SetUsername(cfg.Username)
	opts.SetPassword(cfg.Password)
	opts.SetCleanSession(false)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(10 * time.Second)
	opts.SetWill("climax2mqtt/status", "offline", 1, true)
	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = func(client mqtt.Client) {
		connectHandler(client)
		Publish(client, "climax2mqtt/status", "online", true)
	}
	opts.OnConnectionLost = connectLostHandler
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	return client
}

func Subscribe(client mqtt.Client, topic string, callback mqtt.MessageHandler) {
	token := client.Subscribe(topic, 1, callback)
	token.Wait()
	log.Printf("mqtt: Subscribed to topic %s", topic)
}

func Publish(client mqtt.Client, topic string, message interface{}, retain bool) {
	token := client.Publish(topic, 1, retain, message)
	token.Wait()
}

// PublishHeartbeat publishes HA discovery for status/last update and current heartbeat state.
func PublishHeartbeat(client mqtt.Client, ts time.Time, expireAfterSec int) {
	statusConfig := map[string]interface{}{
		"name":        "climax2mqtt Status",
		"unique_id":   "climax2mqtt_status",
		"state_topic": "climax2mqtt/status",
		"payload_on":  "online",
		"payload_off": "offline",
		"icon":        "mdi:lan-check",
	}
	Publish(client, "homeassistant/binary_sensor/climax2mqtt/status/config", mustJSON(statusConfig), true)

	heartbeatConfig := map[string]interface{}{
		"name":         "climax2mqtt Last Update",
		"unique_id":    "climax2mqtt_last_update",
		"state_topic":  "climax2mqtt/heartbeat/last_update",
		"device_class": "timestamp",
		"expire_after": expireAfterSec,
		"icon":         "mdi:clock-outline",
	}
	Publish(client, "homeassistant/sensor/climax2mqtt/last_update/config", mustJSON(heartbeatConfig), true)

	Publish(client, "climax2mqtt/status", "online", true)
	Publish(client, "climax2mqtt/heartbeat/last_update", ts.UTC().Format(time.RFC3339), false)
}

// PublishOfflineStatus explicitly marks the service offline.
func PublishOfflineStatus(client mqtt.Client) {
	Publish(client, "climax2mqtt/status", "offline", true)
}

func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("mqtt: failed to marshal JSON payload: %v", err)
		return "{}"
	}
	return string(data)
}
