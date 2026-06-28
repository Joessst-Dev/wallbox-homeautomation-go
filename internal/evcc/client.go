// Package evcc integrates wha with a running evcc instance over MQTT. It
// subscribes to evcc's retained state topics, keeps a thread-safe Store of the
// latest values, and publishes loadpoint commands.
package evcc

import (
	"fmt"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

// qos is used for both subscriptions and command publishes. QoS 1 (at least
// once) is appropriate given evcc's cloud-API command latency — we want
// delivery guarantees, and commands are idempotent on the wha side.
const qos byte = 1

// Client wraps a paho MQTT client wired to an evcc Store.
type Client struct {
	mqtt   mqtt.Client
	store  *Store
	topics Topics
	log    *slog.Logger
}

// NewClient builds (but does not connect) an evcc MQTT client. The Store is
// updated as messages arrive; read it via store.Snapshot().
func NewClient(cfg config.MQTT, loadpointID string, log *slog.Logger) *Client {
	topics := NewTopics(cfg.TopicPrefix, loadpointID)
	store := NewStore(topics)

	c := &Client{store: store, topics: topics, log: log}

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.Broker).
		SetClientID(cfg.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetConnectTimeout(10 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetOrderMatters(false)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	// Subscriptions are NOT restored automatically across reconnects, so we
	// (re)subscribe inside the OnConnect handler every time.
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(c.onConnectionLost)

	c.mqtt = mqtt.NewClient(opts)
	return c
}

// Store returns the live state store.
func (c *Client) Store() *Store { return c.store }

// Connect establishes the broker connection and blocks until it succeeds or the
// connect token resolves with an error.
func (c *Client) Connect() error {
	tok := c.mqtt.Connect()
	tok.Wait()
	if err := tok.Error(); err != nil {
		return fmt.Errorf("mqtt connect: %w", err)
	}
	return nil
}

// Disconnect cleanly tears down the connection.
func (c *Client) Disconnect() {
	c.store.setBrokerConnected(false)
	c.mqtt.Disconnect(250)
}

func (c *Client) onConnect(client mqtt.Client) {
	c.store.setBrokerConnected(true)
	filters := make(map[string]byte)
	for _, t := range c.topics.readSubscriptions() {
		filters[t] = qos
	}
	tok := client.SubscribeMultiple(filters, c.handleMessage)
	tok.Wait()
	if err := tok.Error(); err != nil {
		c.log.Error("evcc: subscribe failed", "err", err)
		return
	}
	c.log.Info("evcc: connected and subscribed", "topics", len(filters))
}

func (c *Client) onConnectionLost(_ mqtt.Client, err error) {
	c.store.setBrokerConnected(false)
	c.log.Warn("evcc: connection lost", "err", err)
}

// handleMessage runs on paho's callback goroutine: it must not block or call
// Subscribe/Publish synchronously. It only parses and updates the Store.
func (c *Client) handleMessage(_ mqtt.Client, msg mqtt.Message) {
	if ok := c.store.Apply(msg.Topic(), string(msg.Payload()), time.Now()); !ok {
		c.log.Debug("evcc: ignored message", "topic", msg.Topic(), "payload", string(msg.Payload()))
	}
}
