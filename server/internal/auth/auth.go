// Package auth provides JWT session verification middleware. OIDC/SSO
// integration replaces the local issuer before enterprise GA; the middleware
// interface (tenant + role claims) stays the same.
package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ctxKey string

const (
	CtxTenant ctxKey = "tenant"
	CtxUser   ctxKey = "user"
	CtxRole   ctxKey = "role"
)

type Claims struct {
	TenantID string `json:"tenant_id"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// KeyResolver authenticates a non-JWT bearer token (an API key). It returns
// tenant, principal and role, or ok=false when the key is unknown, revoked or
// expired.
type KeyResolver func(ctx context.Context, token string) (tenant, user, role string, ok bool)

// CtxViaKey marks requests authenticated by an API key rather than a session.
const CtxViaKey ctxKey = "via_key"

func ViaKey(r *http.Request) bool { v, _ := r.Context().Value(CtxViaKey).(bool); return v }

func Middleware(secret []byte, resolvers ...KeyResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			if raw := strings.TrimPrefix(h, "Bearer "); strings.HasPrefix(raw, "hxk_") && len(resolvers) > 0 {
				t, u, role, ok := resolvers[0](r.Context(), raw)
				if !ok {
					http.Error(w, "invalid token", http.StatusUnauthorized)
					return
				}
				if keyLimiter != nil {
					if ok, wait := keyLimiter.Allow(u); !ok {
						w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
						http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
						return
					}
				}
				ctx := context.WithValue(r.Context(), CtxTenant, t)
				ctx = context.WithValue(ctx, CtxUser, u)
				ctx = context.WithValue(ctx, CtxRole, role)
				ctx = context.WithValue(ctx, CtxViaKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			var c Claims
			tok, err := jwt.ParseWithClaims(strings.TrimPrefix(h, "Bearer "), &c,
				func(t *jwt.Token) (any, error) { return secret, nil },
				jwt.WithValidMethods([]string{"HS256"}))
			if err != nil || !tok.Valid || c.TenantID == "" {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), CtxTenant, c.TenantID)
			ctx = context.WithValue(ctx, CtxUser, c.Subject)
			ctx = context.WithValue(ctx, CtxRole, c.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func Tenant(r *http.Request) string { v, _ := r.Context().Value(CtxTenant).(string); return v }
func User(r *http.Request) string   { v, _ := r.Context().Value(CtxUser).(string); return v }
func Role(r *http.Request) string   { v, _ := r.Context().Value(CtxRole).(string); return v }
