package mqttService

import (
	"fmt"
	"log"

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
	opts.SetClientID("go_mqtt_client")
	opts.SetUsername(cfg.Username)
	opts.SetPassword(cfg.Password)
	opts.SetCleanSession(false)
	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = connectHandler
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
