// Package auth provides JWT validation for the Noetic backend, supporting Supabase's ES256 (ECC) JWKS.
package auth

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// SupabaseClaims is the payload embedded inside every Supabase JWT.
type SupabaseClaims struct {
	Email string         `json:"email"`
	Roles []string       `json:"roles"`
	App   map[string]any `json:"app_metadata"`
	User  map[string]any `json:"user_metadata"`
	jwt.RegisteredClaims
}

var (
	jwksOnce sync.Once
	jwks     keyfunc.Keyfunc
)

// initJWKS fetches the JWKS from Supabase Discovery URL if configured.
// It caches it for the lifetime of the process.
func initJWKS() {
	jwksOnce.Do(func() {
		u := os.Getenv("SUPABASE_JWKS_URL")
		if u == "" {
			log.Println("[auth] SUPABASE_JWKS_URL not set — fallback to symmetric secret validation")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		k, err := keyfunc.NewDefaultCtx(ctx, []string{u})
		if err != nil {
			log.Printf("[auth] failed to initialize JWKS from %s: %v", u, err)
			return
		}

		jwks = k
		log.Printf("[auth] ES256 (Asymmetric) validation enabled via %s", u)
	})
}

// jwtSecret reads the symmetric Supabase JWT Secret from the environment for fallback projects.
func jwtSecret() []byte {
	return []byte(os.Getenv("SUPABASE_JWT_SECRET"))
}

// ValidateToken parses and validates a raw Supabase JWT string.
// It supports both modern ES256 (Asymmetric) and fallback HS256 (Symmetric) validation.
func ValidateToken(raw string) (*SupabaseClaims, error) {
	initJWKS()

	var keyFunc jwt.Keyfunc

	if jwks != nil {
		// Use modern Asymmetric (ES256) validation via JWKS.
		keyFunc = jwks.Keyfunc
	} else if os.Getenv("SUPABASE_JWT_SECRET") != "" {
		// Use fallback Symmetric (HS256) validation if secret is provided.
		keyFunc = func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("auth: unexpected signing algorithm (expected HS256)")
			}
			return jwtSecret(), nil
		}
	} else {
		return nil, errors.New("auth: neither SUPABASE_JWKS_URL nor SUPABASE_JWT_SECRET provided")
	}

	token, err := jwt.ParseWithClaims(raw, &SupabaseClaims{}, keyFunc)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*SupabaseClaims)
	if !ok || !token.Valid {
		return nil, errors.New("auth: invalid token claims")
	}

	return claims, nil
}
