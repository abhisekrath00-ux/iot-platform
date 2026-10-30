package oidcstate

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresCrossReplicaState(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	// Isolated schema avoids races with migration tests in other packages.
	schema := "oauth_state_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	ddl, err := os.ReadFile("../../migrations/0089_oauth_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	makePool := func() *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	aPool, bPool := makePool(), makePool()
	if _, err = aPool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err = aPool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	a, b := NewPostgres(aPool), NewPostgres(bPool)
	if err = a.Put("raw-login-state", "oidc-nonce", time.Minute); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = aPool.QueryRow(ctx, "SELECT state_hash FROM oauth_states").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "raw-login-state" || len(stored) != 64 {
		t.Fatal("raw state not hashed")
	}
	if got, ok := b.Take("raw-login-state"); !ok || got != "oidc-nonce" {
		t.Fatalf("other pool got %q %v", got, ok)
	}
	if _, ok := a.Take("raw-login-state"); ok {
		t.Fatal("callback replay accepted")
	}
	if err = a.Put("race", "nonce", time.Minute); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			if v, ok := s.Take("race"); ok {
				if v != "nonce" {
					t.Error("wrong nonce")
				}
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("atomic consumption winners %d", wins.Load())
	}
	if err = a.Put("collision", "first", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = b.Put("collision", "replacement", time.Minute); err == nil {
		t.Fatal("duplicate replaced nonce")
	}
	if got, ok := b.Take("collision"); !ok || got != "first" {
		t.Fatal("original nonce changed")
	}
	if err = a.Put("expired", "old", time.Minute); err != nil {
		t.Fatal(err)
	}
	aPool.Exec(ctx, "UPDATE oauth_states SET expires_at=clock_timestamp()-interval '1 second'")
	if _, ok := b.Take("expired"); ok {
		t.Fatal("database-expired state accepted")
	}
	if err = a.Put("cleanup-expired", "old", time.Minute); err != nil {
		t.Fatal(err)
	}
	aPool.Exec(ctx, "UPDATE oauth_states SET expires_at=clock_timestamp()-interval '1 second'")
	if err = b.Put("live", "new", time.Minute); err != nil {
		t.Fatal(err)
	}
	var n int
	aPool.QueryRow(ctx, "SELECT count(*) FROM oauth_states").Scan(&n)
	if n != 1 {
		t.Fatalf("expired collection left %d rows", n)
	}

	// A login must not perform an unbounded expired-table delete.
	_, err = aPool.Exec(ctx, `INSERT INTO oauth_states(state_hash,nonce,expires_at)
 SELECT 'expired-'||n,'old',clock_timestamp()-interval '1 second' FROM generate_series(1,1002) n`)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Put("bounded-cleanup", "new", time.Minute); err != nil {
		t.Fatal(err)
	}
	aPool.QueryRow(ctx, "SELECT count(*) FROM oauth_states WHERE expires_at<=clock_timestamp()").Scan(&n)
	if n != 2 {
		t.Fatalf("cleanup removed wrong number; expired left %d", n)
	}
	if _, ok := b.Take("bounded-cleanup"); !ok {
		t.Fatal("cleanup damaged live state")
	}
	for _, ttl := range []time.Duration{-time.Second, 0, 31 * time.Minute} {
		if err = a.Put("bad", "n", ttl); err == nil {
			t.Fatal("invalid ttl accepted")
		}
	}
	if err = a.Put("", "nonce", time.Minute); err == nil {
		t.Fatal("empty state accepted")
	}
	if err = a.Put("blank", "", time.Minute); err == nil {
		t.Fatal("empty nonce accepted")
	}
	aPool.Close() // no fallback to memory or another backend on a failed pool
	if err = a.Put("outage", "n", time.Minute); err == nil {
		t.Fatal("outage Put succeeded")
	}
	if _, ok := a.Take("live"); ok {
		t.Fatal("outage accepted state")
	}
	if got, ok := b.Take("live"); !ok || got != "new" {
		t.Fatal("independent pool state lost on API pool failure")
	}
}
