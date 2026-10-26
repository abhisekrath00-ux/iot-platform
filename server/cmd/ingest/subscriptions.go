package main

import (
	"context"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"time"
)

type ingestSubscription struct {
	topic   string
	handler mqtt.MessageHandler
}

// Install on initial connect and every reconnect. Limit SUBACK waits so a
// broken broker cannot leave a live process with permanently missing topics.
func installIngestSubscriptions(ctx context.Context, c mqtt.Client, subscriptions []ingestSubscription) error {
	for _, sub := range subscriptions {
		if err := ctx.Err(); err != nil {
			return err
		}
		tok := c.Subscribe(sub.topic, 1, sub.handler)
		if !tok.WaitTimeout(10 * time.Second) {
			return fmt.Errorf("SUBACK timeout for %s", sub.topic)
		}
		if err := tok.Error(); err != nil {
			return fmt.Errorf("subscribe %s: %w", sub.topic, err)
		}
		if result, ok := tok.(interface{ Result() map[string]byte }); ok {
			qos, granted := result.Result()[sub.topic]
			if !granted || qos > 1 {
				return fmt.Errorf("subscription refused for %s", sub.topic)
			}
		}
	}
	return nil
}
