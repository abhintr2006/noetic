package api

import (
	"encoding/json"
	"net/http"
	"os"

	"cot-backend/internal/auth"
)

// loginRequest is the expected body for POST /auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is returned on successful authentication.
type loginResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
}

// POST /auth/login
//
// Validates credentials against the AUTH_USERNAME / AUTH_PASSWORD env vars
// and returns a signed JWT on success.
//
// Example request:
//
//	{"username":"admin","password":"secret"}
//
// Example response:
//
//	{"token":"eyJ...","message":"login successful"}
func (r *Router) login(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body loginRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil ||
		body.Username == "" || body.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"username and password required"}`))
		return
	}

	// Load accepted credentials from env; fall back to safe demo defaults.
	wantUser := os.Getenv("AUTH_USERNAME")
	wantPass := os.Getenv("AUTH_PASSWORD")
	if wantUser == "" {
		wantUser = "admin"
	}
	if wantPass == "" {
		wantPass = "noetic-secret"
	}

	if body.Username != wantUser || body.Password != wantPass {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid credentials"}`))
		return
	}

	token, err := auth.IssueToken(body.Username)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"token generation failed"}`))
		return
	}

	json.NewEncoder(w).Encode(loginResponse{
		Token:   token,
		Message: "login successful",
	})
}

// GET /auth/me
//
// Returns the authenticated user's identity from the JWT claims.
// Requires a valid Bearer token.
func (r *Router) me(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	claims, ok := auth.ClaimsFromContext(req.Context())
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"not authenticated"}`))
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"username":   claims.Username,
		"issued_at":  claims.RegisteredClaims.IssuedAt,
		"expires_at": claims.RegisteredClaims.ExpiresAt,
	})
}
