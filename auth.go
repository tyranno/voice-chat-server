package main

import (
	"errors"
	"net/http"
	"strings"
)

// AuthError represents authentication errors
var (
	ErrInvalidToken   = errors.New("invalid token")
	ErrMissingToken   = errors.New("missing token")
	ErrUnauthorized   = errors.New("unauthorized")
)

// ExtractBearerToken extracts the token from Authorization header
func ExtractBearerToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", ErrMissingToken
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", ErrInvalidToken
	}

	return strings.TrimPrefix(auth, prefix), nil
}

// ValidateAppToken validates the app authentication token
func ValidateAppToken(config *Config, token string) error {
	if token == "" {
		return ErrMissingToken
	}
	if token != config.AuthToken {
		return ErrUnauthorized
	}
	return nil
}

// ValidateBridgeToken validates the bridge authentication token
func ValidateBridgeToken(config *Config, token string) error {
	if token == "" {
		return ErrMissingToken
	}
	if token != config.BridgeToken {
		return ErrUnauthorized
	}
	return nil
}

// AuthMiddleware creates a middleware for HTTP authentication
func AuthMiddleware(config *Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := ExtractBearerToken(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			if err := ValidateAppToken(config, token); err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}