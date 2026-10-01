package main

import "testing"

// Migrations run on every API start. Flows created after versioning (random
// version ids) and draft-only imported flows must not break the 0007 backfill.
func TestIntegrationMigrateIsRepeatableWithVersionedFlows(t *testing.T) {
	s, _ := testServer(t) // applies all migrations once
	ctx := t.Context()
	seed(t, s, "itest-mg1")
	s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id='itest-mg1'`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id='itest-mg1'`) })
	for _, q := range []string{
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('mg-f1','itest-mg1','a','{}','u'),('mg-f2','itest-mg1','b','{}','u')`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by,published_at) VALUES('mg-v1','mg-f1','itest-mg1',1,'{}','published','u',now())`,
		`UPDATE flows SET published_version_id='mg-v1' WHERE id='mg-f1'`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('mg-v2','mg-f2','itest-mg1',1,'{}','draft','u')`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(ctx, s.st); err != nil {
		t.Fatalf("second migrate run failed: %v", err)
	}
	var pub *string
	s.st.Pool.QueryRow(ctx, `SELECT published_version_id FROM flows WHERE id='mg-f2'`).Scan(&pub)
	if pub != nil {
		t.Fatal("draft-only flow was published by the backfill")
	}
}
