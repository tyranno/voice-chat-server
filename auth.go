package main

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrMissingToken = errors.New("missing token")
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

// ValidateBridgeToken validates the bridge authentication token
func ValidateBridgeToken(config *Config, token string) error {
	if token == "" {
		return ErrMissingToken
	}
	if token != config.BridgeToken {
		return errors.New("unauthorized")
	}
	return nil
}
