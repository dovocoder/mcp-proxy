package auth

import (
	"context"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
)

type claimsKey struct{}
type apiKeyKey struct{}

// WithClaims injects JWT claims into the context.
func WithClaims(ctx context.Context, claims jwt.MapClaims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// ClaimsFromContext extracts JWT claims from context.
func ClaimsFromContext(ctx context.Context) jwt.MapClaims {
	claims, _ := ctx.Value(claimsKey{}).(jwt.MapClaims)
	return claims
}

// WithAPIKey injects an API key into the context.
func WithAPIKey(ctx context.Context, key interface{}) context.Context {
	return context.WithValue(ctx, apiKeyKey{}, key)
}

// APIKeyFromContext extracts the API key from context.
func APIKeyFromContext(ctx context.Context) interface{} {
	return ctx.Value(apiKeyKey{})
}

// AdminMiddleware is an alias for JWTMiddleware for clarity.
func (a *AuthService) AdminMiddleware(next http.Handler) http.Handler {
	return a.JWTMiddleware(next)
}

// CORSMiddleware adds CORS headers.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
