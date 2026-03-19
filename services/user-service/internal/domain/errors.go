package domain

import "errors"

// Sentinel errors for the user domain.
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailTaken         = errors.New("email already in use")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrClientNotFound     = errors.New("client not found")
)
