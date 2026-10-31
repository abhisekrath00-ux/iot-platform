package redisx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// CheckCachePermissions exercises the cache's command/key scope on two random
// scratch keys. SET EX NX seeds both keys before INCR so a successful probe
// leaves only expiring data. No deletes, tenant invalidations or retries occur.
// It proves this endpoint at this instant, not replication or failover safety.
func (r *Client) CheckCachePermissions(ctx context.Context) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return errors.New("cache preflight random source failed")
	}
	return checkCachePermissions(ctx, r.CmdContext, hex.EncodeToString(nonce))
}

func checkCachePermissions(ctx context.Context, cmd func(context.Context, ...string) (string, error), nonce string) error {
	run := func(want, phase string, args ...string) error {
		got, err := cmd(ctx, args...)
		if err != nil || got != want {
			return errors.New("cache preflight failed: " + phase)
		}
		return nil
	}
	if err := run("PONG", "connect/auth/database/PING", "PING"); err != nil {
		return err
	}
	response := "hx:rc:preflight:" + nonce
	generation := "hx:gen:preflight:" + nonce
	if err := run("OK", "response SET", "SET", response, "probe", "EX", "30", "NX"); err != nil {
		return err
	}
	if err := run("probe", "response GET", "GET", response); err != nil {
		return err
	}
	if err := run("OK", "generation SET", "SET", generation, "0", "EX", "30", "NX"); err != nil {
		return err
	}
	if err := run("1", "generation INCR", "INCR", generation); err != nil {
		return err
	}
	return run("1", "generation GET", "GET", generation)
}
