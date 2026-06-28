package evcc

import (
	"fmt"
	"strconv"
	"time"
)

// publishTimeout bounds how long a command publish may block. When the broker
// is down, paho queues QoS-1 publishes and the token would otherwise block for
// the entire outage; bounding it keeps the control loop responsive (the next
// tick simply retries).
const publishTimeout = 3 * time.Second

// SetMode publishes the evcc charge mode for the loadpoint (off|now|minpv|pv).
// Published non-retained: commands must never be retained.
func (c *Client) SetMode(mode string) error {
	return c.publish(c.topics.ModeSet, mode)
}

// SetLimitSoC publishes the loadpoint SoC limit (percent). evcc enforces this
// as the charge stop independently of wha, so it doubles as a dead-man backstop.
func (c *Client) SetLimitSoC(pct int) error {
	return c.publish(c.topics.LimitSoCSet, strconv.Itoa(pct))
}

func (c *Client) publish(topic, payload string) error {
	tok := c.mqtt.Publish(topic, qos, false, payload)
	if !tok.WaitTimeout(publishTimeout) {
		return fmt.Errorf("mqtt publish %s: timed out after %s", topic, publishTimeout)
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("mqtt publish %s: %w", topic, err)
	}
	c.log.Debug("evcc: published", "topic", topic, "payload", payload)
	return nil
}
