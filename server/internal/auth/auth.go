// Package auth provides JWT session verification middleware. OIDC/SSO
// integration replaces the local issuer before enterprise GA; the middleware
// interface (tenant + role claims) stays the same.
package auth

import (
	"context"
	"net/http"
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

func Middleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, "missing token", http.StatusUnauthorized)
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
