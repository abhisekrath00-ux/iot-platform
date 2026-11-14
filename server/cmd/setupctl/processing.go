package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/processingschema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Separate operator path, never called by the current Update adapter or startup.
// Acknowledgements describe the external maintenance fence, not owner authority.
func runProcessing(ctx context.Context, apply bool, input io.Reader, output io.Writer) error {
	if apply {
		var req struct {
			Version        int    `json:"version"`
			Scope          string `json:"scope"`
			WritersStopped bool   `json:"writers_stopped"`
			BackupVerified bool   `json:"backup_verified"`
		}
		d := json.NewDecoder(io.LimitReader(input, 4097))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil {
			return fmt.Errorf("staged processing maintenance acknowledgement required")
		}
		var extra any
		if d.Decode(&extra) != io.EOF || req.Version != 1 || req.Scope != "staged_processing_v1" || !req.WritersStopped || !req.BackupVerified {
			return fmt.Errorf("exact staged processing maintenance acknowledgement required")
		}
	}
	if os.Getenv("DATABASE_URL") == "" {
		return fmt.Errorf("explicit database configuration required")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("database configuration unavailable")
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "hexthings-staged-processing-schema-operator"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("cannot open staged processing operator")
	}
	defer p.Close()
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = "/migrations"
	}
	var result processingschema.Inspection
	if apply {
		result, err = processingschema.Apply(ctx, p, dir)
	} else {
		result, err = processingschema.Inspect(ctx, p, dir)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
