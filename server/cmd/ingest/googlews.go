package main

// googlews.go wires the Google Workspace gmail alert channel into the ingest
// pipeline. The API service owns consent and token storage; ingest only reads
// the tenant grant through the same secrets store, so a gmail channel created
// in the UI sends from the tenant's connected Google account here too.
// Everything is off unless GOOGLE_CLIENT_ID/SECRET are set (air-gapped
// deployments simply never set them).

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
)

// gmailNotifier adds the Gmail capability to the base notifier so
// rules.dispatchOne's gmail case resolves. Email/Slack delegate unchanged.
type gmailNotifier struct {
	base   *notify.Notifier
	sender *googlews.GmailSender
}

func (g *gmailNotifier) Email(ctx context.Context, to []string, subject, body string) error {
	return g.base.Email(ctx, to, subject, body)
}

func (g *gmailNotifier) Slack(ctx context.Context, channel, text string) error {
	return g.base.Slack(ctx, channel, text)
}

func (g *gmailNotifier) Gmail(ctx context.Context, tenant, to, subject, body string) error {
	if g.sender == nil {
		return errors.New("google workspace not configured on this deployment")
	}
	_, err := g.sender.Send(ctx, tenant, to, subject, body)
	return err
}

// googleRulesNotifier returns a Gmail-capable notifier when GOOGLE_CLIENT_ID
// is set and the secrets store is usable, the base notifier plus a reason
// string otherwise. It never fails startup: a misconfigured Google connection
// must not take down telemetry ingest, and gmail sends fail closed at
// dispatch time instead.
func googleRulesNotifier(base *notify.Notifier, pool *pgxpool.Pool) (rules.Notifier, string) {
	cid := os.Getenv("GOOGLE_CLIENT_ID")
	if cid == "" {
		return base, ""
	}
	p, err := googlews.NewProvider(cid, os.Getenv("GOOGLE_CLIENT_SECRET"), os.Getenv("GOOGLE_REDIRECT_URL"))
	if err != nil {
		return base, fmt.Sprintf("google workspace provider: %v", err)
	}
	key, err := secrets.KeyFromEnv(os.Getenv("SECRETS_KEY"))
	if err != nil || len(key) == 0 {
		// Keep the gmail capability so dispatch fails with a clear error
		// instead of errUnsupported; the startup log carries the reason.
		return &gmailNotifier{base: base}, "google workspace: SECRETS_KEY missing or invalid; gmail alert channel will fail at send time"
	}
	return &gmailNotifier{
		base:   base,
		sender: googlews.NewGmailSender(p, &secrets.Store{Pool: pool, Key: key}),
	}, ""
}
