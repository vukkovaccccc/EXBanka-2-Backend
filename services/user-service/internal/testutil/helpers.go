// Package testutil provides shared helpers for user-service unit tests.
// Nothing in this package is compiled into the production binary.
package testutil

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/metadata"

	auth "banka-backend/shared/auth"
)

// Fixed secrets used across all user-service tests. Using constants keeps
// token generation deterministic and avoids accidental secret leakage.
const (
	TestAccessSecret     = "test-access-secret-32-chars-long!"
	TestRefreshSecret    = "test-refresh-secret-32-chars-lon!"
	TestActivationSecret = "test-activation-secret-32chars!!"
)

// AdminContext returns a context with ADMIN JWT claims pre-injected via the
// same key the production interceptor uses. No real JWT is created or verified.
func AdminContext() context.Context {
	return auth.NewContextWithClaims(context.Background(), &auth.AccessClaims{
		Email:       "admin@test.com",
		UserType:    "ADMIN",
		Permissions: []string{},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	})
}

// EmployeeContext returns a context with EMPLOYEE claims + the given permission codes.
func EmployeeContext(userID string, permissions []string) context.Context {
	return auth.NewContextWithClaims(context.Background(), &auth.AccessClaims{
		Email:       "employee@test.com",
		UserType:    "EMPLOYEE",
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	})
}

// UnauthenticatedContext returns a plain background context with no claims.
// Use this to test handlers that should reject unauthenticated callers.
func UnauthenticatedContext() context.Context {
	return context.Background()
}

// MakeAccessToken signs a real access token with TestAccessSecret.
// Useful when testing the auth interceptor itself, where a real JWT is needed.
func MakeAccessToken(userID, email, userType string, permissions []string) string {
	token, _ := auth.GenerateAccessToken(userID, email, userType, permissions, TestAccessSecret)
	return token
}

// MakeActivationToken signs a real activation token with TestActivationSecret.
func MakeActivationToken(email string) string {
	token, _ := auth.GenerateActivationToken(email, TestActivationSecret)
	return token
}

// MakeResetToken signs a real password-reset token with TestActivationSecret.
func MakeResetToken(email string) string {
	token, _ := auth.GenerateResetToken(email, TestActivationSecret)
	return token
}

// GRPCIncomingContext creates a gRPC-style incoming context with an
// Authorization: Bearer <token> metadata header. Used for interceptor tests.
func GRPCIncomingContext(token string) context.Context {
	md := metadata.Pairs("authorization", "Bearer "+token)
	return metadata.NewIncomingContext(context.Background(), md)
}
