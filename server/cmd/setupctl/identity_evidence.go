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

// Separate LOCAL selected-authority maintenance read. No GUI/startup/update calls
// it. No apply action, restore, network listener or authority write exists here.
// Stopped writers and trusted pre-backup provenance remain external prerequisites.
func runIdentityEvidence(ctx context.Context, action string, input io.Reader, output io.Writer) error {
	var want authorityschema.IdentityEvidence
	decoder := json.NewDecoder(io.LimitReader(input, 4097))
	decoder.DisallowUnknownFields()
	var extra any
	if action == "verify-staged-identity" {
		if decoder.Decode(&want) != nil || decoder.Decode(&extra) != io.EOF {
			return errors.New("independently preserved selected identity evidence required")
		}
	} else if decoder.Decode(&extra) != io.EOF {
		return errors.New("identity inspection takes no input or activation instruction")
	}
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("explicit database configuration required")
	}
	if ctx == nil {
		return errors.New("explicit maintenance context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cfg, e := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if e != nil {
		return errors.New("database configuration unavailable")
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "hexthings-selected-identity-maintenance"
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return errors.New("cannot open selected identity maintenance")
	}
	defer p.Close()
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = "/migrations"
	}
	if action == "verify-staged-identity" {
		if e = authorityschema.VerifyIdentityEvidence(ctx, p, dir, want); e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(want)
	}
	got, e := authorityschema.InspectIdentityEvidence(ctx, p, dir)
	if e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(got)
}
