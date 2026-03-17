// Package auth — JWT generation and verification, shared across all microservices.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessClaims is the JWT payload for access tokens.
//
// Exact JSON shape:
//
//	{ "sub": "<id>", "email": "<email>", "user_type": "<type>",
//	  "permissions": [...], "iat": <unix>, "exp": <unix> }
//
// token_type is intentionally absent on valid access tokens; its presence as
// "refresh" is treated as misuse and rejected by VerifyToken.
type AccessClaims struct {
	Email       string   `json:"email"`
	UserType    string   `json:"user_type"`
	Permissions []string `json:"permissions"`
	// TokenType is populated only when a refresh token is accidentally parsed
	// into this struct. VerifyToken rejects any non-empty "refresh" value.
	TokenType string `json:"token_type,omitempty"`
	jwt.RegisteredClaims
}

// RefreshClaims is the JWT payload for refresh tokens.
//
// Exact JSON shape:
//
//	{ "sub": "<id>", "iat": <unix>, "exp": <unix>, "token_type": "refresh" }
type RefreshClaims struct {
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

// GenerateTokens returns a signed access token (15 min) and a signed refresh
// token (7 days). Access and refresh tokens are signed with separate secrets.
func GenerateTokens(
	userID, email, userType string,
	permissions []string,
	accessSecret, refreshSecret string,
) (accessToken, refreshToken string, err error) {
	now := time.Now()

	accessClaims := &AccessClaims{
		Email:       email,
		UserType:    userType,
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).
		SignedString([]byte(accessSecret))
	if err != nil {
		return
	}

	refreshClaims := &RefreshClaims{
		TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
		},
	}
	refreshToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).
		SignedString([]byte(refreshSecret))
	return
}

// GenerateAccessToken signs a new access token (15 min) without touching the
// refresh token. Used exclusively by the RefreshToken handler.
func GenerateAccessToken(
	userID, email, userType string,
	permissions []string,
	accessSecret string,
) (string, error) {
	now := time.Now()
	claims := &AccessClaims{
		Email:       email,
		UserType:    userType,
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(accessSecret))
}

// VerifyRefreshToken parses and validates a refresh token string.
//
// It checks:
//  1. Signature is valid HMAC-SHA256 using secret.
//  2. Token is not expired.
//  3. token_type IS "refresh" (rejects plain access tokens sent here by mistake).
func VerifyRefreshToken(tokenStr, secret string) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &RefreshClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.TokenType != "refresh" {
		return nil, errors.New("not a refresh token")
	}
	return claims, nil
}

// ActivationClaims is the JWT payload for account-activation tokens.
//
// Exact JSON shape:
//
//	{ "sub": "<email>", "token_type": "activation", "iat": <unix>, "exp": <unix> }
type ActivationClaims struct {
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

// GenerateActivationToken issues a short-lived activation token (24 h).
// The subject is the user's email address.
func GenerateActivationToken(email, secret string) (string, error) {
	now := time.Now()
	claims := &ActivationClaims{
		TokenType: "activation",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   email,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// VerifyActivationToken parses and validates an activation token string.
//
// It checks:
//  1. Signature is valid HMAC-SHA256 using secret.
//  2. Token is not expired.
//  3. token_type IS "activation".
//
// Returns the email (sub claim) on success.
func VerifyActivationToken(tokenStr, secret string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &ActivationClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}

	claims, ok := token.Claims.(*ActivationClaims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token")
	}
	if claims.TokenType != "activation" {
		return "", errors.New("not an activation token")
	}
	return claims.Subject, nil
}

// ResetClaims is the JWT payload for password-reset tokens.
//
// Exact JSON shape:
//
//	{ "sub": "<email>", "token_type": "reset", "iat": <unix>, "exp": <unix> }
type ResetClaims struct {
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

// GenerateResetToken issues a short-lived password-reset token (15 min).
// The subject is the user's email address.
func GenerateResetToken(email, secret string) (string, error) {
	now := time.Now()
	claims := &ResetClaims{
		TokenType: "reset",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   email,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// VerifyResetToken parses and validates a password-reset token string.
//
// It checks:
//  1. Signature is valid HMAC-SHA256 using secret.
//  2. Token is not expired.
//  3. token_type IS "reset".
//
// Returns the email (sub claim) on success.
func VerifyResetToken(tokenStr, secret string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &ResetClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}

	claims, ok := token.Claims.(*ResetClaims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token")
	}
	if claims.TokenType != "reset" {
		return "", errors.New("not a reset token")
	}
	return claims.Subject, nil
}

// VerifyToken parses and validates an access token string.
//
// It checks:
//  1. Signature is valid HMAC-SHA256 using secret.
//  2. Token is not expired.
//  3. token_type is NOT "refresh" (prevents refresh tokens being used as access tokens).
func VerifyToken(tokenStr, secret string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &AccessClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*AccessClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.TokenType == "refresh" {
		return nil, errors.New("refresh token cannot be used as an access token")
	}
	return claims, nil
}
