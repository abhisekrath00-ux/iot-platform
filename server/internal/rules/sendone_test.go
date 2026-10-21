package rules

import (
	"context"
	"testing"
)

type captureNotifier struct {
	gmailTenant, gmailTo, gmailSubject, gmailBody string
	gmailCalls                                    int
}

func (c *captureNotifier) Email(ctx context.Context, to []string, subject, body string) error {
	return nil
}
func (c *captureNotifier) Slack(ctx context.Context, channel, text string) error { return nil }
func (c *captureNotifier) Gmail(ctx context.Context, tenant, to, subject, body string) error {
	c.gmailCalls++
	c.gmailTenant, c.gmailTo, c.gmailSubject, c.gmailBody = tenant, to, subject, body
	return nil
}

type plainNotifier struct{}

func (plainNotifier) Email(ctx context.Context, to []string, subject, body string) error {
	return nil
}
func (plainNotifier) Slack(ctx context.Context, channel, text string) error { return nil }

func TestSendOneGmailRoutesTenant(t *testing.T) {
	n := &captureNotifier{}
	err := SendOne(context.Background(), n, "tenant-42", "gmail", "ops@example.com", "critical", "temp high", "alert.raised")
	if err != nil {
		t.Fatal(err)
	}
	if n.gmailCalls != 1 || n.gmailTenant != "tenant-42" || n.gmailTo != "ops@example.com" {
		t.Fatalf("gmail dispatch: calls=%d tenant=%q to=%q", n.gmailCalls, n.gmailTenant, n.gmailTo)
	}
	if n.gmailSubject != "[HexThings] critical alert" || n.gmailBody != "temp high" {
		t.Fatalf("gmail content: %q / %q", n.gmailSubject, n.gmailBody)
	}
}

func TestSendOneGmailUnsupportedWithoutCapability(t *testing.T) {
	err := SendOne(context.Background(), plainNotifier{}, "tenant-42", "gmail", "ops@example.com", "critical", "m", "alert.raised")
	if err != errUnsupported {
		t.Fatalf("want errUnsupported, got %v", err)
	}
}
