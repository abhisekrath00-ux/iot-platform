package main

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/oidcstate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func TestOAuthStateBackendSelection(t *testing.T) {
	client := redisx.New("127.0.0.1:1", "not-used")
	for _, tc := range []struct {
		in    string
		redis *redisx.Client
		pool  *pgxpool.Pool
		want  string
		bad   bool
	}{
		{"", nil, nil, "memory", false}, {"", client, nil, "redis", false},
		{"memory", client, nil, "memory", false}, {"redis", nil, nil, "", true},
		{"postgres", nil, &pgxpool.Pool{}, "postgres", false},
		{"postgres", client, &pgxpool.Pool{}, "postgres", false},
		{"postgres", nil, nil, "", true}, {"unknown", client, nil, "", true},
	} {
		name, s, err := oauthStateStore(tc.in, tc.pool, tc.redis)
		if (err != nil) != tc.bad || name != tc.want {
			t.Fatalf("backend %q -> %q %v", tc.in, name, err)
		}
		if tc.bad {
			if s != nil {
				t.Fatal("failed configuration returned fallback")
			}
			continue
		}
		switch tc.want {
		case "memory":
			if _, ok := s.(*oidcstate.Memory); !ok {
				t.Fatal("wrong memory store")
			}
		case "redis":
			if _, ok := s.(*oidcstate.Redis); !ok {
				t.Fatal("wrong redis store")
			}
		case "postgres":
			if _, ok := s.(*oidcstate.Postgres); !ok {
				t.Fatal("wrong database store")
			}
		}
	}
}
