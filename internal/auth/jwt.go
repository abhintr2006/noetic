// Package auth provides JWT validation for the Noetic backend, compatible with Supabase Auth.
package auth

import (
	"errors"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

// SupabaseClaims is the payload embedded inside every Supabase JWT.
type SupabaseClaims struct {
	Email string            `json:"email"`
	Roles []string          `json:"roles"`
	App   map[string]any    `json:"app_metadata"`
	User  map[string]any    `json:"user_metadata"`
	jwt.RegisteredClaims
}

// jwtSecret reads the Supabase JWT Secret from the environment.
// You MUST set SUPABASE_JWT_SECRET in production. Found in API settings.
func jwtSecret() []byte {
	s := os.Getenv("SUPABASE_JWT_SECRET")
	if s == "" {
		// Placeholder for dev; replace with actual secret from dashboard
		s = "your-supabase-jwt-secret-here" 
	}
	return []byte(s)
}

// ValidateToken parses and validates a raw Supabase JWT string.
func ValidateToken(raw string) (*SupabaseClaims, error) {
	token, err := jwt.ParseWithClaims(raw, &SupabaseClaims{}, func(t *jwt.Token) (any, error) {
		// Supabase uses HS256 for signing by default.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("auth: unexpected signing algorithm")
		}
		return jwtSecret(), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*SupabaseClaims)
	if !ok || !token.Valid {
		return nil, errors.New("auth: invalid token claims")
	}
	return claims, nil
}
