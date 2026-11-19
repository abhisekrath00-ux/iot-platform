package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Separate LOCAL maintenance path. No normal GUI/update/startup calls it.
// Acknowledgements describe prerequisites, NEVER owner permission. Exact target,
// stopped writers, trusted baseline/backup and compatibility are external gates.
func runDenials(ctx context.Context, action string, input io.Reader, output io.Writer) error {
	var expected authorityschema.Evidence
	if action == "apply-staged-denials" {
		var ack struct {
			Version        int    `json:"version"`
			Scope          string `json:"scope"`
			WritersStopped bool   `json:"writers_stopped"`
			BackupVerified bool   `json:"backup_verified"`
		}
		d := json.NewDecoder(io.LimitReader(input, 4097))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&ack) != nil || d.Decode(&extra) != io.EOF || ack.Version != 1 || ack.Scope != "staged_denial_v1" || !ack.WritersStopped || !ack.BackupVerified {
			return errors.New("exact staged denial maintenance acknowledgement required")
		}
	} else if action == "verify-staged-denials" {
		d := json.NewDecoder(io.LimitReader(input, 4097))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&expected) != nil || d.Decode(&extra) != io.EOF {
			return errors.New("independently preserved exact denial evidence required")
		}
	}
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("explicit database configuration required")
	}
	cfg, e := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if e != nil {
		return errors.New("database configuration unavailable")
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "hexthings-staged-denial-maintenance"
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return errors.New("cannot open staged denial maintenance")
	}
	defer p.Close()
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = "/migrations"
	}
	if action == "apply-staged-denials" {
		if e = authorityschema.Apply(ctx, p, dir); e != nil {
			return e
		}
	}
	if action == "verify-staged-denials" {
		if e = authorityschema.VerifyEvidence(ctx, p, dir, expected); e != nil {
			return e
		}
		// Report the exact evidence we compared, not a later second snapshot.
		return json.NewEncoder(output).Encode(expected)
	}
	evidence, e := authorityschema.InspectEvidence(ctx, p, dir)
	if e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(evidence)
}
