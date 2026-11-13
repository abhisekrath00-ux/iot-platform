package setupbootstrap

import (
	"context"
	"testing"
	"time"
)

func TestReadOnlyInspectionChecksReviewedAdditionsNotOnlyLedgerCount(t *testing.T) {
	for _, damage := range []string{"none", "nullable", "default", "missing-fk", "extra-column", "index", "trigger"} {
		t.Run(damage, func(t *testing.T) {
			p := isolated(t)
			applyBaseline(t, p)
			ctx := context.Background()
			before, e := InspectReviewed(ctx, p, sourceDir())
			if e != nil || before.Status != "exact_reviewed_baseline_89" || before.RuntimeAccepted {
				t.Fatalf("baseline %+v %v", before, e)
			}
			if _, e = ApplyReviewed(ctx, p, sourceDir()); e != nil {
				t.Fatal(e)
			}
			after, e := InspectReviewed(ctx, p, sourceDir())
			if e != nil || after.Status != "exact_reviewed_additive_93" || after.RuntimeAccepted {
				t.Fatalf("additive %+v %v", after, e)
			}
			sql := map[string]string{
				"nullable":     "ALTER TABLE edge_pairing_proofs ALTER COLUMN nonce_sha256 DROP NOT NULL",
				"default":      "ALTER TABLE device_profiles ALTER COLUMN connection_defaults SET DEFAULT '{\"changed\":true}'::jsonb",
				"missing-fk":   "ALTER TABLE edge_serial_port_inventory DROP CONSTRAINT edge_serial_port_inventory_gateway_id_fkey",
				"extra-column": "ALTER TABLE edge_identity_migrations ADD COLUMN unreviewed text",
				"index":        "DROP INDEX edge_identity_migrations_pending",
				"trigger":      "CREATE FUNCTION unexpected_schema_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$; CREATE TRIGGER unexpected BEFORE INSERT ON edge_pairing_proofs FOR EACH ROW EXECUTE FUNCTION unexpected_schema_trigger()",
			}[damage]
			if damage != "none" {
				if _, e = p.Exec(ctx, sql); e != nil {
					t.Fatal(e)
				}
				if _, e = InspectReviewed(ctx, p, sourceDir()); e == nil {
					t.Fatal("changed shape accepted from matching ledger")
				}
			}
			assertRows(t, p, 93)
		})
	}
}
func TestInspectionRefusesUnrecordedPartialUnknownOrMissingHistory(t *testing.T) {
	for _, damage := range []string{"object", "column", "partial", "unknown", "missing"} {
		t.Run(damage, func(t *testing.T) {
			p := isolated(t)
			applyBaseline(t, p)
			ctx := context.Background()
			sql := map[string]string{"object": "CREATE TABLE edge_pairing_proofs(id text)", "column": "ALTER TABLE device_profiles ADD COLUMN connection_defaults jsonb", "partial": "DELETE FROM schema_migrations WHERE filename='0089_oauth_state.sql'", "unknown": "INSERT INTO schema_migrations(filename)VALUES('9999_unknown.sql')", "missing": "DROP TABLE schema_migrations"}[damage]
			if _, e := p.Exec(ctx, sql); e != nil {
				t.Fatal(e)
			}
			if _, e := InspectReviewed(ctx, p, sourceDir()); e == nil {
				t.Fatal("unverifiable schema accepted")
			}
		})
	}
}

func TestInspectionAfterContendedMigratorSeesItsCommittedSchema(t *testing.T) {
	p := isolated(t)
	applyBaseline(t, p)
	ctx := context.Background()
	holder, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); e != nil {
		t.Fatal(e)
	}
	files, e := catalog(sourceDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range added {
		if _, e = holder.Exec(ctx, string(files[name])); e != nil {
			t.Fatal(e)
		}
		if _, e = holder.Exec(ctx, "INSERT INTO schema_migrations(filename)VALUES($1)", name); e != nil {
			t.Fatal(e)
		}
	}
	done := make(chan struct {
		value Inspection
		err   error
	}, 1)
	go func() {
		v, e := InspectReviewed(ctx, p, sourceDir())
		done <- struct {
			value Inspection
			err   error
		}{v, e}
	}()
	select {
	case result := <-done:
		t.Fatalf("inspection bypassed held migration lock: %+v", result)
	case <-time.After(200 * time.Millisecond):
	}
	if e = holder.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case result := <-done:
		if result.err != nil || result.value.Status != "exact_reviewed_additive_93" {
			t.Fatalf("stale post-lock snapshot: %+v %v", result.value, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("inspection did not finish after migrator commit")
	}
}

func TestSchemaQualificationNormalizationDoesNotRewriteCheckLiterals(t *testing.T) {
	p := isolated(t)
	applyBaseline(t, p)
	ctx := context.Background()
	if _, e := ApplyReviewed(ctx, p, sourceDir()); e != nil {
		t.Fatal(e)
	}
	schema := p.Config().ConnConfig.RuntimeParams["search_path"]
	sql := "ALTER TABLE edge_identity_migrations DROP CONSTRAINT edge_identity_migrations_state_check; ALTER TABLE edge_identity_migrations ADD CONSTRAINT edge_identity_migrations_state_check CHECK(state IN ('" + schema + ".pending','" + schema + ".issued','" + schema + ".revoked'))"
	if _, e := p.Exec(ctx, sql); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectReviewed(ctx, p, sourceDir()); e == nil {
		t.Fatal("schema-like literal text was normalized into a different accepted check")
	}
}
