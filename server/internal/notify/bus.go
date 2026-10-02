package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/segmentio/kafka-go"
)

// Message-bus channels publish the same JSON event envelope as the webhook
// channel to Kafka or an AMQP 0-9-1 broker (RabbitMQ). They are plaintext and
// the Kafka path has no SASL; see docs/connectors.md for the honest scope.
//
// Kafka target: kafka://host1:9092,host2:9092/<topic>
// AMQP target:  amqp://host:5672/<vhost>?exchange=<name>&key=<routing-key>
// AMQP credentials come from AMQP_USERNAME / AMQP_PASSWORD (never the target,
// because targets are listed back to admins).

func eventBody(event string, data map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"event": event, "sent_at": time.Now().UTC().Format(time.RFC3339), "data": data})
	return b
}

// ParseKafkaTarget returns brokers and topic.
func ParseKafkaTarget(raw string) ([]string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "kafka" || u.Host == "" || u.User != nil || len(raw) > 512 {
		return nil, "", errors.New("kafka target must look like kafka://host:9092[,host2:9092]/topic")
	}
	topic := strings.Trim(u.Path, "/")
	if topic == "" || strings.ContainsAny(topic, "/ ") || len(topic) > 249 {
		return nil, "", errors.New("kafka target needs a single topic name")
	}
	var brokers []string
	for _, h := range strings.Split(u.Host, ",") {
		if _, _, err := net.SplitHostPort(h); err != nil {
			return nil, "", fmt.Errorf("kafka broker %q needs host:port", h)
		}
		brokers = append(brokers, h)
	}
	return brokers, topic, nil
}

// ParseAMQPTarget returns the broker address, vhost, exchange and routing key.
func ParseAMQPTarget(raw string) (addr, vhost, exchange, key string, err error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "amqp" || u.Hostname() == "" || u.User != nil || len(raw) > 512 {
		return "", "", "", "", errors.New("amqp target must look like amqp://host:5672/<vhost>?exchange=<name>&key=<routing-key> (no credentials in the target)")
	}
	port := u.Port()
	if port == "" {
		port = "5672"
	}
	q := u.Query()
	exchange, key = q.Get("exchange"), q.Get("key")
	if exchange == "" && key == "" {
		return "", "", "", "", errors.New("amqp target needs exchange and/or key (default exchange routes by queue name)")
	}
	vhost = "/" + strings.TrimPrefix(u.Path, "/")
	return net.JoinHostPort(u.Hostname(), port), vhost, exchange, key, nil
}

func (n *Notifier) dialer() *net.Dialer {
	return &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || (blockedIP(ip) && !(n.allowLoopback && ip.IsLoopback())) {
			return fmt.Errorf("bus: address %s is not allowed", host)
		}
		return nil
	}}
}

// Kafka produces one event to the topic and waits for the broker's ack.
func (n *Notifier) Kafka(ctx context.Context, target, event string, data map[string]any) error {
	brokers, topic, err := ParseKafkaTarget(target)
	if err != nil {
		return err
	}
	w := &kafka.Writer{
		Addr: kafka.TCP(brokers...), Topic: topic, RequiredAcks: kafka.RequireAll,
		AllowAutoTopicCreation: false, WriteTimeout: 8 * time.Second, MaxAttempts: 2,
		Transport: &kafka.Transport{Dial: n.dialer().DialContext, DialTimeout: 5 * time.Second},
	}
	defer w.Close()
	return w.WriteMessages(ctx, kafka.Message{Key: []byte(event), Value: eventBody(event, data)})
}

// AMQP publishes one persistent event with publisher confirms.
func (n *Notifier) AMQP(ctx context.Context, target, event string, data map[string]any) error {
	addr, vhost, exchange, key, err := ParseAMQPTarget(target)
	if err != nil {
		return err
	}
	user, pass := os.Getenv("AMQP_USERNAME"), os.Getenv("AMQP_PASSWORD")
	if user == "" {
		return errors.New("amqp not configured: set AMQP_USERNAME and AMQP_PASSWORD")
	}
	d := n.dialer()
	conn, err := amqp091.DialConfig("amqp://"+net.JoinHostPort(hostOf(addr), portOf(addr)), amqp091.Config{
		SASL:  []amqp091.Authentication{&amqp091.PlainAuth{Username: user, Password: pass}},
		Vhost: vhost, Heartbeat: 10 * time.Second, Dial: func(network, a string) (net.Conn, error) {
			c, err := d.DialContext(ctx, network, a)
			if err == nil {
				c.SetDeadline(time.Now().Add(10 * time.Second))
			}
			return c, err
		},
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := ch.Confirm(false); err != nil {
		return err
	}
	conf := ch.NotifyPublish(make(chan amqp091.Confirmation, 1))
	if err := ch.PublishWithContext(ctx, exchange, key, true, false, amqp091.Publishing{
		ContentType: "application/json", DeliveryMode: amqp091.Persistent, Type: event, Body: eventBody(event, data),
	}); err != nil {
		return err
	}
	select {
	case c := <-conf:
		if !c.Ack {
			return errors.New("amqp: broker nacked the message")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(8 * time.Second):
		return errors.New("amqp: no publisher confirm")
	}
}

func hostOf(a string) string { h, _, _ := net.SplitHostPort(a); return h }
func portOf(a string) string { _, p, _ := net.SplitHostPort(a); return p }
