// Package auth provides JWT token issuance and validation for the Noetic backend.
package auth

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the payload embedded inside every JWT.
type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// jwtSecret reads the signing key from the environment.
// Falls back to a compile-time default — you MUST override JWT_SECRET in production.
func jwtSecret() []byte {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		s = "noetic-change-me-in-production"
	}
	return []byte(s)
}

// defaultTTL controls token lifetime. Override via JWT_EXPIRY_HOURS env var.
const defaultTTL = 24 * time.Hour

// IssueToken creates and signs a new HS256 JWT for the given username.
// If JWT_EXPIRY_HOURS is set it is used as the expiry duration.
func IssueToken(username string) (string, error) {
	ttl := defaultTTL
	if h := os.Getenv("JWT_EXPIRY_HOURS"); h != "" {
		if d, err := time.ParseDuration(h + "h"); err == nil {
			ttl = d
		}
	}

	now := time.Now()
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			Issuer:    "noetic",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret())
}

// ValidateToken parses and validates a raw JWT string.
// Returns the embedded Claims on success, or an error if the token is
// missing, expired, tampered with, or signed with the wrong algorithm.
func ValidateToken(raw string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		// Reject non-HMAC algorithms to prevent the "alg:none" attack.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("auth: unexpected signing algorithm")
		}
		return jwtSecret(), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("auth: invalid token claims")
	}
	return claims, nil
}
