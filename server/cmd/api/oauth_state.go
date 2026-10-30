package main

import (
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/oidcstate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func oauthStateStore(backend string, pool *pgxpool.Pool, redis *redisx.Client) (string, oidcstate.Store, error) {
	if backend == "" {
		if redis != nil {
			backend = "redis"
		} else {
			backend = "memory"
		}
	}
	switch backend {
	case "postgres":
		if pool == nil {
			return "", nil, errors.New("Postgres OAuth state requires a database pool")
		}
		return backend, oidcstate.NewPostgres(pool), nil
	case "memory":
		return backend, oidcstate.NewMemory(), nil
	case "redis":
		if redis == nil {
			return "", nil, errors.New("OAUTH_STATE_BACKEND=redis requires REDIS_URL")
		}
		return backend, oidcstate.NewRedisClient(redis), nil
	default:
		return "", nil, errors.New("invalid OAUTH_STATE_BACKEND (use memory, postgres or redis)")
	}
}
