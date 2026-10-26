package main

import (
	"context"
	"errors"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"testing"
	"time"
)

type subscriptionClient struct {
	mqtt.Client
	topics []string
	token  mqtt.Token
	qos    byte
}

func (c *subscriptionClient) Subscribe(topic string, qos byte, _ mqtt.MessageHandler) mqtt.Token {
	c.topics = append(c.topics, topic)
	c.qos = qos
	return c.token
}

type subscriptionToken struct {
	mqtt.Token
	done   bool
	err    error
	result map[string]byte
}

func (t subscriptionToken) WaitTimeout(time.Duration) bool { return t.done }
func (t subscriptionToken) Error() error                   { return t.err }
func (t subscriptionToken) Result() map[string]byte        { return t.result }
func TestInstallIngestSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tok     subscriptionToken
		wantErr bool
	}{
		{"qos1", subscriptionToken{done: true, result: map[string]byte{"topic": 1}}, false},
		{"qos0", subscriptionToken{done: true, result: map[string]byte{"topic": 0}}, false},
		{"refused", subscriptionToken{done: true, result: map[string]byte{"topic": 0x80}}, true},
		{"missing", subscriptionToken{done: true, result: map[string]byte{}}, true},
		{"timeout", subscriptionToken{}, true},
		{"error", subscriptionToken{done: true, err: errors.New("broker unavailable")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &subscriptionClient{token: tc.tok}
			err := installIngestSubscriptions(context.Background(), c, []ingestSubscription{{topic: "topic"}})
			if (err != nil) != tc.wantErr || len(c.topics) != 1 || c.qos != 1 {
				t.Fatalf("err=%v topics=%v qos=%d", err, c.topics, c.qos)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &subscriptionClient{}
	if installIngestSubscriptions(ctx, c, []ingestSubscription{{topic: "topic"}}) == nil || len(c.topics) != 0 {
		t.Fatal("cancelled context subscribed")
	}
}
